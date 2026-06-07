# podcast-player-cli

Terminal podcast player with cover art, playback resume, and optional sync via a private GitHub Gist.

## Features

- Browse podcast feeds in a terminal UI
- Play episodes with `ffplay`
- Render podcast cover art in the terminal with `chafa`
- Resume from the last saved playback position
- Cache feeds, cover art, durations, and rendered art locally
- Sync config and play state across machines with GitHub Gists
- Install as a Go binary or through npm-distributed platform binaries

## Requirements

- Go 1.25+ if building from source
- `ffplay` and `ffprobe` from `ffmpeg`
- `chafa` for terminal cover-art rendering
- `gh` if you want to use `sync`

If `ffplay` is missing, playback will fail. If `chafa` is missing, the app still runs, but cover art will not render.

## Install

### npm

```bash
npm install -g podcast-player-cli
```

This package ships prebuilt binaries for:

- macOS `arm64`
- macOS `amd64`
- Linux `arm64`
- Linux `amd64`

Node.js 18+ is required for the npm launcher.

### Go

```bash
go build -o podcast-player-cli .
```

## Run

Pass feed URLs directly:

```bash
podcast-player-cli https://example.com/feed.xml https://example.com/another.xml
```

Or keep your subscriptions in the config file and just run:

```bash
podcast-player-cli
```

You can also route feed and cover-art requests through a proxy:

```bash
podcast-player-cli --proxy http://127.0.0.1:7890
```

## Config

On first launch the app creates:

- `~/.podcast-player-cli/config.json`
- `~/.podcast-player-cli/play-state.json`
- `~/.podcast-player-cli/sync-state.json`
- `~/.podcast-player-cli/cache/`

Example config:

```json
{
  "feed_urls": [
    "https://example.com/feed.xml",
    "https://example.com/another.xml"
  ],
  "proxy_url": "http://127.0.0.1:7890",
  "sync": {
    "gist_id": "YOUR_GIST_ID"
  }
}
```

Notes:

- CLI feed URLs override `feed_urls` for that run
- `--proxy` overrides `proxy_url`
- playback state is saved automatically while listening

## Controls

- `j` / `k` or arrow keys: move selection
- `enter`: open a subscription or play the selected episode
- `l`: move deeper into episodes or episode details
- `h` / `backspace`: move back
- `p`: play selected episode
- `space`: pause or resume playback
- `z`: toggle zen mode
- `r`: refresh feeds
- `g` / `G`: jump to top or bottom
- `esc`: back or exit zen mode
- `ctrl+c`: quit

## Sync

Sync stores `config.json`, `play-state.json`, and a small manifest in a private GitHub Gist.

Before using sync:

- install GitHub CLI `gh`
- run `gh auth login`

Initialize sync:

```bash
podcast-player-cli sync init
```

Pull from the configured gist:

```bash
podcast-player-cli sync pull
```

Push local state to the configured gist:

```bash
podcast-player-cli sync push
```

Override the gist ID for a single command:

```bash
podcast-player-cli sync pull --gist <gist-id>
podcast-player-cli sync push --gist <gist-id>
```

## Development

Run tests:

```bash
go test ./...
```

Build locally:

```bash
go build ./...
```
