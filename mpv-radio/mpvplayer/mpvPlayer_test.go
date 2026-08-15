package mpvplayer

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"
)

// tempSocketPath returns a short-lived path suitable for a unix socket.
// t.TempDir() embeds the full test name, which can exceed the OS's
// short sun_path limit for unix sockets, so this uses a shorter temp dir.
func tempSocketPath(t *testing.T) string {
	t.Helper()

	dir, err := os.MkdirTemp("", "mpvtest")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })

	return filepath.Join(dir, "s.sock")
}

// verifies NewPlayer method starts the command without error
// and creates the correctly shaped socket path
func TestNewPlayer_builds_new_player(t *testing.T) {
	url := "url"

	player, err := NewPlayer(context.Background(), url)

	if player == nil || err != nil {
		t.Errorf("Player failed to initialize and start: %v", err)
	}

	wantSocketPath := regexp.MustCompile(`.*mpvsocket-\d+.sock`)
	if !wantSocketPath.MatchString(player.socketPath) {
		t.Errorf("Player socket %s does not match expected pattern", player.socketPath)
	}

}

// startFakeSocket starts a unix socket listener standing in for mpv's IPC
// socket, and returns its path plus a channel that receives each JSON
// command written to it.
func startFakeSocket(t *testing.T) (string, <-chan map[string]any) {
	t.Helper()

	socketPath := tempSocketPath(t)
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("failed to start fake socket: %v", err)
	}
	t.Cleanup(func() { listener.Close() })

	commands := make(chan map[string]any, 10)

	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		scanner := bufio.NewScanner(conn)
		for scanner.Scan() {
			var cmd map[string]any
			if err := json.Unmarshal(scanner.Bytes(), &cmd); err == nil {
				commands <- cmd

				// mimic mpv's reply so sendCommand's response read doesn't block
				resp, err := json.Marshal(map[string]any{
					"request_id": cmd["request_id"],
					"error":      "success",
				})
				if err != nil {
					continue
				}
				conn.Write(append(resp, '\n'))
			}
		}
	}()

	return socketPath, commands
}

// waitForCommand reads the next decoded command off the channel, failing
// the test if none arrives in time.
func waitForCommand(t *testing.T, commands <-chan map[string]any) map[string]any {
	t.Helper()

	select {
	case cmd := <-commands:
		return cmd
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for command")
		return nil
	}
}

// assertCommand checks that a decoded IPC command's "command" array matches want.
func assertCommand(t *testing.T, cmd map[string]any, want ...any) {
	t.Helper()

	args, ok := cmd["command"].([]any)
	if !ok || len(args) != len(want) {
		t.Fatalf("unexpected command payload: %v", cmd)
	}
	for i, w := range want {
		if args[i] != w {
			t.Errorf("command arg %d = %v, want %v", i, args[i], w)
		}
	}
}

func TestSendCommand_writesJSONToSocket(t *testing.T) {
	socketPath, commands := startFakeSocket(t)
	player := &MpvPlayer{socketPath: socketPath}

	if _, err := player.sendCommand("set-property", "pause", true); err != nil {
		t.Fatalf("sendCommand returned error: %v", err)
	}

	cmd := waitForCommand(t, commands)
	assertCommand(t, cmd, "set-property", "pause", true)
}

func TestSendCommand_connectionError(t *testing.T) {
	player := &MpvPlayer{socketPath: tempSocketPath(t)}

	if _, err := player.sendCommand("quit"); err == nil {
		t.Fatal("expected error connecting to nonexistent socket, got nil")
	}
}

func TestPause_sendsPauseTrue(t *testing.T) {
	socketPath, commands := startFakeSocket(t)
	player := &MpvPlayer{socketPath: socketPath}

	if _, err := player.Pause(); err != nil {
		t.Fatalf("pause returned error: %v", err)
	}

	cmd := waitForCommand(t, commands)
	assertCommand(t, cmd, "set-property", "pause", true)
}

func TestResume_sendsPauseFalse(t *testing.T) {
	socketPath, commands := startFakeSocket(t)
	player := &MpvPlayer{socketPath: socketPath}

	if _, err := player.Resume(); err != nil {
		t.Fatalf("resume returned error: %v", err)
	}

	cmd := waitForCommand(t, commands)
	assertCommand(t, cmd, "set-property", "pause", false)
}

func TestConnectWithRetry_succeedsWhenSocketExists(t *testing.T) {
	socketPath, _ := startFakeSocket(t)

	conn, err := connectWithRetry(socketPath, 3)
	if err != nil {
		t.Fatalf("connectWithRetry returned error: %v", err)
	}
	conn.Close()
}

func TestConnectWithRetry_failsWhenSocketNeverAppears(t *testing.T) {
	socketPath := tempSocketPath(t)

	if _, err := connectWithRetry(socketPath, 2); err == nil {
		t.Fatal("expected error when socket never appears, got nil")
	}
}

func TestConnectWithRetry_succeedsAfterSocketAppearsLate(t *testing.T) {
	socketPath := tempSocketPath(t)

	go func() {
		time.Sleep(75 * time.Millisecond)
		listener, err := net.Listen("unix", socketPath)
		if err != nil {
			return
		}
		defer listener.Close()
		conn, err := listener.Accept()
		if err == nil {
			conn.Close()
		}
	}()

	conn, err := connectWithRetry(socketPath, 5)
	if err != nil {
		t.Fatalf("connectWithRetry returned error: %v", err)
	}
	conn.Close()
}
