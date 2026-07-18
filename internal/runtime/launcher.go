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
}

// ExecLauncher runs the real `mlx_lm.server` out of the managed virtualenv.
type ExecLauncher struct {
	Paths config.Paths
	// LogDir receives one log file per model process.
	LogDir string

	ledgerOnce sync.Once
	ledger     *pidLedger
}

func (l *ExecLauncher) pidLedger() *pidLedger {
	l.ledgerOnce.Do(func() { l.ledger = newPIDLedger(l.Paths.Root) })
	return l.ledger
}

// ReapOrphans kills any model servers left running by a previous, crashed run.
// Call once at startup before launching anything.
func (l *ExecLauncher) ReapOrphans() int {
	return l.pidLedger().reapOrphans()
}

// Launch spawns mlx_lm.server for one model.
func (l *ExecLauncher) Launch(ctx context.Context, spec Spec) (Process, error) {
	python := l.Paths.VenvPython()
	if _, err := os.Stat(python); err != nil {
		return nil, fmt.Errorf("python runtime is not installed (%s): %w", python, err)
	}
	if _, err := os.Stat(spec.ModelPath); err != nil {
		return nil, fmt.Errorf("model directory is missing (%s): %w", spec.ModelPath, err)
	}

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
	if spec.DecodeConcurrency > 1 {
		args = append(args, "--decode-concurrency", strconv.Itoa(spec.DecodeConcurrency))
	}

	cmd := exec.Command(python, args...)
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
	if err := os.MkdirAll(l.LogDir, 0o755); err != nil {
		return nil, err
	}
	// 0600, not the 0644 os.Create would give. In shared mode LogDir sits under
	// the group-readable /Users/Shared/Gropius, and the model server logs at
	// INFO — request-level detail another local account has no business reading.
	// O_TRUNC keeps the per-model log from growing without bound across restarts.
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
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

// logFileName turns a repo id into a safe filename.
func logFileName(repoID string) string {
	safe := make([]rune, 0, len(repoID))
	for _, r := range repoID {
		if r == '/' || r == ' ' {
			r = '_'
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
		// A model server wedged mid-generation will not honour SIGTERM. Do not
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
