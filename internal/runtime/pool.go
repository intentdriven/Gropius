package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"sync"
	"time"
)

// ModelSource resolves a repo id to an on-disk model. The registry implements it.
type ModelSource interface {
	// Resolve returns the model's directory and on-disk size.
	Resolve(repoID string) (path string, bytes int64, err error)
}

// Upstream is a ready model server the gateway can proxy to.
type Upstream struct {
	RepoID string
	// BaseURL is the loopback address of the model server.
	BaseURL string
	// ModelArg is the exact string that must appear in the proxied request's
	// "model" field. mlx-lm treats that field as an instruction to *load* a
	// model, so sending the client's friendly name would make the backend try to
	// download a repo by that name from HuggingFace.
	ModelArg string
}

// Resident describes a loaded model, for the UI.
type Resident struct {
	RepoID   string    `json:"repo_id"`
	Port     int       `json:"port"`
	Bytes    int64     `json:"bytes"`
	LoadedAt time.Time `json:"loaded_at"`
	LastUsed time.Time `json:"last_used"`
	InFlight int       `json:"in_flight"`
}

// PoolOptions configures a Pool.
type PoolOptions struct {
	Launcher Launcher
	Models   ModelSource
	// MaxResidentBytes caps the total on-disk size of simultaneously loaded
	// models. Zero means "60% of physical RAM".
	MaxResidentBytes int64
	// IdleTimeout unloads a model after this long without a request. Zero keeps
	// models resident indefinitely.
	IdleTimeout time.Duration
	// DecodeConcurrency is passed to each model server.
	DecodeConcurrency int
	// ReadyTimeout bounds how long we wait for a model to load. Large models on
	// a cold page cache genuinely take minutes.
	ReadyTimeout time.Duration

	// HTTP is the client used for readiness probes.
	HTTP *http.Client
	// now is injectable for tests.
	now func() time.Time
}

// Pool runs one model server process per model and routes to them.
//
// mlx-lm can switch models within a single process, but doing so *evicts* the
// resident model and reloads from scratch. Behind a multi-client gateway that
// would thrash weights in and out of memory on every alternating request, so the
// pool gives each model its own process and does the routing itself.
type Pool struct {
	opts PoolOptions

	mu      sync.Mutex
	entries map[string]*entry
	closed  bool

	stopIdle chan struct{}
	idleDone chan struct{}
}

// entry is one model server, loaded or loading.
type entry struct {
	repoID   string
	port     int
	bytes    int64
	modelArg string
	proc     Process

	loadedAt time.Time
	lastUsed time.Time
	inFlight int

	// ready is closed once the model answers a real completion.
	ready    chan struct{}
	readyErr error
}

// NewPool creates a pool. Call Close to shut down every model server.
func NewPool(opts PoolOptions) *Pool {
	if opts.HTTP == nil {
		opts.HTTP = &http.Client{Timeout: 30 * time.Second}
	}
	if opts.now == nil {
		opts.now = time.Now
	}
	if opts.MaxResidentBytes == 0 {
		opts.MaxResidentBytes = defaultResidentBudget()
	}
	if opts.ReadyTimeout == 0 {
		opts.ReadyTimeout = 10 * time.Minute
	}
	if opts.DecodeConcurrency < 1 {
		opts.DecodeConcurrency = 1
	}

	p := &Pool{
		opts:     opts,
		entries:  map[string]*entry{},
		stopIdle: make(chan struct{}),
		idleDone: make(chan struct{}),
	}
	go p.reapIdle()
	return p
}

// ErrClosed is returned once the pool is shut down.
var ErrClosed = errors.New("pool is closed")

// Acquire returns a ready upstream for repoID, loading the model if necessary
// and evicting others to make room.
//
// The returned release function must be called when the request finishes. Until
// it is, the model is pinned and cannot be evicted out from under the caller.
func (p *Pool) Acquire(ctx context.Context, repoID string) (*Upstream, func(), error) {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil, nil, ErrClosed
	}

	e, ok := p.entries[repoID]
	if !ok {
		var err error
		e, err = p.startLocked(repoID)
		if err != nil {
			p.mu.Unlock()
			return nil, nil, err
		}
	}
	// Pin it *before* releasing the lock, so a concurrent Acquire for another
	// model cannot evict this one while we are waiting for it to load.
	e.inFlight++
	e.lastUsed = p.opts.now()
	ready := e.ready
	p.mu.Unlock()

	release := func() {
		p.mu.Lock()
		e.inFlight--
		e.lastUsed = p.opts.now()
		p.mu.Unlock()
	}

	select {
	case <-ready:
		if e.readyErr != nil {
			release()
			return nil, nil, e.readyErr
		}
	case <-ctx.Done():
		release()
		return nil, nil, ctx.Err()
	}

	return &Upstream{
		RepoID:   repoID,
		BaseURL:  fmt.Sprintf("http://127.0.0.1:%d", e.port),
		ModelArg: e.modelArg,
	}, release, nil
}

// startLocked launches a model server. Callers must hold p.mu.
func (p *Pool) startLocked(repoID string) (*entry, error) {
	path, size, err := p.opts.Models.Resolve(repoID)
	if err != nil {
		return nil, err
	}

	need := loadCost(size)
	if need > p.opts.MaxResidentBytes {
		return nil, fmt.Errorf(
			"%s needs about %s of memory but the limit is %s — raise the memory budget or choose a smaller quantization",
			repoID, humanBytes(need), humanBytes(p.opts.MaxResidentBytes))
	}
	if err := p.evictForLocked(need); err != nil {
		return nil, err
	}

	port, err := freePort()
	if err != nil {
		return nil, fmt.Errorf("allocate port: %w", err)
	}

	e := &entry{
		repoID:   repoID,
		port:     port,
		bytes:    size,
		modelArg: path,
		loadedAt: p.opts.now(),
		lastUsed: p.opts.now(),
		ready:    make(chan struct{}),
	}

	proc, err := p.opts.Launcher.Launch(context.Background(), Spec{
		RepoID:            repoID,
		ModelPath:         path,
		Port:              port,
		DecodeConcurrency: p.opts.DecodeConcurrency,
	})
	if err != nil {
		return nil, fmt.Errorf("start model server for %s: %w", repoID, err)
	}
	e.proc = proc
	p.entries[repoID] = e

	go p.waitReady(e)
	return e, nil
}

// waitReady probes until the model actually answers a completion, then unblocks
// everyone waiting on it.
func (p *Pool) waitReady(e *entry) {
	ctx, cancel := context.WithTimeout(context.Background(), p.opts.ReadyTimeout)
	defer cancel()

	err := p.probeReady(ctx, e)

	p.mu.Lock()
	e.readyErr = err
	close(e.ready)
	if err != nil {
		// A model that never became ready must not linger in the pool holding a
		// slice of the memory budget. Guard on identity: while this entry was
		// loading it could have been evicted and a *new* entry created under the
		// same repoID key. Deleting by key alone would then orphan that healthy
		// replacement — its process would leak and its memory would stop counting
		// against the budget.
		if p.entries[e.repoID] == e {
			delete(p.entries, e.repoID)
		}
	}
	p.mu.Unlock()

	if err != nil && e.proc != nil {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 15*time.Second)
		_ = e.proc.Stop(stopCtx)
		stopCancel()
	}
}

// probeReady waits for the model to serve a real one-token completion.
//
// /health is not sufficient: mlx_lm.server answers it "ok" the moment the socket
// is up, long before the weights are in memory. The only trustworthy readiness
// signal is a completion that succeeds.
func (p *Pool) probeReady(ctx context.Context, e *entry) error {
	base := fmt.Sprintf("http://127.0.0.1:%d", e.port)

	body, _ := json.Marshal(map[string]any{
		"model":      e.modelArg,
		"messages":   []any{map[string]string{"role": "user", "content": "hi"}},
		"max_tokens": 1,
		"stream":     false,
	})

	backoff := 200 * time.Millisecond
	for {
		// If the process died (bad weights, OOM, missing dependency), stop
		// probing and report it rather than spinning until the timeout.
		select {
		case <-e.proc.Done():
			if err := e.proc.Err(); err != nil {
				return fmt.Errorf("model server for %s exited during startup: %w", e.repoID, err)
			}
			return fmt.Errorf("model server for %s exited during startup", e.repoID)
		default:
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost,
			base+"/v1/chat/completions", bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := p.opts.HTTP.Do(req)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("%s did not become ready within %s", e.repoID, p.opts.ReadyTimeout)
		case <-time.After(backoff):
		}
		if backoff < 2*time.Second {
			backoff *= 2
		}
	}
}

// evictForLocked frees enough budget for need bytes. Callers must hold p.mu.
func (p *Pool) evictForLocked(need int64) error {
	for {
		var used int64
		for _, e := range p.entries {
			used += loadCost(e.bytes)
		}
		if used+need <= p.opts.MaxResidentBytes {
			return nil
		}

		// Evict the least recently used model that nobody is currently using.
		var victim *entry
		for _, e := range p.entries {
			if e.inFlight > 0 {
				continue
			}
			if victim == nil || e.lastUsed.Before(victim.lastUsed) {
				victim = e
			}
		}
		if victim == nil {
			return fmt.Errorf(
				"not enough memory to load another model: every loaded model is currently serving a request (limit %s)",
				humanBytes(p.opts.MaxResidentBytes))
		}
		p.stopEntryLocked(victim)
	}
}

// stopEntryLocked removes an entry and stops its process. Callers must hold p.mu.
func (p *Pool) stopEntryLocked(e *entry) {
	delete(p.entries, e.repoID)
	proc := e.proc
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if proc != nil {
			_ = proc.Stop(ctx)
		}
	}()
}

// ErrNotLoaded is returned by Unload when the model is not resident.
var ErrNotLoaded = errors.New("model is not loaded")

// ErrBusy is returned by Unload when the model is serving a request. Callers can
// test for it with errors.Is rather than matching on message text.
var ErrBusy = errors.New("model is busy")

// Unload stops a model server.
func (p *Pool) Unload(repoID string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	e, ok := p.entries[repoID]
	if !ok {
		return fmt.Errorf("%s: %w", repoID, ErrNotLoaded)
	}
	if e.inFlight > 0 {
		return fmt.Errorf("%s is serving %d request(s); try again in a moment: %w",
			repoID, e.inFlight, ErrBusy)
	}
	p.stopEntryLocked(e)
	return nil
}

// Resident lists the loaded models, most recently used first.
func (p *Pool) Resident() []Resident {
	p.mu.Lock()
	defer p.mu.Unlock()

	out := make([]Resident, 0, len(p.entries))
	for _, e := range p.entries {
		out = append(out, Resident{
			RepoID:   e.repoID,
			Port:     e.port,
			Bytes:    e.bytes,
			LoadedAt: e.loadedAt,
			LastUsed: e.lastUsed,
			InFlight: e.inFlight,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastUsed.After(out[j].LastUsed) })
	return out
}

// reapIdle unloads models that have gone untouched for IdleTimeout.
func (p *Pool) reapIdle() {
	defer close(p.idleDone)

	if p.opts.IdleTimeout <= 0 {
		<-p.stopIdle
		return
	}
	tick := time.NewTicker(p.opts.IdleTimeout / 4)
	defer tick.Stop()

	for {
		select {
		case <-p.stopIdle:
			return
		case <-tick.C:
			p.mu.Lock()
			now := p.opts.now()
			for _, e := range p.entries {
				// Never reap a model that is still loading: its ready channel is
				// open, so tearing it down would waste the load and error every
				// caller waiting on it. isReady checks without blocking.
				if e.inFlight == 0 && isReady(e) && now.Sub(e.lastUsed) >= p.opts.IdleTimeout {
					p.stopEntryLocked(e)
				}
			}
			p.mu.Unlock()
		}
	}
}

// Close stops every model server.
func (p *Pool) Close() error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	procs := make([]Process, 0, len(p.entries))
	for _, e := range p.entries {
		procs = append(procs, e.proc)
	}
	p.entries = map[string]*entry{}
	p.mu.Unlock()

	close(p.stopIdle)
	<-p.idleDone

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	for _, proc := range procs {
		if proc == nil {
			continue
		}
		wg.Add(1)
		go func(pr Process) {
			defer wg.Done()
			_ = pr.Stop(ctx)
		}(proc)
	}
	wg.Wait()
	return nil
}

// isReady reports whether an entry has finished loading (its ready channel is
// closed) without blocking.
func isReady(e *entry) bool {
	select {
	case <-e.ready:
		return true
	default:
		return false
	}
}

// loadCost estimates the memory a model occupies once loaded: its weights plus
// headroom for the KV cache and activations.
func loadCost(diskBytes int64) int64 {
	return diskBytes + diskBytes/5 // 1.2x
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}
