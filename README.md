# Lofi-Radio-Player

Making Lofi hiphop Radio an actual radio since 2026

# Usage
```
cd mpv-radio
go run main.go
```
takes a seconnd to start, I've not added loading bars yet
if it stops playing randomly, reload and wait for a moment

### Requirments
- golang
- [mpv.io](mpv.io)
- [yt-dlp](https://github.com/yt-dlp/yt-dlp)

## TODO:

### Bugs and debt
- Async handling of mpv traffic
    - continuous intake of mpv signals to track loading times and errors
- Logging
- "next/prev" song command not working, currently replacing entire instance of mpv player

    
