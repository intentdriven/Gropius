package main

import (
	"io"
	"log/slog"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/intentdriven/Gropius/internal/bind"
	"github.com/intentdriven/Gropius/internal/config"
)

// portsHandedOut is every port freePort has given a test in this binary.
//
// Two tests given the same port is not a hypothetical: these tests bind
// loopback, the wildcard and specific addresses on a port and then assert what
// answers there, so one test's wildcard listener answering another's dial reads
// as a narrowing that leaked. It was observed, on three different tests,
// because "a port that was free a moment ago" is not the same as "a port
// nothing in this process holds".
var (
	portsMu        sync.Mutex
	portsHandedOut = map[int]bool{}
)

// freePort is a port free on the WILDCARD — so free on loopback and on every
// specific address too — and handed to exactly one test in this binary.
func freePort(t *testing.T) int {
	t.Helper()
	portsMu.Lock()
	defer portsMu.Unlock()
	for i := 0; i < 50; i++ {
		ln, err := net.Listen("tcp", "0.0.0.0:0")
		if err != nil {
			continue
		}
		p := ln.Addr().(*net.TCPAddr).Port
		ln.Close()
		if portsHandedOut[p] {
			continue
		}
		portsHandedOut[p] = true
		return p
	}
	t.Fatal("could not find a port free on the wildcard that no other test here holds")
	return 0
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
	// A port nothing else in this test binary holds the wildcard on. freePort
	// alone is not enough: another test acquiring a wildcard bind on the same
	// port would answer this dial, and the refusal this test is about would
	// read as a failure of the narrowing rather than as a collision.
	port, narrow := 0, []net.Listener(nil)
	for i := 0; i < 20 && narrow == nil; i++ {
		p := freePort(t)
		lns, _, _, err := acquireBind(bind.ForHost("127.0.0.1"), p, time.Second, func() portHolder { return holderNone })
		if err != nil {
			continue
		}
		probe, err := net.Listen("tcp", net.JoinHostPort("0.0.0.0", strconv.Itoa(p)))
		if err != nil {
			closeAll(lns) // somebody holds the wildcard here; try another port
			continue
		}
		probe.Close()
		port, narrow = p, lns
	}
	if narrow == nil {
		t.Skip("could not find a port free on both loopback and the wildcard")
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
			bind.Private("100.101.102.103", []string{"100.101.102.103"}, ""), // abcd-lint:allow: RFC 6598 shared address space, the classifier's own range
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

// A Host that is a NAME resolving to the address the loopback listener already
// holds is this Mac, not a contended port — and the operator is told, because
// they did not ask for this.
//
// internal/bind folds away the loopback spellings it can read — the literal
// address, and "localhost" and its variants — but a name is whatever the
// resolver says it is. Without the first half of this the process collided with
// its own socket: EADDRINUSE on the second listener, loopback released, a probe
// of a port nothing was listening on any more, five seconds of retries, and
// exit 1 — no panel, no app, and the file to hand-edit, which is the iss-7 trap
// this whole record exists to close.
//
// Without the second half it narrowed in silence. A name is a bind Gropius
// treats as exposed — it generates an API key for it — and the resolver is what
// decided it means loopback: a stale hosts entry, split-horizon DNS, or a
// resolver another account on this Mac controls. So the operator sets a bind
// they believe serves the network, and serves this Mac, with no log line and
// nothing in the panel. Every other narrowing says which address it dropped and
// why, and this one is the narrowing least likely to be intended.
//
// "localhost" stands in for the name: it resolves the same way, and a test
// cannot edit the hosts file.
func TestANameThatResolvesToLoopbackNarrowsAndSaysSo(t *testing.T) {
	port := freePort(t)
	start := time.Now()
	lns, plan, claimed, err := acquireBind(bind.Plan{Loopback: "127.0.0.1", Extra: "localhost"}, port, 5*time.Second, func() portHolder { return holderNone })
	if err != nil {
		t.Fatalf("acquireBind failed on a name that resolves to loopback: %v — the app exits 1 on this path", err)
	}
	defer closeAll(lns)
	if !claimed || len(lns) != 1 {
		t.Fatalf("claimed=%v with %d listeners, want the one loopback listener", claimed, len(lns))
	}
	if !plan.LoopbackOnly() {
		t.Errorf("plan = %+v, want this Mac only", plan)
	}
	// The log line and the panel notice are both keyed on the refusal, so an
	// empty one is a silent narrowing on both surfaces at once.
	if !strings.Contains(plan.Refusal, "localhost") {
		t.Errorf("Refusal = %q, want it to name the bind address the operator wrote", plan.Refusal)
	}
	if !strings.Contains(plan.Refusal, "127.0.0.1") {
		t.Errorf("Refusal = %q, want it to name what that address resolved to — that is the fact the operator does not have", plan.Refusal)
	}
	if el := time.Since(start); el > 2*time.Second {
		t.Errorf("acquireBind took %s — it went round the contended-port path rather than recognising its own address", el)
	}
}

// The key follows the sockets, not the settings (adr-2609091123526871 rule 7).
//
// A private-network mode that found no address serves this Mac and nothing
// else, and a key for that is friction with no exposure behind it: nobody off
// this Mac can reach the server, and a loopback connection is exempt from the
// bearer check anyway. A bind that DID acquire an address takes the iss-1 path
// unchanged — generate, persist, announce — because that is a server other
// machines reach with no key.
func TestTheKeyIsRequiredForWhatWasAcquiredAndNothingElse(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	t.Run("a mode that fell back to this Mac needs no key", func(t *testing.T) {
		paths := config.NewPaths(t.TempDir())
		cfg := config.Default()
		cfg.BindMode = config.BindModePrivateNetwork
		cfg.APIKey = ""
		lns, plan, _, err := acquireBind(bind.Private("", nil, "no address matched"), freePort(t), time.Second, func() portHolder { return holderNone })
		if err != nil {
			t.Fatal(err)
		}
		defer closeAll(lns)

		lns, plan, saved := secureExposedBind(paths, &cfg, lns, plan, log)
		if saved {
			t.Error("config.json was rewritten for a bind that needed no key")
		}
		if cfg.APIKey != "" {
			t.Errorf("an API key was generated for a server serving this Mac only: %q is friction with no exposure behind it", cfg.APIKey)
		}
		if _, err := os.Stat(paths.Config); err == nil {
			t.Error("config.json was written for a bind that needed no key")
		}
		if len(lns) != 1 || !plan.LoopbackOnly() {
			t.Errorf("the bind changed: %d listeners, plan %+v", len(lns), plan)
		}
	})

	t.Run("a bind other machines reach generates and persists one", func(t *testing.T) {
		paths := config.NewPaths(t.TempDir())
		cfg := config.Default()
		cfg.Host = "0.0.0.0"
		cfg.APIKey = ""
		lns, plan, _, err := acquireBind(bind.ForHost("0.0.0.0"), freePort(t), time.Second, func() portHolder { return holderNone })
		if err != nil {
			t.Fatal(err)
		}
		defer closeAll(lns)

		lns, plan, saved := secureExposedBind(paths, &cfg, lns, plan, log)
		if !saved {
			t.Error("the save was not reported — the caller clears the repair notice on it, because that write is the file being rewritten from the settings in force")
		}
		if cfg.APIKey == "" {
			t.Fatal("no API key for a bind every machine on the network reaches — this is the window iss-1 closed")
		}
		if len(lns) != 2 || plan.LoopbackOnly() {
			t.Errorf("the bind narrowed although the key was saved: %d listeners, plan %+v", len(lns), plan)
		}
		onDisk, _, err := config.Load(paths.Config)
		if err != nil {
			t.Fatalf("the generated key was not persisted: %v", err)
		}
		if onDisk.APIKey != cfg.APIKey {
			t.Errorf("config.json holds %q, want the generated key — one held only in memory reopens the endpoint at the next start", onDisk.APIKey)
		}
	})

	t.Run("a key that cannot be saved narrows the bind rather than serving open", func(t *testing.T) {
		// A root that cannot be written to: the save fails, and a key held
		// only in memory would vanish at the next start and leave the endpoint
		// open, so the exposure goes instead of the key.
		root := t.TempDir()
		if err := os.Chmod(root, 0o500); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.Chmod(root, 0o700) })
		paths := config.NewPaths(root)
		cfg := config.Default()
		cfg.Host = "0.0.0.0"
		cfg.APIKey = ""
		lns, plan, _, err := acquireBind(bind.ForHost("0.0.0.0"), freePort(t), time.Second, func() portHolder { return holderNone })
		if err != nil {
			t.Fatal(err)
		}
		defer closeAll(lns)

		lns, plan, saved := secureExposedBind(paths, &cfg, lns, plan, log)
		if saved {
			t.Error("a save was reported although it failed")
		}
		if cfg.APIKey != "" {
			t.Errorf("APIKey = %q — a key that could not be saved is one the next start does not have", cfg.APIKey)
		}
		if len(lns) != 1 || !plan.LoopbackOnly() {
			t.Fatalf("the bind stayed open with no key: %d listeners, plan %+v", len(lns), plan)
		}
		if plan.Refusal == "" {
			t.Error("the bind narrowed with nothing said — the log line and the panel notice are both keyed on the refusal")
		}
		// The socket it dropped must be gone, not merely unreported.
		port := lns[0].Addr().(*net.TCPAddr).Port
		probe, err := net.Listen("tcp", net.JoinHostPort("0.0.0.0", strconv.Itoa(port)))
		if err != nil {
			t.Fatalf("the wide listener is still open after the lockdown: %v", err)
		}
		probe.Close()
	})
}
