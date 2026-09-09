package runtime

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/intentdriven/Gropius/internal/config"
)

// Spec describes one model server process to launch.
type Spec struct {
	RepoID string
	// ModelPath is the directory handed to `mlx_lm.server --model`. It is also
	// the exact string clients must put in the request's "model" field, which is
	// why the pool hands it back to the gateway to rewrite with.
	ModelPath string
	Port      int
	// DecodeConcurrency maps to --decode-concurrency: how many requests are
	// batched together during generation.
	DecodeConcurrency int
	// Sampling is the set of sampling defaults this server starts with. The
	// server applies them to any request that omits the parameter, and a
	// request's own value replaces them for that request alone — which is why
	// they belong on the command line rather than in the relayed body.
	Sampling config.Sampling
}

// samplingFlagsVerifiedAgainst is the mlx-lm release whose source the flag
// spellings below, and the ranges in internal/config, were read from. Nothing
// else connects them to it: a renamed flag makes argparse exit on every model
// launch, which no test with a stand-in interpreter can see. A version bump
// therefore has to come past
// TestSamplingFlagsWereVerifiedAgainstThePinnedServer.
const samplingFlagsVerifiedAgainst = "0.31.3"

// samplingFlags is the model server's own spelling of each sampling
// parameter's launch flag, read from its argument parser (see
// .abcd/development/research/notes/2026-09-06-mlx-lm-sampling-launch-flags.md).
var samplingFlags = map[string]string{
	"temperature": "--temp",
	"top_p":       "--top-p",
	"top_k":       "--top-k",
	"min_p":       "--min-p",
	"max_tokens":  "--max-tokens",
}

// samplingArgs renders the sampling defaults as command-line flags.
//
// It walks the same table config validates against, so a parameter added
// there without a flag here is caught by a test rather than accepted, stored
// and never applied. Every value is re-rendered from a number, so nothing a
// client sent and nothing a file contained reaches the argument vector as a
// string, and each flag and its value are separate elements — there is no
// shell here to split them.
//
// Out-of-range values are dropped rather than passed. The settings endpoint
// already refuses them, but this is the last point at which one could still
// do harm, and the harm is large: the model server takes the flag as its
// default and checks the effective value of every request against it, and its
// check raises uncaught — the connection closes with no response and the
// gateway answers 502. A value it will not accept therefore breaks every
// request that omits that parameter, precisely the traffic these defaults
// exist to serve.
func samplingArgs(s config.Sampling) []string {
	sane, _ := s.Sanitized()
	var args []string
	for _, v := range sane.Values() {
		flag, ok := samplingFlags[v.Field]
		if !ok {
			continue
		}
		args = append(args, flag, formatSamplingValue(v))
	}
	return args
}

// formatSamplingValue renders one value for the command line.
//
// Negative zero is the one number that passes a "must be at least zero" check
// and still renders with a leading dash, which would read as another flag.
func formatSamplingValue(v config.SamplingValue) string {
	if v.Integer {
		// From the integer field, never from the float64 the bounds are
		// compared in: narrowing a float64 that is out of integer range is
		// implementation-defined, and on one of Go's architectures it wraps to
		// a negative — which argparse would accept as a token budget.
		return strconv.Itoa(v.Int)
	}
	if v.Number == 0 {
		return "0"
	}
	return strconv.FormatFloat(v.Number, 'f', -1, 64)
}

// Process is a running model server.
type Process interface {
	// Stop terminates the process, gracefully if it can, forcefully if it must.
	Stop(ctx context.Context) error
	// Done is closed when the process exits.
	Done() <-chan struct{}
	// Err reports why the process exited, if it failed.
	Err() error
	// Pid is the OS process id, for diagnostics.
	Pid() int
}

// Launcher starts model server processes. The pool is written against this
// interface so it can be tested without Python or a GPU.
type Launcher interface {
	Launch(ctx context.Context, spec Spec) (Process, error)
	// Precheck reports whether Launch is likely to succeed for spec, without
	// starting a process. The pool calls it before evicting another model to
	// make room: an eviction is not reversible, so a launch failure caught
	// only after the victim is gone destroys a healthy, unrelated model for
	// nothing.
	Precheck(spec Spec) error
}

// LaunchError wraps a Launcher.Launch failure — the process failing to start
// at all. Its message can embed absolute local filesystem paths (the venv
// interpreter, the model directory, the log file) rooted under the serving
// account's home directory, so callers that relay pool errors to the network
// must not forward it verbatim — the gateway matches on this type to log the
// detail server-side and return a generic message instead. It does not cover
// a readiness failure once the process has started (Pool.waitReady's
// readyErr): that error currently carries no local-path detail, since it
// comes from the process's own exit status or a readiness-probe timeout, not
// from Launch.
type LaunchError struct {
	Err error
}

func (e *LaunchError) Error() string { return e.Err.Error() }
func (e *LaunchError) Unwrap() error { return e.Err }

// NotReadyError wraps the other half of a load going wrong: the process
// started and then never answered a completion — it exited during startup, or
// it was still loading when the readiness timeout ran out.
//
// It carries the same message it always did; the type is what lets a caller
// tell "could not be started" from "started and never answered". The two are
// different failures: the first is a broken installation or a vanished model
// directory, the second is usually a model too large for this Mac or weights
// that will not load. Its message is safe to relay, unlike a LaunchError's:
// it comes from the process's own exit status or from the probe's timeout, not
// from a path on this machine.
type NotReadyError struct {
	Err error
}

func (e *NotReadyError) Error() string { return e.Err.Error() }
func (e *NotReadyError) Unwrap() error { return e.Err }

// ExecLauncher runs the real `mlx_lm.server` out of the managed virtualenv.
type ExecLauncher struct {
	Paths config.Paths
	// LogDir receives one log file per model process.
	LogDir string
	// Owner is the uid the interpreter must be owned by (root is always
	// accepted); zero means the current effective uid. See trustedExecutable.
	Owner int

	ledgerOnce sync.Once
	ledger     *pidLedger
}

func (l *ExecLauncher) pidLedger() *pidLedger {
	// This account's own directory, not the data root: the ledger records
	// process groups only the uid that started them can signal, so it is no use
	// to another account — and in a shared root the second account's write over
	// the first account's ledger is refused by the sticky bit and swallowed,
	// which ends orphan reaping for it without a word.
	l.ledgerOnce.Do(func() { l.ledger = newPIDLedger(l.Paths.Account) })
	return l.ledger
}

// ReapOrphans kills any model servers left running by a previous, crashed run.
// Call once at startup before launching anything.
func (l *ExecLauncher) ReapOrphans() int {
	return l.pidLedger().reapOrphans()
}

// Precheck confirms the venv interpreter is present and trustworthy (see
// trustedExecutable) and the model directory exists.
func (l *ExecLauncher) Precheck(spec Spec) error {
	python := l.Paths.VenvPython()
	if err := trustedExecutable(python, ownerOrSelf(l.Owner)); err != nil {
		return fmt.Errorf("python runtime is not installed (%s): %w", python, err)
	}
	if _, err := os.Stat(spec.ModelPath); err != nil {
		return fmt.Errorf("model directory is missing (%s): %w", spec.ModelPath, err)
	}
	return nil
}

// launchArgs is the model server's whole command line, spec by spec.
//
// It is a function of the Spec alone, and the Spec carries nothing about
// logging: the level is a constant here. That is the point. At DEBUG the model
// server writes every request body and every response to its log, prompts and
// completions included, so the level is never something another feature can
// reach — recording request statistics leaves this vector byte for byte as it
// was. Raising it is a separate, deliberate action per model, and it says in
// plain words what it writes.
func launchArgs(spec Spec) []string {
	// `python -m mlx_lm.server` is deprecated in 0.31; `python -m mlx_lm server`
	// is the supported spelling.
	args := []string{
		"-m", "mlx_lm", "server",
		"--model", spec.ModelPath,
		// Model servers are strictly loopback. Only the Go gateway faces the LAN,
		// so it alone enforces auth and rewrites requests.
		"--host", "127.0.0.1",
		"--port", strconv.Itoa(spec.Port),
		"--log-level", "INFO",
	}
	args = append(args, samplingArgs(spec.Sampling)...)
	if spec.DecodeConcurrency > 1 {
		args = append(args, "--decode-concurrency", strconv.Itoa(spec.DecodeConcurrency))
	}
	return args
}

// Launch spawns mlx_lm.server for one model.
func (l *ExecLauncher) Launch(ctx context.Context, spec Spec) (Process, error) {
	if err := l.Precheck(spec); err != nil {
		return nil, err
	}
	python := l.Paths.VenvPython()
	cmd := exec.Command(python, launchArgs(spec)...)
	cmd.Env = append(os.Environ(),
		// Without an existing HF_HUB_CACHE directory, mlx_lm.server raises
		// CacheNotFound while serving /v1/models and returns an empty 200.
		"HF_HOME="+filepath.Dir(l.Paths.HFCache),
		"HF_HUB_CACHE="+l.Paths.HFCache,
		// Inference must never reach the network: everything it needs is already
		// in ModelPath, and a stray download would stall a request for minutes.
		"HF_HUB_OFFLINE=1",
		"PYTHONUNBUFFERED=1",
	)
	// Put the child in its own process group so we can signal the whole group;
	// mlx_lm can spawn helpers that would otherwise outlive it.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	logPath := filepath.Join(l.LogDir, logFileName(spec.RepoID))
	// EnsureDirs created LogDir at startup as a real directory. Re-check rather
	// than MkdirAll: a path-based MkdirAll would follow a link left under that
	// name and put this account's log file inside a directory it did not choose.
	if fi, err := os.Lstat(l.LogDir); err != nil {
		return nil, fmt.Errorf("log directory %s: %w", l.LogDir, err)
	} else if !fi.IsDir() {
		return nil, fmt.Errorf("log directory %s is not a directory", l.LogDir)
	}
	// 0600, not the 0644 os.Create would give: the model server logs at INFO —
	// request-level detail nobody else has business reading. O_TRUNC keeps the
	// per-model log from growing without bound across restarts.
	//
	// LogDir is this account's own directory (config.Paths.Logs resolves through
	// accountDir), which is what makes the open reachable at all: while the logs
	// sat in the shared root, one account's 0600 log under a name derived from
	// the repo id meant the NEXT account's O_CREATE|O_TRUNC returned EACCES and
	// the model would not start for it.
	//
	// The hardening stays. The name is predictable, and a link or a FIFO left
	// under it — by anything that can write this directory, or by an older
	// install that kept logs elsewhere — would let a truncating open empty, then
	// stream logs into, any file this account can write. O_NOFOLLOW refuses the
	// link; O_NONBLOCK keeps a planted FIFO from blocking the open forever (and
	// is inert on the regular file the fstat below guarantees); the fstat on the
	// opened handle refuses anything else that is not a regular file.
	logFile, err := os.OpenFile(logPath,
		os.O_CREATE|os.O_WRONLY|os.O_TRUNC|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, fmt.Errorf("create log %s: %w", logPath, err)
	}
	if info, err := logFile.Stat(); err != nil || !info.Mode().IsRegular() {
		logFile.Close()
		if err == nil {
			err = fmt.Errorf("not a regular file")
		}
		return nil, fmt.Errorf("create log %s: %w", logPath, err)
	}
	cmd.Stdout = logFile
	cmd.Stderr = logFile

	if err := cmd.Start(); err != nil {
		logFile.Close()
		return nil, fmt.Errorf("start mlx_lm.server: %w", err)
	}

	// Record the child's process group so a future run can reap it if we crash
	// before Stop runs. Setpgid makes the child lead its own group (pgid == pid).
	pgid := cmd.Process.Pid
	l.pidLedger().add(pgid)

	p := &execProcess{
		cmd:     cmd,
		log:     logFile,
		logPath: logPath,
		done:    make(chan struct{}),
		ledger:  l.pidLedger(),
		pgid:    pgid,
	}
	go func() {
		err := cmd.Wait()
		p.mu.Lock()
		p.err = err
		p.mu.Unlock()
		logFile.Close()
		// The process is gone; drop it from the crash-recovery ledger.
		p.ledger.remove(p.pgid)
		close(p.done)
	}()
	return p, nil
}

// logFileName turns a repo id into a safe filename. The separator must be a
// character ValidRepoID rejects: with one the id itself can contain (an
// underscore, say), "a/b_c" and "a_b/c" would share a file, and launching the
// second model would truncate the first one's live log.
func logFileName(repoID string) string {
	safe := make([]rune, 0, len(repoID))
	for _, r := range repoID {
		if r == '/' || r == ' ' {
			r = '@'
		}
		safe = append(safe, r)
	}
	return string(safe) + ".log"
}

type execProcess struct {
	cmd     *exec.Cmd
	log     *os.File
	logPath string
	done    chan struct{}
	ledger  *pidLedger
	pgid    int

	mu  sync.Mutex
	err error
}

func (p *execProcess) Done() <-chan struct{} { return p.done }
func (p *execProcess) Pid() int              { return p.cmd.Process.Pid }

func (p *execProcess) Err() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.err
}

// Stop asks the process group to exit, escalating to SIGKILL if it will not.
func (p *execProcess) Stop(ctx context.Context) error {
	select {
	case <-p.done:
		return nil // already gone
	default:
	}

	pgid := -p.cmd.Process.Pid // negative pid signals the whole group
	_ = syscall.Kill(pgid, syscall.SIGTERM)

	deadline := 10 * time.Second
	if dl, ok := ctx.Deadline(); ok {
		if d := time.Until(dl); d < deadline {
			deadline = d
		}
	}

	select {
	case <-p.done:
		return nil
	case <-time.After(deadline):
		// A model server wedged mid-generation will not honor SIGTERM. Do not
		// leave it holding gigabytes of GPU memory.
		_ = syscall.Kill(pgid, syscall.SIGKILL)
		select {
		case <-p.done:
			return nil
		case <-time.After(5 * time.Second):
			return errors.New("model server would not die, even after SIGKILL")
		}
	}
}

// LogPath is where this process's output is being written.
func (p *execProcess) LogPath() string { return p.logPath }

// freePort asks the kernel for an unused loopback TCP port.
//
// There is an unavoidable race between closing the listener and the child
// binding the port. It is tolerable here because the ports are loopback-only and
// handed out one at a time, and because a collision surfaces immediately as a
// failed readiness probe rather than as silent corruption.
func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}
