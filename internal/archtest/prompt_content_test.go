package archtest_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// promptContentReaders are the files allowed to name the fields of a chat
// message, each with the reason it is allowed to.
//
// Gropius reads the content of a request's messages in exactly one place, for
// exactly one purpose, and only for a model the operator switched merging on
// for: adr-2609061610102325 grants that and nothing else. The grant is only
// worth as much as the boundary around it, and a boundary made of prose erodes
// one feature at a time. This is a speed bump on that erosion: a second reader
// of prompt content has to be added to this list, in a diff someone reviews,
// rather than appearing quietly in a package that had no business with prompts.
//
// It is a scan for the field names, not a proof. A reader that goes through a
// type declared in a file on this list, or builds the key by concatenation,
// passes it silently. What it does catch is the ordinary way a second reader
// appears — someone decoding a request's messages in a new place — and it
// makes the list of exceptions a thing that exists and has to be edited.
var promptContentReaders = map[string]string{
	"internal/gateway/systemmerge.go": "the merge itself — the one reader adr-2609061610102325 grants",
	"internal/runtime/pool.go":        "builds the readiness probe's own one-line conversation; reads nothing from a client",
	"internal/mlxtest/fake.go":        "the fake mlx server tests relay to, which answers requests rather than making them",
}

// chatMessageFields are the ways a chat message's fields get named in Go: the
// JSON names themselves, and the constants the merge declares for them.
//
// The constants matter as much as the literals. They are package-level in
// internal/gateway — the package that owns the relay, and so the likeliest
// place a second prompt reader would appear — so a new file there could index
// a message by messagesField and contentField and read every prompt without
// writing a single quoted field name. That is the cheapest way around this
// scan and the one nearest to hand.
var chatMessageFields = []string{
	`"messages"`, `"role"`, `"content"`,
	"messagesField", "roleField", "contentField",
}

// generatedContentReaders are the files allowed to name the fields of a
// completion — the answer coming back — each with the reason it is allowed to.
//
// A conversation has two sides and adr-2609061610102325's boundary is drawn
// round both: what the client sent, and what the model generated. Only the
// request side was armed. The relay names choices and decodes that array to
// tell a chunk of an answer from the counts-only event, and it deliberately
// reads whether the array is empty and nothing more — never what is inside a
// choice, which is the answer itself. Nothing else reads inside one today, and
// until now nothing would have failed if something started.
var generatedContentReaders = map[string]string{
	"internal/gateway/gateway.go": "the relay: it reads whether an event carries a choice, to tell a chunk of the answer from the counts-only event and to time the first token — never what is inside one",
	"internal/mlxtest/fake.go":    "the fake mlx server tests relay to, which produces the answers rather than reading them",
}

// completionFields are the ways a completion's fields get named in Go.
//
// "delta" is here although nothing names it today, and that is the point: it
// is the field a streamed chunk carries the generated text in, so it is the
// spelling a second reader of an answer would reach for first. choicesField is
// here for the same reason the request side lists its constants — it is
// package-level in internal/gateway, so a new file there could index an event
// by it without writing a quoted field name.
//
// "usage" is deliberately NOT here. The token counts are content-free by
// construction and adr-2609061503319212 turns on their being a different kind
// of thing from the answer; scanning for them would report the statistics path
// as a reader of generated content, which is the wrong diagnosis for the right
// file.
var completionFields = []string{
	`"choices"`, `"delta"`,
	"choicesField",
}

// contentBoundary is one side of a conversation: the field names that spell it
// out, and the files allowed to name them.
type contentBoundary struct {
	fields  []string
	readers map[string]string
	// subject and list are the failure message: what naming these fields
	// means, and the variable a genuine new exception is added to.
	subject string
	list    string
}

// Merging is the only rewrite of prompt content Gropius performs, and the only
// reading of it. This walks every Go source file that ships (test files
// excluded — a test may compose whatever conversation it needs) and fails on
// any file outside the list above that names a chat message's fields.
func TestOnlyTheMergeReadsPromptContent(t *testing.T) {
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}

	// walkRepoFiles owns the skip rule, including the dot-directory rule this
	// scan used to carry in its own body: a git worktree checked out under
	// .claude/ holds a whole second copy of the tree, and scanning it would
	// fail this test for a branch someone else is working on. client/ and
	// build/ are this scan's own subject matter — Swift and packaging assets
	// hold no Go.
	walkRepoFiles(t, repoRoot, walkOptions{AlsoSkip: []string{"client", "build"}}, func(path string, d fs.DirEntry) error {
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, err := filepath.Rel(repoRoot, path)
		if err != nil {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		src := string(b)
		for _, boundary := range conversationBoundaries {
			for _, field := range boundary.fields {
				if !strings.Contains(src, field) {
					continue
				}
				if _, allowed := boundary.readers[filepath.ToSlash(rel)]; allowed {
					break
				}
				t.Errorf("%s names %s, so it reads or writes %s. adr-2609061610102325 draws that "+
					"boundary round both sides of a conversation and grants the merge and nothing "+
					"else. If this is genuinely a new exception, it needs a decision record and an "+
					"entry in %s saying why", filepath.ToSlash(rel), field, boundary.subject, boundary.list)
				break
			}
		}
		return nil
	})
}

// conversationBoundaries is both sides of the same boundary, scanned in one
// walk: what the client sent, and what the model generated.
var conversationBoundaries = []contentBoundary{
	{
		fields:  chatMessageFields,
		readers: promptContentReaders,
		subject: "the content of a request's messages",
		list:    "promptContentReaders",
	},
	{
		fields:  completionFields,
		readers: generatedContentReaders,
		subject: "the content of a generated answer",
		list:    "generatedContentReaders",
	},
}

// The list above is only a boundary while every entry on it is a file that
// exists: a stale entry silently exempts nothing, and a renamed reader would
// leave the exemption behind for the next file to inherit.
func TestPromptContentReadersAllExist(t *testing.T) {
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	for _, boundary := range conversationBoundaries {
		for rel, why := range boundary.readers {
			if _, err := os.Stat(filepath.Join(repoRoot, filepath.FromSlash(rel))); err != nil {
				t.Errorf("%s lists %s (%s), which is not in the tree", boundary.list, rel, why)
			}
		}
	}
}
