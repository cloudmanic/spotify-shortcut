# Spotify Shortcut

An HTTP API that makes Spotify Connect speakers controllable from anything that can hit a URL — Siri Shortcuts, Stream Deck, cron, Home Assistant, your terminal, etc.

## Why This Exists

If you have whole-home audio with Spotify Connect speakers (WiiM amps, Sonos, etc.), Siri can't drive them directly. This server proxies playback control through Spotify's API, plus does the dirty work of **claiming back speakers that other household members have re-linked to their accounts** — without anyone needing to open the Spotify app.

## How It Works

1. The server runs on a Mac on your home LAN (e.g. a spare mini, in our case named `stowe`).
2. Clients hit `http://stowe:8080/api/v1/...` with a shared bearer token.
3. For control endpoints (play / pause / volume), the server calls Spotify's Web API.
4. For wake / auto-claim, the server uses **mDNS** to discover speakers on the LAN and the **Spotify Connect zeroconf addUser flow** to push our access token onto a target device — claiming it for our Spotify account regardless of who used it last.

## Features

- **Discover and claim Spotify Connect speakers on the LAN** — even ones currently linked to a different household member's account.
- **Auto-claim during play** — `/api/v1/play?device=Pool+Speakers` claims the device first if it isn't already linked, then plays.
- **List all speakers visible on the LAN** — beyond just what Spotify cloud reports.
- **List, play, pause, volume control** — the basics, with simple JSON responses.
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

# Optional
SPOTIFY_PLAYLIST_ID=...
SPOTIFY_DEVICE_NAME=...
PORT=8080
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
| `GET /auth?token=<API_ACCESS_TOKEN>` | Kick off the OAuth flow (use after first deploy or whenever the token is invalidated). |

### Response shape

Most endpoints return `APIResponse`:

```json
{ "success": true, "message": "...", "error": "..." }
```

`/devices`, `/lan-devices`, and `/playlists` extend this with a typed list under `devices` or `playlists`.

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

`scripts/deploy.sh` builds for `darwin/arm64`, ships the binary plus `.env` (and `.spotify_token.json` if present) to `deploy@stowe`, installs a launchd plist that auto-starts on reboot, and verifies it's running.

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

The permission persists per binary path. You only need to do this once unless the binary location changes.

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
