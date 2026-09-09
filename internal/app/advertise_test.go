package app

import (
	"testing"

	"github.com/intentdriven/Gropius/internal/bind"
	"github.com/intentdriven/Gropius/internal/config"
)

// The Bonjour advert is started once, when the process starts, from the
// configuration and the bind in force then, and is stopped only at shutdown.
// The advertise setting can be changed live over the control plane, and a
// change reaches nothing until the next start. So what the app reports about
// the advert is the decision it made at start, never the setting as it stands
// now — a posture page reading the live setting would say "not announcing"
// while the process went on announcing (adr-2609081118587999 rule 4, the
// same rule the keyless warning is held to).
func TestTheAdvertDecisionIsMadeAtStartAndDoesNotFollowASave(t *testing.T) {
	cfg := config.Default()
	cfg.Host = "0.0.0.0"
	cfg.Advertise = true
	a, err := New(Options{Paths: config.NewPaths(t.TempDir()), Config: cfg, Bind: bind.ForHost("0.0.0.0")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	if !a.Advertising() {
		t.Fatal("Advertising() = false for a wildcard bind with the setting on")
	}
	live := a.Config()
	live.Advertise = false
	if err := a.SetConfig(live); err != nil {
		t.Fatal(err)
	}
	if !a.Advertising() {
		t.Error("Advertising() followed a live save — the advert started at launch is still running")
	}
}

// Advertises is the one spelling of the rule cmd/gropius starts the advert
// by: the setting, the mode, and whether the bind reaches another machine.
func TestAdvertisesIsTheSettingTheModeAndTheReach(t *testing.T) {
	wildcard := bind.ForHost("0.0.0.0")
	cases := []struct {
		name string
		cfg  func(c *config.Config)
		plan bind.Plan
		want bool
	}{
		{"the wildcard, advertising on", func(c *config.Config) { c.Host = "0.0.0.0" }, wildcard, true},
		{"advertising switched off", func(c *config.Config) { c.Advertise = false }, wildcard, false},
		{"this Mac only", func(c *config.Config) { c.Host = "127.0.0.1" }, bind.ForHost("127.0.0.1"), false},
		{
			"the private-network mode, with an address bound",
			func(c *config.Config) { c.BindMode = config.BindModePrivateNetwork },
			bind.Private("100.101.102.103", []string{"100.101.102.103"}, ""), // abcd-lint:allow: RFC 6598 shared address space, the classifier's own range
			false,
		},
		{"a bind that narrowed to this Mac", func(c *config.Config) { c.Host = "0.0.0.0" }, wildcard.WithoutExtra("could not listen"), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := config.Default()
			cfg.Advertise = true
			c.cfg(&cfg)
			if got := Advertises(cfg, c.plan); got != c.want {
				t.Errorf("Advertises() = %v, want %v", got, c.want)
			}
		})
	}
}
