package runtime

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/intentdriven/Gropius/internal/config"
)

// mlxRequirements is the fully-resolved, hash-locked dependency set for the
// pinned mlx-lm. Installing with `uv pip install --require-hashes` against this
// extends the "exact bytes or fail" guarantee — the one the SHA-256-pinned uv
// binary already gives — to the entire Python payload: a compromised or
// republished PyPI package, or dependency confusion on a transitive name, is
// rejected by hash rather than silently executed under the user's account.
//
// Regenerate when bumping mlxLMVersion (needs the pinned uv, macOS/arm64):
//
//	echo "mlx-lm==<version>" > requirements.in
//	uv pip compile requirements.in --generate-hashes --python-version 3.12 \
//	    -o internal/runtime/mlx-requirements.txt
//
//go:embed mlx-requirements.txt
var mlxRequirements []byte

// mlxPin is the exact MLX stack Gropius installs.
//
// Pinned deliberately: an unpinned `uv pip install mlx-lm` resolves differently
// on different days, and a silent minor bump in mlx-lm has repeatedly changed
// server flags and response shapes. These versions are the ones Gropius is
// tested against.
const (
	mlxLMVersion  = "0.31.3"
	pythonVersion = "3.12"
)

// SetupStage is a step in first-run provisioning.
type SetupStage string

const (
	StageIdle   SetupStage = "idle"
	StageUV     SetupStage = "installing uv"
	StagePython SetupStage = "installing Python " + pythonVersion
	StageMLX    SetupStage = "installing MLX"
	StageReady  SetupStage = "ready"
	StageFailed SetupStage = "failed"
)

// SetupStatus is the current provisioning state, for the UI.
type SetupStatus struct {
	Stage SetupStage `json:"stage"`
	// Detail is a human-readable line, e.g. the current pip output.
	Detail string `json:"detail"`
	Err    string `json:"err,omitempty"`
	Ready  bool   `json:"ready"`
}

// Provisioner installs and verifies the private Python runtime.
//
// Everything it creates lives under Paths.Root, so uninstalling Gropius is
// `rm -rf` of one directory. It never touches the user's own Python.
type Provisioner struct {
	// Owner is the uid the runtime's executables must be owned by (root is
	// always accepted). Zero means the current effective uid; tests set it to
	// simulate files another account planted.
	Owner int
	Paths config.Paths

	mu     sync.Mutex
	status SetupStatus
}

// NewProvisioner creates a Provisioner.
func NewProvisioner(paths config.Paths) *Provisioner {
	return &Provisioner{
		Paths:  paths,
		status: SetupStatus{Stage: StageIdle},
	}
}

// Status returns the current provisioning status.
func (p *Provisioner) Status() SetupStatus {
	p.mu.Lock()
	defer p.mu.Unlock()
	s := p.status
	s.Ready = p.installed()
	return s
}

func (p *Provisioner) setStatus(stage SetupStage, detail, errMsg string) {
	p.mu.Lock()
	p.status = SetupStatus{Stage: stage, Detail: detail, Err: errMsg}
	p.mu.Unlock()
}

// trustedExecutable refuses an executable this account should not run: one
// that is not a regular file (after following the venv's interpreter symlink
// to its uv-managed target), that is writable by group or other, or that is
// not owned by owner (the account about to execute it) or by root. In
// shared-cache mode the runtime sits under a setgid staff root: a
// group-writable file is one any local account can rewrite in place, and an
// absent name there is one any account can claim first with a file of its
// own — mode bits are not provenance, only ownership is. uv has been
// observed to write parts of its CPython tree group-writable; lockdown strips
// that before this check runs, so a runtime this account provisioned passes.
// A runtime another account provisioned does not: reusing it is the design
// decision the ledger records, and until it is made the refusal is loud.
func trustedExecutable(path string, owner int) error {
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", path)
	}
	if fi.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("%s is writable by other accounts (mode %o) — refusing to run it", path, fi.Mode().Perm())
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("%s: cannot determine the owner — refusing to run it", path)
	}
	if st.Uid != 0 && int(st.Uid) != owner {
		return fmt.Errorf("%s is owned by another account (uid %d) — refusing to run it", path, st.Uid)
	}
	return nil
}

// ownerOrSelf resolves the Owner seam: zero means the current effective uid.
func ownerOrSelf(owner int) int {
	if owner == 0 {
		return os.Geteuid()
	}
	return owner
}

// lockdown strips group and other write permission from every regular file
// and directory under dir, following no symlinks. uv installs CPython
// group-writable; under a setgid shared root that would hand every local
// account write access to the interpreter each of them runs. Errors are
// ignored — a file another account owns cannot be re-moded by this one, and
// trustedExecutable is what decides whether the result is runnable.
func lockdown(dir string) {
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if perm := info.Mode().Perm(); perm&0o022 != 0 {
			_ = os.Chmod(path, perm&^0o022)
		}
		return nil
	})
}

// installed reports whether the MLX runtime is usable.
func (p *Provisioner) installed() bool {
	if err := trustedExecutable(p.Paths.VenvPython(), ownerOrSelf(p.Owner)); err != nil {
		return false
	}
	// The venv existing is not enough — a half-finished pip install leaves the
	// interpreter in place without mlx_lm.
	marker := filepath.Join(p.Paths.Venv, ".gropius-mlx-"+mlxLMVersion)
	_, err := os.Stat(marker)
	return err == nil
}

// Installed reports whether the runtime is ready to serve models.
func (p *Provisioner) Installed() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.installed()
}

// Ensure installs the runtime if it is not already present. It is idempotent and
// safe to call on every launch.
func (p *Provisioner) Ensure(ctx context.Context) error {
	// Close what uv may have left open before judging the install, so an
	// existing runtime is repaired in place rather than reinstalled.
	lockdown(p.Paths.Venv)
	lockdown(p.Paths.Python)
	lockdown(p.Paths.Bin)
	if p.Installed() {
		p.setStatus(StageReady, "MLX "+mlxLMVersion+" is installed", "")
		return nil
	}

	if err := p.Paths.EnsureDirs(); err != nil {
		p.setStatus(StageFailed, "", err.Error())
		return err
	}

	steps := []struct {
		stage SetupStage
		run   func(context.Context) error
	}{
		{StageUV, p.ensureUV},
		{StagePython, p.ensureVenv},
		{StageMLX, p.ensureMLX},
	}
	for _, s := range steps {
		p.setStatus(s.stage, "", "")
		if err := s.run(ctx); err != nil {
			p.setStatus(StageFailed, "", err.Error())
			return fmt.Errorf("%s: %w", s.stage, err)
		}
	}
	// Close what uv left open, then confirm the result is something this
	// account may run — loudly, rather than reporting ready and refusing at
	// every launch. A file another account owns stays as it is and fails here.
	lockdown(p.Paths.Venv)
	lockdown(p.Paths.Python)
	lockdown(p.Paths.Bin)
	if err := trustedExecutable(p.Paths.VenvPython(), ownerOrSelf(p.Owner)); err != nil {
		p.setStatus(StageFailed, "", err.Error())
		return err
	}

	p.setStatus(StageReady, "MLX "+mlxLMVersion+" is installed", "")
	return nil
}

// uvVersion pins the exact uv release Gropius installs, and uvSHA256 the
// expected digest of its macOS release tarball per architecture. Pinning both
// turns "run whatever astral.sh serves today through sh" into "install these
// exact bytes or fail": a compromised CDN, a tampered release, or a
// truncated download all stop at the hash check instead of executing.
const uvVersion = "0.11.29"

var uvSHA256 = map[string]string{
	"arm64": "61c04acc52a33ef0f331e494bdfbedcdb6c26c6970c022ed3699e5860f8930e3", // uv-aarch64-apple-darwin.tar.gz
	"amd64": "c4c4de482da9ccdd076dc4fb5cfe7b740609029385c72f58606be3153602387d", // uv-x86_64-apple-darwin.tar.gz
}

// uvArch maps GOARCH onto uv's release-artifact naming.
var uvArch = map[string]string{
	"arm64": "aarch64",
	"amd64": "x86_64",
}

// maxUVArchive bounds how much of the release download we are willing to
// buffer. The real tarball is ~20 MB; anything near this limit is not uv.
const maxUVArchive = 256 << 20

// uvBaseURL is a var only so tests can point ensureUV at a local server.
var uvBaseURL = "https://github.com/astral-sh/uv/releases/download"

// ensureUV downloads the pinned uv release, verifies its SHA-256, and installs
// the binary into the app directory. No shell, no installer script.
func (p *Provisioner) ensureUV(ctx context.Context) error {
	if trustedExecutable(p.Paths.UV(), ownerOrSelf(p.Owner)) == nil {
		return nil
	}
	arch, ok := uvArch[goruntime.GOARCH]
	if !ok {
		return fmt.Errorf("no pinned uv build for %s/%s", goruntime.GOOS, goruntime.GOARCH)
	}

	url := fmt.Sprintf("%s/%s/uv-%s-apple-darwin.tar.gz", uvBaseURL, uvVersion, arch)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	// A bounded client, not http.DefaultClient: provisioning runs on a background
	// context with no deadline, so a stalled or slow-loris peer on this download
	// would otherwise hang first-run setup indefinitely. Integrity is unaffected
	// either way — the SHA-256 pin below still gates execution — this only bounds
	// the wait. 10 minutes is far beyond a real ~20 MB fetch.
	client := &http.Client{Timeout: 10 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("download uv %s: %w", uvVersion, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download uv %s: %s from %s", uvVersion, resp.Status, url)
	}
	archive, err := io.ReadAll(io.LimitReader(resp.Body, maxUVArchive+1))
	if err != nil {
		return fmt.Errorf("download uv %s: %w", uvVersion, err)
	}
	if len(archive) > maxUVArchive {
		return fmt.Errorf("uv download exceeds %d bytes — refusing it", maxUVArchive)
	}

	// The hash check is the security boundary: only after the whole artifact
	// matches the pinned digest do any of its bytes get interpreted.
	sum := sha256.Sum256(archive)
	if got := hex.EncodeToString(sum[:]); got != uvSHA256[goruntime.GOARCH] {
		return fmt.Errorf("uv %s download failed SHA-256 verification (got %s) — refusing to install it", uvVersion, got)
	}

	bin, err := extractUV(archive)
	if err != nil {
		return fmt.Errorf("extract uv %s: %w", uvVersion, err)
	}

	// Random temp name + rename: never leave a half-written binary at the final
	// path, and never write through a name another account could pre-plant in a
	// shared root.
	tmp, err := os.CreateTemp(p.Paths.Bin, ".uv-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once the rename succeeds
	if err := tmp.Chmod(0o755); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(bin); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, p.Paths.UV())
}

// extractUV returns the "uv" binary from the release tarball.
func extractUV(archive []byte) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil, fmt.Errorf("no uv binary in archive")
		}
		if err != nil {
			return nil, err
		}
		if hdr.Typeflag != tar.TypeReg || filepath.Base(hdr.Name) != "uv" {
			continue
		}
		bin, err := io.ReadAll(io.LimitReader(tr, maxUVArchive))
		if err != nil {
			return nil, err
		}
		return bin, nil
	}
}

// ensureVenv creates a virtualenv on a private, pinned CPython.
func (p *Provisioner) ensureVenv(ctx context.Context) error {
	if _, err := os.Stat(p.Paths.VenvPython()); err == nil {
		return nil
	}
	cmd := exec.CommandContext(ctx, p.Paths.UV(),
		"venv", "--python", pythonVersion, p.Paths.Venv)
	cmd.Env = p.uvEnv()
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("create venv: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// ensureMLX installs the pinned, hash-locked mlx-lm stack into the venv and
// verifies it imports.
func (p *Provisioner) ensureMLX(ctx context.Context) error {
	// Install from the embedded, fully-resolved lock with --require-hashes, so
	// every wheel (mlx-lm and its whole transitive tree) must match a hash baked
	// into the binary or the install fails. This closes the gap a bare
	// `mlx-lm==<v>` left open: version-pinning stops drift, not a compromised or
	// republished PyPI package. uv reads the requirements from stdin ("-r -").
	cmd := exec.CommandContext(ctx, p.Paths.UV(),
		"pip", "install",
		"--python", p.Paths.VenvPython(),
		"--require-hashes",
		"-r", "-",
	)
	cmd.Env = p.uvEnv()
	cmd.Stdin = bytes.NewReader(mlxRequirements)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("install mlx-lm: %w: %s", err, tail(string(out), 500))
	}

	// Import it for real. A wheel can install cleanly and still fail to load —
	// wrong architecture, missing Metal — and finding that out here is far better
	// than at first inference.
	check := exec.CommandContext(ctx, p.Paths.VenvPython(), "-c",
		`import mlx.core as mx, mlx_lm; assert mx.metal.is_available(); print(mx.__version__)`)
	out, err := check.CombinedOutput()
	if err != nil {
		return fmt.Errorf("MLX installed but will not run on this machine: %w: %s",
			err, tail(string(out), 500))
	}

	marker := filepath.Join(p.Paths.Venv, ".gropius-mlx-"+mlxLMVersion)
	if err := os.WriteFile(marker, []byte(strings.TrimSpace(string(out))), 0o644); err != nil {
		return err
	}
	return nil
}

// uvEnv keeps uv's Python downloads inside the app directory.
func (p *Provisioner) uvEnv() []string {
	return append(os.Environ(),
		"UV_PYTHON_INSTALL_DIR="+p.Paths.Python,
		"UV_NO_MODIFY_PATH=1",
	)
}

// Uninstall removes the managed Python runtime (but not downloaded models).
func (p *Provisioner) Uninstall() error {
	for _, d := range []string{p.Paths.Venv, p.Paths.Python, p.Paths.Bin} {
		if err := os.RemoveAll(d); err != nil {
			return err
		}
	}
	p.setStatus(StageIdle, "", "")
	return nil
}

// tail returns the last n characters of s, which is where a failing pip puts the
// actual error.
func tail(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return "..." + s[len(s)-n:]
}

// ProbeTimeout is how long the readiness probe waits for a freshly spawned
// model server before giving up on the whole load.
const ProbeTimeout = 10 * time.Minute
