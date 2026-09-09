package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/intentdriven/Gropius/internal/app"
	"github.com/intentdriven/Gropius/internal/capability"
	"github.com/intentdriven/Gropius/internal/config"
	"github.com/intentdriven/Gropius/internal/mlxtest"
	"github.com/intentdriven/Gropius/internal/runtime"
)

// wedgedLauncher stands up a real fake server, and its processes do not exit
// when they are stopped: the state a model server is in between being killed
// and the kernel letting go of its memory.
type wedgedLauncher struct{}

type wedgedProcess struct {
	srv  *mlxtest.Server
	done chan struct{}
	once sync.Once
}

func (p *wedgedProcess) Stop(context.Context) error {
	// The socket goes, the process does not: Done stays open.
	p.once.Do(func() { p.srv.Close() })
	return nil
}
func (p *wedgedProcess) Done() <-chan struct{} { return p.done }
func (p *wedgedProcess) Err() error            { return nil }
func (p *wedgedProcess) Pid() int              { return 4242 }

func (l *wedgedLauncher) Precheck(runtime.Spec) error { return nil }

func (l *wedgedLauncher) Launch(_ context.Context, spec runtime.Spec) (runtime.Process, error) {
	srv := mlxtest.Start(mlxtest.Options{ModelArg: spec.ModelPath, Port: spec.Port})
	return &wedgedProcess{srv: srv, done: make(chan struct{})}, nil
}

// sizedSource resolves one model at a size the test chooses, so the pool's
// memory accounting can be driven without a file of that size on disk.
type sizedSource struct {
	path string
	size int64
}

func (s sizedSource) Resolve(string) (string, int64, error) { return s.path, s.size, nil }

// Memory held by a model server that was stopped and would not go is memory the
// pool will not hand out, and it belongs to no model in the list — so a panel
// built only from the models it is holding would show room that does not exist.
// The state snapshot carries it, counts the servers, folds it into the charged
// total the over-budget verdict is made from, and says so in a warning.
func TestThePanelReportsMemoryHeldByAServerThatWouldNotStop(t *testing.T) {
	const modelSize = 2 * gb
	paths := config.NewPaths(t.TempDir())
	l := &wedgedLauncher{}
	a, err := app.New(app.Options{
		Paths: paths, Config: config.Default(), Launcher: l,
		PhysicalMemory: func() int64 { return 128 * gb },
	})
	if err != nil {
		t.Fatalf("app.New: %v", err)
	}
	t.Cleanup(func() { a.Close() })

	dir := paths.ModelDir("org/wedged")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "weights.safetensors"), []byte("w"), 0o644); err != nil {
		t.Fatal(err)
	}

	// A pool that gives up on a stop quickly, so the test does not sit out the
	// real bound. Everything else is the composition root's own wiring.
	old := a.Pool
	a.Pool = runtime.NewPool(runtime.PoolOptions{
		Launcher:         l,
		Models:           sizedSource{path: dir, size: modelSize},
		MaxResidentBytes: 8 * gb,
		DrainWait:        150 * time.Millisecond,
	})
	old.Close()

	ctrl := &Control{App: a}
	mux := http.NewServeMux()
	ctrl.Routes(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	_, release, err := a.Pool.Acquire(context.Background(), "org/wedged")
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	release()
	if err := a.Pool.Unload("org/wedged"); err != nil {
		t.Fatalf("Unload: %v", err)
	}

	charge := capability.LoadCost(modelSize)
	deadline := time.Now().Add(10 * time.Second)
	for {
		st := stateOf(t, srv)
		if st.Machine.StuckServers == 1 {
			if st.Machine.ExitingBytes != charge {
				t.Errorf("machine.exiting_bytes = %d, want the charge the wedged server holds %d",
					st.Machine.ExitingBytes, charge)
			}
			// It is in no models list, so the charged total is the only place a
			// reader could learn the memory is spoken for.
			if len(st.Resident) != 0 {
				t.Errorf("resident = %+v, want none: the model was unloaded", st.Resident)
			}
			if st.Machine.ResidentBytes != charge {
				t.Errorf("machine.resident_bytes = %d, want it to include the %d still held",
					st.Machine.ResidentBytes, charge)
			}
			var said bool
			for _, w := range st.Warnings {
				if strings.Contains(w, "stopped and have not exited") {
					said = true
				}
			}
			if !said {
				t.Errorf("no warning about the memory being held: %q", st.Warnings)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("machine.stuck_servers = %d, exiting_bytes = %d; the panel never reported the wedged server",
				st.Machine.StuckServers, st.Machine.ExitingBytes)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
