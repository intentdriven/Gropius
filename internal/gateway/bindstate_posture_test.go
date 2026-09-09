//go:build !prod

// Fixes this machine's interface list through netshape.SetEnumerator, which
// the release build compiles out, so this file is compiled out with it.

package gateway

import (
	"reflect"
	"testing"

	"github.com/intentdriven/Gropius/internal/bind"
	"github.com/intentdriven/Gropius/internal/config"
)

// The posture page (itd-2609081718534201) states whether the server reaches
// another machine and whether the Bonjour advert runs, and both are facts
// about the RUNNING bind. Read off the stored configuration, a mode saved and
// not yet in force would report the advert off while it was still up; read
// off the endpoint list, an IPv6-only Mac — whose addresses the list cannot
// name — would report a wildcard bind as loopback-only. So the snapshot
// carries the plan's own answers, the way the exposure warning is asked of
// the sockets (adr-2609081118587999 rule 4), and the page reads those.
func TestTheBindStateCarriesWhatTheRunningBindIs(t *testing.T) {
	stubIfaces(t, testIface("lo0", "127.0.0.1"), testIface("en0", "192.0.2.10"))
	cases := []struct {
		name string
		cfg  func(c *config.Config)
		plan bind.Plan
		want BindState
	}{
		{
			"the wildcard",
			func(c *config.Config) { c.Host = "0.0.0.0" },
			bind.ForHost("0.0.0.0"),
			BindState{InForce: config.BindModeHost, Wildcard: true, ReachesOtherMachines: true},
		},
		{
			"one address",
			func(c *config.Config) { c.Host = "192.0.2.10" },
			bind.ForHost("192.0.2.10"),
			BindState{InForce: config.BindModeHost, Bound: "192.0.2.10", ReachesOtherMachines: true},
		},
		{
			"this Mac only",
			func(c *config.Config) { c.Host = "127.0.0.1" },
			bind.ForHost("127.0.0.1"),
			BindState{InForce: config.BindModeHost},
		},
		{
			// The mode is chosen and not running: the page must not report
			// the advert off, or the wildcard's addresses gone, on a save.
			"the private-network mode saved and not yet in force",
			func(c *config.Config) { c.Host = "0.0.0.0"; c.BindMode = config.BindModePrivateNetwork },
			bind.ForHost("0.0.0.0"),
			BindState{Mode: config.BindModePrivateNetwork, InForce: config.BindModeHost, Wildcard: true, ReachesOtherMachines: true},
		},
		{
			"a bind that narrowed to this Mac",
			func(c *config.Config) { c.Host = "0.0.0.0" },
			bind.ForHost("0.0.0.0").WithoutExtra("could not listen"),
			BindState{InForce: config.BindModeHost, Refusal: "could not listen"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := config.Default()
			c.cfg(&cfg)
			got := bindState(cfg, c.plan)
			got.Candidates = nil
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("bindState() = %+v, want %+v", got, c.want)
			}
		})
	}
}

// The snapshot carries the advert decision the app made at start, and a live
// change to the setting does not move it: the posture page reads this, and
// the advert a save cannot stop must not be reported stopped.
func TestTheSnapshotCarriesTheAdvertDecisionMadeAtStart(t *testing.T) {
	stubIfaces(t, testIface("lo0", "127.0.0.1"), testIface("en0", "192.0.2.5"))
	cfg := config.Default()
	cfg.Host = "0.0.0.0"
	cfg.Advertise = true
	ctrl := newTestControlAppWithBind(t, cfg, bind.ForHost("0.0.0.0"))
	if !ctrl.snapshot().Bind.Advertising {
		t.Fatal("Bind.Advertising = false for a wildcard bind with the setting on")
	}
	live := ctrl.App.Config()
	live.Advertise = false
	if err := ctrl.App.SetConfig(live); err != nil {
		t.Fatal(err)
	}
	if !ctrl.snapshot().Bind.Advertising {
		t.Error("Bind.Advertising followed a live save — the advert started at launch is still running")
	}
	off := config.Default()
	off.Host = "0.0.0.0"
	off.Advertise = false
	if newTestControlAppWithBind(t, off, bind.ForHost("0.0.0.0")).snapshot().Bind.Advertising {
		t.Error("Bind.Advertising = true with the setting off at start")
	}
}
