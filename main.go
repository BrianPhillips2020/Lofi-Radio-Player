package main

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"image/color"
	"lofi-radio/mpvplayer"
	"math"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	//bubbletea deps
	"charm.land/bubbles/v2/progress"
	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// number of most-recent player log lines kept for display
const maxLogLines = 4

var nextPlayerId atomic.Int64

//go:embed lofi.txt
var lofiArtRaw string

//go:embed synthwave2.txt
var synthwaveRaw string

// loadASCIIArt returns the embedded lofi.txt ascii art, trimmed of any
// trailing newline left over from the source file.
func loadASCIIArt() string {
	return strings.TrimRight(lofiArtRaw, "\n")
}

func loadSynthWave() string {
	return strings.TrimRight(synthwaveRaw, "\n")
}

type model struct {
	styles       *styles
	player       *mpvplayer.MpvPlayer      //sub process handling audio streaming
	tempPlayer   *mpvplayer.MpvPlayer      //temp player holder
	selected     int                       //which option is currently selected
	prevSelected int                       //handles going to the quit button
	ctx          context.Context           //context which manages killing the process
	videos       []mpvplayer.PlaylistVideo //the list of currently loaded videos
	vidIndex     int                       //which video we're currently watching
	loading      bool                      //whether or not we're loading the stream
	choices      []string                  //choices for the interface
	paused       bool                      //paused or playing?
	clear        bool
	spinner      spinner.Model
	db           bool
	logLines     []string //most recent lines read from the player's stdout by WatchForInterrupt
	volume       int
	muted        bool
	volumeBar    progress.Model
	help         bool
}

type styles struct {
	cutOffText,
	loading,
	display,
	selected,
	buttonUnselected,
	spinStyle,
	frame,
	outer,
	logs,
	text,
	title,
	nowPlayingLabel,
	songTitle,
	art,
	hint,
	volumeLabel,
	clock lipgloss.Style
}

// appendLog records a line for the on-screen log pane, keeping only the
// most recent maxLogLines.
func (m *model) appendLog(line string) {
	m.logLines = append(m.logLines, line)
	if len(m.logLines) > maxLogLines {
		m.logLines = m.logLines[len(m.logLines)-maxLogLines:]
	}
}

var (
	accentColor = lipgloss.Color("#B084FF")
	dimColor    = lipgloss.Color("#6C6C7A")
	textColor   = lipgloss.Color("#F4F1FF")
)

const cardWidth = 38

func newStyles() (s *styles) {
	s = new(styles)
	s.text = lipgloss.NewStyle().Foreground(lipgloss.Cyan)

	s.frame = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(accentColor).
		Padding(1, 3)

	s.outer = lipgloss.NewStyle().
		Border(lipgloss.ThickBorder()).
		BorderForeground(accentColor).
		Padding(1)

	s.title = lipgloss.NewStyle().
		Bold(true).
		Foreground(accentColor).
		Width(cardWidth).
		Align(lipgloss.Center)

	s.spinStyle = lipgloss.NewStyle().Foreground(lipgloss.BrightMagenta)

	s.nowPlayingLabel = lipgloss.NewStyle().
		Foreground(dimColor).
		Bold(true).
		Width(cardWidth).
		Align(lipgloss.Center)

	s.songTitle = lipgloss.NewStyle().
		Bold(true).
		Foreground(textColor).
		Width(cardWidth).
		Align(lipgloss.Center)

	s.art = lipgloss.NewStyle().
		Foreground(lipgloss.White).
		Align(lipgloss.Center)

	s.buttonUnselected = lipgloss.NewStyle().
		Foreground(textColor).
		Padding(0, 2)

	s.selected = lipgloss.NewStyle().
		Foreground(lipgloss.Color("#1A1620")).
		Background(accentColor).
		Bold(true).
		Padding(0, 2)

	s.loading = lipgloss.NewStyle().Width(cardWidth).Align(lipgloss.Center)
	s.cutOffText = lipgloss.NewStyle().Inline(true).MaxWidth(cardWidth)
	s.display = s.loading

	s.volumeLabel = lipgloss.NewStyle().Foreground(dimColor)

	s.clock = lipgloss.NewStyle().Foreground(dimColor)

	s.hint = lipgloss.NewStyle().
		Foreground(dimColor).
		Italic(true).
		Width(cardWidth + 8). // matches frame content + padding + border
		Align(lipgloss.Center)

	s.logs = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(dimColor).
		Padding(0, 1).
		Width(45)

	return s
}

func initialModel(ctx context.Context, playlist string, arg bool) model {

	videos, err := mpvplayer.GetVideosFromPlaylist(playlist)

	if err != nil {
		fmt.Printf("Initialization error: %v", err)
	}

	var firstUrl string
	if len(videos) > 0 {
		firstUrl = videos[0].URL
	}

	player, err := mpvplayer.NewPlayer(ctx, firstUrl, int(nextPlayerId.Add(1)))

	if err != nil {
		fmt.Printf("Initialization error: %v", err)
	}

	return model{
		styles:    newStyles(),
		player:    player,
		videos:    videos,
		ctx:       ctx,
		vidIndex:  0,
		choices:   []string{"⏮", "⏸", "⏭"},
		paused:    false,
		loading:   true,
		clear:     false,
		spinner:   spinner.New(spinner.WithSpinner(spinner.Dot)),
		db:        arg,
		volume:    50,
		muted:     false,
		volumeBar: progress.New(progress.WithScaled(true)),
		help:      false,
	}

}

// Bubbletea Msg types
// ------------------------------------
type playlistLoaderMsg struct {
	videos []mpvplayer.PlaylistVideo
	err    error
}

type playerChangedMsg struct {
	player *mpvplayer.MpvPlayer
	err    error
}

type playerLoadedMsg struct {
	err error
}

type tickMsg time.Time

// shimmerMsg drives the periodic re-coloring of the synthwave art's
// foreground so it pulses between white and neon blue.
type shimmerMsg time.Time

type quitMsg struct {
	err error
}

type playbackInterruptMsg struct {
	err error
}

type tempPlayerCreatedMsg struct {
	tempPlayer *mpvplayer.MpvPlayer
	err        error
}

type tempPlayerLoadedMsg struct {
	err error
}

// logLineMsg carries one line read from the player's stdout by WatchForInterrupt
type logLineMsg string

// Bubbletea Commands
// -------------------------------------

func ticketCmd() tea.Cmd {
	return tea.Tick(500*time.Millisecond, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

// shimmerInterval controls how often the shimmer sweep is recomputed;
// shimmerSweepTime is how long the highlight band takes to cross the art;
// shimmerWaitTime is the pause before the next sweep starts;
// shimmerBandWidth is how many columns wide the band is, in characters.
const (
	shimmerInterval  = 80 * time.Millisecond
	shimmerSweepTime = 1250 * time.Millisecond
	shimmerWaitTime  = 2 * time.Second
	shimmerBandWidth = 10
)

func shimmerCmd() tea.Cmd {
	return tea.Tick(shimmerInterval, func(t time.Time) tea.Msg {
		return shimmerMsg(t)
	})
}

// shimmerSweepColor returns the color for the character at col out of width
// total columns, at time t: neon blue inside a band that sweeps left to
// right once per cycle, then pauses white for shimmerWaitTime before the
// next sweep.
func shimmerSweepColor(col, width int, t time.Time) color.Color {
	white := [3]float64{255, 255, 255}
	neonBlue := [3]float64{5, 130, 180}

	seconds := float64(t.UnixNano()) / float64(time.Second)
	cycle := shimmerSweepTime.Seconds() + shimmerWaitTime.Seconds()
	phase := math.Mod(seconds, cycle)

	if phase >= shimmerSweepTime.Seconds() {
		return lipgloss.Color("#FFFFFF")
	}

	sweepPos := (phase / shimmerSweepTime.Seconds()) * float64(width)
	dist := math.Abs(float64(col) - sweepPos)
	blend := math.Max(0, 1-dist/shimmerBandWidth)

	r := int(white[0] + blend*(neonBlue[0]-white[0]))
	g := int(white[1] + blend*(neonBlue[1]-white[1]))
	b := int(white[2] + blend*(neonBlue[2]-white[2]))

	return lipgloss.Color(fmt.Sprintf("#%02X%02X%02X", r, g, b))
}

// renderShimmerSweep renders art with style applied per character, colored
// by shimmerSweepColor so a highlight band sweeps left to right across it.
func renderShimmerSweep(style lipgloss.Style, art string, t time.Time) string {
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
			out.WriteString(style.Foreground(shimmerSweepColor(col, width, t)).Render(string(r)))
		}
	}
	return out.String()
}

func loadPlaylistCmd(ctx context.Context, url string) tea.Cmd {
	return func() tea.Msg {
		videos, err := mpvplayer.GetVideosFromPlaylist(url)
		return playlistLoaderMsg{videos, err}
	}
}

func quitPlayerCmd(player *mpvplayer.MpvPlayer) tea.Cmd {
	return func() tea.Msg {
		player.Quit()
		<-player.Done()
		return quitMsg{}
	}
}

// quitStalePlayerCmd retires a player that's no longer m.player (the old
// player after a background swap, or a temp player that failed to load)
// without emitting quitMsg, which is reserved for full app shutdown.
func quitStalePlayerCmd(player *mpvplayer.MpvPlayer) tea.Cmd {
	return func() tea.Msg {
		player.Quit()
		<-player.Done()
		return nil
	}
}

func newPlayerCmd(ctx context.Context, player *mpvplayer.MpvPlayer, url string) tea.Cmd {
	return func() tea.Msg {
		if _, err := player.Quit(); err != nil {
			return playerChangedMsg{err: fmt.Errorf("could not quit player: %w", err)}
		}

		// wait for the old process to fully exit so its goroutine/socket clean up
		<-player.Done()

		newPlayer, err := mpvplayer.NewPlayer(ctx, url, int(nextPlayerId.Add(1)))
		return playerChangedMsg{player: newPlayer, err: err}
	}
}

// Load a new temporary player
func newTempPlayerCmd(ctx context.Context, url string) tea.Cmd {
	return func() tea.Msg {
		tempPlayer, err := mpvplayer.NewPlayer(ctx, url, int(nextPlayerId.Add(1)))
		return tempPlayerCreatedMsg{tempPlayer: tempPlayer, err: err}
	}

}

func togglePlayCmd(player *mpvplayer.MpvPlayer) tea.Cmd {
	return func() tea.Msg {
		player.TogglePause()
		return nil
	}
}

func loadingRadioCmd(ctx context.Context, player *mpvplayer.MpvPlayer, temp bool) tea.Cmd {
	return func() tea.Msg {
		if !temp {
			return playerLoadedMsg{err: player.HasPlaybackBegun(ctx)}
		}
		return tempPlayerLoadedMsg{err: player.HasPlaybackBegun(ctx)}
	}
}

func playerReconnectCmd(ctx context.Context, player *mpvplayer.MpvPlayer) tea.Cmd {
	return func() tea.Msg {
		return playbackInterruptMsg{err: player.WatchForInterrupt(ctx)}
	}
}

// listenLogsCmd waits for the next line the player emits and re-issues
// itself so the model keeps draining player.Logs() one line at a time.
func listenLogsCmd(player *mpvplayer.MpvPlayer) tea.Cmd {
	return func() tea.Msg {
		line, ok := <-player.Logs()
		if !ok {
			return nil
		}
		return logLineMsg(line)
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(
		m.spinner.Tick,
		loadPlaylistCmd(m.ctx, "https://www.youtube.com/playlist?list=PL6NdkXsPL07Il2hEQGcLI4dg_LTg7xA2L"),
		loadingRadioCmd(m.ctx, m.player, false),
		listenLogsCmd(m.player),
		ticketCmd(),
		shimmerCmd(),
	)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tickMsg:
		return m, ticketCmd()

	case shimmerMsg:
		return m, shimmerCmd()

	case playlistLoaderMsg:
		if msg.err != nil {
			m.loading = false
			return m, nil
		}

		m.videos = msg.videos
		m.loading = false

	case playerChangedMsg:
		if msg.err != nil {
			return m, nil
		}
		m.player = msg.player
		m.loading = true
		return m, tea.Batch(loadingRadioCmd(m.ctx, m.player, false), listenLogsCmd(m.player))

	case logLineMsg:
		m.appendLog(string(msg))
		return m, listenLogsCmd(m.player)

	case playerLoadedMsg:
		if msg.err != nil {
			return m, nil
		}
		m.loading = false
		m.player.SetVolume(m.volume)
		if m.muted {
			m.player.SetMute(true)
		}
		return m, playerReconnectCmd(m.ctx, m.player)

	case quitMsg:
		m.clear = true
		return m, tea.ClearScreen

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case playbackInterruptMsg:
		// stdout can also close because we deliberately quit the player
		// (manual reload/next/prev); only auto-reconnect on a real 403
		if !errors.Is(msg.err, mpvplayer.ErrPlaybackInterrupted) {
			return m, nil
		}
		return m, newTempPlayerCmd(m.ctx, m.videos[m.vidIndex].URL)

	// a temp player has been created, now wait for temp player to finish loading
	case tempPlayerCreatedMsg:
		if msg.err != nil {
			m.appendLog(fmt.Sprintf("reconnect: failed to start replacement player: %v", msg.err))
			return m, nil
		}
		m.tempPlayer = msg.tempPlayer
		return m, loadingRadioCmd(m.ctx, m.tempPlayer, true)

	// TODO: If temp player is loading while the user goes to a new station
	// the new station will be switched back because of the intermediate temp player logic
	// This should be fixed.
	case tempPlayerLoadedMsg:
		if msg.err != nil {
			m.appendLog(fmt.Sprintf("reconnect: replacement player failed to load: %v", msg.err))
			failedPlayer := m.tempPlayer
			m.tempPlayer = nil
			return m, quitStalePlayerCmd(failedPlayer)
		}
		//swap out old player
		oldPlayer := m.player
		m.player = m.tempPlayer

		//remove reference in temp player
		m.tempPlayer = nil

		return m, tea.Batch(quitStalePlayerCmd(oldPlayer), listenLogsCmd(m.player), playerReconnectCmd(m.ctx, m.player))

	case tea.KeyPressMsg:

		switch msg.String() {
		case "ctrl+c", "q":
			// graceful shutdown of the player and bubbletea
			return m, tea.Sequence(quitPlayerCmd(m.player), tea.ClearScreen, tea.Quit)

		// move selector arrow right
		case "right":
			if m.selected < len(m.choices)-1 {
				m.selected++
			}

		// move selector arrow left
		case "left":
			if m.selected > 0 {
				m.selected--
			}

		case "up":
			if m.volume < 100 {
				if m.muted {
					_, _ = m.player.SetMute(false)
					m.muted = false
				}
				m.volume += 10
				m.player.VolumeUp()
			}

		case "down":
			if m.volume >= 10 {
				if m.muted {
					_, _ = m.player.SetMute(false)
					m.muted = false
				}
				m.volume -= 10
				m.player.VolumeDown()
				if m.volume == 0 {
					_, _ = m.player.SetMute(true)
					m.muted = true
				}
			}

		case "m":
			if muted, err := m.player.ToggleMute(); err == nil {
				m.muted = muted
			}

		case "r":
			m.loading = true
			return m, newPlayerCmd(m.ctx, m.player, m.videos[m.vidIndex].URL)

		case "h":
			m.help = !m.help

		// select option
		case "enter":
			switch m.selected {
			// select prev station
			case 0:
				m.vidIndex--
				if m.vidIndex < 0 {
					m.vidIndex = len(m.videos) - 1
				}
				m.loading = true
				return m, newPlayerCmd(m.ctx, m.player, m.videos[m.vidIndex].URL)

			// select toggle pause
			case 1:
				m.paused = !m.paused
				return m, togglePlayCmd(m.player)

			// select next song
			case 2:
				m.vidIndex++
				if m.vidIndex == len(m.videos) {
					m.vidIndex = 0
				}
				m.loading = true
				return m, newPlayerCmd(m.ctx, m.player, m.videos[m.vidIndex].URL)

			}

		}
	}
	return m, nil
}

func (m model) View() tea.View {

	if m.clear {
		return tea.NewView("")
	}

	title := m.styles.title.Render("L O F I   R A D I O")
	clock := m.styles.clock.Render(time.Now().Format("3:04 PM"))

	// now-playing section
	var nowPlaying string
	if m.loading || m.vidIndex >= len(m.videos) {
		spin := fmt.Sprintf("%s tuning in…", m.styles.spinStyle.Render(m.spinner.View()))
		nowPlaying = m.styles.loading.Render(spin)
	} else {
		label := m.styles.nowPlayingLabel.Render("YOU ARE LISTENING TO")
		art := m.styles.art.Render(loadASCIIArt())
		if m.vidIndex == 1 {
			art = renderShimmerSweep(m.styles.art, loadSynthWave(), time.Now())
		}
		nowPlaying = lipgloss.JoinVertical(lipgloss.Center, label, art)
	}

	// transport controls
	buttons := make([]string, 0, len(m.choices))
	for i, choice := range m.choices {

		style := m.styles.buttonUnselected
		if m.selected == i {
			style = m.styles.selected
		}

		if i == 1 {
			if !m.paused {
				choice = "⏸"
			} else {
				blink := time.Now().UnixMilli()/500%2 == 0
				if blink {
					choice = "▶"
				} else {
					choice = " "
				}
			}
		}
		buttons = append(buttons, style.Render(choice))
	}
	controls := lipgloss.NewStyle().Width(cardWidth).Align(lipgloss.Center).
		Render(lipgloss.JoinHorizontal(lipgloss.Top, buttons...))

	// volumeRow := m.volumeBar.ViewAs(float64(m.volume) / float64(100))

	var body string
	if m.loading || m.vidIndex >= len(m.videos) {
		body = lipgloss.JoinVertical(lipgloss.Center, title, "", nowPlaying)
	} else {
		body = lipgloss.JoinVertical(lipgloss.Center, title, "", nowPlaying, "", controls)
		// body = lipgloss.JoinVertical(lipgloss.Center, title, "", nowPlaying, "", controls, "", volumeRow)
	}

	card := m.styles.frame.Render(body)

	hint := m.styles.hint.Render("←/→ select · enter confirm · ↑/↓ vol · m mute · q quit")

	radioDisplay := card

	if m.help {
		radioDisplay = lipgloss.JoinVertical(lipgloss.Center, card, hint)
	}

	if m.db {
		logs := m.styles.logs.Render(strings.Join(m.logLines, "\n"))
		radioDisplay = lipgloss.JoinHorizontal(lipgloss.Top, radioDisplay, logs)
	}

	// clock pinned to the top-left corner, above the untouched layout below
	radioDisplay = lipgloss.JoinVertical(lipgloss.Left, clock, radioDisplay)

	v := tea.NewView(radioDisplay)

	v.AltScreen = true

	return v

}

func main() {

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	playlistUrl := "https://www.youtube.com/playlist?list=PL6NdkXsPL07Il2hEQGcLI4dg_LTg7xA2L"

	// Take in arguments. If no arguments passed in or error, assume false
	db := false
	var err error
	//note for brian -> checking for >1 because the program name is also in os.Args
	if len(os.Args) > 1 {
		db, err = strconv.ParseBool(os.Args[1])
		if err != nil {
			db = false
		}
	}

	p := tea.NewProgram(initialModel(ctx, playlistUrl, db))

	if _, err := p.Run(); err != nil {
		fmt.Printf("ha you suck: %v", err)
		os.Exit(1)
	}

}
