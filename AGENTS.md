# Project Instructions

## File Headers

Every source file must include the following header at the top:

```
//
// Date: YYYY-MM-DD
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Copyright (c) YYYY Cloudmanic Labs, LLC. All rights reserved.
//
// Description: [Brief description of what this file does]
//
```

Replace `YYYY-MM-DD` with the current date and `YYYY` with the current year.

## Code Style

- Put detailed comments above every function (public and private)
- One test file per source file (the project uses a single `spotify_test.go` for the whole package)
- Imports should be grouped in this order, separated by blank lines:
  1. Standard library
  2. Third-party packages
  3. Aliased imports

## Testing

After every code change, run the test suite:

```bash
go test -v ./...
```

All tests must pass before committing.

## Architecture

- `main.go` — entry point, flag parsing, dispatches to CLI or server mode
- `spotify/` — package containing all logic
  - `auth.go`, `config.go` — OAuth + global state
  - `server.go` — HTTP handlers and routing
  - `player.go` — `PlayPlaylist`, `PausePlayback`, `SetVolume`, `ListDevices`
  - `playlist.go` — playlist resolution and listing
  - `device.go` — CLI device table rendering
  - `discovery.go` — mDNS device discovery + caching, with platform-agnostic types
  - `discovery_darwin.go` — darwin-specific discoverer that shells out to `dns-sd`
  - `zeroconf.go` — Spotify Connect zeroconf protocol client (getInfo + addUser)
  - `claim.go` — high-level "claim a device for our account" orchestration
  - `types.go` — shared types and the `Client` interface used for mocking
  - `ask.go` — `/api/v1/ask` text-to-action: Jev questions, decisions, reply sentences
  - `jev.go` — TypeSafe Jev client (choice and yes/no questions)
  - `catalog.go` — Spotify search, Jev picking the right result, newest release, play on a named speaker
  - `control.go` — what's playing where, stop everywhere, pause/resume/skip/volume
  - `speakers.go` — speaker directory: friendly name ↔ Spotify device ID ↔ LAN IP, saved to `.speakers.json`
  - `wiim.go` — WiiM (LinkPlay) local HTTPS API client
- `scripts/deploy.sh` — builds and deploys to `spicer@stowe`

## Deployment

This app deploys to a local Mac on the LAN (`stowe`), not Fly.io or any cloud provider. The deploy script (`scripts/deploy.sh`) cross-compiles for `darwin/arm64`, ships the binary + config, installs a launchd plist that auto-starts on reboot.

There is no automated CI/CD deploy. CI runs tests only.

## macOS Sequoia Local Network gotcha

When running under launchd on macOS 15+ (Sequoia), the binary needs **Local Network** privacy permission, granted via System Settings GUI on the host. Without it:

- mDNS multicast bind silently returns zero results
- Outbound TCP to `192.168.x.x` returns "no route to host"

Workarounds already in code:

- `discovery_darwin.go` shells out to the system `dns-sd` tool, which talks to `mDNSResponder` over a Unix socket — bypasses the TCC gate.
- Direct HTTP to LAN IPs (used by `/api/v1/wake`) still needs the permission. There's no Unix-socket workaround.

If a fresh deploy can't reach speakers, it's almost always this. README documents the manual grant procedure.

## Spotify Connect zeroconf claim flow

The `/api/v1/wake` and auto-claim path use the Spotify Connect zeroconf `addUser` protocol. The implementation in `zeroconf.go` handles two payload modes determined by the device's `getInfo` response:

- `tokenType=accesstoken` (modern eSDK, e.g. WiiM): blob is the raw OAuth access token in plaintext, `clientKey` is empty, plus an extra `tokenType=accesstoken` form field. **No DH/AES envelope.**
- anything else (legacy): blob is wrapped in DH (768-bit MODP) + AES-128-CTR + HMAC-SHA1, exactly as librespot does.

If you're debugging a "device returns status 101 OK but doesn't actually log in" issue, it's almost certainly the OAuth token scope. The eSDK requires `streaming`, `user-read-email`, and `user-read-private` to log in — the basic playback-control scopes are not enough. Adding scopes requires the user to re-do the OAuth consent flow.

## Multi-account households

Multiple Spotify accounts in the same house cause speakers to get re-linked frequently — whoever played last "owns" the speaker. The auto-claim path in `/api/v1/play` handles this transparently: if the named device isn't in our cloud devices list, it runs the zeroconf claim before transferring playback. Single OAuth blob in `.env`/`.spotify_token.json` is enough; only the user running this app needs to re-claim — other family members continue to use their phones normally.

## /api/v1/ask and Jev

`/api/v1/ask` turns a sentence into an action using TypeSafe's Jev model (`TYPESAFE_API_KEY`). Jev only answers typed questions (pick one option, or yes/no) and cannot write text, so:

- Names (artist, song, album, genre) are picked from every 1–6 word run of the sentence.
- Spotify search results are matched by a second Jev choice with a NONE option.
- Dates and numbers stay in code.
- Thresholds in `ask.go` are tuned against the pinned model `jev-1.13.0`. Re-check them with real sentences (`-ask "..." -dry-run`) before moving to a newer model.

Spotify's API only sees our own account's playback, so stop/pause/skip/volume/now-playing use each WiiM speaker's local API (`wiim.go`) for anything not on our session.

## Friendly-name vs hex-ID quirk

Newly-claimed devices appear in `/me/player/devices` (and our `/api/v1/devices`) with their raw hex `deviceID` instead of their friendly name until they actually complete a first playback session. Spotify cloud caches the friendly name only after the eSDK reports it post-session. Each speaker's zeroconf `getInfo` reports the same hex ID, so the speaker directory (`speakers.go`) maps it back to the friendly name for `/devices`, `/play`, `/volume` and `/ask`. Both `/wake` and `/play` accept either name or ID.
