# Spotify Shortcut

A Spotify proxy that makes voice assistant integrations simple. Control Spotify playback on any Spotify Connect device via HTTP requests—perfect for Apple Siri Shortcuts, Home Assistant, and other automation tools.

## Why This Exists

Controlling Spotify on third-party speakers through Siri is frustrating. If you have whole-home audio with devices like [WiiM Amps](https://www.amazon.com/dp/B0CGCLXH4H) or other Spotify Connect receivers, you can't easily use Siri to start music on them. HomePods and AirPlay work great with Apple Music, but Spotify users are left with clunky workarounds.

This app solves that problem by acting as a Spotify proxy:

1. **Deploy once** - Run the server on a Raspberry Pi, NAS, or cloud service
2. **Create a Siri Shortcut** - Make a shortcut that sends an HTTP request to this app
3. **Say "Hey Siri, play my playlist"** - Siri triggers the shortcut, which tells Spotify to play on your chosen speaker

Because it's a simple HTTP API, you can use it with any automation platform—not just Siri. Stream Deck buttons, cron jobs, Home Assistant automations, or any system that can make HTTP requests.

## Features

- Play any playlist by name, ID, or URL
- Pause playback
- Target specific Spotify Connect devices
- Optional shuffle mode with random starting track
- List available devices and playlists
- Persistent OAuth token (authenticate once)
- Environment variable and command-line flag configuration
- **HTTP API server mode** for remote control and integrations

## Prerequisites

- Go 1.21 or later
- A Spotify Premium account (required for playback control)
- Spotify Developer credentials

## Spotify Developer Setup

1. Go to the [Spotify Developer Dashboard](https://developer.spotify.com/dashboard)
2. Create a new application
3. Add `http://127.0.0.1:8080/callback` to the Redirect URIs in your app settings
4. Copy your Client ID and Client Secret

## Installation

```bash
# Clone the repository
git clone https://github.com/cloudmanic/spotify-shortcut.git
cd spotify-shortcut

# Install dependencies
go mod tidy

# Build the binary
go build -o spotify-shortcut
```

## Configuration

Copy the sample environment file and add your credentials:

```bash
cp .env.sample .env
```

Edit `.env` with your Spotify credentials:

```bash
# Required
SPOTIFY_CLIENT_ID=your-client-id-here
SPOTIFY_CLIENT_SECRET=your-client-secret-here

# Optional defaults
SPOTIFY_PLAYLIST_ID=your-default-playlist-id
SPOTIFY_DEVICE_NAME=your-default-device-name
SPOTIFY_REDIRECT_URI=http://127.0.0.1:8080/callback

# API Server mode (required for -server flag)
API_ACCESS_TOKEN=your-secret-api-token-here
PORT=8080
```

## Usage

### First Run (Authentication)

On first run, the app will open a URL for Spotify authentication. Visit the URL in your browser and authorize the application. The token is saved locally for future use.

### Play a Playlist

```bash
# Play by playlist name
./spotify-shortcut -playlist "My Favorite Songs"

# Play by playlist ID
./spotify-shortcut -playlist 37i9dQZF1DXcBWIGoYBM5M

# Play by Spotify URL
./spotify-shortcut -playlist "https://open.spotify.com/playlist/37i9dQZF1DXcBWIGoYBM5M"
```

### Shuffle Mode

```bash
# Enable shuffle and start at a random track
./spotify-shortcut -playlist "My Playlist" -shuffle
```

### Target a Specific Device

```bash
# Play on a specific device by name
./spotify-shortcut -playlist "My Playlist" -device "Living Room Speaker"

# Play on a specific device by ID
./spotify-shortcut -playlist "My Playlist" -device "abc123deviceid"
```

### List Available Devices

```bash
./spotify-shortcut -devices
```

### List Your Playlists

```bash
./spotify-shortcut -playlists
```

### Pause Playback

```bash
# Pause music on all devices
./spotify-shortcut -pause
```

### Debug Mode

```bash
# Show raw API responses
./spotify-shortcut -devices -debug
./spotify-shortcut -playlists -debug
```

## Command-Line Flags

| Flag         | Description                                     |
| ------------ | ----------------------------------------------- |
| `-playlist`  | Playlist name, ID, or URL to play               |
| `-device`    | Device name or ID to play on                    |
| `-shuffle`   | Enable shuffle mode and start at random track   |
| `-pause`     | Pause playback on all devices                   |
| `-devices`   | List available Spotify Connect devices and exit |
| `-playlists` | List your Spotify playlists and exit            |
| `-server`    | Start as HTTP API server                        |
| `-debug`     | Print raw API responses for debugging           |

## Examples

```bash
# Play "Chill Vibes" playlist on the kitchen speaker with shuffle
./spotify-shortcut -playlist "Chill Vibes" -device "Kitchen" -shuffle

# List all devices to find the correct name
./spotify-shortcut -devices

# List all playlists to find the correct name
./spotify-shortcut -playlists

# Play using environment variable defaults (set in .env)
./spotify-shortcut
```

## API Server Mode

Run the application as an HTTP API server for remote control and integrations:

```bash
./spotify-shortcut -server
```

The server listens on the port specified by `PORT` (default: 8080).

### Authentication

All API requests require authentication via the `API_ACCESS_TOKEN`. You can provide the token in two ways:

1. **Query parameter**: `?token=your-secret-token`
2. **Authorization header**: `Authorization: Bearer your-secret-token`

### Endpoints

#### GET /api/v1/play

Start playback of a playlist.

**Parameters:**
| Parameter | Required | Description |
|-----------|----------|-------------|
| `token` | Yes* | API access token (*or use Authorization header) |
| `playlist` | Yes | Playlist name, ID, or URL |
| `device` | No | Device name or ID (uses first active device if not specified) |
| `shuffle` | No | `true` or `false` (default: false) |

**Example requests:**

```bash
# Using query parameter for token
curl "http://localhost:8080/api/v1/play?token=your-token&playlist=Chill%20Vibes&shuffle=true"

# Using Authorization header
curl -H "Authorization: Bearer your-token" \
  "http://localhost:8080/api/v1/play?playlist=Chill%20Vibes&device=Kitchen&shuffle=true"
```

**Response:**

```json
{
  "success": true,
  "message": "Now playing \"Chill Vibes\" on Kitchen (shuffle enabled, starting at track 5 of 50)"
}
```

**Error response:**

```json
{
  "success": false,
  "error": "Invalid or missing access token"
}
```

#### GET /api/v1/pause

Pause the current playback.

**Parameters:**
| Parameter | Required | Description |
|-----------|----------|-------------|
| `token` | Yes* | API access token (*or use Authorization header) |

**Example requests:**

```bash
# Using query parameter for token
curl "http://localhost:8080/api/v1/pause?token=your-token"

# Using Authorization header
curl -H "Authorization: Bearer your-token" \
  "http://localhost:8080/api/v1/pause"
```

**Response:**

```json
{
  "success": true,
  "message": "Playback paused"
}
```

## Automation

This tool is designed for automation. Example use cases:

- **Home Assistant**: Trigger playlist playback with automations
- **Cron jobs**: Schedule music at specific times
- **Shell scripts**: Chain with other commands
- **Stream Deck**: Quick playlist buttons

### Example: Morning Alarm Script

```bash
#!/bin/bash
./spotify-shortcut -playlist "Morning Energy" -device "Bedroom Speaker" -shuffle
```

## Troubleshooting

### "No Spotify Connect devices found"

Make sure you have an active Spotify session on at least one device. Open Spotify on your phone, computer, or speaker before running the command.

### "Invalid redirect URI"

Ensure the redirect URI in your Spotify Developer Dashboard matches exactly: `http://127.0.0.1:8080/callback`

### "Resource not found" for playlist

- Verify the playlist exists and is accessible to your account
- Try using the playlist URL instead of the name
- Use `-playlists` to see your available playlists

### Token expired

The app automatically refreshes tokens, but if authentication fails, delete `.spotify_token.json` and re-authenticate.

## License

Copyright (c) 2025 Cloudmanic Labs, LLC. All rights reserved.
