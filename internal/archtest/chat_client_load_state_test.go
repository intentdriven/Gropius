package archtest_test

import (
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/intentdriven/Gropius/internal/config"
	"github.com/intentdriven/Gropius/internal/gateway"
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

// clientChattableDefault matches the client's verdict on a models-list entry.
// Both spellings of the same rule are accepted -- "yes unless the field is
// present and false" -- because the rule is what matters and neither phrasing
// can express the opposite default.
var clientChattableDefault = regexp.MustCompile(
	`var\s+chattable\s*:\s*Bool\s*\{\s*(?:chat\s*\?\?\s*true|chat\s*!=\s*false)\s*\}`)

// clientPickerFiltersOnChattable matches the promise rather than one spelling
// of it: the picker's list is derived by filtering the served list through a
// call to the rule's verdict, whatever the rule value is called at that point
// and whether the closure or the method reference is used. What it cannot match
// is a picker built from the served list directly, which is the failure.
var clientPickerFiltersOnChattable = regexp.MustCompile(
	`chatModels\s*=\s*[^\n]*\.filter\s*[({][^\n]*\.offers\b`)

// clientChatRuleDefaults match the two halves of the rule the client ships,
// each as the stored setting a person can change in its Settings -- so a
// comment describing the rule cannot stand in for the rule.
var clientChatPipelineDefault = regexp.MustCompile(
	`@AppStorage\("chatPipelineTags"\)\s+var\s+chatPipelineTags\s*:\s*String\s*=\s*"([^"]*)"`)
var clientChatRequiredDefault = regexp.MustCompile(
	`@AppStorage\("chatRequiredTags"\)\s+var\s+chatRequiredTags\s*:\s*String\s*=\s*"([^"]*)"`)

// clientCategoryFields match the two fields the client decodes the Hub's words
// out of. Codable keys off the property name, so the property IS the wire field.
var clientPipelineField = regexp.MustCompile(
	`(?m)^\s*let\s+pipeline_tag\s*:\s*String\?`)
var clientTagsField = regexp.MustCompile(
	`(?m)^\s*let\s+tags\s*:\s*\[String\]\?`)

// gatewayCategoryFields match the gateway writing the same two fields onto a
// models-list entry.
var gatewayPipelineField = regexp.MustCompile(
	`(?m)^\s*entry\["pipeline_tag"\]\s*=`)
var gatewayTagsField = regexp.MustCompile(
	`(?m)^\s*entry\["tags"\]\s*=`)

// clientRuleIsEditable match each half of the rule being handed to a control as
// a two-way binding -- the `$` projection, which in SwiftUI exists for nothing
// else -- rather than one spelling of one control. A rule nothing binds is a
// default nobody can move.
var clientRuleIsEditable = regexp.MustCompile(
	`\$(?:model\.|self\.)?chatPipelineTags\b`)
var clientRequiredIsEditable = regexp.MustCompile(
	`\$(?:model\.|self\.)?chatRequiredTags\b`)

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
		gatewaySource := readRepoFile(t, root, filepath.Join("internal", "gateway", "gateway.go"))
		if !gatewayResidencyField.MatchString(gatewaySource) {
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
// While a streaming request waits for its model to load, the gateway is to emit
// SSE comment lines -- lines beginning with a colon, which the SSE format
// defines as comments and every conforming client already ignores -- carrying
// gateway.LoadingComment, about one a second, until the first data frame. That
// choice is what lets the signal be added without changing the response's
// content type, its status code, or what a client that knows nothing about it
// does with the stream. The emitter is a separate change; the constant it will
// write is already declared, and this is the client held to it.
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
	// gateway.LoadingComment, not a literal repeated here: a test that compares
	// one spelling of the protocol against another spelling in its own file
	// pins nothing, since both move together in one edit. The wire form has one
	// home, in the package that sends it, and this is the client being held to
	// it. The emitter is still to be written; the constant is not waiting on it.
	if m[1] != gateway.LoadingComment {
		t.Errorf("the chat client watches for the SSE comment %q; the gateway sends %q",
			m[1], gateway.LoadingComment)
	}
}

// TestChatClientOffersEveryModelAServerDoesNotRuleOut holds the picker's
// default for a models list that says nothing about a model at all.
//
// The client judges a model by the words the list publishes for it. A server
// that publishes no words and no verdict is one that predates the whole idea,
// and every model on it must still be offered: default it the other way and the
// client shows an empty picker against every Gropius already installed -- a
// total failure to chat, from a feature that was added to hide an OCR model.
func TestChatClientOffersEveryModelAServerDoesNotRuleOut(t *testing.T) {
	root := repoRootDir(t)
	source := readRepoFile(t, root, filepath.Join("client", "GropiusChat", "GropiusChat.swift"))

	// Two halves of the one behavior: the verdict defaults to yes, and the
	// picker's list is actually derived through it. Either alone passes while
	// the feature is broken -- a correct default nothing consults hides
	// nothing, and a filter over a wrong default hides everything.
	if !clientChattableDefault.MatchString(source) {
		t.Error("client/GropiusChat/GropiusChat.swift does not decide chattable as `chat ?? true` " +
			"(or `chat != false`); a models list that publishes no chat capability must offer every model")
	}
	if !clientPickerFiltersOnChattable.MatchString(source) {
		t.Error("client/GropiusChat/GropiusChat.swift does not build its picker list by filtering " +
			"the served list through its own chat rule; a model the rule excludes would be offered anyway")
	}
}

// TestChatClientReadsTheCategoryTheGatewayPublishes holds the client's reading
// of what a model is to the fields the server writes it under.
//
// Two more silent failures, the same shape as the residency above: a renamed
// field decodes as nil, the client's rule then sees a model with no words on
// it, and the picker quietly falls back to the server's own verdict. Nothing
// errors, and nobody can see why a model is or is not offered.
func TestChatClientReadsTheCategoryTheGatewayPublishes(t *testing.T) {
	root := repoRootDir(t)
	source := readRepoFile(t, root, filepath.Join("client", "GropiusChat", "GropiusChat.swift"))
	gatewaySource := readRepoFile(t, root, filepath.Join("internal", "gateway", "gateway.go"))

	for _, c := range []struct {
		field   string
		client  *regexp.Regexp
		gateway *regexp.Regexp
	}{
		{"pipeline_tag", clientPipelineField, gatewayPipelineField},
		{"tags", clientTagsField, gatewayTagsField},
	} {
		if !c.client.MatchString(source) {
			t.Errorf("client/GropiusChat/GropiusChat.swift declares no %q on its models decoder; "+
				"the words its own rule reads are unchecked", c.field)
		}
		if !c.gateway.MatchString(gatewaySource) {
			t.Errorf("internal/gateway/gateway.go no longer writes entry[%q] onto a models-list entry; "+
				"the chat client decodes the category under that name", c.field)
		}
	}
}

// TestChatClientShipsTheServersOwnChatRule holds the client's default rule to
// the server's.
//
// The rule is the client's own -- it applies it to the tags the models list
// publishes, and a person can change it in the client's Settings -- but the
// default it ships with is not two independent decisions. A client whose
// shipped rule differs from the server's would offer a different set of models
// than the same server's `chat` flag names, with nothing on either surface to
// explain the difference.
func TestChatClientShipsTheServersOwnChatRule(t *testing.T) {
	root := repoRootDir(t)
	source := readRepoFile(t, root, filepath.Join("client", "GropiusChat", "GropiusChat.swift"))
	rule := config.DefaultChatRule()

	for _, c := range []struct {
		what string
		re   *regexp.Regexp
		want []string
	}{
		{"pipeline tags", clientChatPipelineDefault, rule.PipelineTags},
		{"required tags", clientChatRequiredDefault, rule.RequiredTags},
	} {
		m := c.re.FindStringSubmatch(source)
		if m == nil {
			t.Errorf("client/GropiusChat/GropiusChat.swift declares no stored default for the rule's %s", c.what)
			continue
		}
		got := []string{}
		for _, word := range strings.Split(m[1], ",") {
			if word = strings.TrimSpace(word); word != "" {
				got = append(got, word)
			}
		}
		if !slices.Equal(got, c.want) {
			t.Errorf("the chat client ships %v as its %s; the server ships %v", got, c.what, c.want)
		}
	}

	// And the rule has to be changeable where the intent says it is, or it is
	// a default rather than a setting.
	if !clientRuleIsEditable.MatchString(source) || !clientRequiredIsEditable.MatchString(source) {
		t.Error("client/GropiusChat/GropiusChat.swift binds no Settings control to both halves of the rule; " +
			"the rule is then a constant a user cannot change")
	}
}
