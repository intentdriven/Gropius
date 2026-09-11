package lifecycle

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/intentdriven/Gropius/internal/config"
)

// The seams a real run uses are the ones a table over fakes can say nothing
// about. They are small and they are where the verbs meet the Mac, which is
// exactly where a silent mistake would live: a decode that reads nothing, a
// writability test that answers for the wrong directory, a settings read that
// calls a missing file a fault.

// fetchState reads the snapshot the control panel polls, over loopback.
func TestFetchStateReadsTheSnapshot(t *testing.T) {
	body := `{"config":{"host":"0.0.0.0","port":11535},
	          "bind":{"mode":"private-network","selected":"192.0.2.7"},
	          "resident":[{"repo_id":"example-org/example-model-4bit","state":"loaded"}]}`
	srv := serveOnLoopback(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/state" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	})

	state, err := fetchState(portOfServer(t, srv))
	if err != nil {
		t.Fatalf("fetchState: %v", err)
	}
	if state.Config.Host != "0.0.0.0" || state.Config.Port != 11535 {
		t.Errorf("config = %+v", state.Config)
	}
	if state.Bind.Selected != "192.0.2.7" {
		t.Errorf("bind = %+v", state.Bind)
	}
	if len(state.Resident) != 1 || state.Resident[0].State != "loaded" {
		t.Errorf("resident = %+v", state.Resident)
	}
}

// A control plane that refuses, or answers with something that is not the
// snapshot, is an error rather than an empty state — status says what it could
// not read instead of reporting a server with no address and no models.
func TestFetchStateRefusesWhatIsNotASnapshot(t *testing.T) {
	refusing := serveOnLoopback(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "forbidden", http.StatusForbidden)
	})
	if _, err := fetchState(portOfServer(t, refusing)); err == nil {
		t.Error("fetchState accepted a refusal as a snapshot")
	}

	garbage := serveOnLoopback(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("<html>not this</html>"))
	})
	if _, err := fetchState(portOfServer(t, garbage)); err == nil {
		t.Error("fetchState accepted a page as a snapshot")
	}
}

func TestWritableDirAnswersForTheDirectoryItWasGiven(t *testing.T) {
	dir := t.TempDir()
	if err := writableDir(dir); err != nil {
		t.Errorf("writableDir(%q) = %v, want nil for a directory this account owns", dir, err)
	}
	if err := writableDir(filepath.Join(dir, "not-there")); err == nil {
		t.Error("writableDir called a directory that does not exist writable")
	}
	// And it leaves nothing behind: the probe file is removed, so a doctor run
	// does not litter the root it is reporting on.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("writableDir left %d files in the root it tested", len(entries))
	}
}

func TestLoadSettingsReportsWhatTheFileDid(t *testing.T) {
	dir := t.TempDir()
	absent := filepath.Join(dir, "config.json")
	if s := loadSettings(absent); s.Present || s.Err != nil {
		t.Errorf("a missing settings file reported as %+v, want absent and no fault", s)
	}

	good := filepath.Join(dir, "good.json")
	if err := os.WriteFile(good, []byte(`{"port":11999}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if s := loadSettings(good); !s.Present || s.Err != nil || !s.Notices.Empty() {
		t.Errorf("a file that loads reported as %+v", s)
	}

	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte(`{"port":`), 0o600); err != nil {
		t.Fatal(err)
	}
	if s := loadSettings(bad); !s.Present || s.Err == nil {
		t.Errorf("a file that will not parse reported as %+v", s)
	}
}

// Detect against a real stream: a file is not a terminal, so it takes neither
// colour nor a redrawn line.
func TestDetectOnARealStream(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "stream-*")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	term := Detect(f, func(string) (string, bool) { return "", false })
	if term.Color || term.Redraw {
		t.Errorf("Detect on a file = %+v, want neither colour nor redraw", term)
	}
	if term.Out != f {
		t.Error("Detect did not carry the stream it was given")
	}
	if isTerminal(nil) {
		t.Error("a stream that is not there is not a terminal")
	}
}

// The two entry points, end to end in this package: a temporary root, a port
// nothing is listening on, and the real seams behind them.
func TestTheVerbEntryPointsRunAgainstATemporaryRoot(t *testing.T) {
	env, out, errOut := testEnv()
	root := t.TempDir()
	env.Paths = config.NewPaths(root)
	env.Port = freePort(t)

	if code := RunStatus(env, []string{"--json"}); code != ExitOK {
		t.Fatalf("RunStatus exit = %d (%s)", code, errOut)
	}
	var s Status
	if err := json.Unmarshal(out.Bytes(), &s); err != nil {
		t.Fatalf("RunStatus wrote no JSON: %v\n%s", err, out)
	}
	if s.Serving {
		t.Error("RunStatus reports a server on a port nothing is listening on")
	}

	out.Reset()
	// Nothing here is a fault Gropius owns — a temporary root is writable, the
	// settings file is absent, the runtime is not installed and no server is on
	// the port — so doctor reports warnings and exits zero. This is also what
	// covers the live checks: the provisioner read, the writability probe, the
	// settings read and the system firewall query.
	if code := RunDoctor(env, nil); code != ExitOK {
		t.Fatalf("RunDoctor exit = %d, want %d\n%s\n%s", code, ExitOK, out, errOut)
	}
	for _, want := range []string{runtimeCheckName, rootCheckName, settingsCheckName, firewallCheckName, localNetworkCheckName} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("RunDoctor did not report %q:\n%s", want, out)
		}
	}
}

func serveOnLoopback(t *testing.T, h http.HandlerFunc) *httptest.Server {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := httptest.NewUnstartedServer(h)
	srv.Listener = ln
	srv.Start()
	t.Cleanup(srv.Close)
	return srv
}

func portOfServer(t *testing.T, srv *httptest.Server) int {
	t.Helper()
	_, port, err := net.SplitHostPort(srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	n, err := strconv.Atoi(port)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	ln.Close()
	n, _ := strconv.Atoi(port)
	return n
}
