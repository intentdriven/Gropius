// Package discovery advertises the Gropius server over Bonjour/mDNS so other
// machines on the network can find it without being told an IP address.
package discovery

import (
	"context"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/brutella/dnssd"

	"github.com/intentdriven/Gropius/internal/config"
)

// ServiceType is Gropius' own mDNS service type.
//
// A dedicated type rather than _http._tcp: clients looking for an inference
// endpoint should not have to sift through every web server on the network.
const ServiceType = "_gropius._tcp"

// maxDNSLabel is the RFC 1035 ceiling for a single DNS label, in octets. Both
// the host label and the service instance name must respect it: dnssd performs
// no length validation of its own, and a message carrying an over-long label
// fails to pack deep inside its mDNS transport — the send is silently skipped,
// so the service advertises nothing while every API reports success. macOS
// accepts LocalHostNames up to the full 63 characters, so the names built here
// must be clamped, not trusted.
const maxDNSLabel = 63

// labelHeadroom keeps room for the suffix dnssd appends when it renames a
// service to resolve a genuine mDNS name conflict, so the renamed label stays
// within maxDNSLabel too.
const labelHeadroom = 4

// truncateLabel caps s at max bytes without splitting a UTF-8 rune, then drops
// any hyphens left trailing (a DNS host label must not end with one, and a cut
// can expose one).
func truncateLabel(s string, max int) string {
	for len(s) > max {
		_, size := utf8.DecodeLastRuneInString(s)
		s = s[:len(s)-size]
	}
	return strings.TrimRight(s, "-")
}

// serviceHost derives the name Gropius publishes its own address records under.
//
// It is deliberately NOT the machine's LocalHostName. See the comment on
// dnssd.Config.Host below: claiming the machine's name makes macOS rename the
// machine. "gropius-alicesmac.local" collides with nothing.
func serviceHost(localHostName string) string {
	const prefix = "gropius-"
	safe := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
			return r
		case r >= 'A' && r <= 'Z':
			return r + ('a' - 'A')
		}
		return '-'
	}, localHostName)
	safe = truncateLabel(safe, maxDNSLabel-len(prefix)-labelHeadroom)
	if safe == "" {
		safe = "host"
	}
	return prefix + safe
}

// serviceName builds the human-visible service instance name. An instance name
// is a single DNS label just like the host label, so the hostname it embeds is
// capped to keep the whole name legal.
func serviceName(host string) string {
	const wrap = len("Gropius ()")
	return "Gropius (" + truncateLabel(host, maxDNSLabel-wrap-labelHeadroom) + ")"
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

	// announce registers the service. nil means the real dnssd path; tests
	// substitute a fake so the lifecycle can be driven without a socket.
	announce announcer
	// interval is how often the advertised TXT record is re-derived from the
	// callbacks. Zero means defaultRefreshInterval.
	interval time.Duration

	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
}

// defaultRefreshInterval is how often the advertised auth/model hints are
// re-derived from the live callbacks.
const defaultRefreshInterval = 15 * time.Second

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
	if a.announce == nil {
		a.announce = dnssdAnnouncer{}
	}

	host := config.LocalHostName()
	if host == "" {
		host = "gropius"
	}

	cfg := dnssd.Config{
		Name: serviceName(host),
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
	}

	rctx, cancel := context.WithCancel(ctx)
	ad, err := a.publish(rctx, cfg, a.txtRecord())
	if err != nil {
		cancel()
		return err
	}

	a.cancel = cancel
	a.done = make(chan struct{})
	go a.refresh(rctx, cfg, ad, a.done)

	a.Log.Info("advertising on the local network",
		"service", ServiceType, "name", cfg.Name, "port", a.Port)
	return nil
}

// advertisement is one live registration: a service that has been handed to a
// responder which is serving it until its context is cancelled.
type advertisement struct {
	reg    registration
	text   map[string]string
	cancel context.CancelFunc
	done   chan struct{}
}

// publish registers the service carrying text and starts responding for it.
func (a *Advertiser) publish(ctx context.Context, cfg dnssd.Config, text map[string]string) (*advertisement, error) {
	cfg.Text = text
	reg, err := a.announce.Register(cfg)
	if err != nil {
		return nil, err
	}
	rctx, cancel := context.WithCancel(ctx)
	ad := &advertisement{reg: reg, text: text, cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(ad.done)
		// Respond blocks until the context is cancelled. On darwin it logs a
		// benign "unable to wait for link updates" (netlink is Linux-only).
		if err := reg.Respond(rctx); err != nil && rctx.Err() == nil {
			a.Log.Warn("mDNS advertising stopped", "err", err)
		}
	}()
	return ad, nil
}

// withdraw cancels the responder and waits for it to actually exit, so its
// goodbye is on the wire before anything else touches the same name.
func (ad *advertisement) withdraw() {
	ad.cancel()
	<-ad.done
}

// refresh periodically re-publishes the TXT record when the advertised auth
// state or model count changes, so a runtime config change (e.g. setting an API
// key in the control panel) is reflected to clients rather than left stale.
func (a *Advertiser) refresh(ctx context.Context, cfg dnssd.Config, ad *advertisement, done chan struct{}) {
	defer close(done)
	defer func() { ad.withdraw() }()

	interval := a.interval
	if interval <= 0 {
		interval = defaultRefreshInterval
	}
	tick := time.NewTicker(interval)
	defer tick.Stop()

	last := ad.text
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			cur := a.txtRecord()
			if sameText(last, cur) {
				continue
			}
			ad.reg.UpdateText(cur)
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
