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
type registration interface {
	// Respond serves the registered service until ctx is cancelled, then
	// withdraws it (the mDNS "goodbye") and returns.
	Respond(ctx context.Context) error

	// UpdateText re-announces the service under a new TXT record without
	// withdrawing it.
	UpdateText(text map[string]string)
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
	handle, err := responder.Add(service)
	if err != nil {
		return nil, fmt.Errorf("add mDNS service: %w", err)
	}
	return &dnssdRegistration{responder: responder, handle: handle}, nil
}

type dnssdRegistration struct {
	responder dnssd.Responder
	handle    dnssd.ServiceHandle
}

func (r *dnssdRegistration) Respond(ctx context.Context) error {
	return r.responder.Respond(ctx)
}

func (r *dnssdRegistration) UpdateText(text map[string]string) {
	r.handle.UpdateText(text, r.responder)
}
