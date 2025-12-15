//
// Date: 2025-12-15
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Copyright (c) 2025 Cloudmanic Labs, LLC. All rights reserved.
//
// Description: Playlist resolution and display functions.
//

package spotify

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/fatih/color"
	"github.com/jedib0t/go-pretty/v6/table"

	spotifyLib "github.com/zmb3/spotify/v2"
)

// ExtractPlaylistID extracts the playlist ID from a Spotify URL or returns
// the input as-is if it's already just an ID.
func ExtractPlaylistID(input string) string {
	// If it's a full URL like https://open.spotify.com/playlist/37i9dQZF1DXcBWIGoYBM5M?si=xxx
	if strings.Contains(input, "spotify.com/playlist/") {
		parts := strings.Split(input, "/playlist/")
		if len(parts) > 1 {
			// Remove any query parameters
			id := strings.Split(parts[1], "?")[0]
			return id
		}
	}
	// Already just an ID
	return input
}

// ResolvePlaylistID resolves a playlist input (URL, name, or ID) to a playlist ID.
// It first checks if it's a URL, then searches the user's playlists by name,
// and finally assumes it's an ID if no match is found.
func ResolvePlaylistID(ctx context.Context, client *spotifyLib.Client, input string) (string, error) {
	// First, check if it's a URL and extract the ID
	if strings.Contains(input, "spotify.com/playlist/") {
		return ExtractPlaylistID(input), nil
	}

	// Check if it looks like a Spotify ID (22 alphanumeric characters)
	// If so, try it directly first
	if len(input) == 22 && !strings.Contains(input, " ") {
		return input, nil
	}

	// Search user's playlists by name
	fmt.Printf("Searching for playlist: \"%s\"...\n", input)

	limit := 50
	offset := 0

	for {
		playlists, err := client.CurrentUsersPlaylists(ctx, spotifyLib.Limit(limit), spotifyLib.Offset(offset))
		if err != nil {
			return "", fmt.Errorf("failed to get playlists: %w", err)
		}

		for _, playlist := range playlists.Playlists {
			// Check for exact name match (case-insensitive)
			if strings.EqualFold(playlist.Name, input) {
				fmt.Printf("Found playlist: \"%s\" (ID: %s)\n", playlist.Name, playlist.ID)
				return string(playlist.ID), nil
			}
			// Also check if ID matches
			if string(playlist.ID) == input {
				return input, nil
			}
		}

		// Check if there are more playlists to fetch
		if len(playlists.Playlists) < limit {
			break
		}
		offset += limit
	}

	// If no match found by name, assume it's an ID
	fmt.Printf("No playlist found with name \"%s\", trying as ID...\n", input)
	return input, nil
}

// ResolvePlaylistIDQuiet resolves a playlist input without printing to stdout.
// Used by the API server to avoid cluttering logs.
func ResolvePlaylistIDQuiet(ctx context.Context, client Client, input string) (string, error) {
	// First, check if it's a URL and extract the ID
	if strings.Contains(input, "spotify.com/playlist/") {
		return ExtractPlaylistID(input), nil
	}

	// Check if it looks like a Spotify ID (22 alphanumeric characters)
	if len(input) == 22 && !strings.Contains(input, " ") {
		return input, nil
	}

	// Search user's playlists by name
	limit := 50
	offset := 0

	for {
		playlists, err := client.CurrentUsersPlaylists(ctx, spotifyLib.Limit(limit), spotifyLib.Offset(offset))
		if err != nil {
			return "", fmt.Errorf("failed to get playlists: %w", err)
		}

		for _, playlist := range playlists.Playlists {
			if strings.EqualFold(playlist.Name, input) {
				return string(playlist.ID), nil
			}
			if string(playlist.ID) == input {
				return input, nil
			}
		}

		if len(playlists.Playlists) < limit {
			break
		}
		offset += limit
	}

	// Assume it's an ID
	return input, nil
}

// PrintPlaylistsTable displays the user's Spotify playlists in a formatted table.
func PrintPlaylistsTable(playlists []spotifyLib.SimplePlaylist) {
	green := color.New(color.FgGreen, color.Bold)
	cyan := color.New(color.FgCyan)

	fmt.Println()
	cyan.Println("🎵 Your Spotify Playlists")
	fmt.Println()

	t := table.NewWriter()
	t.SetOutputMirror(os.Stdout)
	t.AppendHeader(table.Row{"#", "Name", "Tracks", "Owner", "Playlist ID"})

	for i, playlist := range playlists {
		t.AppendRow(table.Row{
			i + 1,
			color.New(color.Bold).Sprint(playlist.Name),
			playlist.Tracks.Total,
			playlist.Owner.DisplayName,
			color.HiBlackString(string(playlist.ID)),
		})
	}

	t.SetStyle(table.StyleRounded)
	t.Render()

	fmt.Println()
	green.Printf("Total playlists: %d\n", len(playlists))
}
