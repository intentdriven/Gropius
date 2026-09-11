package runtime

import (
	"testing"

	"github.com/intentdriven/Gropius/internal/config"
)

// Provisioning reports a PROPORTION, not a spinner.
//
// The failure the installing criteria are written against is an operator who
// cannot tell working from stuck: today's banner names no proportion, no size
// and no retry. The proportion lives on SetupStatus rather than in the terminal
// renderer because the panel reads that same value — a Go capability with no
// panel equivalent is a gap, not a feature tier — so one source feeds both.
func TestSetupStatusCountsTheStagesThatAreComplete(t *testing.T) {
	p := NewProvisioner(config.NewPaths(t.TempDir()))

	for _, tc := range []struct {
		stage SetupStage
		done  int
	}{
		{StageIdle, 0},
		// While uv is installing, none of the three stages is complete.
		{StageUV, 0},
		{StagePython, 1},
		{StageMLX, 2},
		{StageReady, 3},
	} {
		p.setStatus(tc.stage, "", "")
		s := p.Status()
		if s.Step != tc.done || s.Steps != provisionSteps {
			t.Errorf("%s reports %d of %d stages complete, want %d of %d",
				tc.stage, s.Step, s.Steps, tc.done, provisionSteps)
		}
	}
}

// A failure keeps the count it had reached. The stage that failed says what
// went wrong; the proportion still says how far the run got, which is what a
// person reading a failed install wants to know.
func TestSetupStatusKeepsTheProportionWhenAStageFails(t *testing.T) {
	p := NewProvisioner(config.NewPaths(t.TempDir()))

	p.setStatus(StageMLX, "", "")
	p.setStatus(StageFailed, "", "the wheel would not install")

	s := p.Status()
	if s.Step != 2 || s.Steps != provisionSteps {
		t.Errorf("a failure during the third stage reports %d of %d complete, want 2 of %d", s.Step, s.Steps, provisionSteps)
	}
}
