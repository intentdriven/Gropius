package app

import (
	"bytes"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/intentdriven/Gropius/internal/capability"
	"github.com/intentdriven/Gropius/internal/config"
	"github.com/intentdriven/Gropius/internal/runtime"
)

const gb = int64(1) << 30

// newBudgetApp builds an App on a machine of a size the test chooses, so that
// every figure below is the same on whatever Mac the suite runs on — and so
// that the machine nobody can measure is testable at all.
func newBudgetApp(t *testing.T, ram int64, cfg config.Config) *App {
	t.Helper()
	// A literal config leaves the statistics retention figures at zero, which
	// a settings save refuses for a reason unrelated to the budget these tests
	// exercise; carry the shipped defaults for them.
	if cfg.StatsMonths == 0 && cfg.StatsMaxBytes == 0 {
		d := config.Default()
		cfg.StatsMonths, cfg.StatsMaxBytes = d.StatsMonths, d.StatsMaxBytes
	}
	a, err := New(Options{
		Paths:          config.NewPaths(t.TempDir()),
		Config:         cfg,
		PhysicalMemory: func() int64 { return ram },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { a.Close() })
	return a
}

// A fresh install stores no budget and runs on the default share of this Mac's
// memory, which is the figure Settings shows back.
func TestAFreshInstallRunsOnTheDefaultShareOfTheMachine(t *testing.T) {
	a := newBudgetApp(t, 128*gb, config.Default())

	if got, want := a.MachineRAM(), 128*gb; got != want {
		t.Errorf("MachineRAM() = %d, want %d", got, want)
	}
	if got, want := a.Pool.MemoryBudget(), capability.DefaultBudget(128*gb); got != want {
		t.Errorf("MemoryBudget() = %d, want the default share %d", got, want)
	}
	if got := a.Config().MaxResidentBytes; got != 0 {
		t.Errorf("MaxResidentBytes = %d, want 0 — the default is resolved, not stored", got)
	}
}

// A budget saved in Settings governs the next load, not the next start-up.
func TestSetConfigAppliesTheBudgetToThePoolWithoutARestart(t *testing.T) {
	a := newBudgetApp(t, 128*gb, config.Default())

	c := a.Config()
	c.MaxResidentBytes = 96 * gb
	if err := a.SetConfig(c); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}
	if got := a.Pool.MemoryBudget(); got != 96*gb {
		t.Errorf("the pool holds a budget of %d, want the one just saved (%d)", got, 96*gb)
	}
	// Clearing it goes back to the default rather than to a budget of nothing.
	c.MaxResidentBytes = 0
	if err := a.SetConfig(c); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}
	if got, want := a.Pool.MemoryBudget(), capability.DefaultBudget(128*gb); got != want {
		t.Errorf("the pool holds %d after the budget was cleared, want the default %d", got, want)
	}
}

// A budget larger than the machine is refused, naming what the machine has,
// and nothing is written: the check is at a save, where a human is waiting for
// an answer, rather than at a read, where refusing would take the install down
// to loopback over one figure.
func TestSetConfigRefusesABudgetLargerThanTheMachine(t *testing.T) {
	a := newBudgetApp(t, 16*gb, config.Default())

	base := a.Config()
	base.APIKey = "bh_before"
	if err := a.SetConfig(base); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}
	before, err := os.ReadFile(a.Paths.Config)
	if err != nil {
		t.Fatal(err)
	}

	over := base.Clone()
	over.MaxResidentBytes = 32 * gb
	err = a.SetConfig(over)
	if err == nil {
		t.Fatal("SetConfig accepted a budget larger than the whole machine")
	}
	if !strings.Contains(err.Error(), runtime.HumanBytes(16*gb)) {
		t.Errorf("error = %q, want it to name this Mac's memory (%s)", err, runtime.HumanBytes(16*gb))
	}
	after, err := os.ReadFile(a.Paths.Config)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Error("a refused save rewrote config.json")
	}
	if got := a.Config().MaxResidentBytes; got != 0 {
		t.Errorf("the refused budget reached the live configuration: %d", got)
	}
	if got, want := a.Pool.MemoryBudget(), capability.DefaultBudget(16*gb); got != want {
		t.Errorf("the refused budget reached the pool: %d, want %d", got, want)
	}
}

// When the machine's memory cannot be read there is no ceiling to check
// against, so an explicit figure is taken at its word rather than refused.
func TestAMachineOfUnknownSizeAcceptsAnyBudget(t *testing.T) {
	a := newBudgetApp(t, 0, config.Default())

	if got := a.MachineRAM(); got != 0 {
		t.Fatalf("MachineRAM() = %d, want 0 for a machine that cannot be measured", got)
	}
	c := a.Config()
	c.MaxResidentBytes = 512 * gb
	if err := a.SetConfig(c); err != nil {
		t.Fatalf("SetConfig refused a budget on an unmeasurable machine: %v", err)
	}
	if got := a.Pool.MemoryBudget(); got != 512*gb {
		t.Errorf("the pool holds %d, want the budget just saved", got)
	}
	// And there is no share of the machine to warn about either.
	if got := a.BudgetWarnAbove(); got != 0 {
		t.Errorf("BudgetWarnAbove() = %d, want 0 when the machine cannot be measured", got)
	}
	if w := a.MemoryBudgetWarning(); w != "" {
		t.Errorf("MemoryBudgetWarning() = %q, want none when the machine cannot be measured", w)
	}
}

// Lowering the budget under the pinned models is the other way to make a
// pinned set worse, and it is refused the same way adding a pin is: naming the
// sum, changing nothing.
func TestSetConfigRefusesABudgetBelowThePinnedSum(t *testing.T) {
	a := newBudgetApp(t, 128*gb, config.Default())
	putReady(t, a, "org/writer", 20*gb)
	putReady(t, a, "org/reviewer", 20*gb)

	base := a.Config()
	base.Models = pinnedModels("org/writer", "org/reviewer")
	base.APIKey = "bh_before"
	if err := a.SetConfig(base); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}
	before, err := os.ReadFile(a.Paths.Config)
	if err != nil {
		t.Fatal(err)
	}

	sum := 2 * capability.LoadCost(20*gb)
	lower := base.Clone()
	lower.MaxResidentBytes = sum - 1
	err = a.SetConfig(lower)
	if err == nil {
		t.Fatal("SetConfig accepted a budget below the sum of the pinned models")
	}
	if !strings.Contains(err.Error(), runtime.HumanBytes(sum)) {
		t.Errorf("error = %q, want it to name the pinned sum (%s)", err, runtime.HumanBytes(sum))
	}
	after, err := os.ReadFile(a.Paths.Config)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Error("a refused save rewrote config.json")
	}

	// A budget that still holds the set is saved, and so is one that makes the
	// fit better.
	ok := base.Clone()
	ok.MaxResidentBytes = sum
	if err := a.SetConfig(ok); err != nil {
		t.Errorf("SetConfig refused a budget that holds the pinned set exactly: %v", err)
	}
}

// A pinned set that arrives already over budget — a config.json carried from a
// Mac with more memory — is applied and warned about, not refused: a settings
// page that will not save an API key over a set the operator did not touch on
// this machine is the wedge. Raising the budget makes it better, so it is
// accepted even while the set still does not fit.
func TestAnInheritedOverBudgetSetDoesNotBlockABudgetSave(t *testing.T) {
	a := newBudgetApp(t, 128*gb, config.Config{
		Host: "127.0.0.1", Port: 11535, DecodeConcurrency: 4,
		MaxResidentBytes: 2 * gb,
		Models:           pinnedModels("org/writer"),
	})
	putReady(t, a, "org/writer", 20*gb)

	c := a.Config()
	c.APIKey = "bh_unrelated"
	if err := a.SetConfig(c); err != nil {
		t.Fatalf("an unrelated save was refused over an inherited pinned set: %v", err)
	}
	raise := a.Config()
	raise.MaxResidentBytes = 4 * gb
	if err := a.SetConfig(raise); err != nil {
		t.Fatalf("a save raising the budget was refused although it makes the fit better: %v", err)
	}
	if got := a.Pool.MemoryBudget(); got != 4*gb {
		t.Errorf("the pool holds %d, want the raised budget", got)
	}
	// It is still reported, since nothing about the save made it fit.
	if w := a.PinnedFitWarning(); w == "" {
		t.Error("a pinned set that still does not fit is not reported to the operator")
	}
}

// A budget that takes most of the machine is advice, not an error: the charge
// counts the weights a model loads and not the cache a long conversation adds,
// so a Mac that is fully committed on paper can still run out under load.
func TestAHighBudgetIsWarnedAboutRatherThanRefused(t *testing.T) {
	a := newBudgetApp(t, 100*gb, config.Default())

	if got, want := a.BudgetWarnAbove(), 85*gb; got != want {
		t.Errorf("BudgetWarnAbove() = %d, want %d", got, want)
	}
	c := a.Config()
	c.MaxResidentBytes = 95 * gb
	if err := a.SetConfig(c); err != nil {
		t.Fatalf("a high budget was refused rather than warned about: %v", err)
	}
	if got := a.Config().MaxResidentBytes; got != 95*gb {
		t.Errorf("the high budget was not stored: %d", got)
	}
	w := a.MemoryBudgetWarning()
	if w == "" {
		t.Fatal("a budget above the warning threshold draws no warning")
	}
	if !strings.Contains(w, runtime.HumanBytes(100*gb)) {
		t.Errorf("warning = %q, want it to name this Mac's memory", w)
	}

	c.MaxResidentBytes = 50 * gb
	if err := a.SetConfig(c); err != nil {
		t.Fatal(err)
	}
	if w := a.MemoryBudgetWarning(); w != "" {
		t.Errorf("MemoryBudgetWarning() = %q, want none for a modest budget", w)
	}
}

// A budget that arrives from a Mac with more memory — a settings file carried
// or restored, or a start where the machine could not be measured — is not a
// figure the operator chose on this machine, and refusing every save over it
// would put a memory setting between them and their API key. It is applied,
// warned about, and left alone until they change it.
func TestAnInheritedOverMachineBudgetDoesNotBlockAnUnrelatedSave(t *testing.T) {
	a := newBudgetApp(t, 16*gb, config.Config{
		Host: "0.0.0.0", Port: 11535, DecodeConcurrency: 4,
		MaxResidentBytes: 128 * gb,
	})

	c := a.Config()
	c.APIKey = "bh_lock_it_down"
	if err := a.SetConfig(c); err != nil {
		t.Fatalf("an unrelated save was refused over an inherited budget: %v", err)
	}
	if got := a.Config().APIKey; got != "bh_lock_it_down" {
		t.Errorf("APIKey = %q, want the key the save carried", got)
	}
	// Lowering it, even to a figure still over the machine, makes it better.
	lower := a.Config()
	lower.MaxResidentBytes = 64 * gb
	if err := a.SetConfig(lower); err != nil {
		t.Errorf("a save lowering an inherited budget was refused: %v", err)
	}
	// Raising it further is the operator's own choice, and still refused.
	raise := a.Config()
	raise.MaxResidentBytes = 256 * gb
	if err := a.SetConfig(raise); err == nil {
		t.Error("SetConfig accepted a save raising the budget further above this Mac")
	}
}

// The operator is told on the way in, since nothing else will tell them: the
// pool is enforcing that figure from the first request.
func TestStartupWarnsWhenTheBudgetIsLargerThanTheMachine(t *testing.T) {
	var logged bytes.Buffer
	a, err := New(Options{
		Paths: config.NewPaths(t.TempDir()),
		Config: config.Config{
			Host: "127.0.0.1", Port: 11535, DecodeConcurrency: 4,
			MaxResidentBytes: 128 * gb,
		},
		PhysicalMemory: func() int64 { return 16 * gb },
		Log:            slog.New(slog.NewTextHandler(&logged, nil)),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { a.Close() })

	if !strings.Contains(logged.String(), "memory budget") {
		t.Errorf("startup logged %q, want a warning that the budget is larger than this Mac", logged.String())
	}
	// The stored figure is the operator's and is left alone; what the pool is
	// allowed to fill is bounded by the memory that exists.
	if got := a.Config().MaxResidentBytes; got != 128*gb {
		t.Errorf("MaxResidentBytes = %d, want the stored figure left alone", got)
	}
	if got := a.Pool.MemoryBudget(); got != 16*gb {
		t.Errorf("the pool holds %d, want models held to this Mac's %d", got, 16*gb)
	}
}

// The panel renders the budget in gigabytes and posts it back, so a save can
// carry a figure a few bytes under the one in force without meaning to lower
// anything. A pinned set that fits neither figure must not flip from warned to
// refused over that.
func TestARoundedBudgetDoesNotTurnAWarningIntoARefusal(t *testing.T) {
	a := newBudgetApp(t, 128*gb, config.Config{
		Host: "127.0.0.1", Port: 11535, DecodeConcurrency: 4,
		MaxResidentBytes: 8 * gb,
		Models:           pinnedModels("org/writer"),
	})
	putReady(t, a, "org/writer", 20*gb) // charged 24 GB: fits neither budget

	c := a.Config()
	c.MaxResidentBytes = 8*gb - 4_000_000 // what a round trip through the panel costs
	c.APIKey = "bh_unrelated"
	if err := a.SetConfig(c); err != nil {
		t.Fatalf("a save was refused over a pinned set that fitted neither budget: %v", err)
	}
}

// A pin whose model this Mac cannot measure is refused as it is added, because
// a fit check that skips a model is a promise it cannot keep. It must not also
// block every later budget change: there would be no way out but unpinning.
func TestAnUnmeasurablePinDoesNotBlockALowerBudget(t *testing.T) {
	a := newBudgetApp(t, 128*gb, config.Default())
	putReady(t, a, "org/unmeasured", 0)

	c := a.Config()
	c.Models = pinnedModels("org/unmeasured")
	if err := a.SetConfig(c); err == nil {
		t.Fatal("SetConfig accepted a pin on a model of unknown size")
	}

	// Same set, arriving from the file rather than added here.
	b := newBudgetApp(t, 128*gb, config.Config{
		Host: "127.0.0.1", Port: 11535, DecodeConcurrency: 4,
		Models: pinnedModels("org/unmeasured"),
	})
	putReady(t, b, "org/unmeasured", 0)
	lower := b.Config()
	lower.MaxResidentBytes = 32 * gb
	if err := b.SetConfig(lower); err != nil {
		t.Errorf("a budget change was refused over a pin this Mac cannot measure: %v", err)
	}
}

// A budget larger than the Mac is kept as the operator's figure and warned
// about — but what the pool is allowed to fill is bounded by what exists.
// Under the shared install another account can write the settings file, and a
// planted figure would otherwise admit every model a LAN client names until
// the machine swaps, restart after restart, with no way to clear it from a
// panel whose own save cannot replace that account's file.
func TestAPlantedBudgetIsEnforcedNoHigherThanTheMachine(t *testing.T) {
	var logged bytes.Buffer
	// Start from the defaults so the fields other settings validate (the
	// statistics retention figures among them) carry their shipped values;
	// the test is about the budget alone.
	planted := config.Default()
	planted.Host, planted.Port, planted.DecodeConcurrency = "127.0.0.1", 11535, 4
	planted.MaxResidentBytes = 1 << 62
	a, err := New(Options{
		Paths:          config.NewPaths(t.TempDir()),
		Config:         planted,
		PhysicalMemory: func() int64 { return 16 * gb },
		Log:            slog.New(slog.NewTextHandler(&logged, nil)),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { a.Close() })

	if got := a.Pool.MemoryBudget(); got > 16*gb {
		t.Errorf("the pool is holding models to %d, want no more than this Mac's %d", got, 16*gb)
	}
	if !strings.Contains(logged.String(), "memory budget") {
		t.Errorf("startup logged %q, want a warning about the budget", logged.String())
	}
	// The stored figure is the operator's and is not rewritten behind them.
	if got := a.Config().MaxResidentBytes; got != 1<<62 {
		t.Errorf("MaxResidentBytes = %d, want the stored figure left alone", got)
	}
	// And the save path still judges what they asked for, not the clamp: an
	// unrelated save goes through, a raise does not.
	c := a.Config()
	c.APIKey = "bh_unrelated"
	if err := a.SetConfig(c); err != nil {
		t.Errorf("an unrelated save was refused over a planted budget: %v", err)
	}
	raise := a.Config()
	raise.MaxResidentBytes = 1 << 63 >> 1 // one step further than the planted figure
	if raise.MaxResidentBytes > 1<<62 {
		if err := a.SetConfig(raise); err == nil {
			t.Error("SetConfig accepted a save raising the budget further above this Mac")
		}
	}
}

// The budget has advice at the top of its range; it needs the same at the
// bottom, where a figure too small to hold anything refuses every request with
// nothing on the panel to say why.
func TestABudgetTooSmallForAnyModelIsWarnedAbout(t *testing.T) {
	a := newBudgetApp(t, 128*gb, config.Default())
	putReady(t, a, "org/small", 2*gb)
	putReady(t, a, "org/large", 40*gb)

	c := a.Config()
	c.MaxResidentBytes = 1 << 20
	if err := a.SetConfig(c); err != nil {
		t.Fatalf("SetConfig refused a small budget rather than warning: %v", err)
	}
	w := a.MemoryBudgetWarning()
	if w == "" {
		t.Fatal("a budget too small to hold any model on this Mac draws no warning")
	}
	if !strings.Contains(w, runtime.HumanBytes(capability.LoadCost(2*gb))) {
		t.Errorf("warning = %q, want it to name what the smallest model on this Mac costs", w)
	}

	// A budget that holds the smallest model is not warned about, even though
	// it cannot hold the largest: what to keep is the operator's business.
	c.MaxResidentBytes = capability.LoadCost(2 * gb)
	if err := a.SetConfig(c); err != nil {
		t.Fatal(err)
	}
	if w := a.MemoryBudgetWarning(); w != "" {
		t.Errorf("MemoryBudgetWarning() = %q, want none for a budget that holds a model", w)
	}
}
