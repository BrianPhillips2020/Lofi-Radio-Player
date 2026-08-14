package mpvplayer

import (
	"bytes"
	"encoding/json"
	"os/exec"
)

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
