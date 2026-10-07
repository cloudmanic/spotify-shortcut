# Spotify Shortcut

An HTTP API that makes Spotify Connect speakers controllable from anything that can hit a URL — Siri Shortcuts, Stream Deck, cron, Home Assistant, your terminal, etc.

## Why This Exists

If you have whole-home audio with Spotify Connect speakers (WiiM amps, Sonos, etc.), Siri can't drive them directly. This server proxies playback control through Spotify's API, plus does the dirty work of **claiming back speakers that other household members have re-linked to their accounts** — without anyone needing to open the Spotify app.

## How It Works

1. The server runs on a Mac on your home LAN (e.g. a spare mini, in our case named `stowe`).
2. Clients hit `http://stowe:8080/api/v1/...` with a shared bearer token.
3. For control endpoints (play / pause / volume), the server calls Spotify's Web API.
4. For wake / auto-claim, the server uses **mDNS** to discover speakers on the LAN and the **Spotify Connect zeroconf addUser flow** to push our access token onto a target device — claiming it for our Spotify account regardless of who used it last.
5. For `/api/v1/ask`, the server sends the spoken sentence to [Jev](https://docs.typesafe.ai/introduction), a fast decision model from TypeSafe, to work out what to do, then acts and returns a sentence to read back.

## Features

- **Discover and claim Spotify Connect speakers on the LAN** — even ones currently linked to a different household member's account.
- **Auto-claim during play** — `/api/v1/play?device=Pool+Speakers` claims the device first if it isn't already linked, then plays.
- **List all speakers visible on the LAN** — beyond just what Spotify cloud reports.
- **List, play, pause, volume control** — the basics, with simple JSON responses.
- **Plain-English requests** — `/api/v1/ask?text=play green day in the master bedroom` plays an artist, album, song, genre or playlist, stops music everywhere, pauses, skips, changes volume, and says what's playing where.
- **Speaker names, not hex IDs** — each speaker reports its Spotify device ID, so the server shows "Pool Speakers" where Spotify shows `3b116b85…`.
- **Persistent OAuth token** — authenticate once, refresh automatically.
- **CLI mode and HTTP server mode** — same binary.

## Prerequisites

- Go 1.24+
- Spotify Premium (volume control + playback transfer require Premium)
- Spotify Developer credentials ([dashboard](https://developer.spotify.com/dashboard))

## Spotify App Setup

1. Create an app at the Spotify Developer Dashboard.
2. Add `http://127.0.0.1:8080/callback` to the **Redirect URIs**.
3. Copy your Client ID and Client Secret into `.env`.

## Installation

```bash
git clone https://github.com/cloudmanic/spotify-shortcut.git
cd spotify-shortcut
go mod tidy
go build -o spotify-shortcut .
```

## Configuration

```bash
cp .env.sample .env
```

Edit `.env`:

```bash
SPOTIFY_CLIENT_ID=...
SPOTIFY_CLIENT_SECRET=...
SPOTIFY_REDIRECT_URI=http://127.0.0.1:8080/callback
SPOTIFY_TOKEN_FILE=.spotify_token.json

# Required for server mode — generate via `openssl rand -hex 32`
API_ACCESS_TOKEN=...

# Required for /api/v1/ask and -ask — from the TypeSafe console (1Password: "typesafe.ai API Key (Jev)")
TYPESAFE_API_KEY=...

# Optional
SPOTIFY_PLAYLIST_ID=...
SPOTIFY_DEVICE_NAME=...
PORT=8080
TYPESAFE_MODEL=jev-1.13.0
```

OAuth scopes the app requests:

- `user-read-playback-state`, `user-modify-playback-state`, `user-read-currently-playing`
- `playlist-read-private`, `playlist-read-collaborative`
- `streaming`, `user-read-email`, `user-read-private` — required by the Spotify Connect eSDK on third-party speakers when we push our access token via zeroconf

## First Run / Authentication

CLI mode triggers OAuth automatically:

```bash
./spotify-shortcut -devices
```

A browser window opens to Spotify's consent page. After you approve, the token is saved to `.spotify_token.json` and reused on subsequent runs.

Server mode tries to load an existing token; if missing or invalid, it tells you to visit `/auth?token=<API_ACCESS_TOKEN>`.

## CLI Mode

| Flag | Description |
|------|-------------|
| `-playlist <name\|id\|url>` | Playlist to play |
| `-device <name\|id>` | Speaker to play on |
| `-shuffle` | Shuffle, starting at a random track |
| `-pause` | Pause all playback |
| `-devices` | List available Spotify Connect devices |
| `-playlists` | List your playlists |
| `-server` | Start the HTTP API server |
| `-ask "<request>"` | Run one plain-English request, e.g. `-ask "what's playing?"` |
| `-dry-run` | With `-ask`: decide what to do but change nothing |
| `-debug` | Print raw API responses |

## Server Mode

```bash
./spotify-shortcut -server
```

Serves on `:$PORT` (default 8080). All endpoints accept the API access token as a query param `?token=...` or `Authorization: Bearer ...` header.

### Endpoints

| Method & Path | Description |
|---|---|
| `GET /api/v1/play?device=&playlist=&shuffle=` | Start playback. Auto-claims the named device via zeroconf if it isn't already linked to your account. `playlist` accepts a name, ID, or URL. |
| `GET /api/v1/pause` | Pause current playback. |
| `GET /api/v1/volume?level=0-100&device=<optional>` | Set volume (Premium-only). Targets active device if `device` not given. |
| `GET /api/v1/devices` | Spotify Connect devices currently linked to your account (cloud-side). |
| `GET /api/v1/lan-devices` | Every Spotify Connect device discovered on the LAN via mDNS — including ones linked to other accounts. Use this to find the names you can pass to `/wake`. |
| `GET /api/v1/wake?device=<name>` | Discover the named device via mDNS and run the zeroconf `addUser` handshake to claim it for your Spotify account. Idempotent. |
| `GET /api/v1/playlists` | List every playlist owned/followed by the authenticated user. Server paginates. |
| `GET\|POST /api/v1/ask?text=<request>&dry_run=<true\|false>` | Do whatever a plain-English request asks (see below). POST takes `{"text": "...", "dry_run": false}`. |
| `GET /auth?token=<API_ACCESS_TOKEN>` | Kick off the OAuth flow (use after first deploy or whenever the token is invalidated). |

### Response shape

Most endpoints return `APIResponse`:

```json
{ "success": true, "message": "...", "error": "..." }
```

`/devices`, `/lan-devices`, and `/playlists` extend this with a typed list under `devices` or `playlists`.

### `/api/v1/ask`

Send a sentence; get back a sentence to read aloud and whether anything was done:

```json
{ "action_taken": true, "message": "Playing Green Day on Master Bedroom Speakers.", "intent": "play" }
```

`action_taken` is `false` when nothing was done — for example "I can't play Less Than Jake because you didn't say which speaker.", "Did you mean Pool Speakers or Pool Porch Speakers?", or "Nothing is playing right now." With `dry_run=true` it decides everything (including which Spotify item it would play) but changes nothing, and the message starts with `Dry run:`.

What it understands:

| Ask | What happens |
|---|---|
| "Play my coding mix on the living room speakers" | Plays your playlist from track one. Say "shuffle" to shuffle. |
| "Play Green Day in the master bedroom" | Plays the artist. Albums, songs ("the Goldfinger version of 99 Red Balloons"), "the latest … album" and genres ("some jazz") work too. |
| "Kill all music" / "Stop all the music" | Pauses every speaker that's playing, whichever account or app started it. |
| "Pause the music" / "Resume" / "Skip this song" | Acts on the speaker that's playing; name a speaker if several are. |
| "Turn it up" / "Turn down the pool speakers" / "Volume 40 in the living room" | Moves volume by 10, or sets a number. |
| "What's playing right now?" | Lists each speaker that's playing and the song. |
| "What are my playlists?" / "What are my possible speakers?" | Lists them. |

How it decides: one Jev call asks every question at once (what to do, which speaker, what kind of music, which words are the artist or song). Jev can only pick from options it is given, so the server offers it every short run of words from the sentence to pick names from, and a second Jev call picks the right Spotify search result. Dates (newest album) and numbers (volume) are handled in code. When Jev isn't sure — say, between two similar speaker names — the server asks instead of guessing.

Stop, pause, skip, volume and "what's playing" talk to WiiM speakers directly over their local HTTPS API, because Spotify's API only sees playback on our own account. Known speakers are saved to `.speakers.json` and re-checked at their last address each minute, so one missed mDNS scan doesn't lose them.

### Examples

```bash
# Set up shorthand (assuming ~/.config/spotify-shortcut.json — see "Client config" below)
URL=$(jq -r .server_url ~/.config/spotify-shortcut.json)
TOK=$(jq -r .api_access_token ~/.config/spotify-shortcut.json)

# What's on the LAN?
curl -s "$URL/api/v1/lan-devices?token=$TOK" | jq '.devices[].name'

# Wake the bedroom speakers (they were linked to someone else's account)
curl -s "$URL/api/v1/wake?token=$TOK&device=Master+Bedroom+Speakers" | jq

# Play a playlist
curl -s "$URL/api/v1/play?token=$TOK&device=Living+Room+Speakers&playlist=Uplifting+Pop" | jq

# Adjust volume
curl -s "$URL/api/v1/volume?token=$TOK&level=40&device=Living+Room+Speakers" | jq

# Stop everything
curl -s "$URL/api/v1/pause?token=$TOK" | jq

# Say it in plain English
curl -s -G "$URL/api/v1/ask" --data-urlencode "token=$TOK" --data-urlencode "text=play green day in the master bedroom" | jq
```

## Client config (`~/.config/spotify-shortcut.json`)

A small JSON file used by clients (curl shortcuts, the iOS Shortcut, etc.) so they don't have to hard-code the server URL or token:

```json
{
  "api_access_token": "<same as API_ACCESS_TOKEN in .env>",
  "server_url": "http://stowe:8080",
  "speakers": [
    "Living Room Speakers",
    "Master Bedroom Speakers",
    "Pool Porch Speakers",
    "Pool Speakers",
    "House Outdoor Speakers"
  ]
}
```

The server itself does **not** read this file — it's a pure client convenience. The `speakers` list is just a curated list of friendly names for use in shortcut UIs.

## Deployment

`scripts/deploy.sh` builds for `darwin/arm64`, ships the binary plus `.env` (and `.spotify_token.json` if present) to `spicer@stowe`, installs a launchd plist that auto-starts on reboot, and verifies it's running.

```bash
./scripts/deploy.sh
```

The launchd plist is written to `~deploy/Library/LaunchAgents/com.cloudmanic.spotify-shortcut.plist` and the binary lives at `~deploy/spotify-shortcut/`. Logs go to `~deploy/spotify-shortcut/server.{log,err}`.

### One-time: macOS Sequoia Local Network permission

macOS 15+ (Sequoia) blocks LAN multicast and unicast-to-LAN-IPs from launchd-managed processes that haven't been granted **Local Network** permission. Without it, `/api/v1/lan-devices` and `/api/v1/wake` will silently return zero results or "no route to host."

Workarounds in this codebase:

- **Discovery (`/lan-devices`)** uses the system `dns-sd` tool on darwin — it talks to mDNSResponder over a Unix socket and is unaffected by the TCC gate.
- **Outbound HTTP to LAN IPs (`/wake`)** still requires the permission. There's no Unix-socket workaround.

To grant it:

1. Sign in to the deploy machine via Screen Sharing/VNC (or attach a monitor).
2. Open Terminal and run `~/spotify-shortcut/spotify-shortcut -server`.
3. macOS pops "spotify-shortcut would like to find devices on your local network" — click **Allow**.
4. `Ctrl-C` to stop the foreground server.
5. The launchd-managed copy now has permission too. Restart it:
   ```bash
   launchctl bootout gui/$(id -u) ~/Library/LaunchAgents/com.cloudmanic.spotify-shortcut.plist
   launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/com.cloudmanic.spotify-shortcut.plist
   ```

`scripts/deploy.sh` signs the binary with the Cloudmanic Developer ID (identifier `com.cloudmanic.spotify-shortcut`). macOS ties the permission to that signature, so it survives redeploys. An unsigned build gets a new signature every time and loses the permission.

If the prompt doesn't appear, open **System Settings → Privacy & Security → Local Network** on the deploy machine and turn on `spotify-shortcut`.

## Development

```bash
go test -v ./...   # full test suite
go build ./...     # type/compile check
go run . -server   # run locally on :8080
```

## Troubleshooting

**`/api/v1/lan-devices` returns 0 devices in launchd context** → Local Network permission (see above).

**`/api/v1/wake` returns "no route to host"** → Same root cause as above. The launchd-managed process can't reach `192.168.x.x`.

**`/api/v1/play` returns "Restriction violated"** → Usually means there's no active Spotify session yet. Either nothing is playing anywhere, or the target device just got claimed and hasn't fully established a session. Hit it again, or play to an already-active device first to bootstrap.

**Newly-claimed device shows up with a hex ID instead of friendly name** → Cosmetic. Spotify cloud doesn't know the friendly name until the device completes its first playback session under your account. Both `/wake` and `/play` accept the hex ID, so functionality is unaffected.

**Token expired / invalid** → Delete `.spotify_token.json` on the deploy host and re-run the OAuth flow via `/auth?token=...`.

## License

Copyright (c) 2026 Cloudmanic Labs, LLC. All rights reserved.
