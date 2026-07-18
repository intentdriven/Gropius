package runtime

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"sync"
	"time"

	"github.com/intentdriven/Gropius/internal/config"
)

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

// installed reports whether the MLX runtime is usable.
func (p *Provisioner) installed() bool {
	_, err := os.Stat(p.Paths.VenvPython())
	if err != nil {
		return false
	}
	// The venv existing is not enough — a half-finished pip install leaves the
	// interpreter in place without mlx_lm.
	marker := filepath.Join(p.Paths.Venv, ".gropius-mlx-"+mlxLMVersion)
	_, err = os.Stat(marker)
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
	if _, err := os.Stat(p.Paths.UV()); err == nil {
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
	resp, err := http.DefaultClient.Do(req)
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

// ensureMLX installs the pinned mlx-lm into the venv and verifies it imports.
func (p *Provisioner) ensureMLX(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, p.Paths.UV(),
		"pip", "install",
		"--python", p.Paths.VenvPython(),
		"mlx-lm=="+mlxLMVersion,
	)
	cmd.Env = p.uvEnv()
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
