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
	"time"

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
	// AuthRequired reports whether clients currently need a bearer token. It is a
	// callback, not a snapshot: the API key can be set or cleared at runtime from
	// the control panel, and the advertisement must follow — otherwise a client
	// that trusts the hint sends no token to a now-protected server (401) or keeps
	// sending a stale one.
	AuthRequired func() bool
	Log          *slog.Logger

	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
}

// txtRecord builds the TXT map from the live callbacks.
func (a *Advertiser) txtRecord() map[string]string {
	auth := "none"
	if a.AuthRequired != nil && a.AuthRequired() {
		auth = "bearer"
	}
	models := 0
	if a.Models != nil {
		models = a.Models()
	}
	return map[string]string{
		"txtvers": "1",
		"api":     "openai",
		"path":    "/v1",
		"auth":    auth,
		"models":  strconv.Itoa(models),
	}
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
		Text: a.txtRecord(),
	}
	service, err := dnssd.NewService(cfg)
	if err != nil {
		return fmt.Errorf("build mDNS service: %w", err)
	}

	responder, err := dnssd.NewResponder()
	if err != nil {
		return fmt.Errorf("create mDNS responder: %w", err)
	}
	handle, err := responder.Add(service)
	if err != nil {
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

	// Keep the advertised auth/model hints in step with runtime config changes.
	go a.refresh(rctx, responder, handle, cfg.Text)

	a.Log.Info("advertising on the local network",
		"service", ServiceType, "name", cfg.Name, "port", a.Port)
	return nil
}

// refresh periodically re-publishes the TXT record when the advertised auth
// state or model count changes, so a runtime config change (e.g. setting an API
// key in the control panel) is reflected to clients rather than left stale.
func (a *Advertiser) refresh(ctx context.Context, r dnssd.Responder, h dnssd.ServiceHandle, last map[string]string) {
	tick := time.NewTicker(15 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			cur := a.txtRecord()
			if sameText(last, cur) {
				continue
			}
			h.UpdateText(cur, r)
			last = cur
			a.Log.Info("updated network advertisement",
				"auth", cur["auth"], "models", cur["models"])
		}
	}
}

// sameText reports whether two TXT maps are equal.
func sameText(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
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
