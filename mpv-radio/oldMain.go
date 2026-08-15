package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/signal"

	"lofi-radio/mpvplayer"
)

// readInput continuously reads lines from stdin into keys, closing keys when stdin is exhausted.
func readInput(keys chan string) {
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		keys <- scanner.Text()
	}
	if err := scanner.Err(); err != nil {
		fmt.Println("stdin read error:", err)
	}
	close(keys)
}

func dispalyTitle(title string) {
	fmt.Printf("\tYou are listening to:\n\t%s\n\n", title)
}

// reload quits the current mpv process and starts a fresh one on the same
// url, returning the new player once it's up.
func reload(ctx context.Context, player *mpvplayer.MpvPlayer, url string) (*mpvplayer.MpvPlayer, error) {
	if _, err := player.Quit(); err != nil {
		return nil, fmt.Errorf("could not quit player for reload: %w", err)
	}

	// wait for the old process to fully exit so its goroutine/socket clean up
	<-player.Done()

	return mpvplayer.NewPlayer(ctx, url)
}

func oldMain() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	playlistUrl := "https://www.youtube.com/playlist?list=PL6NdkXsPL07Il2hEQGcLI4dg_LTg7xA2L"

	videos, err := mpvplayer.GetVideosFromPlaylist(playlistUrl)

	if err != nil {
		fmt.Printf("Could not resolve videos from playlist: %v", err)
	}

	curr := 0

	// url := "https://www.youtube.com/watch?v=X4VbdwhkE10"

	player, err := mpvplayer.NewPlayer(ctx, videos[curr].URL)
	if err != nil {
		fmt.Println("Could not start player:", err)
		os.Exit(1)
	}

	fmt.Println("Playing. Commands: p = pause, r = resume, t = toggle pause, + = volume up, - = volume down, R = reload, q = quit")

	title, err := player.GetTitle()
	if err != nil {
		fmt.Printf("title: %s. err: %v\n", title, err)
	}

	dispalyTitle(videos[curr].Title)

	keys := make(chan string)
	//continuously take in key presses into the channel "keys"
	go readInput(keys)

	// for loop here blocks on the `select` statement continuously
	for {
		// blocks on input from one of the options. user input, mpv process ending, or ctrl+C from the user
		select {

		case err := <-player.Done():
			if err != nil {
				fmt.Println("mpv exited with error:", err)
			} else {
				fmt.Println("Playback finished")
			}
			return

		case <-ctx.Done():
			fmt.Println("Interrupted, stopping mpv")
			player.Quit()
			<-player.Done()
			return

		case key, ok := <-keys:
			if !ok {
				return
			}

			if key == "R" {
				newPlayer, err := reload(ctx, player, videos[curr].URL)
				if err != nil {
					fmt.Println("reload failed:", err)
					continue
				}
				player = newPlayer
				dispalyTitle(videos[curr].Title)
				continue
			}

			var cmdErr error
			switch key {
			case "t":
				_, cmdErr = player.TogglePause()
			case "+":
				_, cmdErr = player.VolumeUp()
			case "-":
				_, cmdErr = player.VolumeDown()
			case "q":
				_, cmdErr = player.Quit()
			case "k":
				dispalyTitle(videos[curr].Title)
			case "n":
				curr++
				if curr == len(videos) {
					curr = 0
				}

				newPlayer, err := reload(ctx, player, videos[curr].URL)
				if err != nil {
					fmt.Println("error changing channel:", err)
					continue
				}
				player = newPlayer
				dispalyTitle(videos[curr].Title)
				continue
			case "p":
				curr--
				if curr == -1 {
					curr = len(videos) - 1
				}

				newPlayer, err := reload(ctx, player, videos[curr].URL)
				if err != nil {
					fmt.Println("error changing channel:", err)
					continue
				}
				player = newPlayer
				dispalyTitle(videos[curr].Title)
				continue

			default:
				fmt.Println("unknown command:", key)
				continue
			}
			if cmdErr != nil {
				fmt.Println("command failed:", cmdErr)
			}
		}
	}
}
