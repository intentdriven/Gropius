package archtest_test

import (
	"path/filepath"
	"regexp"
	"testing"

	"github.com/intentdriven/Gropius/internal/runtime"
)

// A second set of client/server promises, alongside the address and the service
// type held in chat_client_discovery_test.go: the words the chat client reads
// out of the server's answers. Three of them, and each fails silently on its
// own -- a renamed residency field decodes as nil and the client simply never
// says a model is loading; an unrecognized SSE comment is skipped as an unknown
// line; a mis-defaulted capability hides every model on a server that does not
// publish one. Swift is compiled by client/build.sh, not by `go test`, so these
// are read out of the client's own source here.

// clientResidencyLoaded matches the client's declaration of the residency value
// that means "already in memory", so a mention in a comment cannot stand in for
// it.
var clientResidencyLoaded = regexp.MustCompile(
	`(?m)^\s*let\s+residencyLoaded\s*=\s*"([^"]*)"`)

// clientResidencyField matches the property the client's models decoder reads
// the residency out of. Codable keys off the property name, so the property IS
// the wire field.
var clientResidencyField = regexp.MustCompile(
	`(?m)^\s*let\s+state\s*:\s*String\?`)

// gatewayResidencyField matches the gateway writing the same field onto a
// models-list entry.
var gatewayResidencyField = regexp.MustCompile(
	`(?m)^\s*entry\["state"\]\s*=`)

// clientLoadingComment matches the client's declaration of the SSE comment the
// gateway sends while a model loads.
var clientLoadingComment = regexp.MustCompile(
	`(?m)^\s*let\s+modelLoadingComment\s*=\s*"([^"]*)"`)

// clientChattableDefault matches the client's default for a models-list entry
// that carries no chat capability at all.
var clientChattableDefault = regexp.MustCompile(
	`var\s+chattable\s*:\s*Bool\s*\{\s*chat\s*\?\?\s*true\s*\}`)

// TestChatClientReadsTheResidencyTheGatewayPublishes holds the client's reading
// of a model's residency to the field and the value the server writes.
//
// The client polls the models list while a request is in flight and says
// "Loading" for a model that is not resident. Both halves of that reading are
// the server's to name: the field the value arrives under, and the one value
// out of three that means the wait is over. Rename either on the server and the
// client's decode yields nil or a never-equal string -- no error, no warning,
// just a loading state that never appears.
func TestChatClientReadsTheResidencyTheGatewayPublishes(t *testing.T) {
	root := repoRootDir(t)
	source := readRepoFile(t, root, filepath.Join("client", "GropiusChat", "GropiusChat.swift"))

	t.Run("field name", func(t *testing.T) {
		if !clientResidencyField.MatchString(source) {
			t.Fatal("client/GropiusChat/GropiusChat.swift declares no `let state: String?` on its " +
				"models decoder; the field the residency arrives under is unchecked")
		}
		gateway := readRepoFile(t, root, filepath.Join("internal", "gateway", "gateway.go"))
		if !gatewayResidencyField.MatchString(gateway) {
			t.Error(`internal/gateway/gateway.go no longer writes entry["state"] onto a models-list ` +
				"entry; the chat client decodes the residency under that name")
		}
	})

	t.Run("loaded value", func(t *testing.T) {
		m := clientResidencyLoaded.FindStringSubmatch(source)
		if m == nil {
			t.Fatal("client/GropiusChat/GropiusChat.swift declares no residencyLoaded; " +
				"the value the client reads as warm is unchecked")
		}
		if want := string(runtime.ResidencyLoaded); m[1] != want {
			t.Errorf("the chat client reads %q as a loaded model; the pool reports %q",
				m[1], want)
		}
	})
}

// TestChatClientRecognizesTheGatewaysLoadingComment holds the client's reading
// of the streaming load signal to the wire form the gateway sends.
//
// While a streaming request waits for its model to load, the gateway emits SSE
// comment lines -- lines beginning with a colon, which the SSE format defines
// as comments and every conforming client already ignores -- reading
// ": loading", about one a second, until the first data frame. That choice is
// what lets the signal be added without changing the response's content type,
// its status code, or what a client that knows nothing about it does with the
// stream.
//
// The client must recognize the same words. A client looking for another prefix
// treats the comments as unknown lines and shows the ordinary generation
// spinner through the whole load, which is exactly the hang the loading state
// exists to explain.
func TestChatClientRecognizesTheGatewaysLoadingComment(t *testing.T) {
	root := repoRootDir(t)
	source := readRepoFile(t, root, filepath.Join("client", "GropiusChat", "GropiusChat.swift"))

	m := clientLoadingComment.FindStringSubmatch(source)
	if m == nil {
		t.Fatal("client/GropiusChat/GropiusChat.swift declares no modelLoadingComment; " +
			"the comment the client watches the stream for is unchecked")
	}
	// The protocol's wire form, written out here rather than referenced from
	// the gateway: the emitter is a separate change, and this is the contract
	// it will be built to. When it lands, this literal becomes its constant.
	if want := ": loading"; m[1] != want {
		t.Errorf("the chat client watches for the SSE comment %q; the gateway sends %q",
			m[1], want)
	}
}

// TestChatClientOffersEveryModelAServerDoesNotRuleOut holds the picker's
// default for a models list that says nothing about a model's chat capability.
//
// The capability is published per entry and absent means yes: a server that
// predates the field, and every model on it, must still be offered. Default it
// the other way and the client shows an empty picker against every Gropius
// already installed -- a total failure to chat, from a field that was added to
// hide an OCR model.
func TestChatClientOffersEveryModelAServerDoesNotRuleOut(t *testing.T) {
	root := repoRootDir(t)
	source := readRepoFile(t, root, filepath.Join("client", "GropiusChat", "GropiusChat.swift"))

	if !clientChattableDefault.MatchString(source) {
		t.Error("client/GropiusChat/GropiusChat.swift does not derive its picker list with " +
			"`chat ?? true`; a models list that publishes no chat capability must offer every model")
	}
}
