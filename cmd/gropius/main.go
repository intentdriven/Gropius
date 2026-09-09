// Command gropius is the menu-bar app and model server.
//
// One process does both jobs. If another instance already holds the port — for
// example, one started by a different macOS user account — this one does not
// fight it: it becomes a menu-bar client of the server that is already running.
// That way a single copy of each model is loaded on the GPU no matter how many
// accounts are logged in.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/intentdriven/Gropius/internal/app"
	"github.com/intentdriven/Gropius/internal/config"
	"github.com/intentdriven/Gropius/internal/discovery"
	"github.com/intentdriven/Gropius/internal/gateway"
	"github.com/intentdriven/Gropius/internal/ui"
)

// loopbackBind is the Host every fail-closed path narrows to: reachable from
// this Mac and from nothing else.
const loopbackBind = "127.0.0.1"

// version is stamped by the Makefile via -ldflags "-X main.version=…"; a
// build outside make reports "dev".
var version = "dev"

func main() {
	var (
		headless = flag.Bool("headless", false, "run without the menu bar icon (for launchd)")
		root     = flag.String("root", "", "override the data directory")
		showVer  = flag.Bool("version", false, "print the version and exit")
	)
	flag.Parse()

	// Go's flag package stops at the first non-flag argument and leaves the rest
	// in flag.Args(), which nothing here used to read — so a positional argument
	// was silently discarded and the process went on to start the server. That is
	// the wrong default for this program in particular: `gropius install` bound
	// the configured address, generated and printed an API key, advertised over
	// Bonjour and never returned, so a typo or a subcommand name guessed against
	// a build that predates it stood up a LAN-exposed server instead of saying it
	// did not understand. Refuse before any of that happens.
	if msg := refuseUnknownArgs(flag.Args()); msg != "" {
		fmt.Fprintln(os.Stderr, msg)
		os.Exit(2)
	}

	if *showVer {
		fmt.Println("gropius " + version)
		return
	}

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	rootDir := *root
	if rootDir == "" {
		var err error
		rootDir, err = config.DefaultRoot()
		if err != nil {
			log.Error("cannot find the application support directory", "err", err)
			os.Exit(1)
		}
	}
	paths := config.NewPaths(rootDir)

	start := loadStartupConfig(paths.Config)
	cfg := start.Config
	if start.Problem != "" {
		log.Error(start.Problem, start.Args...)
	}
	warnStartupNotices(log, start.Notices)

	// Fail closed on an exposed bind with no key. A LAN-bound listener with no
	// API key is reachable, unauthenticated, by everyone on the network, and a
	// warning is not a control: the operator running headless never reads it,
	// and the window between first launch and setting a key is exactly when the
	// machine is undefended. Generate a key, persist it so it survives the
	// restart, and announce it loudly enough to be used.
	//
	// Done before app.New so the gateway never serves a request under the empty
	// key. A save that fails is fatal to the exposure, not to the process: the
	// bind drops to loopback rather than continuing open, because a key held
	// only in memory would vanish on restart and reopen the endpoint.
	if cfg.ExposedToLAN() && cfg.APIKey == "" {
		lockDown := func(msg string, args ...any) {
			log.Error(msg, args...)
			cfg.APIKey = ""
			cfg.Host = loopbackBind
			cfg.Advertise = false
		}
		key, err := config.GenerateAPIKey()
		switch {
		case err != nil:
			lockDown("could not generate an API key for a LAN-exposed bind — starting locked down to loopback only", "err", err)
		default:
			cfg.APIKey = key
			if err := config.Save(paths.Config, cfg); err != nil {
				lockDown("could not save the generated API key — starting locked down to loopback only so the endpoint is not left open", "path", paths.Config, "err", err)
				break
			}
			// That save wrote the file from the settings in force, repairs
			// included, so there is nothing left in it to warn about.
			start.Notices.Repaired = nil
			log.Warn("SECURITY: this server binds a LAN address, so an API key was generated and saved; clients must send it as \"Authorization: Bearer <key>\". Change or clear it in Settings.",
				"api_key", key)
		}
	}

	// Claim the port. Losing this race to a live server is a normal outcome, not
	// an error: another account (or another copy of the app) is already serving.
	// A predecessor still shutting down is NOT a loss — acquireListener waits for
	// the port to free rather than falling into client mode with nothing serving.
	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	ln, claimed, err := acquireListener(addr, 5*time.Second, func() portHolder {
		return probePortHolder(paths, cfg.Port)
	})
	if err != nil {
		log.Error("cannot listen", "addr", addr, "err", err)
		os.Exit(1)
	}
	if !claimed {
		log.Info("another Gropius server is already running; starting as a client",
			"port", cfg.Port)
		runClient(cfg, *headless, log)
		return
	}

	if err := runServer(ln, paths, cfg, start.Notices.Repaired, *headless, log); err != nil {
		log.Error("server stopped", "err", err)
		os.Exit(1)
	}
}

// startupConfig is what the process will run with, and what to say about how
// it got there.
type startupConfig struct {
	Config  config.Config
	Notices config.Notices
	// Problem is the message explaining a lockdown, empty when config.json
	// loaded cleanly. Args carries its structured fields.
	Problem string
	Args    []any
}

// loadStartupConfig loads config.json and decides what to run when it will not
// load. Every failure narrows the bind to loopback and stops Bonjour; what
// differs is how much of the operator's configuration survives, and what the
// log says happened.
//
// The three cases are genuinely different, and collapsing them was a bug in
// both halves:
//
//   - The file is absent. A fresh install: the shipping defaults, no message.
//   - The file cannot be read or parsed. Nothing is known about what the
//     operator wanted — a truncated file is a truncated file — so the shipping
//     defaults, locked down. The defaults are LAN-exposed with no key, so
//     running them as-is would swing a hardened server wide open.
//   - The file read and parsed and one setting failed Validate. Everything
//     except the bind is known and good, and it was all being thrown away: the
//     operator's API key, port, pinned models, memory budget and statistics
//     retention were replaced by defaults because a hand-edited Host would not
//     bind — under a log line reading "config.json could not be read" about a
//     file that read perfectly. The rest is kept and only the bind is locked
//     down. If the configuration is still invalid with a loopback bind, then
//     the objection was to something else and the defaults are all that is
//     left.
func loadStartupConfig(path string) startupConfig {
	cfg, notices, err := config.Load(path)
	if err == nil {
		return startupConfig{Config: cfg, Notices: notices}
	}

	lockedDefaults := config.Default()
	lockedDefaults.Host = loopbackBind
	lockedDefaults.Advertise = false

	var invalid *config.InvalidError
	if !errors.As(err, &invalid) {
		return startupConfig{
			Config:  lockedDefaults,
			Problem: "config.json could not be read — starting locked down to loopback only so the server is not unintentionally exposed; fix or delete it and restart",
			Args:    []any{"path", path, "err", err},
		}
	}

	locked := invalid.Parsed
	locked.Host = loopbackBind
	locked.Advertise = false
	if err := locked.Validate(); err != nil {
		return startupConfig{
			Config:  lockedDefaults,
			Problem: "config.json is not a valid configuration and locking the bind down does not make it one — starting from the shipping defaults, loopback only; fix or delete it and restart",
			Args:    []any{"path", path, "err", invalid.Err, "with_a_loopback_bind", err},
		}
	}
	return startupConfig{
		Config:  locked,
		Notices: invalid.Notices,
		Problem: "config.json is not a valid configuration — the bind address is locked down to loopback only and the rest of your settings are kept; fix it in Settings and restart",
		Args:    []any{"path", path, "err", invalid.Err},
	}
}

// warnStartupNotices reports what loading config.json had to change about it.
//
// Two lines, because the two lists mean opposite things to the person reading
// them. A setting that was IGNORED is not in force: a sampling preference the
// model server would refuse is dropped rather than applied, because applying it
// would break every request that omits that parameter and refusing the file
// would lock the server down to loopback — and dropping it silently would be
// worse than either, leaving a field that shows as blank in the panel with
// nothing to say where it went.
//
// A setting that was REPAIRED is in force right now, in a changed form. Saying
// it was ignored sends an operator to look for a setting that is working, and
// for one of them it is worse than that: a trimmed API key is the key their
// clients must send from that moment on. Neither line blames the model server,
// which has nothing to do with a length this build chose.
func warnStartupNotices(log *slog.Logger, n config.Notices) {
	if len(n.Ignored) > 0 {
		log.Warn("ignoring settings this build cannot use — set them again in Settings",
			"fields", strings.Join(n.Ignored, ", "))
	}
	if len(n.Repaired) > 0 {
		log.Warn("some settings could not be used as written and are in force in a changed form — check them in Settings",
			"fields", strings.Join(n.Repaired, ", "))
	}
}

// runServer is the primary instance: it owns the models and the GPU.
//
// repaired names the settings config.json could not carry as written, which the
// control panel warns about until a save rewrites the file. It is carried in
// rather than re-derived because loading happens once, before the bind is
// decided, and nothing downstream can tell a value that was repaired from one
// the operator wrote that way.
func runServer(ln net.Listener, paths config.Paths, cfg config.Config, repaired []string, headless bool, log *slog.Logger) error {
	a, err := app.New(app.Options{Paths: paths, Config: cfg, Log: log})
	if err != nil {
		return err
	}
	defer a.Close()

	// Install the MLX runtime in the background. The UI is usable immediately and
	// shows setup progress; blocking the whole app behind a multi-minute pip
	// install would just look like a hang.
	go func() {
		if err := a.Provisioner.Ensure(context.Background()); err != nil {
			log.Error("MLX runtime setup failed", "err", err)
			return
		}
		log.Info("MLX runtime ready")
	}()

	mux := http.NewServeMux()

	// OpenAI-compatible API — LAN-facing, guarded by the optional API key. The
	// gateway reads the key live (a.Config) so setting one in the control panel
	// takes effect without a restart.
	g := gateway.New(gateway.Options{ConfigFunc: a.Config, Pool: a.Pool, Models: a.Registry, Log: log, Stats: a.Stats})
	apiHandler := g.Handler()
	for _, p := range []string{"/v1/", "/health"} {
		mux.Handle(p, apiHandler)
	}

	// Control plane + web UI — administrative, so loopback-only (Control.Handler
	// enforces it). Mounted at "/" as the catch-all for everything that is not a
	// /v1 or /health request.
	ctrl := &gateway.Control{App: a, UI: ui.Handler(), Root: paths.Root, Repaired: repaired}
	mux.Handle("/", ctrl.Handler())

	srv := &http.Server{
		Handler: withLogging(mux, log),
		// Generation legitimately takes minutes, so there is no write timeout.
		ReadHeaderTimeout: 15 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	go func() {
		log.Info("serving", "addr", ln.Addr().String(), "ui", panelURL(cfg))
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("http server failed", "err", err)
		}
	}()

	// Advertise on the network so other machines can find this Mac by name.
	var adv *discovery.Advertiser
	if cfg.Advertise && cfg.ExposedToLAN() {
		adv = &discovery.Advertiser{
			Port:         cfg.Port,
			Models:       func() int { return len(a.Registry.Ready()) },
			AuthRequired: func() bool { return a.Config().APIKey != "" },
			Log:          log,
		}
		if err := adv.Start(context.Background()); err != nil {
			// Not fatal: clients can still use an IP address.
			log.Warn("could not advertise on the network", "err", err)
			adv = nil
		}
	}

	stop := func() {
		if adv != nil {
			adv.Stop()
		}
		// Graceful shutdown waits for open connections to finish — but the control
		// panel holds a /api/events SSE stream open indefinitely, so a plain
		// Shutdown would block for the whole timeout on any open dashboard tab.
		// Give real in-flight requests a short grace period, then force-close the
		// long-lived streams so Quit is near-instant.
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			srv.Close()
		}
	}

	// Quit on ^C / SIGTERM as well as from the menu.
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

	if headless {
		<-sigs
		log.Info("shutting down")
		stop()
		return nil
	}

	// AppKit requires its event loop to own the main thread, so systray.Run must
	// be called from the main goroutine — running it in a `go` statement crashes
	// with SIGTRAP the moment the tray initializes. Everything else is already
	// running in the background, so blocking here is exactly right.
	go func() {
		<-sigs
		log.Info("shutting down")
		quitMenuBar()
	}()

	runMenuBar(a, log) // blocks until Quit
	stop()
	return nil
}

// runClient is a secondary instance: another Gropius already owns the port, so
// this one is just a menu-bar shortcut to it.
func runClient(cfg config.Config, headless bool, log *slog.Logger) {
	if headless {
		log.Info("a server is already running; nothing to do")
		return
	}
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigs
		quitMenuBar()
	}()

	// Must run on the main goroutine — see runServer.
	runClientMenuBar(cfg)
}

func isAddrInUse(err error) bool {
	return errors.Is(err, syscall.EADDRINUSE)
}

func panelURL(cfg config.Config) string {
	return fmt.Sprintf("http://localhost:%d/", cfg.Port)
}

// openBrowser opens the control panel. Reap the child in the background: a
// Start without Wait leaves one zombie per menu click for the app's lifetime.
//
// /usr/bin/open by absolute path, not by name: the URL handed over carries
// this server's address, and on a Mac shared by several accounts a directory
// another account writes can sit ahead of /usr/bin on this one's PATH. Same
// rule as internal/capability's /usr/sbin/sysctl.
func openBrowser(url string) {
	cmd := exec.Command("/usr/bin/open", url)
	if cmd.Start() == nil {
		go cmd.Wait()
	}
}

// withLogging logs API requests but not the noisy static-asset and polling ones.
//
// The line describes the call, never the caller: method, path, status and
// duration are what a failure is diagnosed from, and the client's network
// address is deliberately left out so that serving a request records nothing
// about who made it. Debugging one client goes through the per-model debug
// action instead.
func withLogging(next http.Handler, log *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A handler panic is the other way a client address reaches stderr:
		// net/http's own recover logs "http: panic serving <addr>" through the
		// default error log. Report the panic here without the address, then
		// re-panic with ErrAbortHandler, which tells net/http to drop the
		// connection without logging anything itself. This runs before the
		// isNoisy check so a panic on a polled path is reported too.
		defer func() {
			v := recover()
			if v == nil {
				return
			}
			if v != http.ErrAbortHandler {
				var stack [4 << 10]byte
				log.Error("panic serving request",
					"method", r.Method, "path", r.URL.Path,
					"panic", fmt.Sprint(v),
					"stack", string(stack[:runtime.Stack(stack[:], false)]))
			}
			panic(http.ErrAbortHandler)
		}()

		if isNoisy(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)
		log.Info("request",
			"method", r.Method, "path", r.URL.Path,
			"status", rec.status(), "took", time.Since(start).Round(time.Millisecond))
	})
}

// statusRecorder remembers the response status so the log can report it, and
// is otherwise transparent: every call is forwarded to the writer underneath,
// return value and all.
//
// Handlers running under it must reach optional writer features through
// http.NewResponseController, never an http.Flusher or http.Hijacker type
// assertion — embedding the interface means this type satisfies neither.
// Unwrap is what makes the controller work: the streaming completions path
// flushes each SSE chunk that way, and without it the flush fails and chunks
// only leave when net/http's own buffer fills, so a stream arrives in lumps
// with its tail delayed.
type statusRecorder struct {
	http.ResponseWriter
	code int
}

func (s *statusRecorder) WriteHeader(code int) {
	// 1xx codes are interim responses: net/http allows repeated WriteHeader
	// calls for them and the terminal status arrives later, so latching one
	// would report 103 for a request that ended 200 (or 500).
	if s.code == 0 && code >= 200 {
		s.code = code
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if s.code == 0 {
		s.code = http.StatusOK // an implicit WriteHeader
	}
	return s.ResponseWriter.Write(b)
}

func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// status is the code the handler produced; a handler that wrote nothing at all
// still ends as a 200, which is what net/http sends.
func (s *statusRecorder) status() int {
	if s.code == 0 {
		return http.StatusOK
	}
	return s.code
}

func isNoisy(path string) bool {
	switch path {
	case "/api/events", "/api/state", "/style.css", "/app.js", "/favicon.ico", "/":
		return true
	}
	return filepath.Ext(path) != ""
}
