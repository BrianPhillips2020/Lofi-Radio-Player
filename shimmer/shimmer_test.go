package shimmer

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/lipgloss"
)

func TestNextIDIncreasing(t *testing.T) {
	a := nextID()
	b := nextID()
	if b <= a {
		t.Fatalf("nextID not increasing: got %d then %d", a, b)
	}
}

func TestNewAppliesDefaults(t *testing.T) {
	m := New()

	if m.id == 0 {
		t.Error("New did not assign an id")
	}
	if m.midShimmer {
		t.Error("New should start in the resting phase")
	}
	if m.shimmerFrameInterval != defaultShimmerFrameInterval {
		t.Errorf("frameInterval = %v, want %v", m.shimmerFrameInterval, defaultShimmerFrameInterval)
	}
	if m.shimmerDuration != defaultShimmerDuration {
		t.Errorf("duration = %v, want %v", m.shimmerDuration, defaultShimmerDuration)
	}
	if m.shimmerWaitTime != defaultShimmerWaitTime {
		t.Errorf("waitTime = %v, want %v", m.shimmerWaitTime, defaultShimmerWaitTime)
	}
	if m.bandWidth != defaultBandWidth {
		t.Errorf("bandWidth = %d, want %d", m.bandWidth, defaultBandWidth)
	}
}

func TestNewParsesColors(t *testing.T) {
	m := New()

	if got := strings.ToLower(m.base.Hex()); got != strings.ToLower(string(defaultBaseColor)) {
		t.Errorf("parsed base = %s, want %s", got, defaultBaseColor)
	}
	if got := strings.ToLower(m.shimmer.Hex()); got != strings.ToLower(string(defaultShimmerColor)) {
		t.Errorf("parsed shimmer = %s, want %s", got, defaultShimmerColor)
	}
}

func TestReparseColorsKeepsPreviousOnInvalidHex(t *testing.T) {
	m := New()
	prevBase, prevShimmer := m.base, m.shimmer

	m.baseColor = lipgloss.Color("not-a-color")
	m.shimmerColor = lipgloss.Color("")
	m.reparseColors()

	if m.base != prevBase {
		t.Errorf("invalid base hex mutated parsed color: %v -> %v", prevBase, m.base)
	}
	if m.shimmer != prevShimmer {
		t.Errorf("invalid shimmer hex mutated parsed color: %v -> %v", prevShimmer, m.shimmer)
	}
}

func TestUpdateIgnoresForeignTick(t *testing.T) {
	m := New()
	before := m

	got, cmd := m.Update(TickMsg{Time: time.Now(), id: m.id + 1})

	if cmd != nil {
		t.Error("foreign tick should not produce a command")
	}
	if got.midShimmer != before.midShimmer || !got.phaseStart.Equal(before.phaseStart) || got.elapsed != before.elapsed {
		t.Error("foreign tick mutated animation state")
	}
}

func TestUpdateIgnoresUnknownMessage(t *testing.T) {
	m := New()

	_, cmd := m.Update(struct{}{})

	if cmd != nil {
		t.Error("unknown message should not produce a command")
	}
}

func TestUpdateStartsSweepFromRest(t *testing.T) {
	m := New()
	m.midShimmer = false
	now := time.Now()

	got, cmd := m.Update(TickMsg{Time: now, id: m.id})

	if !got.midShimmer {
		t.Error("tick during rest should begin a sweep")
	}
	if !got.phaseStart.Equal(now) {
		t.Errorf("phaseStart = %v, want %v", got.phaseStart, now)
	}
	if got.elapsed != 0 {
		t.Errorf("elapsed = %v, want 0", got.elapsed)
	}
	if cmd == nil {
		t.Error("expected a follow-up frame command")
	}
}

func TestUpdateAdvancesMidSweep(t *testing.T) {
	m := New()
	m.midShimmer = true
	start := time.Now()
	m.phaseStart = start
	half := m.shimmerDuration / 2

	got, cmd := m.Update(TickMsg{Time: start.Add(half), id: m.id})

	if !got.midShimmer {
		t.Error("sweep ended before its duration elapsed")
	}
	if got.elapsed != half {
		t.Errorf("elapsed = %v, want %v", got.elapsed, half)
	}
	if !got.phaseStart.Equal(start) {
		t.Errorf("phaseStart moved mid-sweep: %v", got.phaseStart)
	}
	if cmd == nil {
		t.Error("expected a next-frame command")
	}
}

func TestUpdateEndsSweepAfterDuration(t *testing.T) {
	m := New()
	m.midShimmer = true
	start := time.Now()
	m.phaseStart = start
	tick := start.Add(m.shimmerDuration + time.Millisecond)

	got, cmd := m.Update(TickMsg{Time: tick, id: m.id})

	if got.midShimmer {
		t.Error("sweep should end once its duration elapses")
	}
	if !got.phaseStart.Equal(tick) {
		t.Errorf("phaseStart = %v, want reset to %v", got.phaseStart, tick)
	}
	if got.elapsed != 0 {
		t.Errorf("elapsed = %v, want 0", got.elapsed)
	}
	if cmd == nil {
		t.Error("expected a wait command")
	}
}

// Drives the model through many ticks on a virtual clock and confirms it keeps
// cycling rest -> sweep -> rest without ever stalling (nil command).
func TestUpdateCyclesWithoutStalling(t *testing.T) {
	m := New()
	m.midShimmer = false
	clock := time.Now()

	sweepsStarted := 0
	prevMid := m.midShimmer

	for i := 0; i < 3000; i++ {
		var cmd tea.Cmd
		m, cmd = m.Update(TickMsg{Time: clock, id: m.id})
		if cmd == nil {
			t.Fatalf("iteration %d: nil command, animation stalled", i)
		}

		if m.midShimmer && !prevMid {
			sweepsStarted++
		}
		prevMid = m.midShimmer

		if m.midShimmer {
			clock = clock.Add(m.shimmerFrameInterval)
		} else {
			clock = clock.Add(m.shimmerWaitTime)
		}
	}

	if sweepsStarted < 3 {
		t.Errorf("expected several sweeps over the run, got %d", sweepsStarted)
	}
}

func TestViewWhileRestingUsesBaseColor(t *testing.T) {
	m := New()
	m.text = "hello"
	m.midShimmer = false

	want := m.style.Foreground(m.baseColor).Render("hello")
	if got := m.View(); got != want {
		t.Errorf("View() = %q, want %q", got, want)
	}
}

func TestViewMidSweepPreservesLineCount(t *testing.T) {
	m := New()
	m.text = "abc\ndefg\nhi"
	m.midShimmer = true
	m.phaseStart = time.Now()
	m.elapsed = m.shimmerDuration / 2

	out := m.View()

	if out == "" {
		t.Fatal("View() returned empty string mid-sweep")
	}
	if got, want := strings.Count(out, "\n"), strings.Count(m.text, "\n"); got != want {
		t.Errorf("newline count = %d, want %d", got, want)
	}
}

func TestShimmerSweepColorFarColumnIsBase(t *testing.T) {
	m := New()
	m.bandWidth = 4

	// sweepPos = 0*10 = 0; column 100 is well outside the band -> blend 0 -> base
	got := shimmerSweepColor(m, 100, 10, 0.0)
	want := lipgloss.Color(m.base.Clamped().Hex())

	if got != want {
		t.Errorf("far column = %q, want base color %q", got, want)
	}
}

func TestShimmerSweepColorCenterDiffersFromEdge(t *testing.T) {
	m := New()
	m.bandWidth = 4

	// frac 0.5, width 10 -> sweepPos 5: column 5 sits at the band center.
	center := shimmerSweepColor(m, 5, 10, 0.5)
	edge := shimmerSweepColor(m, 100, 10, 0.5)

	if center == edge {
		t.Errorf("band center and far edge rendered the same color %q", center)
	}
}

func TestRenderShimmerSweepPreservesLineCount(t *testing.T) {
	m := New()
	art := "one\ntwo\nthree\nfour"

	out := renderShimmerSweep(m, m.style, art, 0.3)

	if got, want := strings.Count(out, "\n"), strings.Count(art, "\n"); got != want {
		t.Errorf("newline count = %d, want %d", got, want)
	}
}
