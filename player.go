//
// Date: 2025-12-09
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Copyright (c) 2025 Cloudmanic Labs, LLC. All rights reserved.
//
// Description: Playback control functions for Spotify.
//

package main

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"time"

	"github.com/zmb3/spotify/v2"
)

// playPlaylist starts playback of a playlist on the specified device.
// This function is used by both CLI and API server modes.
func playPlaylist(deviceName, playlistInput string, shuffle bool) (string, error) {
	if spotifyClient == nil {
		return "", fmt.Errorf("Spotify not authenticated. Visit /auth to authenticate")
	}

	ctx := context.Background()

	// Get available devices
	devices, err := spotifyClient.PlayerDevices(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get devices: %w", err)
	}

	if len(devices) == 0 {
		return "", fmt.Errorf("no Spotify Connect devices found")
	}

	// Find the target device
	var targetDevice *spotify.PlayerDevice
	for i, device := range devices {
		if deviceName != "" && (device.Name == deviceName || string(device.ID) == deviceName) {
			targetDevice = &devices[i]
			break
		}
	}

	// If no device specified or not found, use first active or first device
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
	playlistID, err := resolvePlaylistIDQuiet(ctx, spotifyClient, playlistInput)
	if err != nil {
		return "", fmt.Errorf("failed to resolve playlist: %w", err)
	}

	// Get playlist info
	playlist, err := spotifyClient.GetPlaylist(ctx, spotify.ID(playlistID))
	if err != nil {
		return "", fmt.Errorf("failed to get playlist: %w", err)
	}

	trackCount := int(playlist.Tracks.Total)
	playlistURI := spotify.URI("spotify:playlist:" + playlistID)

	// Build play options
	opts := &spotify.PlayOptions{
		DeviceID:        &targetDevice.ID,
		PlaybackContext: &playlistURI,
	}

	if shuffle {
		// Pick random starting track
		randomOffset := rand.Intn(trackCount)
		opts.PlaybackOffset = &spotify.PlaybackOffset{Position: &randomOffset}

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
	opts.PlaybackOffset = &spotify.PlaybackOffset{Position: &startPosition}

	err = spotifyClient.PlayOpt(ctx, opts)
	if err != nil {
		return "", fmt.Errorf("failed to start playback: %w", err)
	}

	return fmt.Sprintf("Now playing \"%s\" on %s (starting at track 1)", playlist.Name, targetDevice.Name), nil
}

// pausePlayback pauses the current Spotify playback.
// This function is used by both CLI and API server modes.
func pausePlayback() (string, error) {
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
