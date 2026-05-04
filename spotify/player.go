//
// Date: 2025-12-15
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Copyright (c) 2025 Cloudmanic Labs, LLC. All rights reserved.
//
// Description: Playback control functions for Spotify.
//

package spotify

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"time"

	spotifyLib "github.com/zmb3/spotify/v2"
)

// PlayPlaylist starts playback of a playlist on the specified device.
// This function is used by both CLI and API server modes.
func PlayPlaylist(deviceName, playlistInput string, shuffle bool) (string, error) {
	if spotifyClient == nil {
		return "", fmt.Errorf("Spotify not authenticated. Visit /auth to authenticate")
	}

	ctx := context.Background()

	// Get available devices
	devices, err := spotifyClient.PlayerDevices(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get devices: %w", err)
	}

	// Find the target device in the existing cloud list.
	var targetDevice *spotifyLib.PlayerDevice
	for i, device := range devices {
		if deviceName != "" && (device.Name == deviceName || string(device.ID) == deviceName) {
			targetDevice = &devices[i]
			break
		}
	}

	// If a specific device was requested but isn't in the cloud list, try
	// to claim it via zeroconf. This is the multi-account-household path:
	// another user previously linked this speaker to their account and we
	// need to take it back.
	if targetDevice == nil && deviceName != "" {
		log.Printf("device %q not in Spotify cloud list, attempting zeroconf claim", deviceName)
		claim, claimErr := ClaimDevice(ctx, deviceName)
		if claimErr != nil {
			return "", fmt.Errorf("device %q not available and zeroconf claim failed: %w", deviceName, claimErr)
		}
		log.Printf("claimed %q -> deviceID=%s", deviceName, claim.DeviceID)

		// Re-fetch devices and find the now-registered one.
		devices, err = spotifyClient.PlayerDevices(ctx)
		if err != nil {
			return "", fmt.Errorf("failed to refresh devices after claim: %w", err)
		}
		for i, device := range devices {
			if string(device.ID) == claim.DeviceID {
				targetDevice = &devices[i]
				break
			}
		}
	}

	if targetDevice == nil && len(devices) == 0 {
		return "", fmt.Errorf("no Spotify Connect devices found")
	}

	// If no device specified or still not found, fall back to first active or first device.
	if targetDevice == nil {
		for i, device := range devices {
			if device.Active {
				targetDevice = &devices[i]
				break
			}
		}
		if targetDevice == nil {
			targetDevice = &devices[0]
		}
	}

	// Resolve playlist
	playlistID, err := ResolvePlaylistIDQuiet(ctx, spotifyClient, playlistInput)
	if err != nil {
		return "", fmt.Errorf("failed to resolve playlist: %w", err)
	}

	// Get playlist info
	playlist, err := spotifyClient.GetPlaylist(ctx, spotifyLib.ID(playlistID))
	if err != nil {
		return "", fmt.Errorf("failed to get playlist: %w", err)
	}

	trackCount := int(playlist.Tracks.Total)
	playlistURI := spotifyLib.URI("spotify:playlist:" + playlistID)

	// Build play options
	opts := &spotifyLib.PlayOptions{
		DeviceID:        &targetDevice.ID,
		PlaybackContext: &playlistURI,
	}

	if shuffle {
		// Pick random starting track
		randomOffset := rand.Intn(trackCount)
		opts.PlaybackOffset = &spotifyLib.PlaybackOffset{Position: &randomOffset}

		err = spotifyClient.PlayOpt(ctx, opts)
		if err != nil {
			return "", fmt.Errorf("failed to start playback: %w", err)
		}

		// Wait for playback to initialize before setting shuffle
		time.Sleep(500 * time.Millisecond)

		// Enable shuffle mode
		err = spotifyClient.Shuffle(ctx, true)
		if err != nil {
			log.Printf("Warning: Failed to enable shuffle: %v", err)
		}

		return fmt.Sprintf("Now playing \"%s\" on %s (shuffle enabled, starting at track %d of %d)",
			playlist.Name, targetDevice.Name, randomOffset+1, trackCount), nil
	}

	// Start from track 1
	startPosition := 0
	opts.PlaybackOffset = &spotifyLib.PlaybackOffset{Position: &startPosition}

	err = spotifyClient.PlayOpt(ctx, opts)
	if err != nil {
		return "", fmt.Errorf("failed to start playback: %w", err)
	}

	return fmt.Sprintf("Now playing \"%s\" on %s (starting at track 1)", playlist.Name, targetDevice.Name), nil
}

// ListDevices returns the list of available Spotify Connect devices for the
// authenticated user. Used by the API server to expose device discovery to
// clients (e.g., the iOS Shortcut) so they can pick a target before calling
// /api/v1/play.
func ListDevices() ([]spotifyLib.PlayerDevice, error) {
	if spotifyClient == nil {
		return nil, fmt.Errorf("Spotify not authenticated. Visit /auth to authenticate")
	}

	ctx := context.Background()

	devices, err := spotifyClient.PlayerDevices(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get devices: %w", err)
	}

	return devices, nil
}

// SetVolume sets the playback volume on a Spotify Connect device. If
// `deviceName` is empty, the current active device is targeted. If a name
// is supplied, we resolve it against the cloud devices list and pass the
// ID to Spotify's volume API. `percent` must be 0-100.
//
// Spotify Premium is required for volume control — non-Premium accounts
// will get a "Restriction violated" error from the upstream API.
func SetVolume(percent int, deviceName string) (string, error) {
	if spotifyClient == nil {
		return "", fmt.Errorf("Spotify not authenticated. Visit /auth to authenticate")
	}
	if percent < 0 || percent > 100 {
		return "", fmt.Errorf("level must be between 0 and 100, got %d", percent)
	}

	ctx := context.Background()

	// No device specified — set on whatever's currently active.
	if deviceName == "" {
		if err := spotifyClient.Volume(ctx, percent); err != nil {
			return "", fmt.Errorf("failed to set volume: %w", err)
		}
		return fmt.Sprintf("Volume set to %d%% on active device", percent), nil
	}

	// Device specified — resolve to an ID via the cloud devices list.
	devices, err := spotifyClient.PlayerDevices(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get devices: %w", err)
	}

	var targetID spotifyLib.ID
	var matchedName string
	for _, d := range devices {
		if d.Name == deviceName || string(d.ID) == deviceName {
			targetID = d.ID
			matchedName = d.Name
			break
		}
	}

	if targetID == "" {
		return "", fmt.Errorf("device %q not in Spotify cloud devices list — call /api/v1/wake first", deviceName)
	}

	opts := &spotifyLib.PlayOptions{DeviceID: &targetID}
	if err := spotifyClient.VolumeOpt(ctx, percent, opts); err != nil {
		return "", fmt.Errorf("failed to set volume on %s: %w", matchedName, err)
	}
	return fmt.Sprintf("Volume set to %d%% on %s", percent, matchedName), nil
}

// SkipToNext advances playback to the next track in the current queue.
// Targets whichever device is currently active in the user's session —
// Spotify's API doesn't accept a device override here, so callers can't
// skip "the bedroom speaker" when something else is the active device.
// Premium-only.
func SkipToNext() (string, error) {
	if spotifyClient == nil {
		return "", fmt.Errorf("Spotify not authenticated. Visit /auth to authenticate")
	}

	ctx := context.Background()
	if err := spotifyClient.Next(ctx); err != nil {
		return "", fmt.Errorf("failed to skip: %w", err)
	}
	return "Skipped to next track", nil
}

// PausePlayback pauses the current Spotify playback.
// This function is used by both CLI and API server modes.
func PausePlayback() (string, error) {
	if spotifyClient == nil {
		return "", fmt.Errorf("Spotify not authenticated. Visit /auth to authenticate")
	}

	ctx := context.Background()

	err := spotifyClient.Pause(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to pause playback: %w", err)
	}

	return "Playback paused", nil
}
