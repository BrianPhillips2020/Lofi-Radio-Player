package main

import (
	"context"
	"fmt"
	"lofi-radio/mpvplayer"
	"os"
	"os/signal"
	"time"

	tea "charm.land/bubbletea/v2"
)

type model struct {
	player       *mpvplayer.MpvPlayer      //sub process handling audio streaming
	selected     int                       //which option is currently selected
	prevSelected int                       //handles going to the quit button
	ctx          context.Context           //context which manages killing the process
	videos       []mpvplayer.PlaylistVideo //the list of currently loaded videos
	vidIndex     int                       //which video we're currently watching
	loading      bool                      //whether or not we're loading the stream
	choices      []string                  //choices for the interface
	paused       bool                      //paused or playing?
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
		player:   player,
		videos:   videos,
		ctx:      ctx,
		vidIndex: 0,
		choices:  []string{"<<", "pause", "reload", ">>"},
		paused:   false,
		loading:  true,
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

func (m model) Init() tea.Cmd {
	return tea.Batch(
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

	case tea.KeyPressMsg:

		switch msg.String() {
		case "ctrl+c", "q":
			// graceful shutdown of the player and bubbletea
			return m, tea.Sequence(quitPlayerCmd(m.player), tea.Quit)

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

		case "down":
			if m.selected != 4 {
				m.prevSelected = m.selected
				m.selected = 4
			}

		case "up":
			if m.selected == 4 {
				m.selected = m.prevSelected
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

			case 4:
				// graceful shutdown of the player and bubbletea
				return m, tea.Sequence(quitPlayerCmd(m.player), tea.Quit)
			}

		}
	}
	return m, nil
}

func (m model) View() tea.View {

	// player header
	header := "\n--------Lofi Radio--------\n\n"

	//main display section
	var display string

	if m.loading {
		display = "loading...\n\n\n"
	} else {
		display += "\tYou are listening to:\n"
		if m.vidIndex < len(m.videos) {
			display += m.videos[m.vidIndex].Title + "\n\n"
		} else {
			display += "loading...\n\n\n"
		}
	}

	options := ""
	for i, choice := range m.choices {
		l := " "
		r := " "
		if m.selected == i {
			l = "["
			r = "]"
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
		options += fmt.Sprintf("|%s%s%s|", l, choice, r)
	}

	l := " "
	r := " "
	if m.selected == 4 {
		l = "["
		r = "]"
	}

	//footer
	options += fmt.Sprintf("\n\n%squit%s\n", l, r)

	//I suppose that means bubblettea understands the entire view as a string
	return tea.NewView(header + display + options)

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
