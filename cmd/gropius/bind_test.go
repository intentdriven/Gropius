package main

import (
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/intentdriven/Gropius/internal/bind"
	"github.com/intentdriven/Gropius/internal/config"
)

// freePort is a port that was momentarily bound and released, so it is very
// likely free for the next Listen.
func freePort(t *testing.T) int {
	t.Helper()
	_, p, err := net.SplitHostPort(freeAddr(t))
	if err != nil {
		t.Fatalf("splitting a probe address: %v", err)
	}
	n, err := strconv.Atoi(p)
	if err != nil {
		t.Fatalf("port %q: %v", p, err)
	}
	return n
}

func closeAll(lns []net.Listener) {
	for _, ln := range lns {
		ln.Close()
	}
}

// The bind acquires both addresses, loopback first. First is not decoration:
// it is the address every mode takes, so it is the only one two instances are
// guaranteed to collide on, and it is the one the port-ownership challenge
// contacts (adr-2609091123526871 rule 2).
func TestTheBindAcquiresLoopbackFirstAndThenTheSecondAddress(t *testing.T) {
	port := freePort(t)
	lns, plan, claimed, err := acquireBind(bind.ForHost("0.0.0.0"), port, time.Second, func() portHolder { return holderNone })
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer closeAll(lns)
	if !claimed {
		t.Fatal("claimed=false on a free port")
	}
	if len(lns) != 2 {
		t.Fatalf("acquired %d listeners, want 2 — the wildcard bind takes loopback as well", len(lns))
	}
	if got := lns[0].Addr().String(); got != net.JoinHostPort("127.0.0.1", strconv.Itoa(port)) {
		t.Errorf("first listener is %s, want loopback — the singleton and the challenge probe both rest on loopback being taken first", got)
	}
	if plan.Extra != "0.0.0.0" || plan.Refusal != "" {
		t.Errorf("plan = %+v, want the second address acquired and nothing refused", plan)
	}
}

// A loopback-only bind takes one listener and asks for nothing else. The
// second net.Listen would be on the address already held and the kernel would
// refuse it, which would turn the narrowest install there is into a startup
// failure.
func TestALoopbackBindTakesOneListener(t *testing.T) {
	port := freePort(t)
	lns, plan, claimed, err := acquireBind(bind.ForHost("127.0.0.1"), port, time.Second, func() portHolder { return holderNone })
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer closeAll(lns)
	if !claimed || len(lns) != 1 {
		t.Fatalf("claimed=%v with %d listeners, want one", claimed, len(lns))
	}
	if !plan.LoopbackOnly() || plan.Refusal != "" {
		t.Errorf("plan = %+v, want this Mac only with nothing refused — the operator asked for exactly this", plan)
	}
}

// The fail-closed case, and the one iss-7 is about. The second address is not
// on this Mac: Gropius serves loopback, says which address it dropped, and does
// not exit. Exiting is what left the operator with no panel and a file to
// hand-edit; serving this Mac gives them the panel the setting is changed from.
//
// 192.0.2.5 is TEST-NET-1, which is reserved for documentation and is never
// assigned to an interface, so the listen fails for the reason a departed
// private-network address would.
func TestASecondAddressThisMacDoesNotHoldServesLoopbackAndSaysSo(t *testing.T) {
	port := freePort(t)
	lns, plan, claimed, err := acquireBind(bind.ForHost("192.0.2.5"), port, time.Second, func() portHolder { return holderNone })
	if err != nil {
		t.Fatalf("acquireBind returned an error rather than narrowing: %v", err)
	}
	defer closeAll(lns)
	if !claimed || len(lns) != 1 {
		t.Fatalf("claimed=%v with %d listeners, want one on loopback", claimed, len(lns))
	}
	if !plan.LoopbackOnly() {
		t.Errorf("plan = %+v, want this Mac only", plan)
	}
	if !strings.Contains(plan.Refusal, "192.0.2.5") {
		t.Errorf("Refusal = %q, want it to name the address that could not be taken — this is all the operator has to go on", plan.Refusal)
	}
}

// The narrowing never widens. Whatever happens to the second listener, the
// server answers on loopback and on nothing the plan did not name.
func TestAFailedSecondListenerNeverWidensTheBind(t *testing.T) {
	port := freePort(t)
	lns, _, _, err := acquireBind(bind.ForHost("192.0.2.5"), port, time.Second, func() portHolder { return holderNone })
	if err != nil {
		t.Fatal(err)
	}
	defer closeAll(lns)
	// If the fallback had widened to the wildcard, this listen would be
	// refused: the wildcard covers every address on the machine.
	probe, err := net.Listen("tcp", net.JoinHostPort("0.0.0.0", strconv.Itoa(port)))
	if err != nil {
		t.Fatalf("the wildcard is held after a failed second listener (%v) — the fallback widened the bind", err)
	}
	probe.Close()
}

// Loopback is the contention point, so a peer holding it is the ordinary
// singleton outcome and behaves exactly as it did with one listener.
func TestAPeerHoldingLoopbackIsStillClientMode(t *testing.T) {
	port := freePort(t)
	occupied, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		t.Fatalf("could not occupy loopback: %v", err)
	}
	defer occupied.Close()

	lns, _, claimed, err := acquireBind(bind.ForHost("0.0.0.0"), port, time.Second, func() portHolder { return holderOurs })
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if claimed || len(lns) != 0 {
		closeAll(lns)
		t.Fatalf("claimed=%v with %d listeners, want client mode", claimed, len(lns))
	}
}

// The cross-version case. An older build binds one address and can therefore
// hold the wide address while this one takes loopback — measured, those two
// binds do not collide. Holding loopback while it serves would break its
// control plane and load a second copy of every model, which is the outcome the
// singleton exists to prevent, so loopback is released and the port is
// classified the way it always was.
func TestAPeerHoldingOnlyTheSecondAddressReleasesLoopbackAndDefers(t *testing.T) {
	port := freePort(t)
	occupied, err := net.Listen("tcp", net.JoinHostPort("0.0.0.0", strconv.Itoa(port)))
	if err != nil {
		t.Fatalf("could not occupy the wildcard: %v", err)
	}
	defer occupied.Close()

	lns, _, claimed, err := acquireBind(bind.ForHost("0.0.0.0"), port, time.Second, func() portHolder { return holderOurs })
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if claimed || len(lns) != 0 {
		closeAll(lns)
		t.Fatalf("claimed=%v with %d listeners, want client mode", claimed, len(lns))
	}
	// The loopback listener must not be left behind: a later instance probes
	// loopback, and a socket nobody is serving on answers nothing while
	// refusing everyone else's attempt to take it.
	probe, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		t.Fatalf("loopback is still held after dropping into client mode: %v", err)
	}
	probe.Close()
}

// The hijack defence, on the second address. An unidentified process holds it,
// so this instance refuses rather than becoming its client — and releases
// loopback on the way out for the same reason as above.
func TestAForeignHolderOfTheSecondAddressIsRefused(t *testing.T) {
	port := freePort(t)
	occupied, err := net.Listen("tcp", net.JoinHostPort("0.0.0.0", strconv.Itoa(port)))
	if err != nil {
		t.Fatalf("could not occupy the wildcard: %v", err)
	}
	defer occupied.Close()

	lns, _, claimed, err := acquireBind(bind.ForHost("0.0.0.0"), port, time.Second, func() portHolder { return holderForeign })
	if err == nil {
		closeAll(lns)
		t.Fatal("expected a refusal when an unidentified process holds the second address")
	}
	if claimed || len(lns) != 0 {
		closeAll(lns)
		t.Fatalf("claimed=%v with %d listeners, want none", claimed, len(lns))
	}
	probe, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		t.Fatalf("loopback is still held after refusing: %v", err)
	}
	probe.Close()
}

// Criterion 2 of the private-network intent, in the only form a unit test can
// take it: an address outside the bind refuses the connection. It is the
// property the whole record rests on — the narrowing is in the socket rather
// than in a code path — so it is measured rather than reasoned about.
//
// Environment-dependent by nature, and written to skip rather than to fail
// when the environment is not there: a Mac with no non-loopback IPv4 address
// has nothing to dial. The wildcard half is what keeps the skip honest, by
// showing that the dial can tell the two binds apart at all.
func TestAnAddressOutsideTheBindRefusesTheConnection(t *testing.T) {
	other := aNonLoopbackIPv4(t)
	if other == "" {
		t.Skip("this Mac holds no non-loopback IPv4 address, so there is nothing outside the bind to dial")
	}
	port := freePort(t)

	narrow, _, _, err := acquireBind(bind.ForHost("127.0.0.1"), port, time.Second, func() portHolder { return holderNone })
	if err != nil {
		t.Fatal(err)
	}
	if conn, err := net.DialTimeout("tcp", net.JoinHostPort(other, strconv.Itoa(port)), time.Second); err == nil {
		conn.Close()
		closeAll(narrow)
		t.Fatal("an address outside the bind accepted a connection — the narrowing is not in the socket")
	}
	closeAll(narrow)

	// The same dial against the wildcard, so a skip or a pass above cannot be
	// the dial failing for its own reasons.
	wide, _, _, err := acquireBind(bind.ForHost("0.0.0.0"), freePort(t), time.Second, func() portHolder { return holderNone })
	if err != nil {
		t.Fatal(err)
	}
	defer closeAll(wide)
	widePort := wide[len(wide)-1].Addr().(*net.TCPAddr).Port
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(other, strconv.Itoa(widePort)), time.Second)
	if err != nil {
		t.Fatalf("the wildcard bind refused a connection on %s: %v — the dial cannot tell the two binds apart, so the refusal above proves nothing", "an address this Mac holds", err)
	}
	conn.Close()
}

// aNonLoopbackIPv4 is one address this Mac holds that is not loopback, or ""
// when it holds none.
func aNonLoopbackIPv4(t *testing.T) string {
	t.Helper()
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	for _, a := range addrs {
		n, ok := a.(*net.IPNet)
		if !ok || n.IP.IsLoopback() || n.IP.To4() == nil {
			continue
		}
		return n.IP.String()
	}
	return ""
}

// Criterion 8 of the private-network intent: Gropius does not advertise to
// networks the bind excludes. The advert is mDNS on the local link, so under
// the private-network mode every advert it produced would name an address its
// recipients cannot reach, while disclosing this Mac's hostname, the port, the
// model count and whether a key is required to exactly the network the mode
// exists to exclude (adr-2609091123526871 rule 8).
//
// It reads the configured mode and the plan, and never the detection: both are
// state Gropius owns.
func TestWhatGropiusAdvertisesItselfOn(t *testing.T) {
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
			bind.Private("100.101.102.103", []string{"100.101.102.103"}, ""),
			false,
		},
		{
			"a bind that narrowed to this Mac",
			func(c *config.Config) { c.Host = "0.0.0.0" },
			wildcard.WithoutExtra("could not listen"),
			false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := config.Default()
			cfg.Advertise = true
			c.cfg(&cfg)
			if got := advertises(cfg, c.plan); got != c.want {
				t.Errorf("advertises() = %v, want %v", got, c.want)
			}
		})
	}
}
