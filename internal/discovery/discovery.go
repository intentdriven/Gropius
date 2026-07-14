// Package discovery advertises the Gropius server over Bonjour/mDNS so other
// machines on the network can find it without being told an IP address.
package discovery

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"

	"github.com/brutella/dnssd"

	"github.com/intentdriven/Gropius/internal/config"
)

// ServiceType is Gropius' own mDNS service type.
//
// A dedicated type rather than _http._tcp: clients looking for an inference
// endpoint should not have to sift through every web server on the network.
const ServiceType = "_gropius._tcp"

// serviceHost derives the name Gropius publishes its own address records under.
//
// It is deliberately NOT the machine's LocalHostName. See the comment on
// dnssd.Config.Host below: claiming the machine's name makes macOS rename the
// machine. "gropius-alicesmac.local" collides with nothing.
func serviceHost(localHostName string) string {
	safe := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
			return r
		case r >= 'A' && r <= 'Z':
			return r + ('a' - 'A')
		}
		return '-'
	}, localHostName)
	if safe == "" {
		safe = "host"
	}
	return "gropius-" + safe
}

// Advertiser publishes the service on the local network.
type Advertiser struct {
	Port int
	// Models is the number of servable models, published in the TXT record.
	Models func() int
	// AuthRequired reports whether clients need a bearer token.
	AuthRequired bool
	Log          *slog.Logger

	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
}

// Start begins advertising. It returns immediately; the responder runs in the
// background until Stop.
//
// Advertising failures are not fatal: the server still works, callers just have
// to use an IP or hostname. Bonjour is a convenience, not a dependency.
func (a *Advertiser) Start(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.cancel != nil {
		return nil // already advertising
	}
	if a.Log == nil {
		a.Log = slog.Default()
	}

	host := config.LocalHostName()
	if host == "" {
		host = "gropius"
	}

	auth := "none"
	if a.AuthRequired {
		auth = "bearer"
	}
	models := 0
	if a.Models != nil {
		models = a.Models()
	}

	cfg := dnssd.Config{
		Name: "Gropius (" + host + ")",
		Type: ServiceType,

		// Host is the name in OUR address records — and it must NEVER be the
		// machine's own LocalHostName.
		//
		// dnssd is a standalone responder: whatever Host is set to, it publishes
		// A/AAAA records claiming that name. macOS's mDNSResponder already owns
		// <LocalHostName>.local authoritatively. Claiming the same name makes
		// macOS detect a collision and *rename the user's machine* (AlicesMac ->
		// AlicesMac-2) to resolve it. That is a real, persistent change to the
		// user's system settings, and we caused it once.
		//
		// So we publish under a name of our own that nothing else can own. The
		// SRV target points here, our A record resolves it, and macOS's ownership
		// of <LocalHostName>.local is left completely alone.
		Host: serviceHost(host),
		Port: a.Port,
		Text: map[string]string{
			"txtvers": "1",
			"api":     "openai",
			"path":    "/v1",
			"auth":    auth,
			"models":  strconv.Itoa(models),
		},
	}
	service, err := dnssd.NewService(cfg)
	if err != nil {
		return fmt.Errorf("build mDNS service: %w", err)
	}

	responder, err := dnssd.NewResponder()
	if err != nil {
		return fmt.Errorf("create mDNS responder: %w", err)
	}
	if _, err := responder.Add(service); err != nil {
		return fmt.Errorf("add mDNS service: %w", err)
	}

	rctx, cancel := context.WithCancel(ctx)
	a.cancel = cancel
	a.done = make(chan struct{})

	go func() {
		defer close(a.done)
		// Respond blocks until the context is cancelled. On darwin it logs a
		// benign "unable to wait for link updates" (netlink is Linux-only).
		if err := responder.Respond(rctx); err != nil && rctx.Err() == nil {
			a.Log.Warn("mDNS advertising stopped", "err", err)
		}
	}()

	a.Log.Info("advertising on the local network",
		"service", ServiceType, "name", cfg.Name, "port", a.Port)
	return nil
}

// Stop withdraws the advertisement.
func (a *Advertiser) Stop() {
	a.mu.Lock()
	cancel, done := a.cancel, a.done
	a.cancel, a.done = nil, nil
	a.mu.Unlock()

	if cancel != nil {
		cancel()
		<-done
	}
}
