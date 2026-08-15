package mpvplayer

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

// mpv wrapper scruct
type MpvPlayer struct {
	mu sync.Mutex
	// actual command running the mpv instance
	cmd *exec.Cmd
	// socket for ipc commands
	socketPath string
	// the channel outputting the error returned by the command execution
	done chan error
	// the url passed to mpv, used to detect when media-title hasn't
	// resolved yet (e.g. youtube titles resolved async via yt-dlp)
	url string
}

// Return a new Player. ctx controls the mpv process's lifetime: canceling
// it kills mpv. Callers typically pass a context tied to program shutdown
// (e.g. signal.NotifyContext), not one that's canceled right away.
func NewPlayer(ctx context.Context, url string) (*MpvPlayer, error) {

	// sets the path for communicating with mpv
	socketPath := filepath.Join(os.TempDir(), fmt.Sprintf("mpvsocket-%d.sock", os.Getpid()))

	cmd := exec.CommandContext(ctx, "mpv", "--no-video", fmt.Sprintf("--input-ipc-server=%s", socketPath), url)

	//ensures child process isolated from parent processing
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
		return nil, err
	}

	player := &MpvPlayer{cmd: cmd, socketPath: socketPath, done: make(chan error), url: url}

	go func() {
		player.done <- cmd.Wait()
		os.Remove(socketPath)
	}()

	return player, nil
}

// Sends controls to mpv goroutine through json ipc
// Currently this is blocking, Only 1 command will be recieved and response read when it is sent
// TODO: make this method non-blocking
func (p *MpvPlayer) sendCommand(args ...any) (string, error) {

	//Get a new connection for this command
	conn, err := connectWithRetry(p.socketPath, 10)

	if err != nil {
		return "", fmt.Errorf("Error connecting to mpv socket: %w", err)
	}

	//defer connection close until after the method exits
	defer conn.Close()

	// bail out instead of blocking forever if mpv never responds
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		return "", err
	}

	// generate random request_id. Could track currently active requests but fucking whatever, what are the odds
	// I say that but the odds are 1 : 900 so if I run this long enough there will DEF be at least 1 collission lol
	reqId := rand.IntN(900) + 100

	payload, err := json.Marshal(map[string]any{"command": args, "request_id": reqId})

	if err != nil {
		return "", err
	}

	reader := bufio.NewReader(conn)

	payload = append(payload, '\n')

	//write the command to the socket
	if _, err := conn.Write(payload); err != nil {
		return "", err
	}

	// mpv can interleave unsolicited event lines on the same connection,
	// so keep reading until we see the line that matches our request_id.
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return "", fmt.Errorf("error reading mpv response: %w", err)
		}

		// mpv.io json response struct
		var resp struct {
			RequestID int    `json:"request_id"`
			Error     string `json:"error"`
			Data      any    `json:"data"`
		}

		if err := json.Unmarshal([]byte(line), &resp); err != nil {
			continue
		}

		if resp.RequestID != reqId {
			continue
		}

		if resp.Error != "success" {
			return "", fmt.Errorf("mpv error: %s", resp.Error)
		}

		return fmt.Sprint(resp.Data), nil
	}
}

// create the socket connection, retry maxRetries number of times if failed
func connectWithRetry(socketPath string, maxRetries int) (net.Conn, error) {
	var conn net.Conn
	var err error

	for i := 1; i <= maxRetries; i++ {

		conn, err = net.Dial("unix", socketPath)

		if err == nil {
			return conn, nil
		}

		if i < maxRetries {
			time.Sleep(50 * time.Millisecond)
		}
	}

	return nil, err
}

// Command Functions!
//----------------------------------------------

func (p *MpvPlayer) Pause() (string, error) {
	return p.sendCommand("set-property", "pause", true)
}
func (p *MpvPlayer) Resume() (string, error)      { return p.sendCommand("set-property", "pause", false) }
func (p *MpvPlayer) TogglePause() (string, error) { return p.sendCommand("cycle", "pause") }

func (p *MpvPlayer) VolumeUp() (string, error)   { return p.sendCommand("add", "volume", 5) }
func (p *MpvPlayer) VolumeDown() (string, error) { return p.sendCommand("add", "volume", -5) }

func (p *MpvPlayer) Quit() (string, error) { return p.sendCommand("quit") }

func (p *MpvPlayer) NextSong() (string, error) { return p.sendCommand("playlist-next") }
func (p *MpvPlayer) PrevSong() (string, error) { return p.sendCommand("playlist-prev") }

// GetTitle returns mpv's media-title. For sources like YouTube, mpv resolves
// the real title asynchronously (via yt-dlp) shortly after playback starts,
// so this retries until the title differs from the raw url or retries run out.
func (p *MpvPlayer) GetTitle() (string, error) {
	var title string
	var err error

	for i := 0; i < 10; i++ {
		title, err = p.sendCommand("get_property", "media-title")
		if err != nil {
			return "", err
		}

		if strings.Contains(title, p.url) {
			return title, nil
		}

		time.Sleep(300 * time.Millisecond)
	}

	return title, nil
}

// Done returns a channel that receives mpv's exit error (nil on clean exit)
// once the process ends, whether from Quit, an external kill, or finishing
// playback on its own.
func (p *MpvPlayer) Done() <-chan error { return p.done }

// Playlist resolution
//----------------------------------------------

// PlaylistVideo is the subset of fields yt-dlp prints per line
type PlaylistVideo struct {
	URL   string `json:"url"`
	Title string `json:"title"`
}

func GetVideosFromPlaylist(url string) ([]PlaylistVideo, error) {

	// yt-dlp -j prints one JSON object per line, one per playlist entry
	cmd := exec.Command("yt-dlp", "--flat-playlist", "-j", url)

	result, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	split := bytes.Split(result, []byte("\n"))

	videos := make([]PlaylistVideo, 0, len(split))

	for _, line := range split {

		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}

		var video PlaylistVideo
		if err := json.Unmarshal(line, &video); err != nil {
			return nil, err
		}

		videos = append(videos, video)
	}

	return videos, nil
}
