package gateway

import (
	"encoding/json"
	"strings"
)

// chatCompletionsPath is the one route on which merging can apply. A plain
// completion carries a prompt, not messages, and is never touched.
const chatCompletionsPath = "/v1/chat/completions"

// systemRole names the messages merging gathers. It is the role a chat
// template treats as the conversation's instructions.
const systemRole = "system"

// mergedSystemSeparator joins the texts of the messages merging folds into
// one. A client can see the result in the model's answer, so the choice is
// documented rather than incidental: a blank line, the way one instruction is
// separated from the next in a prompt written by hand.
const mergedSystemSeparator = "\n\n"

// messagesField is the request field merging reads. Naming it here and
// nowhere else is what keeps the whole reading footprint inside this file,
// which is the boundary internal/archtest holds Gropius to.
const messagesField = "messages"

// mergeSystemMessagesInto folds the system messages of a buffered chat
// completion request, in place, and reports whether it rewrote anything. The
// caller hands over the whole request and learns nothing about its contents.
func mergeSystemMessagesInto(payload map[string]json.RawMessage) bool {
	merged, ok := mergeSystemMessages(payload[messagesField])
	if !ok {
		return false
	}
	payload[messagesField] = merged
	return true
}

// mergeSystemMessages folds every system-role message of a chat completion's
// messages array into a single leading system message, returning the rebuilt
// array and whether it rebuilt one.
//
// This is the only place in Gropius that reads the content of a request's
// messages, it runs only for a model the operator switched merging on for, and
// it exists for one reason: a chat template that refuses a system message
// anywhere but the front refuses the whole conversation an agent client
// produces when it re-sends its system prompt on every turn
// (adr-2609061610102325).
//
// The reading footprint is exactly this: the role of every element, and the
// content of the elements whose role is "system". Nothing read is logged, put
// in an error, counted or kept anywhere; the array it returns lives only until
// the relay ends. Every element that is not a system message is carried over
// as its original bytes, so its content is preserved as it was sent rather
// than re-encoded field by field.
//
// It returns false — relay the request exactly as it came — for every shape it
// cannot rebuild faithfully: an array it cannot decode, an element that is not
// an object, a system message whose content is not a plain string (a list of
// content parts, say) or is absent, and the conversations that need no rewrite
// at all, which are the ones with no system message and the ones whose single
// system message is already leading. Rewriting those last two would re-encode
// a message from the two fields merging knows and silently drop any other
// field it carries.
func mergeSystemMessages(raw json.RawMessage) (json.RawMessage, bool) {
	var elements []json.RawMessage
	if err := json.Unmarshal(raw, &elements); err != nil {
		return nil, false
	}

	var (
		texts         []string
		others        []json.RawMessage
		firstSystemAt = -1
	)
	for i, element := range elements {
		var envelope struct {
			Role string `json:"role"`
		}
		if err := json.Unmarshal(element, &envelope); err != nil {
			return nil, false
		}
		if envelope.Role != systemRole {
			others = append(others, element)
			continue
		}
		// The one content read, and only for the role whose position the
		// template objects to. A pointer tells an absent content apart from an
		// empty one; both mean this is not a message merging can fold.
		var instructions struct {
			Content *string `json:"content"`
		}
		if err := json.Unmarshal(element, &instructions); err != nil || instructions.Content == nil {
			return nil, false
		}
		if firstSystemAt < 0 {
			firstSystemAt = i
		}
		texts = append(texts, *instructions.Content)
	}

	if len(texts) == 0 || (len(texts) == 1 && firstSystemAt == 0) {
		return nil, false // already the shape the template wants
	}

	merged, err := json.Marshal(map[string]string{
		"role":    systemRole,
		"content": strings.Join(texts, mergedSystemSeparator),
	})
	if err != nil {
		return nil, false
	}
	rebuilt, err := json.Marshal(append([]json.RawMessage{merged}, others...))
	if err != nil {
		return nil, false
	}
	return rebuilt, true
}
