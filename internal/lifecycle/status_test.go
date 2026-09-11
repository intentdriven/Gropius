package lifecycle

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/intentdriven/Gropius/internal/instance"
)

// servingState is the snapshot a running server would have answered with.
func servingState() ServerState {
	var s ServerState
	s.Config.Host = "0.0.0.0"
	s.Config.Port = 11535
	s.Resident = []ResidentModel{{RepoID: "example-org/example-model-4bit", State: "loaded"}}
	return s
}

// The JSON is the contract: the menu bar and scripts read it, the human text is
// a rendering of the same value and may change. Compared as a decoded value
// against a fixture, so a renamed field is a visible diff here rather than a
// silent break in something that polls it.
func TestStatusJSONIsTheContract(t *testing.T) {
	for _, tc := range []struct {
		name    string
		env     StatusEnv
		fixture string
	}{
		{
			name: "serving",
			env: StatusEnv{
				Version: "test",
				Port:    11535,
				Holder:  func() instance.Holder { return instance.HolderOurs },
				State:   func() (ServerState, error) { return servingState(), nil },
			},
			fixture: "status_serving.json",
		},
		{
			name: "not serving",
			env: StatusEnv{
				Version: "test",
				Port:    11535,
				Holder:  func() instance.Holder { return instance.HolderNone },
				State:   func() (ServerState, error) { return ServerState{}, errors.New("connection refused") },
			},
			fixture: "status_not_serving.json",
		},
		{
			name: "the port is held by something that cannot prove it is ours",
			env: StatusEnv{
				Version: "test",
				Port:    11535,
				Holder:  func() instance.Holder { return instance.HolderForeign },
				State:   func() (ServerState, error) { return ServerState{}, errors.New("connection refused") },
			},
			fixture: "status_foreign.json",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := encodeJSON(t, StatusOf(tc.env))
			want := decodeFile(t, filepath.Join("testdata", tc.fixture))
			if !reflect.DeepEqual(got, want) {
				t.Errorf("status --json does not match %s\n got: %#v\nwant: %#v", tc.fixture, got, want)
			}
		})
	}
}

// Resident is a list even when it is empty. A caller that ranges over it must
// not have to tell a missing key, a null and an empty list apart.
func TestStatusJSONCarriesAnEmptyListRatherThanNull(t *testing.T) {
	b, err := json.Marshal(StatusOf(StatusEnv{
		Port:   11535,
		Holder: func() instance.Holder { return instance.HolderNone },
		State:  func() (ServerState, error) { return ServerState{}, errors.New("refused") },
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"resident":[]`) {
		t.Errorf("status --json = %s, want an empty resident list rather than null", b)
	}
}

// status answers from state that already exists: the port's holder, and the
// snapshot the control panel is already polling. Anything more expensive — a
// provisioning check, a walk of the model directory, a request to the network —
// makes polling it unsafe, which is the whole difference between this verb and
// doctor.
func TestStatusAsksTheTwoCheapQuestionsAndNoOthers(t *testing.T) {
	var holders, states int
	env := StatusEnv{
		Port: 11535,
		Holder: func() instance.Holder {
			holders++
			return instance.HolderOurs
		},
		State: func() (ServerState, error) {
			states++
			return servingState(), nil
		},
	}
	StatusOf(env)
	if holders != 1 || states != 1 {
		t.Errorf("StatusOf asked the holder %d times and the control plane %d times, want one each", holders, states)
	}

	// The other direction: a holder that is not ours is answered without
	// asking the control plane anything at all.
	holders, states = 0, 0
	env.Holder = func() instance.Holder {
		holders++
		return instance.HolderNone
	}
	StatusOf(env)
	if holders != 1 || states != 0 {
		t.Errorf("with nothing on the port, StatusOf asked the holder %d times and the control plane %d times, want 1 and 0", holders, states)
	}
}

// A server that answered the challenge and then would not answer the control
// plane is still serving — the challenge is proof it is there — and status says
// what it could not read rather than reporting the server as down.
func TestStatusReportsAServerItCouldNotReadTheStateOf(t *testing.T) {
	got := StatusOf(StatusEnv{
		Port:   11535,
		Holder: func() instance.Holder { return instance.HolderOurs },
		State:  func() (ServerState, error) { return ServerState{}, errors.New("connection reset") },
	})
	if !got.Serving {
		t.Error("a server that proved it is there is serving, whatever the control plane said")
	}
	if got.Reason == "" {
		t.Error("status says nothing about the state it could not read")
	}
}

// The private-network bind mode leaves Config.Host as whatever the operator
// last set, and the address the server actually bound is the one the mode
// selected. status reports the bind, not the setting.
func TestStatusReportsTheAddressThatWasBound(t *testing.T) {
	state := servingState()
	state.Config.Host = "0.0.0.0"
	state.Bind.Mode = "private"
	state.Bind.Selected = "192.0.2.7"

	got := StatusOf(StatusEnv{
		Port:   11535,
		Holder: func() instance.Holder { return instance.HolderOurs },
		State:  func() (ServerState, error) { return state, nil },
	})
	if got.Address != "192.0.2.7:11535" {
		t.Errorf("Address = %q, want the address the bind selected", got.Address)
	}
}

// The human text is a rendering of the same value: it names the address and
// every resident model, so an operator reading it learns what a caller parsing
// the JSON learns.
func TestStatusTextRendersTheSameFacts(t *testing.T) {
	var buf bytes.Buffer
	s := StatusOf(StatusEnv{
		Version: "test",
		Port:    11535,
		Holder:  func() instance.Holder { return instance.HolderOurs },
		State:   func() (ServerState, error) { return servingState(), nil },
	})
	RenderStatus(Terminal{Out: &buf}, s)
	got := buf.String()
	for _, want := range []string{"0.0.0.0:11535", "example-org/example-model-4bit", "test"} {
		if !strings.Contains(got, want) {
			t.Errorf("status text = %q, which does not name %q", got, want)
		}
	}
}

func encodeJSON(t *testing.T, v any) any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded any
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return decoded
}

func decodeFile(t *testing.T, path string) any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var decoded any
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatalf("fixture %s: %v", path, err)
	}
	return decoded
}
