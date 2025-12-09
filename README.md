# Spotify Shortcut

A command-line tool to quickly play Spotify playlists on Spotify Connect devices. Built for automation and quick access to your favorite playlists.

## Features

- Play any playlist by name, ID, or URL
- Target specific Spotify Connect devices
- Optional shuffle mode with random starting track
- List available devices and playlists
- Persistent OAuth token (authenticate once)
- Environment variable and command-line flag configuration

## Prerequisites

- Go 1.21 or later
- A Spotify Premium account (required for playback control)
- Spotify Developer credentials

## Spotify Developer Setup

1. Go to the [Spotify Developer Dashboard](https://developer.spotify.com/dashboard)
2. Create a new application
3. Add `http://127.0.0.1:8888/callback` to the Redirect URIs in your app settings
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
SPOTIFY_REDIRECT_URI=http://127.0.0.1:8888/callback
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

### Debug Mode

```bash
# Show raw API responses
./spotify-shortcut -devices -debug
./spotify-shortcut -playlists -debug
```

## Command-Line Flags

| Flag | Description |
|------|-------------|
| `-playlist` | Playlist name, ID, or URL to play |
| `-device` | Device name or ID to play on |
| `-shuffle` | Enable shuffle mode and start at random track |
| `-devices` | List available Spotify Connect devices and exit |
| `-playlists` | List your Spotify playlists and exit |
| `-debug` | Print raw API responses for debugging |

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

Ensure the redirect URI in your Spotify Developer Dashboard matches exactly: `http://127.0.0.1:8888/callback`

### "Resource not found" for playlist

- Verify the playlist exists and is accessible to your account
- Try using the playlist URL instead of the name
- Use `-playlists` to see your available playlists

### Token expired

The app automatically refreshes tokens, but if authentication fails, delete `.spotify_token.json` and re-authenticate.

## License

Copyright (c) 2025 Cloudmanic Labs, LLC. All rights reserved.
