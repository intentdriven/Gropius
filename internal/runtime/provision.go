package runtime

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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

// ensureUV downloads the uv binary into the app directory.
func (p *Provisioner) ensureUV(ctx context.Context) error {
	if _, err := os.Stat(p.Paths.UV()); err == nil {
		return nil
	}
	// uv's installer script honours UV_UNMANAGED_INSTALL, which puts the binary
	// exactly where we ask and skips any shell-profile modification.
	script := `curl -LsSf https://astral.sh/uv/install.sh | sh`
	cmd := exec.CommandContext(ctx, "sh", "-c", script)
	cmd.Env = append(os.Environ(),
		"UV_UNMANAGED_INSTALL="+p.Paths.Bin,
		"UV_INSTALL_DIR="+p.Paths.Bin,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("install uv: %w: %s", err, strings.TrimSpace(string(out)))
	}
	if _, err := os.Stat(p.Paths.UV()); err != nil {
		return fmt.Errorf("uv installer finished but no binary at %s", p.Paths.UV())
	}
	return nil
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
