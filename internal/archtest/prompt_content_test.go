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

// chatMessageFields are the JSON names a chat message is made of. Code that
// reads a prompt has to name at least one of them.
var chatMessageFields = []string{`"messages"`, `"role"`, `"content"`}

// Merging is the only rewrite of prompt content Gropius performs, and the only
// reading of it. This walks every Go source file that ships (test files
// excluded — a test may compose whatever conversation it needs) and fails on
// any file outside the list above that names a chat message's fields.
func TestOnlyTheMergeReadsPromptContent(t *testing.T) {
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}

	err = filepath.WalkDir(repoRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// Dot-directories hold no shipping Go source, and one of them can
			// hold a whole second copy of the tree: a git worktree checked out
			// under .claude/ would otherwise be scanned as if its files were
			// this module's, so a branch someone else is working on could fail
			// this test here.
			if path != repoRoot && strings.HasPrefix(d.Name(), ".") {
				return fs.SkipDir
			}
			switch d.Name() {
			case "bin", "dist", "client", "build":
				return fs.SkipDir
			}
			return nil
		}
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
		for _, field := range chatMessageFields {
			if !strings.Contains(src, field) {
				continue
			}
			if _, allowed := promptContentReaders[filepath.ToSlash(rel)]; allowed {
				return nil
			}
			t.Errorf("%s names %s, so it reads or writes the content of a request's messages. "+
				"Merging is the only such reader Gropius has (adr-2609061610102325). If this one is "+
				"genuinely a new exception, it needs a decision record and an entry in "+
				"promptContentReaders saying why", rel, field)
			return nil
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// The list above is only a boundary while every entry on it is a file that
// exists: a stale entry silently exempts nothing, and a renamed reader would
// leave the exemption behind for the next file to inherit.
func TestPromptContentReadersAllExist(t *testing.T) {
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	for rel, why := range promptContentReaders {
		if _, err := os.Stat(filepath.Join(repoRoot, filepath.FromSlash(rel))); err != nil {
			t.Errorf("promptContentReaders lists %s (%s), which is not in the tree", rel, why)
		}
	}
}
