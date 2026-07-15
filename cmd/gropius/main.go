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
	"syscall"
	"time"

	"github.com/intentdriven/Gropius/internal/app"
	"github.com/intentdriven/Gropius/internal/config"
	"github.com/intentdriven/Gropius/internal/discovery"
	"github.com/intentdriven/Gropius/internal/gateway"
	"github.com/intentdriven/Gropius/internal/ui"
)

func main() {
	var (
		headless = flag.Bool("headless", false, "run without the menu bar icon (for launchd)")
		root     = flag.String("root", "", "override the data directory")
		showVer  = flag.Bool("version", false, "print the version and exit")
	)
	flag.Parse()

	if *showVer {
		fmt.Println("gropius 1.0")
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

	cfg, err := config.Load(paths.Config)
	if err != nil {
		log.Warn("config could not be read; using defaults", "err", err)
		cfg = config.Default()
	}

	// Claim the port. Losing this race to a live server is a normal outcome, not
	// an error: another account (or another copy of the app) is already serving.
	// A predecessor still shutting down is NOT a loss — acquireListener waits for
	// the port to free rather than falling into client mode with nothing serving.
	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	ln, claimed, err := acquireListener(addr, 5*time.Second, func() bool {
		return serverResponding(cfg.Port)
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

	if err := runServer(ln, paths, cfg, *headless, log); err != nil {
		log.Error("server stopped", "err", err)
		os.Exit(1)
	}
}

// runServer is the primary instance: it owns the models and the GPU.
func runServer(ln net.Listener, paths config.Paths, cfg config.Config, headless bool, log *slog.Logger) error {
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

	// A LAN-bound listener with no key is open to the whole network. The control
	// panel warns about this, but a user running headless never sees it — say so
	// on stderr too.
	if cfg.ExposedToLAN() && cfg.APIKey == "" {
		log.Warn("SECURITY: bound to a LAN address with no API key — anyone on your network can use this server; set a key in Settings or bind to 127.0.0.1")
	}

	// OpenAI-compatible API — LAN-facing, guarded by the optional API key. The
	// gateway reads the key live (a.Config) so setting one in the control panel
	// takes effect without a restart.
	g := gateway.New(gateway.Options{ConfigFunc: a.Config, Pool: a.Pool, Models: a.Registry, Log: log})
	apiHandler := g.Handler()
	for _, p := range []string{"/v1/", "/health"} {
		mux.Handle(p, apiHandler)
	}

	// Control plane + web UI — administrative, so loopback-only (Control.Handler
	// enforces it). Mounted at "/" as the catch-all for everything that is not a
	// /v1 or /health request.
	ctrl := &gateway.Control{App: a, UI: ui.Handler()}
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
			AuthRequired: cfg.APIKey != "",
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
	// with SIGTRAP the moment the tray initialises. Everything else is already
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

// openBrowser opens the control panel.
func openBrowser(url string) {
	exec.Command("open", url).Start()
}

// withLogging logs API requests but not the noisy static-asset and polling ones.
func withLogging(next http.Handler, log *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isNoisy(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Info("request",
			"method", r.Method, "path", r.URL.Path,
			"from", r.RemoteAddr, "took", time.Since(start).Round(time.Millisecond))
	})
}

func isNoisy(path string) bool {
	switch path {
	case "/api/events", "/api/state", "/style.css", "/app.js", "/favicon.ico", "/":
		return true
	}
	return filepath.Ext(path) != ""
}
