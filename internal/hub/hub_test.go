package hub

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

func TestSearchParsesHubResponse(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `[
			{"id":"mlx-community/Qwen3-8B-4bit","downloads":1234,"likes":7,"tags":["mlx","safetensors"],"pipeline_tag":"text-generation"},
			{"id":"mlx-community/Llama-3.2-1B-Instruct-4bit","downloads":99,"likes":1,"tags":["mlx"]}
		]`)
	}))
	defer srv.Close()

	c := &Client{BaseURL: srv.URL, HTTP: srv.Client()}
	models, err := c.Search(context.Background(), SearchQuery{
		Search: "qwen", Author: "mlx-community", Limit: 5, Sort: "downloads",
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("got %d models, want 2", len(models))
	}
	if models[0].ID != "mlx-community/Qwen3-8B-4bit" {
		t.Errorf("ID = %q", models[0].ID)
	}
	if models[0].Downloads != 1234 {
		t.Errorf("Downloads = %d, want 1234", models[0].Downloads)
	}
	for _, want := range []string{"search=qwen", "author=mlx-community", "limit=5", "sort=downloads"} {
		if !containsStr(gotQuery, want) {
			t.Errorf("query %q missing %q", gotQuery, want)
		}
	}
}

func TestSearchSurfacesHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := &Client{BaseURL: srv.URL, HTTP: srv.Client()}
	if _, err := c.Search(context.Background(), SearchQuery{}); err == nil {
		t.Fatal("expected an error for HTTP 500")
	}
}

func TestModelMetadataHelpers(t *testing.T) {
	tests := []struct {
		id    string
		org   string
		name  string
		quant string
	}{
		{"mlx-community/Qwen3-8B-4bit", "mlx-community", "Qwen3-8B-4bit", "4bit"},
		{"mlx-community/Llama-3.3-70B-Instruct-8bit", "mlx-community", "Llama-3.3-70B-Instruct-8bit", "8bit"},
		{"mlx-community/Qwen3-4B-4bit-DWQ", "mlx-community", "Qwen3-4B-4bit-DWQ", "4bit-dwq"},
		{"mlx-community/Mistral-7B-bf16", "mlx-community", "Mistral-7B-bf16", "bf16"},
		{"someone/plain-model", "someone", "plain-model", ""},
	}
	for _, tt := range tests {
		m := Model{ID: tt.id}
		if got := m.Org(); got != tt.org {
			t.Errorf("%s: Org = %q, want %q", tt.id, got, tt.org)
		}
		if got := m.Name(); got != tt.name {
			t.Errorf("%s: Name = %q, want %q", tt.id, got, tt.name)
		}
		if got := m.Quantization(); got != tt.quant {
			t.Errorf("%s: Quantization = %q, want %q", tt.id, got, tt.quant)
		}
	}
}

func TestFilesPrefersLFSSize(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/models/org/repo/tree/main" {
			t.Errorf("unexpected tree path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		// The weights entry reports the LFS size in a nested object.
		fmt.Fprint(w, `[
			{"path":"config.json","size":937,"oid":"abc"},
			{"path":"model.safetensors","size":135,"oid":"ptr","lfs":{"oid":"deadbeef","size":335450584}}
		]`)
	}))
	defer srv.Close()

	c := &Client{BaseURL: srv.URL, HTTP: srv.Client()}
	files, err := c.Files(context.Background(), "org/repo", "")
	if err != nil {
		t.Fatalf("Files: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("got %d files, want 2", len(files))
	}
	weights := files[1]
	if weights.Size != 335450584 {
		t.Errorf("LFS size = %d, want the nested LFS size 335450584 (not the pointer size)", weights.Size)
	}
	if weights.OID != "deadbeef" {
		t.Errorf("OID = %q, want the LFS oid", weights.OID)
	}
}

// The recursive tree listing includes directory entries; they are not fetchable
// (a GET of one 404s and aborts the whole download), so Files must drop them.
// A tree that lists the same path twice must be de-duplicated, or two goroutines
// race to write the same file and TotalSize double-counts it.
func TestWantedFilesDeduplicates(t *testing.T) {
	files := []File{
		{Path: "model.safetensors", Size: 100},
		{Path: "config.json", Size: 10},
		{Path: "model.safetensors", Size: 100}, // duplicate
		{Path: "./config.json", Size: 10},      // duplicate via a different spelling
	}
	got := WantedFiles(files)
	if len(got) != 2 {
		t.Fatalf("WantedFiles kept %d entries, want 2 deduplicated: %+v", len(got), got)
	}
	if n := TotalSize(got); n != 110 {
		t.Errorf("TotalSize after dedup = %d, want 110 (a double-count keeps progress under 100%%)", n)
	}
}

func TestFilesDropsDirectoryEntries(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `[
			{"type":"directory","path":"assets","size":0},
			{"type":"file","path":"config.json","size":10,"oid":"a"},
			{"type":"file","path":"model.safetensors","size":20,"oid":"b"}
		]`)
	}))
	defer srv.Close()

	c := &Client{BaseURL: srv.URL, HTTP: srv.Client()}
	files, err := c.Files(context.Background(), "org/repo", "")
	if err != nil {
		t.Fatalf("Files: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("got %d files, want 2 (the directory entry must be dropped)", len(files))
	}
	for _, f := range files {
		if f.Path == "assets" {
			t.Error("directory entry 'assets' leaked into the file list")
		}
	}
}

// Repos with more than 1000 tree entries paginate via a Link rel="next" header;
// Files must follow it or downloads silently omit later shards.
func TestFilesFollowsPagination(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("cursor") == "" {
			w.Header().Set("Link", "<"+srv.URL+"/api/models/org/repo/tree/main?recursive=true&cursor=p2>; rel=\"next\"")
			fmt.Fprint(w, `[{"type":"file","path":"a.safetensors","size":1,"oid":"a"}]`)
			return
		}
		fmt.Fprint(w, `[{"type":"file","path":"b.safetensors","size":2,"oid":"b"}]`)
	}))
	defer srv.Close()

	c := &Client{BaseURL: srv.URL, HTTP: srv.Client()}
	files, err := c.Files(context.Background(), "org/repo", "")
	if err != nil {
		t.Fatalf("Files: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("got %d files, want 2 across both pages", len(files))
	}
}

// A rel="next" pointing at a different host must not be followed: newRequest
// attaches the bearer token, so following it would leak the HuggingFace token
// to the attacker-named host.
func TestFilesRefusesCrossOriginNextPage(t *testing.T) {
	var leaked bool
	evil := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			leaked = true
		}
		fmt.Fprint(w, `[]`)
	}))
	defer evil.Close()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Link", "<"+evil.URL+"/api/models/org/repo/tree/main?cursor=p2>; rel=\"next\"")
		fmt.Fprint(w, `[{"type":"file","path":"a.safetensors","size":1,"oid":"a"}]`)
	}))
	defer srv.Close()

	c := &Client{BaseURL: srv.URL, HTTP: srv.Client(), Token: "secret-hf-token"}
	_, err := c.Files(context.Background(), "org/repo", "")
	if err == nil {
		t.Fatal("Files followed a cross-origin next page; it must refuse")
	}
	if leaked {
		t.Fatal("TOKEN LEAK: the bearer token was sent to the cross-origin host")
	}
}

func TestFilesNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()

	c := &Client{BaseURL: srv.URL, HTTP: srv.Client()}
	_, err := c.Files(context.Background(), "org/nope", "")
	if err == nil {
		t.Fatal("expected error")
	}
	if !IsNotFound(err) {
		t.Errorf("IsNotFound should be true for a 404, got err = %v", err)
	}
}

func TestGatedRepoReportsAuthRequired(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "gated", http.StatusForbidden)
	}))
	defer srv.Close()

	c := &Client{BaseURL: srv.URL, HTTP: srv.Client()}
	_, err := c.Files(context.Background(), "meta/gated", "")
	if !IsAuthRequired(err) {
		t.Errorf("IsAuthRequired should be true for a 403, got err = %v", err)
	}
}

func TestTokenIsSentAsBearer(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		fmt.Fprint(w, `[]`)
	}))
	defer srv.Close()

	c := &Client{BaseURL: srv.URL, HTTP: srv.Client(), Token: "hf_abc123"}
	if _, err := c.Search(context.Background(), SearchQuery{}); err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer hf_abc123" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer hf_abc123")
	}
}

func TestResolveURL(t *testing.T) {
	c := &Client{BaseURL: "https://huggingface.co"}
	got := c.ResolveURL("mlx-community/Qwen3-0.6B-4bit", "", "model.safetensors")
	want := "https://huggingface.co/mlx-community/Qwen3-0.6B-4bit/resolve/main/model.safetensors"
	if got != want {
		t.Errorf("ResolveURL = %q, want %q", got, want)
	}
}

// A filename with URL-significant characters must be escaped, or the '#' turns
// the rest into a fragment and the GET hits the wrong path.
func TestResolveURLEscapesSpecialChars(t *testing.T) {
	c := &Client{BaseURL: "https://huggingface.co"}
	got := c.ResolveURL("org/repo", "main", "weights#2.safetensors")
	want := "https://huggingface.co/org/repo/resolve/main/weights%232.safetensors"
	if got != want {
		t.Errorf("ResolveURL = %q, want %q", got, want)
	}
	// Path separators must survive as separators, not be escaped.
	nested := c.ResolveURL("org/repo", "main", "sub/dir/model.json")
	if nested != "https://huggingface.co/org/repo/resolve/main/sub/dir/model.json" {
		t.Errorf("nested path mangled: %q", nested)
	}
}

func TestWantedFilesKeepsMLXFilesAndDropsForeignWeights(t *testing.T) {
	files := []File{
		{Path: "config.json"},
		{Path: "model.safetensors"},
		{Path: "model.safetensors.index.json"},
		{Path: "tokenizer.json"},
		{Path: "tokenizer_config.json"},
		{Path: "special_tokens_map.json"},
		{Path: "merges.txt"},
		{Path: "vocab.json"},
		{Path: "chat_template.jinja"},
		{Path: "tokenizer.model"}, // sentencepiece — required by some models
		// Not needed / wrong runtime:
		{Path: ".gitattributes"},
		{Path: "pytorch_model.bin"},
		{Path: "model.onnx"},
		{Path: "model.gguf"},
		{Path: "figure.png"},
	}
	got := WantedFiles(files)

	wantKept := map[string]bool{
		"config.json": true, "model.safetensors": true,
		"model.safetensors.index.json": true, "tokenizer.json": true,
		"tokenizer_config.json": true, "special_tokens_map.json": true,
		"merges.txt": true, "vocab.json": true,
		"chat_template.jinja": true, "tokenizer.model": true,
		"README.md": true,
	}
	for _, f := range got {
		if !wantKept[f.Path] {
			t.Errorf("file %q should have been filtered out", f.Path)
		}
	}
	kept := map[string]bool{}
	for _, f := range got {
		kept[f.Path] = true
	}
	// tokenizer.model must survive: dropping it silently breaks sentencepiece models.
	if !kept["tokenizer.model"] {
		t.Error("tokenizer.model must be kept — some models cannot tokenize without it")
	}
	if !kept["model.safetensors"] {
		t.Error("weights must be kept")
	}
	if kept["pytorch_model.bin"] || kept["model.onnx"] || kept["model.gguf"] {
		t.Error("non-MLX weight formats must be filtered out to save bandwidth")
	}
}

func TestHasWeights(t *testing.T) {
	if HasWeights([]File{{Path: "config.json"}}) {
		t.Error("a repo with no safetensors has no weights")
	}
	if !HasWeights([]File{{Path: "model-00001-of-00002.safetensors"}}) {
		t.Error("sharded safetensors count as weights")
	}
}

func TestTotalSize(t *testing.T) {
	got := TotalSize([]File{{Size: 100}, {Size: 250}})
	if got != 350 {
		t.Errorf("TotalSize = %d, want 350", got)
	}
}

func TestProgressPercentAndETA(t *testing.T) {
	p := Progress{Completed: 50, Total: 200, BytesPerSec: 10}
	if got := p.Percent(); got != 25 {
		t.Errorf("Percent = %v, want 25", got)
	}
	if got := p.ETA().Seconds(); got != 15 {
		t.Errorf("ETA = %vs, want 15s", got)
	}
	// Guard against divide-by-zero on an empty repo.
	if got := (Progress{}).Percent(); got != 0 {
		t.Errorf("Percent of empty progress = %v, want 0", got)
	}
}

// fakeHub serves a tree endpoint and resolve endpoints for a set of files,
// honouring Range requests the way the real Hub CDN does.
//
// Handlers run concurrently (the downloader fetches files in parallel), so the
// bookkeeping maps are mutex-guarded.
type fakeHub struct {
	files map[string][]byte
	// ignoreRange makes the server reply 200 with the whole body even when a
	// Range was requested, which some CDNs and proxies do.
	ignoreRange bool
	// lfs maps a path to the LFS content oid (sha256) advertised in the tree.
	lfs map[string]string
	// badContentRange makes a 206 reply carry a Content-Range that starts at the
	// wrong offset, simulating a misbehaving CDN/proxy.
	badContentRange bool
	// always416 makes every resolve request fail with 416, even a plain GET
	// without a Range header, simulating a broken proxy or CDN edge.
	always416 bool

	mu sync.Mutex
	// ranges records the Range header seen per file path.
	ranges map[string]string
	hits   map[string]int
}

func newFakeHub(files map[string][]byte) *fakeHub {
	return &fakeHub{files: files, ranges: map[string]string{}, hits: map[string]int{}}
}

// rangeFor returns the Range header the server saw for a path.
func (f *fakeHub) rangeFor(path string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.ranges[path]
}

// hitsFor returns how many times a path was fetched.
func (f *fakeHub) hitsFor(path string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.hits[path]
}

func (f *fakeHub) server(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/models/org/repo/tree/main", func(w http.ResponseWriter, r *http.Request) {
		var entries []File
		for p, b := range f.files {
			e := File{Path: p, Size: int64(len(b))}
			if oid, ok := f.lfs[p]; ok {
				e.LFS = &struct {
					OID  string `json:"oid"`
					Size int64  `json:"size"`
				}{OID: oid, Size: int64(len(b))}
			}
			entries = append(entries, e)
		}
		json.NewEncoder(w).Encode(entries)
	})
	mux.HandleFunc("/org/repo/resolve/main/", func(w http.ResponseWriter, r *http.Request) {
		name := r.URL.Path[len("/org/repo/resolve/main/"):]
		body, ok := f.files[name]
		if !ok {
			http.Error(w, "no such file", http.StatusNotFound)
			return
		}
		rng := r.Header.Get("Range")
		f.mu.Lock()
		f.hits[name]++
		if rng != "" {
			f.ranges[name] = rng
		}
		f.mu.Unlock()
		if f.always416 {
			w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
			return
		}
		if rng != "" && !f.ignoreRange {
			var start int64
			fmt.Sscanf(rng, "bytes=%d-", &start)
			if start >= int64(len(body)) {
				w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
				return
			}
			if f.badContentRange {
				// A misbehaving proxy: claims 206 but resumes at offset 0 and sends
				// the WHOLE body. Appending that onto our .part overflows the size
				// check; the client must detect the wrong range and restart instead.
				w.Header().Set("Content-Range", fmt.Sprintf("bytes 0-%d/%d", len(body)-1, len(body)))
				w.WriteHeader(http.StatusPartialContent)
				w.Write(body)
				return
			}
			w.Header().Set("Content-Range",
				fmt.Sprintf("bytes %d-%d/%d", start, len(body)-1, len(body)))
			w.WriteHeader(http.StatusPartialContent)
			w.Write(body[start:])
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write(body)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func containsStr(hay, needle string) bool {
	return len(hay) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(hay); i++ {
			if hay[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}
