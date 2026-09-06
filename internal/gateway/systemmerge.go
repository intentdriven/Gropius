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

// roleField and contentField are the two keys of a message merging looks at,
// and they are matched exactly.
//
// Exactly is load-bearing. encoding/json matches an object key to a struct
// field case-insensitively when no key matches exactly, and — decoding an
// object key by key — a later case-variant overwrites what the exact key
// already set, so a message sent as {"role":"user",…,"Role":"system"} decodes
// into a struct as a system message. The model server matches the key
// exactly, so to it that message is a user turn. Reading it as an instruction
// would fold text the model would never have obeyed into the prompt it does
// obey, and drop the turn the client actually sent. Merging therefore reads
// the message's own keys rather than letting a struct tag match them.
const (
	roleField    = "role"
	contentField = "content"
)

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
// The reading footprint is exactly this: the field names of every element,
// the value of the "role" field of every element, and the value of the
// "content" field of the elements whose role is "system". Every other field's
// value stays the bytes it arrived in and is never interpreted. Nothing read
// is logged, put in an error, counted or kept anywhere; the array this returns
// lives only until the relay ends. Every element that is not a system message
// is carried over as its original bytes, so its content reaches the model
// exactly as the client sent it.
//
// It returns false — relay the request exactly as it came — for every shape it
// cannot rebuild faithfully, rather than rebuild one lossily:
//
//   - an array it cannot decode, or an element that is not an object;
//   - a system message whose content is not a plain string (a list of content
//     parts, say) or is absent;
//   - a system message carrying any field beyond "role" and "content", since
//     the merged message is written from those two alone and the rest would
//     silently go missing;
//   - the conversations that need no rewrite at all, which are the ones with
//     no system message and the ones whose single system message is already
//     leading — rewriting those would re-encode a message that was already the
//     shape the template wants.
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
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(element, &fields); err != nil {
			return nil, false // not an object; not a conversation this can rebuild
		}

		var role string
		if raw, ok := fields[roleField]; ok {
			if err := json.Unmarshal(raw, &role); err != nil {
				return nil, false
			}
		}
		if role != systemRole {
			// Every other message is carried over as the bytes it arrived in,
			// so its content reaches the model exactly as the client sent it.
			others = append(others, element)
			continue
		}

		// A merged message is written from these two fields alone, so a system
		// message carrying anything else — a name, a cache directive, a
		// case-variant of either key — is one merging cannot rebuild without
		// quietly dropping part of it. Relay the request instead.
		if len(fields) != 2 {
			return nil, false
		}
		// The one content read, and only for the role whose position the
		// template objects to.
		raw, ok := fields[contentField]
		if !ok {
			return nil, false
		}
		var text string
		if err := json.Unmarshal(raw, &text); err != nil {
			return nil, false // a list of content parts, say
		}
		if firstSystemAt < 0 {
			firstSystemAt = i
		}
		texts = append(texts, text)
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
