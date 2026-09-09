package discovery

import (
	"context"
	"fmt"

	"github.com/brutella/dnssd"
)

// announcer registers one mDNS service and hands back the registration that
// serves it.
//
// The seam exists so the advertisement lifecycle — register, respond, withdraw
// — can be driven in a test without opening a multicast socket. The only
// production implementation is dnssdAnnouncer.
type announcer interface {
	// Register builds the service described by cfg and hands it to a fresh
	// responder. It does not begin responding; the caller does that.
	Register(cfg dnssd.Config) (registration, error)
}

// registration is one service that has been handed to a responder.
//
// There is deliberately no way to change a registration's TXT record. dnssd
// offers one — ServiceHandle.UpdateText — and it is not safe to call: it writes
// Service.Text with no locking while the responder goroutine reads that same
// field under a mutex private to the library. A TXT change is therefore a new
// registration, never an edit to a live one (iss-12).
type registration interface {
	// Respond serves the registered service until ctx is cancelled, then
	// withdraws it (the mDNS "goodbye") and returns.
	Respond(ctx context.Context) error
}

// dnssdAnnouncer is the real registration path, over github.com/brutella/dnssd.
type dnssdAnnouncer struct{}

func (dnssdAnnouncer) Register(cfg dnssd.Config) (registration, error) {
	service, err := dnssd.NewService(cfg)
	if err != nil {
		return nil, fmt.Errorf("build mDNS service: %w", err)
	}
	responder, err := dnssd.NewResponder()
	if err != nil {
		return nil, fmt.Errorf("create mDNS responder: %w", err)
	}
	// The service handle is deliberately discarded: the only thing it offers
	// beyond what Service() reports is UpdateText, which races the responder.
	if _, err := responder.Add(service); err != nil {
		return nil, fmt.Errorf("add mDNS service: %w", err)
	}
	return &dnssdRegistration{responder: responder}, nil
}

type dnssdRegistration struct {
	responder dnssd.Responder
}

func (r *dnssdRegistration) Respond(ctx context.Context) error {
	return r.responder.Respond(ctx)
}
