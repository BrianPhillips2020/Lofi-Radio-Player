package mpvplayer

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
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

	// mpv's stdout, piped so it can be scanned for specific messages
	stdout io.Reader
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

	stdout, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}

	if err := cmd.Start(); err != nil {
		return nil, err
	}

	player := &MpvPlayer{cmd: cmd, socketPath: socketPath, done: make(chan error), url: url, stdout: stdout}

	go func() {
		player.done <- cmd.Wait()
		os.Remove(socketPath)
	}()

	return player, nil
}

// Sends controls to mpv goroutine through json ipc
// Currently this is blocking, Only 1 command will be recieved and response read when it is sent
// TODO: make this method non-blocking
func (p *MpvPlayer) sendCommand(args ...any) error {

	//Get a new connection for this command
	conn, err := connectWithRetry(p.socketPath, 10)

	if err != nil {
		return fmt.Errorf("Error connecting to mpv socket: %w", err)
	}

	//defer connection close until after the method exits
	defer conn.Close()

	// bail out instead of blocking forever if mpv never responds
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		return err
	}

	// generate random request_id. Could track currently active requests but fucking whatever, what are the odds
	// I say that but the odds are 1 : 900 so if I run this long enough there will DEF be at least 1 collission lol
	reqId := rand.IntN(900) + 100

	payload, err := json.Marshal(map[string]any{"command": args, "request_id": reqId})

	if err != nil {
		return err
	}

	payload = append(payload, '\n')

	//write the command to the socket
	if _, err := conn.Write(payload); err != nil {
		return err
	}

	return nil
}

// Opens a connection and checks for the `playback-restart` message
// Which should signal that the audio playback has started
func (p *MpvPlayer) HasPlaybackBegun(ctx context.Context) error {

	conn, err := connectWithRetry(p.socketPath, 10)

	if err != nil {
		return err
	}

	defer conn.Close()

	reader := bufio.NewReader(conn)

	for {

		if err := ctx.Err(); err != nil {
			return err
		}

		conn.SetReadDeadline(time.Now().Add(10 * time.Second))

		line, err := reader.ReadString('\n')
		if err != nil {
			return fmt.Errorf("error reading mpv response: %w", err)
		}

		// mpv.io json response struct
		var evt struct {
			Event string `json:"event"`
		}

		if err := json.Unmarshal([]byte(line), &evt); err != nil {
			continue
		}

		if evt.Event == "playback-restart" {
			return nil
		}
	}
}

// Intended to wait for the `endFile` command to force reload, however this doesn't seem to work. I think I'm gonna have to read from the stdout
func (p *MpvPlayer) WaitForPlaybackStopped(ctx context.Context) error {
	conn, err := connectWithRetry(p.socketPath, 10)

	if err != nil {
		return err
	}

	defer conn.Close()

	var evt struct {
		Event string `json:"event"`
		ID    int    `json:"id"`
		Name  string `json:"name"`
		Data  bool   `json:"data"`
	}

	reader := bufio.NewReader(conn)
	payload, _ := json.Marshal(map[string]any{
		"command":    []any{"observe_property", 1, "eof-reached"},
		"request_id": 1,
	})

	conn.Write(append(payload, '\n'))

	for {

		if err := ctx.Err(); err != nil {
			return err
		}

		conn.SetReadDeadline(time.Now().Add(10 * time.Second))

		line, err := reader.ReadString('\n')
		if err != nil {
			return fmt.Errorf("error reading mpv response: %w", err)
		}

		if err := json.Unmarshal([]byte(line), &evt); err != nil {
			continue
		}

		if evt.Event == "end-file" {
			return nil
		}
	}
}

// WatchStdout scans mpv's stdout line by line until it finds a line
// containing match, returning nil as soon as it's seen. Returns an error if
// ctx is canceled, or if mpv's stdout closes (e.g. the process exits) before
// a match is found.
func (p *MpvPlayer) WatchStdout(ctx context.Context, match string) error {

	debugLog, err := os.OpenFile("mpv-debug.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("error opening debug log: %w", err)
	}
	defer debugLog.Close()

	scanner := bufio.NewScanner(p.stdout)

	for scanner.Scan() {

		if err := ctx.Err(); err != nil {
			return err
		}

		line := scanner.Text()
		fmt.Fprintln(debugLog, line)

		if strings.Contains(line, match) {
			return nil
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("error reading mpv stdout: %w", err)
	}

	return fmt.Errorf("mpv stdout closed before %q was seen", match)
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

func (p *MpvPlayer) Pause() error {
	return p.sendCommand("set-property", "pause", true)
}
func (p *MpvPlayer) Resume() error      { return p.sendCommand("set-property", "pause", false) }
func (p *MpvPlayer) TogglePause() error { return p.sendCommand("cycle", "pause") }

func (p *MpvPlayer) VolumeUp() error   { return p.sendCommand("add", "volume", 5) }
func (p *MpvPlayer) VolumeDown() error { return p.sendCommand("add", "volume", -5) }

func (p *MpvPlayer) Quit() error { return p.sendCommand("quit") }

func (p *MpvPlayer) NextSong() error { return p.sendCommand("playlist-next") }
func (p *MpvPlayer) PrevSong() error { return p.sendCommand("playlist-prev") }

// Done returns a channel that receives mpv's exit error (nil on clean exit)
// once the process ends, whether from Quit, an external kill, or finishing
// playback on its own.
func (p *MpvPlayer) Done() <-chan error { return p.done }

// SocketPath returns the path to mpv's IPC socket.
func (p *MpvPlayer) SocketPath() string { return p.socketPath }

// DialWithRetry opens a new connection to mpv's IPC socket, retrying
// maxRetries times (50ms apart) since the socket file may not exist yet
// immediately after the mpv process is started.
func (p *MpvPlayer) DialWithRetry(maxRetries int) (net.Conn, error) {
	return connectWithRetry(p.socketPath, maxRetries)
}

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
