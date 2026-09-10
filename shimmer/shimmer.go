package shimmer

/**
	Applies an intermittent shimmer to any text.
**/

import (
	"fmt"
	"math"
	"strings"
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/lipgloss"
	"github.com/lucasb-eyer/go-colorful"
)

// mode representing the shimmer object
type Model struct {
	text                 string
	baseColor            lipgloss.Color //lipgloss colors for customization
	shimmerColor         lipgloss.Color
	base                 colorful.Color //converted color for processing
	shimmer              colorful.Color
	id                   int           //id for sending and recieving messages
	phaseStart           time.Time     //anchor for the timer starting
	elapsed              time.Duration //msg.Time - phaseStart
	style                lipgloss.Style
	shimmerWaitTime      time.Duration //time between shimmers
	shimmerFrameInterval time.Duration //time between animation frames of the shimmer
	shimmerDuration      time.Duration //total time of the animation
	fps                  time.Time
	midShimmer           bool
	bandWidth            int //number of chracters wide the shimmer band is
}

// TickMsg indicates that the timer has ticked and we should render a frame.
type TickMsg struct {
	Time time.Time
	id   int
}

func shimmerCmd(d time.Duration, id int) tea.Cmd {
	return tea.Tick(d, func(t time.Time) tea.Msg {
		return TickMsg{
			Time: t,
			id:   id,
		}
	})
}

// Used to ensure shimmer update messages are associated with the proper command that sent them
var lastID int64

func nextID() int {
	return int(atomic.AddInt64(&lastID, 1))
}

// Defaults applied by New before any Options run.
const (
	defaultBaseColor            = lipgloss.Color("#F4F1FF")
	defaultShimmerColor         = lipgloss.Color("#B084FF")
	defaultShimmerFrameInterval = 80 * time.Millisecond   // ~12.5fps sweep
	defaultShimmerDuration      = 1250 * time.Millisecond // time for the band to cross
	defaultShimmerWaitTime      = 4 * time.Second         // rest between sweeps
	defaultBandWidth            = 10                      // band half-width, in characters
)

// Initializes a new instance of a shimmer bubble with options
func New(opts ...Options) Model {
	m := Model{
		id:                   nextID(),
		midShimmer:           false,
		baseColor:            defaultBaseColor,
		shimmerColor:         defaultShimmerColor,
		shimmerFrameInterval: defaultShimmerFrameInterval,
		shimmerDuration:      defaultShimmerDuration,
		shimmerWaitTime:      defaultShimmerWaitTime,
		bandWidth:            defaultBandWidth,
	}

	for _, opt := range opts {
		opt(&m)
	}

	m.reparseColors()

	return m
}

// reparseColors refreshes the colorful.Color copies used for blending from the
// lipgloss.Color fields. Called by New and by any Option that changes a color so
// the parsed values never drift from the configured hex strings.
func (m *Model) reparseColors() {
	if c, err := colorful.Hex(string(m.baseColor)); err == nil {
		m.base = c
	}
	if c, err := colorful.Hex(string(m.shimmerColor)); err == nil {
		m.shimmer = c
	}
}

func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {

	case TickMsg:
		//not our message, disregard
		if msg.id != m.id {
			return m, nil
		}
		//if we're not mid shimmer
		if !m.midShimmer {
			m.midShimmer = true
			m.phaseStart = msg.Time
			m.elapsed = 0
			return m, shimmerCmd(m.shimmerFrameInterval, m.id)
		}
		// calculate and udpate how much time has passed
		m.elapsed = msg.Time.Sub(m.phaseStart)

		if m.elapsed >= m.shimmerDuration {
			//shimmer has finished, reset to wait
			m.midShimmer = false
			m.phaseStart = msg.Time
			m.elapsed = 0
			return m, shimmerCmd(m.shimmerWaitTime, m.id)
		}
		//still shimmering, advance to next frame
		return m, shimmerCmd(m.shimmerFrameInterval, m.id)
	}
	return m, nil
}

func (m Model) View() string {
	if !m.midShimmer {
		return m.style.Foreground(m.baseColor).Render(m.text) //while waiting
	}
	frac := float64(m.elapsed) / float64(m.shimmerDuration) // ratio of how far in the sweep we are 0->1
	return renderShimmerSweep(m, m.style, m.text, frac)     //render next frame of sweep
}

// Tick that's decorated with a time and an Id so we can track where the message came from and
// use the timing to see how long we've been in the phase
func (m Model) Tick() tea.Msg {
	return TickMsg{
		Time: time.Now(),
		id:   m.id,
	}
}

// Calculates a single characters color to render
func shimmerSweepColor(m Model, col int, width int, frac float64) lipgloss.Color {
	sweepPos := frac * float64(width)         //wheres the center of the shimmer
	dist := math.Abs(float64(col) - sweepPos) //how close are we to the center
	blend := math.Max(0, 1-dist/float64(m.bandWidth))

	c := m.base.BlendLab(m.shimmer, blend).Clamped()
	return lipgloss.Color(c.Hex())
}

// renderShimmerSweep renders art with style applied per character, colored
// by shimmerSweepColor so a highlight band sweeps left to right across it.
func renderShimmerSweep(m Model, style lipgloss.Style, art string, frac float64) string {
	lines := strings.Split(art, "\n")

	width := 0
	for _, line := range lines {
		if len(line) > width {
			width = len(line)
		}
	}

	var out strings.Builder
	for i, line := range lines {
		if i > 0 {
			out.WriteByte('\n')
		}
		for col, r := range line {
			out.WriteString(style.Foreground(shimmerSweepColor(m, col, width, frac)).Render(string(r)))
		}
	}
	return out.String()
}

// Options configures a Model. Pass any number of these to New.
type Options func(*Model)

// --- content ---

// WithText sets the string the shimmer sweeps across. Multi-line text is fine;
// the sweep spans the width of the widest line.
func WithText(text string) Options {
	return func(m *Model) {
		m.text = text
	}
}

// WithStyle sets the base lipgloss.Style applied to every character. The
// shimmer only overrides the foreground; bold, padding, borders, etc. are
// preserved.
func WithStyle(style lipgloss.Style) Options {
	return func(m *Model) {
		m.style = style
	}
}

// --- colors ---

// WithBaseColor sets the resting color (shown between sweeps and at the edges
// of the band).
func WithBaseColor(c lipgloss.Color) Options {
	return func(m *Model) {
		m.baseColor = c
		m.reparseColors()
	}
}

// WithShimmerColor sets the highlight color at the center of the sweeping band.
func WithShimmerColor(c lipgloss.Color) Options {
	return func(m *Model) {
		m.shimmerColor = c
		m.reparseColors()
	}
}

// WithColors sets both endpoints of the blend at once.
func WithColors(base, shimmer lipgloss.Color) Options {
	return func(m *Model) {
		m.baseColor = base
		m.shimmerColor = shimmer
		m.reparseColors()
	}
}

// WithBaseRGB sets the resting color from 0-255 RGB components.
func WithBaseRGB(r, g, b uint8) Options {
	return WithBaseColor(rgbHex(r, g, b))
}

// WithShimmerRGB sets the highlight color from 0-255 RGB components.
func WithShimmerRGB(r, g, b uint8) Options {
	return WithShimmerColor(rgbHex(r, g, b))
}

// rgbHex formats 0-255 RGB components as a "#RRGGBB" lipgloss.Color.
func rgbHex(r, g, b uint8) lipgloss.Color {
	return lipgloss.Color(fmt.Sprintf("#%02X%02X%02X", r, g, b))
}

// --- timing ---

// WithSweepDuration sets how long the highlight band takes to travel across the
// text. Non-positive values are ignored.
func WithSweepDuration(d time.Duration) Options {
	return func(m *Model) {
		if d > 0 {
			m.shimmerDuration = d
		}
	}
}

// WithWaitTime sets the pause between the end of one sweep and the start of the
// next. Non-positive values are ignored.
func WithWaitTime(d time.Duration) Options {
	return func(m *Model) {
		if d > 0 {
			m.shimmerWaitTime = d
		}
	}
}

// WithFrameInterval sets the time between animation frames during a sweep
// (i.e. the sweep's effective frame rate). Non-positive values are ignored.
func WithFrameInterval(d time.Duration) Options {
	return func(m *Model) {
		if d > 0 {
			m.shimmerFrameInterval = d
		}
	}
}

// WithFPS is a convenience wrapper over WithFrameInterval, expressed as frames
// per second. Non-positive values are ignored.
func WithFPS(fps int) Options {
	return func(m *Model) {
		if fps > 0 {
			m.shimmerFrameInterval = time.Second / time.Duration(fps)
		}
	}
}

// --- appearance ---

// WithBandWidth sets how many characters wide the highlight band's falloff is.
// Larger is a softer, broader glow. Non-positive values are ignored.
func WithBandWidth(chars int) Options {
	return func(m *Model) {
		if chars > 0 {
			m.bandWidth = chars
		}
	}
}
