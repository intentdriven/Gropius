// Package hub is a pure-Go HuggingFace Hub client: search, file listing, and a
// resumable downloader.
//
// It deliberately does not reproduce huggingface_hub's blobs/snapshots/symlinks
// cache format. mlx-lm loads a model from any plain directory of files, so
// Gropius downloads each repo into <models>/<org>/<name>/ and hands that path
// to the server process. That keeps the on-disk result inspectable and means a
// half-finished download can never masquerade as a valid cache entry.
package hub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"
)

// DefaultBaseURL is the public HuggingFace Hub.
const DefaultBaseURL = "https://huggingface.co"

// Client talks to the HuggingFace Hub HTTP API.
type Client struct {
	BaseURL string
	HTTP    *http.Client
	Token   string // optional; required for gated repos
}

// New returns a Client pointed at the public Hub.
func New() *Client {
	return &Client{
		BaseURL: DefaultBaseURL,
		HTTP: &http.Client{
			// No overall timeout: model downloads legitimately take many
			// minutes. Per-request deadlines come from the caller's context,
			// and stalled reads are caught by the transport's own timeouts.
			Transport: &http.Transport{
				Proxy:                 http.ProxyFromEnvironment,
				MaxIdleConnsPerHost:   8,
				ResponseHeaderTimeout: 30 * time.Second,
				TLSHandshakeTimeout:   15 * time.Second,
			},
		},
	}
}

// Model is a search result / repo summary.
type Model struct {
	ID           string    `json:"id"`
	Downloads    int       `json:"downloads"`
	Likes        int       `json:"likes"`
	Tags         []string  `json:"tags"`
	PipelineTag  string    `json:"pipeline_tag"`
	LastModified time.Time `json:"lastModified"`
}

// Org returns the account that owns the repo ("mlx-community/Foo" -> "mlx-community").
func (m Model) Org() string {
	if i := strings.Index(m.ID, "/"); i >= 0 {
		return m.ID[:i]
	}
	return ""
}

// Name returns the repo name without the owner.
func (m Model) Name() string {
	if i := strings.Index(m.ID, "/"); i >= 0 {
		return m.ID[i+1:]
	}
	return m.ID
}

// Quantization extracts the quant suffix from a model name ("...-4bit" -> "4bit").
// Returns "" when the name carries no recognizable quantization marker.
func (m Model) Quantization() string {
	name := strings.ToLower(m.Name())
	// Ordered longest-first so "-4bit-dwq" wins over "-4bit".
	for _, q := range []string{
		"8bit-dwq", "6bit-dwq", "4bit-dwq", "3bit-dwq",
		"8bit", "6bit", "5bit", "4bit", "3bit", "2bit",
		"bf16", "fp16", "float16", "fp32",
	} {
		if strings.HasSuffix(name, "-"+q) || strings.Contains(name, "-"+q+"-") {
			return q
		}
	}
	return ""
}

// File is one entry in a repo's file tree.
type File struct {
	Path string `json:"path"`
	// Type is "file" or "directory" in the Hub tree listing. Directory entries
	// must be dropped before download: they are not fetchable and a GET of one
	// 404s, which would abort the whole repo download.
	Type string `json:"type"`
	Size int64  `json:"size"`
	OID  string `json:"oid"`
	LFS  *struct {
		OID  string `json:"oid"`
		Size int64  `json:"size"`
	} `json:"lfs,omitempty"`
}

// SearchQuery parameterises a model search.
type SearchQuery struct {
	// Search is a free-text term matched against the repo name.
	Search string
	// Author restricts results to one org, e.g. "mlx-community".
	Author string
	// Limit caps the number of results (the Hub's own default is small).
	Limit int
	// Sort is a Hub sort key, e.g. "downloads", "lastModified", "likes".
	Sort string
}

func (c *Client) baseURL() string {
	if c.BaseURL != "" {
		return c.BaseURL
	}
	return DefaultBaseURL
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}

func (c *Client) newRequest(ctx context.Context, method, u string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, u, nil)
	if err != nil {
		return nil, err
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	req.Header.Set("User-Agent", "gropius/1.0 (+https://github.com/intentdriven/Gropius)")
	return req, nil
}

// APIError is a non-2xx response from the Hub.
type APIError struct {
	StatusCode int
	URL        string
	Body       string
}

func (e *APIError) Error() string {
	switch e.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Sprintf("huggingface denied access to %s (HTTP %d) — the repo may be gated; add an access token in Settings", e.URL, e.StatusCode)
	case http.StatusNotFound:
		return fmt.Sprintf("huggingface has no such repo or file: %s", e.URL)
	case http.StatusTooManyRequests:
		return fmt.Sprintf("huggingface rate-limited the request to %s (HTTP 429)", e.URL)
	}
	return fmt.Sprintf("huggingface returned HTTP %d for %s: %s", e.StatusCode, e.URL, e.Body)
}

// IsNotFound reports whether err is a 404 from the Hub.
func IsNotFound(err error) bool {
	var ae *APIError
	if errors.As(err, &ae) {
		return ae.StatusCode == http.StatusNotFound
	}
	return false
}

// IsAuthRequired reports whether err is the Hub refusing access, which for a
// model repo almost always means it is gated and needs a token.
func IsAuthRequired(err error) bool {
	var ae *APIError
	if errors.As(err, &ae) {
		return ae.StatusCode == http.StatusUnauthorized || ae.StatusCode == http.StatusForbidden
	}
	return false
}

// Search finds models on the Hub.
func (c *Client) Search(ctx context.Context, q SearchQuery) ([]Model, error) {
	v := url.Values{}
	if q.Search != "" {
		v.Set("search", q.Search)
	}
	if q.Author != "" {
		v.Set("author", q.Author)
	}
	limit := q.Limit
	if limit <= 0 {
		limit = 30
	}
	v.Set("limit", strconv.Itoa(limit))
	if q.Sort != "" {
		v.Set("sort", q.Sort)
		v.Set("direction", "-1")
	}
	// full=false keeps the payload small; we only need summary fields here.
	u := c.baseURL() + "/api/models?" + v.Encode()

	req, err := c.newRequest(ctx, http.MethodGet, u)
	if err != nil {
		return nil, err
	}
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("search huggingface: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, apiError(resp, u)
	}
	var models []Model
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxJSONBody)).Decode(&models); err != nil {
		return nil, fmt.Errorf("decode search results: %w", err)
	}
	return models, nil
}

// maxJSONBody caps how large a Hub JSON response we will buffer/decode. Search
// results and a single tree page are at most a few MB; a body near this limit is
// a broken or hostile endpoint, not a real repo listing.
const maxJSONBody = 32 << 20

// maxTreePages bounds how many pagination hops Files will follow. The tree
// endpoint pages at 1000 entries, so this covers repos with up to ~1M files —
// far beyond any real model — while refusing an endpoint that loops forever.
const maxTreePages = 1000

// Files lists a repo's file tree at the given revision (default "main"),
// including sizes, which the downloader needs for progress reporting.
//
// The tree endpoint paginates at 1000 entries and signals more with a
// `Link: <...>; rel="next"` header. We follow it to the end: a repo with more
// than 1000 tree entries (a heavily-sharded model, say) would otherwise yield a
// silently truncated list, and the download would "succeed" while missing shards.
func (c *Client) Files(ctx context.Context, repoID, revision string) ([]File, error) {
	if revision == "" {
		revision = "main"
	}
	u := fmt.Sprintf("%s/api/models/%s/tree/%s?recursive=true",
		c.baseURL(), repoID, url.PathEscape(revision))

	var entries []File
	for page := 0; u != ""; page++ {
		if page >= maxTreePages {
			return nil, fmt.Errorf("file tree for %s did not terminate after %d pages", repoID, maxTreePages)
		}
		req, err := c.newRequest(ctx, http.MethodGet, u)
		if err != nil {
			return nil, err
		}
		resp, err := c.httpClient().Do(req)
		if err != nil {
			return nil, fmt.Errorf("list files for %s: %w", repoID, err)
		}
		if resp.StatusCode != http.StatusOK {
			err := apiError(resp, u)
			resp.Body.Close()
			return nil, err
		}

		var pageEntries []File
		if err := json.NewDecoder(io.LimitReader(resp.Body, maxJSONBody)).Decode(&pageEntries); err != nil {
			resp.Body.Close()
			return nil, fmt.Errorf("decode file tree for %s: %w", repoID, err)
		}
		next := nextPageURL(resp.Header.Get("Link"))
		resp.Body.Close()

		// The Link header is attacker-influenced (it comes from the Hub response).
		// newRequest attaches the bearer token to whatever URL we pass, so a
		// "rel=next" pointing at another host would leak the HuggingFace token off
		// to it. Only follow a next-page URL on the same origin we started from.
		if next != "" && !sameOrigin(c.baseURL(), next) {
			return nil, fmt.Errorf("file tree for %s returned a cross-origin next page (%s) — refusing to follow it", repoID, next)
		}

		entries = append(entries, pageEntries...)
		u = next
	}

	// LFS entries carry the true size in the nested object; the outer size is
	// the pointer file's size for some repos. Prefer the LFS size when present.
	// Directory entries are not fetchable — drop them so they never reach the
	// downloader (a GET of a directory 404s and aborts the whole download).
	out := entries[:0]
	for _, e := range entries {
		if e.Type == "directory" {
			continue
		}
		if e.LFS != nil && e.LFS.Size > 0 {
			e.Size = e.LFS.Size
			e.OID = e.LFS.OID
		}
		out = append(out, e)
	}
	return out, nil
}

// sameOrigin reports whether target has the same scheme and host as base. A
// parse failure or missing host counts as different, i.e. refuse it.
func sameOrigin(base, target string) bool {
	b, err := url.Parse(base)
	if err != nil {
		return false
	}
	t, err := url.Parse(target)
	if err != nil || t.Host == "" {
		return false
	}
	return strings.EqualFold(b.Scheme, t.Scheme) && strings.EqualFold(b.Host, t.Host)
}

// nextPageURL extracts the rel="next" URL from an RFC 8288 Link header, or "".
func nextPageURL(link string) string {
	for _, part := range strings.Split(link, ",") {
		segs := strings.Split(strings.TrimSpace(part), ";")
		if len(segs) < 2 {
			continue
		}
		isNext := false
		for _, s := range segs[1:] {
			if strings.Contains(strings.ToLower(s), `rel="next"`) {
				isNext = true
				break
			}
		}
		if !isNext {
			continue
		}
		u := strings.TrimSpace(segs[0])
		return strings.TrimSuffix(strings.TrimPrefix(u, "<"), ">")
	}
	return ""
}

// RepoSize returns the total download size, in bytes, of the MLX-relevant files
// in a repo at the default revision. This is what Gropius would actually fetch.
func (c *Client) RepoSize(ctx context.Context, repoID string) (int64, error) {
	files, err := c.Files(ctx, repoID, "main")
	if err != nil {
		return 0, err
	}
	return TotalSize(WantedFiles(files)), nil
}

// ResolveURL is the direct-download URL for one file in a repo.
//
// The revision and each file-path segment are escaped: a file named e.g.
// "weights#2.safetensors" would otherwise have everything after '#' parsed as a
// URL fragment, producing a wrong request that 404s and aborts the download.
func (c *Client) ResolveURL(repoID, revision, file string) string {
	if revision == "" {
		revision = "main"
	}
	return fmt.Sprintf("%s/%s/resolve/%s/%s",
		c.baseURL(), repoID, url.PathEscape(revision), escapePathSegments(file))
}

// escapePathSegments percent-escapes each '/'-separated segment while keeping the
// separators, so a repo-relative path stays a valid, correct URL path.
func escapePathSegments(p string) string {
	segs := strings.Split(p, "/")
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}
	return strings.Join(segs, "/")
}

// wantedFile reports whether a repo file is needed to run the model.
//
// This is a deny list rather than an allow list: missing a required file breaks
// the model in ways that surface only at load time, whereas an unexpected extra
// file merely wastes a little disk. The denied extensions are weights in
// formats MLX cannot use (PyTorch, ONNX, GGUF, TF) plus repo cruft — these are
// the entries big enough to matter.
func wantedFile(p string) bool {
	if p == "" || strings.HasSuffix(p, "/") {
		return false
	}
	// Reject any path that could escape the destination directory. safeJoin in
	// the downloader is the real guard, but filtering here means such files never
	// even appear in progress totals or the file list.
	if strings.Contains(p, "..") {
		return false
	}
	base := path.Base(p)
	if base == ".gitattributes" {
		return false
	}
	// Skip anything nested under a docs/assets folder.
	if strings.HasPrefix(p, ".") {
		return false
	}
	deniedExt := []string{
		".bin", ".pth", ".pt", ".ckpt", // PyTorch weights
		".onnx", ".gguf", ".ggml", // other runtimes
		".h5", ".msgpack", ".tflite", // TF/Flax
		".png", ".jpg", ".jpeg", ".gif", ".webp", ".svg", ".mp4", // media
		".zip", ".tar", ".gz",
	}
	lower := strings.ToLower(base)
	for _, ext := range deniedExt {
		if strings.HasSuffix(lower, ext) {
			return false
		}
	}
	return true
}

// WantedFiles filters a file tree down to what MLX needs to load the model,
// de-duplicating by cleaned path. A tree that lists the same path twice (a
// malformed or hostile manifest) would otherwise spawn two goroutines racing to
// write and rename the same file, and double-count it in the size total so
// progress never reaches 100%.
func WantedFiles(files []File) []File {
	out := make([]File, 0, len(files))
	seen := make(map[string]bool, len(files))
	for _, f := range files {
		if !wantedFile(f.Path) {
			continue
		}
		key := path.Clean(f.Path)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, f)
	}
	return out
}

// TotalSize sums the sizes of a file list.
func TotalSize(files []File) int64 {
	var n int64
	for _, f := range files {
		n += f.Size
	}
	return n
}

// HasWeights reports whether the file list contains MLX-loadable weights.
// A repo without safetensors is not a usable MLX model, and we would rather say
// so before downloading gigabytes than after.
func HasWeights(files []File) bool {
	for _, f := range files {
		if strings.HasSuffix(strings.ToLower(f.Path), ".safetensors") {
			return true
		}
	}
	return false
}

func apiError(resp *http.Response, u string) error {
	body := make([]byte, 512)
	n, _ := resp.Body.Read(body)
	return &APIError{StatusCode: resp.StatusCode, URL: u, Body: strings.TrimSpace(string(body[:n]))}
}
