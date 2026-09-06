package gateway

import (
	"encoding/json"
	"strings"
	"unicode/utf8"
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

// mergeOutcome says what merging did with a request, so the caller can tell a
// conversation that needed no rewrite from one it could not rewrite. Reporting
// both as "not merged" put a line in the log for every ordinary request and
// told the operator their client's messages were unreadable when they were
// simply already in order.
type mergeOutcome int

const (
	// mergeNotNeeded: the conversation is already the shape the template
	// wants, so it is relayed untouched.
	mergeNotNeeded mergeOutcome = iota
	// mergeDone: the messages were folded.
	mergeDone
	// mergeRefused: a shape merging cannot rebuild faithfully. Relayed
	// untouched, and worth a line in the log, because the operator switched
	// merging on and is not getting it.
	mergeRefused
)

func (o mergeOutcome) String() string {
	switch o {
	case mergeNotNeeded:
		return "not needed"
	case mergeDone:
		return "done"
	default:
		return "refused"
	}
}

// mergeSystemMessagesInto folds the system messages of a buffered chat
// completion request, in place, and reports what it did. The caller hands over
// the whole request and learns nothing about its contents beyond that.
func mergeSystemMessagesInto(payload map[string]json.RawMessage) mergeOutcome {
	merged, outcome := mergeSystemMessages(payload[messagesField])
	if outcome == mergeDone {
		payload[messagesField] = merged
	}
	return outcome
}

// mergeSystemMessages folds every system-role message of a chat completion's
// messages array into a single leading system message, returning the rebuilt
// array and what it did.
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
// The merged message is the conversation's own leading system message with its
// content replaced by the join, when the conversation begins with one. Its
// other fields — a name, a cache directive — therefore stay on the message
// that owned them, and nothing is invented: the later instructions are
// appended to the text of the message that was already there. A conversation
// that does not begin with a system message has no such owner, so the merged
// message is written from role and content alone.
//
// It returns mergeRefused — relay the request exactly as it came — for every
// shape it cannot rebuild faithfully, rather than rebuild one lossily:
//
//   - an array it cannot decode, or an element that is not an object;
//   - a system message whose content is not a plain string: a list of content
//     parts, JSON null, absent, or bytes that are not valid UTF-8, which Go's
//     own string decoding would quietly repair into characters the client
//     never sent;
//   - a system message that is not the leading one and carries any field
//     beyond "role" and "content". Its text is appended to another message, so
//     a name on it would have to be dropped or reattributed, and neither is
//     something the gateway gets to decide.
//
// It returns mergeNotNeeded for the conversations that are already the shape
// the template wants — no system message, or a single one already leading —
// which are relayed untouched and are not a refusal.
func mergeSystemMessages(raw json.RawMessage) (json.RawMessage, mergeOutcome) {
	var elements []json.RawMessage
	if err := json.Unmarshal(raw, &elements); err != nil {
		return nil, mergeRefused
	}

	var (
		texts   []string
		others  []json.RawMessage
		leading map[string]json.RawMessage
		systems int
	)
	for i, element := range elements {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(element, &fields); err != nil {
			return nil, mergeRefused // not an object; not a conversation this can rebuild
		}

		var role string
		if raw, ok := fields[roleField]; ok {
			if err := json.Unmarshal(raw, &role); err != nil {
				return nil, mergeRefused
			}
		}
		if role != systemRole {
			// Every other message is carried over as the bytes it arrived in,
			// so its content reaches the model exactly as the client sent it.
			others = append(others, element)
			continue
		}
		systems++

		text, ok := instructionText(fields)
		if !ok {
			return nil, mergeRefused
		}
		if i == 0 {
			// The message the merged one is built from: everything it carries
			// besides its text is kept, because it keeps its own identity.
			leading = fields
		} else if len(fields) != 2 {
			// Its text is about to be appended to another message. Anything
			// else it carries has nowhere faithful to go.
			return nil, mergeRefused
		}
		// An instruction that renders to nothing contributes nothing, rather
		// than a separator with no text on one side of it.
		if text != "" {
			texts = append(texts, text)
		}
	}

	if systems == 0 || (systems == 1 && leading != nil) {
		return nil, mergeNotNeeded // already the shape the template wants
	}

	content, err := json.Marshal(strings.Join(texts, mergedSystemSeparator))
	if err != nil {
		return nil, mergeRefused
	}
	merged := leading
	if merged == nil {
		role, err := json.Marshal(systemRole)
		if err != nil {
			return nil, mergeRefused
		}
		merged = map[string]json.RawMessage{roleField: role}
	}
	merged[contentField] = content

	mergedRaw, err := json.Marshal(merged)
	if err != nil {
		return nil, mergeRefused
	}
	rebuilt, err := json.Marshal(append([]json.RawMessage{mergedRaw}, others...))
	if err != nil {
		return nil, mergeRefused
	}
	return rebuilt, mergeDone
}

// instructionText returns the text of a system message's content, and whether
// it is text merging can join at all.
//
// The UTF-8 check is on the bytes as they arrived, not on the decoded string:
// decoding is what does the damage. Go replaces an invalid byte sequence with
// U+FFFD and re-encodes it as valid UTF-8, so a merged prompt would carry
// characters the client never sent while an unmerged relay passes the same
// bytes through untouched. Substituting a character is a rewrite of content
// beyond merging, so a request carrying one is relayed instead.
func instructionText(fields map[string]json.RawMessage) (string, bool) {
	raw, ok := fields[contentField]
	if !ok {
		return "", false
	}
	if !utf8.Valid(raw) {
		return "", false
	}
	// A pointer tells JSON null apart from an empty string: null is the
	// canonical absent value and is refused with the absent one.
	var text *string
	if err := json.Unmarshal(raw, &text); err != nil || text == nil {
		return "", false
	}
	return *text, true
}
