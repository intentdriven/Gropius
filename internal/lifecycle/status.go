package lifecycle

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/intentdriven/Gropius/internal/config"
	"github.com/intentdriven/Gropius/internal/instance"
)

// maxStateBytes caps what status will read from the control plane. The snapshot
// is small; a cap means a wedged or hostile responder on the port cannot make a
// terminal command read forever.
const maxStateBytes = 4 << 20

// Status is what `gropius status` answers.
//
// The JSON is the contract — the menu bar polls it and scripts parse it — and
// the human rendering is a rendering of this same value. A field renamed here
// is a break for somebody, which is why the shape is held by a fixture.
type Status struct {
	// Version is the build that answered, which is this binary and not
	// necessarily the build that is serving: on a Mac where another account
	// holds the port, the server is that account's copy.
	Version string `json:"version"`
	Serving bool   `json:"serving"`
	Port    int    `json:"port"`
	// Address is host:port as the server bound it, empty when nothing is
	// serving or when the server would not say.
	Address string `json:"address,omitempty"`
	// Reason says why this is not the plain answer: nothing on the port,
	// somebody else on the port, or a server that did not answer.
	Reason string `json:"reason,omitempty"`
	// Resident is the models in memory. Always a list, never null: a caller
	// ranging over it should not have to tell three kinds of nothing apart.
	Resident []ResidentModel `json:"resident"`
}

// ResidentModel is one model in memory, as status reports it.
type ResidentModel struct {
	RepoID string `json:"repo_id"`
	State  string `json:"state"`
}

// ServerState is the part of the running server's control-plane snapshot that
// status reads.
//
// Decoded structurally rather than by importing the gateway's own type, and
// that is a rule rather than a convenience: nothing on the control plane's path
// may see this package, so the dependency may not run the other way either
// (adr-2609111126115848 condition 3). What this costs is that a field renamed
// in the gateway goes unnoticed here, which is what the status contract's
// fixture and the panel-parity work are for.
type ServerState struct {
	Config struct {
		Host string `json:"host"`
		Port int    `json:"port"`
	} `json:"config"`
	Bind struct {
		Mode string `json:"mode"`
		// Selected is the address the private-network mode bound. Under that
		// mode Config.Host is whatever the operator last set and is not what
		// the server is answering on.
		Selected string `json:"selected"`
	} `json:"bind"`
	Resident []ResidentModel `json:"resident"`
}

// StatusEnv is what status is allowed to ask, and it is deliberately two
// questions: who holds the port, and what the snapshot the panel already polls
// says. Both are reads of state that exists; neither provisions, walks the
// model directory or reaches the network.
type StatusEnv struct {
	Version string
	Port    int
	// Holder classifies the process on the port through the instance
	// challenge — the same primitive the server's own singleton election uses.
	Holder func() instance.Holder
	// State reads the running server's snapshot over the loopback control
	// plane. It is asked only when the holder proved it is ours.
	State func() (ServerState, error)
}

// StatusOf answers from the environment's two reads. It is pure over them, so
// every case below is a test with no server, no port and no Mac.
func StatusOf(env StatusEnv) Status {
	s := Status{
		Version:  env.Version,
		Port:     env.Port,
		Resident: []ResidentModel{},
	}
	switch env.Holder() {
	case instance.HolderOurs:
		// The challenge answered, so the server is there whatever happens
		// next.
		s.Serving = true
	case instance.HolderForeign:
		s.Reason = "port " + strconv.Itoa(env.Port) + " is held by a process that could not prove it is this account's Gropius"
		return s
	default: // instance.HolderNone
		s.Reason = "nothing is serving on port " + strconv.Itoa(env.Port)
		return s
	}

	state, err := env.State()
	if err != nil {
		s.Reason = "the server on port " + strconv.Itoa(env.Port) + " did not answer the control plane, so its address and models are not reported"
		return s
	}
	host := state.Config.Host
	if state.Bind.Selected != "" {
		host = state.Bind.Selected
	}
	port := state.Config.Port
	if port == 0 {
		port = env.Port
	}
	if host != "" {
		s.Address = net.JoinHostPort(host, strconv.Itoa(port))
	}
	if len(state.Resident) > 0 {
		s.Resident = state.Resident
	}
	return s
}

// RenderStatus writes the human form of the same value the JSON carries.
func RenderStatus(t Terminal, s Status) {
	if s.Serving {
		fmt.Fprintln(t.Out, t.Paint(Green, "serving")+"  "+s.Address)
	} else {
		fmt.Fprintln(t.Out, t.Paint(Yellow, "not serving"))
	}
	if s.Reason != "" {
		fmt.Fprintln(t.Out, "  "+s.Reason)
	}
	if len(s.Resident) == 0 {
		fmt.Fprintln(t.Out, "  no models in memory")
	}
	for _, m := range s.Resident {
		fmt.Fprintln(t.Out, "  "+m.RepoID+"  "+m.State)
	}
	fmt.Fprintln(t.Out, t.Paint(Dim, "  this build: "+s.Version))
}

// liveStatusEnv is the StatusEnv a real run uses: the instance challenge
// against this account's data root, and a loopback read of the control plane.
func liveStatusEnv(version string, paths config.Paths, port int) StatusEnv {
	return StatusEnv{
		Version: version,
		Port:    port,
		Holder:  func() instance.Holder { return instance.ProbeExisting(paths, port) },
		State:   func() (ServerState, error) { return fetchState(port) },
	}
}

// fetchState reads the snapshot the control panel polls.
func fetchState(port int) (ServerState, error) {
	body, err := controlPlaneGet(port, "/api/state")
	if err != nil {
		return ServerState{}, err
	}
	var state ServerState
	if err := json.Unmarshal(body, &state); err != nil {
		return ServerState{}, err
	}
	return state, nil
}

// controlPlaneGet is the ONE place this package reads the running server, and
// it is a read in the strong sense: a GET of a route that reveals what a
// world-readable bundle already reveals.
//
// One call site rather than one per caller, so the rule can be checked by
// looking at this file: no lifecycle verb may ask the control plane to DO
// anything. A route that could drive a quit, an update or an elevation is
// exactly what the boundary the parent drew forbids, and it would hand every
// local account a cross-account stop button on a plane with no bearer check.
//
// Loopback by construction: the control plane answers nothing else, and a verb
// has no business asking any other machine what this one is doing. The read is
// capped, so a wedged or hostile responder on the port cannot make a terminal
// command read forever.
func controlPlaneGet(port int, path string) ([]byte, error) {
	c := &http.Client{Timeout: 2 * time.Second}
	resp, err := c.Get("http://127.0.0.1:" + strconv.Itoa(port) + path)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("the control plane answered %s", resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxStateBytes))
}
