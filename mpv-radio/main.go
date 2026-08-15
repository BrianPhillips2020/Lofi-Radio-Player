package main

import (
	"context"
	"fmt"
	"lofi-radio/mpvplayer"
	"os"
	"os/signal"

	tea "charm.land/bubbletea/v2"
)

type model struct {
	player   *mpvplayer.MpvPlayer      //sub process handling audio streaming
	selected int                       //which option is currently selected
	ctx      context.Context           //context which manages killing the process
	videos   []mpvplayer.PlaylistVideo //the list of currently loaded videos
	vidIndex int                       //which video we're currently watching
	loading  bool                      //whether or not we're loading the stream
	choices  []string                  //choices for the interface
	paused   bool                      //paused or playing?
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
	}

}

type playlistLoaderMsg struct {
	videos []mpvplayer.PlaylistVideo
	err    error
}

type playerChangedMsg struct {
	player *mpvplayer.MpvPlayer
	err    error
}

// Bubbletea Commands
// -------------------------------------

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

func (m model) Init() tea.Cmd {
	return loadPlaylistCmd(m.ctx, "https://www.youtube.com/playlist?list=PL6NdkXsPL07Il2hEQGcLI4dg_LTg7xA2L")
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

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

		// select option
		case "enter":
			switch m.selected {
			// select prev station
			case 0:
				m.vidIndex--
				if m.vidIndex < 0 {
					m.vidIndex = len(m.videos) - 1
				}
				return m, newPlayerCmd(m.ctx, m.player, m.videos[m.vidIndex].URL)

			// select toggle pause
			case 1:
				m.paused = !m.paused
				return m, togglePlayCmd(m.player)

			// select reload player
			case 2:
				return m, newPlayerCmd(m.ctx, m.player, m.videos[m.vidIndex].URL)

			// select next song
			case 3:
				m.vidIndex++
				if m.vidIndex == len(m.videos) {
					m.vidIndex = 0
				}
				return m, newPlayerCmd(m.ctx, m.player, m.videos[m.vidIndex].URL)

			}

		}
	}
	return m, nil
}

func (m model) View() tea.View {
	s := "\tYou are listening to:\n"
	if m.vidIndex < len(m.videos) {
		s += m.videos[m.vidIndex].Title + "\n\n"
	} else {
		s += "loading...\n\n\n"
	}

	for i, choice := range m.choices {
		l := " "
		r := " "
		if m.selected == i {
			l = "["
			r = "]"
		}

		if i == 1 {
			if !m.paused {
				choice = "play"
			} else {
				choice = "pause"
			}
		}
		s += fmt.Sprintf("|%s%s%s|", l, choice, r)
	}

	//footer
	s += "\n\nq to quit\n"

	//I suppose that means bubblettea understands the entire view as a string
	return tea.NewView(s)

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
