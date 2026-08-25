package main

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"lofi-radio/mpvplayer"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	//bubbletea deps
	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// number of most-recent player log lines kept for display
const maxLogLines = 4

var nextPlayerId atomic.Int64

// //go:embed ascii/lofi-hiphop.txt
// var headerArt string

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
}

type styles struct {
	cutOffText,
	loading,
	display,
	selected,
	buttonUnselected,
	spinStyle,
	frame,
	logs,
	text lipgloss.Style
	// buttons lipgloss.Style
}

// appendLog records a line for the on-screen log pane, keeping only the
// most recent maxLogLines.
func (m *model) appendLog(line string) {
	m.logLines = append(m.logLines, line)
	if len(m.logLines) > maxLogLines {
		m.logLines = m.logLines[len(m.logLines)-maxLogLines:]
	}
}

func newStyles() (s *styles) {
	s = new(styles)
	// s.text = lipgloss.NewStyle().Foreground(lipgloss.Color("#0288D1"))
	s.text = lipgloss.NewStyle().Foreground(lipgloss.Cyan)
	s.frame = lipgloss.NewStyle().Border(lipgloss.NormalBorder()).BorderForeground(lipgloss.Color("#864EFF")).Width(45).Height(2)
	s.spinStyle = lipgloss.NewStyle().Foreground(lipgloss.BrightMagenta)
	s.buttonUnselected = lipgloss.NewStyle().Foreground(lipgloss.BrightWhite).Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.BrightBlue).Width(9).Height(-1).Align(lipgloss.Center)
	s.selected = lipgloss.NewStyle().Foreground(lipgloss.BrightMagenta).Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.BrightBlue).Width(9).Height(1).Align(lipgloss.Center)
	s.loading = lipgloss.NewStyle().Width(s.frame.GetWidth() - 2).Height(s.frame.GetHeight() + 2).Align(lipgloss.Center)
	s.cutOffText = lipgloss.NewStyle().Inline(true).MaxWidth(25)
	s.display = lipgloss.NewStyle().Inherit(s.loading).Border(lipgloss.NormalBorder())
	s.logs = lipgloss.NewStyle().Border(lipgloss.NormalBorder()).BorderForeground(lipgloss.Color("#864EFF")).Width(45).Height(s.frame.GetHeight())
	return s
}

func initialModel(ctx context.Context, playlist string, arg string) model {

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

	db, _ := strconv.ParseBool(arg)

	return model{
		styles:   newStyles(),
		player:   player,
		videos:   videos,
		ctx:      ctx,
		vidIndex: 0,
		choices:  []string{"<<", "pause", "reload", ">>"},
		paused:   false,
		loading:  true,
		clear:    false,
		spinner:  spinner.New(spinner.WithSpinner(spinner.Dot)),
		db:       db,
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
	)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tickMsg:
		return m, ticketCmd()

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

			// select reload player
			case 2:
				m.loading = true
				return m, newPlayerCmd(m.ctx, m.player, m.videos[m.vidIndex].URL)

			// select next song
			case 3:
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

	//main display section
	var display string
	var displayStyle lipgloss.Style
	if m.loading {
		display = fmt.Sprintf("%s loading", m.styles.spinStyle.Render(m.spinner.View()))
		displayStyle = m.styles.loading
	} else {
		if m.vidIndex < len(m.videos) {
			display += m.styles.cutOffText.Render(m.videos[m.vidIndex].Title)
			displayStyle = m.styles.display
		} else {
			display = fmt.Sprintf("%s loading", m.styles.spinStyle.Render(m.spinner.View()))
			displayStyle = m.styles.loading
		}
	}

	display = "\n" + displayStyle.Render(display) + "\n"

	buttons := make([]string, 0, len(m.choices))
	for i, choice := range m.choices {

		// determine button style
		style := m.styles.buttonUnselected
		if m.selected == i {
			style = m.styles.selected
		}

		if i == 1 {
			if !m.paused {
				choice = "pause"
			} else {
				blink := time.Now().UnixMilli()/500%2 == 0
				if blink {
					choice = "play"
				} else {
					choice = "    "
				}
			}
		}
		buttons = append(buttons, style.Render(fmt.Sprintf("%s", choice)))
	}

	//footer
	options := lipgloss.NewStyle().Width(m.styles.frame.GetWidth()).Align(lipgloss.Center).Render(lipgloss.JoinHorizontal(lipgloss.Top, buttons...))

	text := display + options

	radioDisplay := m.styles.frame.Render(text)

	logs := m.styles.logs.Render(strings.Join(m.logLines, "\n"))

	if m.db {
		radioDisplay = lipgloss.JoinHorizontal(lipgloss.Top, radioDisplay, logs)
	}

	//I suppose that means bubblettea understands the entire view as a string
	return tea.NewView(radioDisplay)

}

func main() {

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	playlistUrl := "https://www.youtube.com/playlist?list=PL6NdkXsPL07Il2hEQGcLI4dg_LTg7xA2L"

	db := os.Args[1]

	p := tea.NewProgram(initialModel(ctx, playlistUrl, db))

	if _, err := p.Run(); err != nil {
		fmt.Printf("ha you suck: %v", err)
		os.Exit(1)
	}

}
