package main

import (
	"context"
	_ "embed"
	"fmt"
	"lofi-radio/mpvplayer"
	"os"
	"os/signal"
	"time"

	//bubbletea deps
	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

//go:embed ascii/lofi-hiphop.txt
var headerArt string

const debugFile = "mpv-debug.log"

type model struct {
	styles       *styles
	player       *mpvplayer.MpvPlayer      //sub process handling audio streaming
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
}

type styles struct {
	cutOffText,
	loading,
	display,
	selected,
	buttonUnselected,
	spinStyle,
	frame,
	text lipgloss.Style
	// buttons lipgloss.Style
}

func newStyles() (s *styles) {
	s = new(styles)
	// s.text = lipgloss.NewStyle().Foreground(lipgloss.Color("#0288D1"))
	s.text = lipgloss.NewStyle().Foreground(lipgloss.Cyan)
	s.frame = lipgloss.NewStyle().Border(lipgloss.NormalBorder()).BorderForeground(lipgloss.Color("#864EFF")).Width(45)
	s.spinStyle = lipgloss.NewStyle().Foreground(lipgloss.BrightMagenta)
	s.buttonUnselected = lipgloss.NewStyle().Foreground(lipgloss.BrightWhite).Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.BrightBlue).Width(9).Height(-1).Align(lipgloss.Center)
	s.selected = lipgloss.NewStyle().Foreground(lipgloss.BrightMagenta).Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.BrightBlue).Width(9).Height(1).Align(lipgloss.Center)
	s.loading = lipgloss.NewStyle().Width(s.frame.GetWidth() - 2).Height(s.frame.GetHeight() + 2).Align(lipgloss.Center)
	s.cutOffText = lipgloss.NewStyle().Inline(true).MaxWidth(25)
	s.display = lipgloss.NewStyle().Inherit(s.loading).Border(lipgloss.NormalBorder())
	return s
}

func initialModel(ctx context.Context, playlist string) model {

	videos, err := mpvplayer.GetVideosFromPlaylist(playlist)

	if err != nil {
		fmt.Errorf("Initialization error: %v", err)
	}

	var firstUrl string
	if len(videos) > 0 {
		firstUrl = videos[0].URL
	}

	player, err := mpvplayer.NewPlayer(ctx, firstUrl)

	if err != nil {
		fmt.Errorf("Initialization error: %v", err)
	}

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

type playbackErrorMsg struct {
	err error
}

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

func newPlayerCmd(ctx context.Context, player *mpvplayer.MpvPlayer, url string) tea.Cmd {
	return func() tea.Msg {
		if err := player.Quit(); err != nil {
			return playerChangedMsg{err: fmt.Errorf("could not quit player: %w", err)}
		}

		// wait for the old process to fully exit so its goroutine/socket clean up
		<-player.Done()

		newPlayer, err := mpvplayer.NewPlayer(ctx, url)
		return playerChangedMsg{player: newPlayer, err: err}
	}
}

func togglePlayCmd(player *mpvplayer.MpvPlayer) tea.Cmd {
	return func() tea.Msg {
		player.TogglePause()
		return nil
	}
}

func loadingRadioCmd(ctx context.Context, player *mpvplayer.MpvPlayer) tea.Cmd {
	return func() tea.Msg {
		return playerLoadedMsg{err: player.HasPlaybackBegun(ctx)}
	}
}

func watchForAudioFailureCmd(ctx context.Context, player *mpvplayer.MpvPlayer) tea.Cmd {
	return func() tea.Msg {
		return playbackErrorMsg{err: player.WatchStdout(ctx, "HTTP error")}
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(
		m.spinner.Tick,
		loadPlaylistCmd(m.ctx, "https://www.youtube.com/playlist?list=PL6NdkXsPL07Il2hEQGcLI4dg_LTg7xA2L"),
		loadingRadioCmd(m.ctx, m.player),
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
		return m, loadingRadioCmd(m.ctx, m.player)

	case playerLoadedMsg:
		if msg.err != nil {
			return m, nil
		}
		m.loading = false
		return m, watchForAudioFailureCmd(m.ctx, m.player)

	case playbackErrorMsg:
		if msg.err != nil {
			return m, newPlayerCmd(m.ctx, m.player, m.videos[m.vidIndex].URL)
		}

	case quitMsg:
		m.clear = true
		return m, tea.ClearScreen

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

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

		// case "down":
		// 	if m.selected != 4 {
		// 		m.prevSelected = m.selected
		// 		m.selected = 4
		// 	}

		// case "up":
		// 	if m.selected == 4 {
		// 		m.selected = m.prevSelected
		// 	}

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

				// case 4:
				// 	// graceful shutdown of the player and bubbletea
				// 	return m, tea.Sequence(quitPlayerCmd(m.player), tea.ClearScreen, tea.Quit)
			}

		}
	}
	return m, nil
}

func (m model) View() tea.View {

	if m.clear {
		return tea.NewView("")
	}
	// player header
	// header := "\n--------Lofi Radio--------\n\n"

	// header := m.styles.text.Render(headerArt)

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

	// style := m.styles.buttonUnselected
	// if m.selected == 4 {
	// 	style = m.styles.selected
	// }

	//footer
	options := lipgloss.NewStyle().Width(m.styles.frame.GetWidth()).Align(lipgloss.Center).Render(lipgloss.JoinHorizontal(lipgloss.Top, buttons...))

	text := display + options

	result := m.styles.frame.Render(text)

	//I suppose that means bubblettea understands the entire view as a string
	return tea.NewView(result)

}

func main() {

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	playlistUrl := "https://www.youtube.com/playlist?list=PL6NdkXsPL07Il2hEQGcLI4dg_LTg7xA2L"

	p := tea.NewProgram(initialModel(ctx, playlistUrl))

	if _, err := p.Run(); err != nil {
		fmt.Printf("ha you suck: %v", err)
		os.Exit(1)
	}

}
