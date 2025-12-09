//
// Date: 2025-12-09
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Copyright (c) 2025 Cloudmanic Labs, LLC. All rights reserved.
//
// Description: Type definitions and interfaces for the Spotify Shortcut application.
//

package main

import (
	"context"

	"github.com/zmb3/spotify/v2"
)

// SpotifyClient defines the interface for Spotify API operations.
// This allows for mocking in tests.
type SpotifyClient interface {
	CurrentUser(ctx context.Context) (*spotify.PrivateUser, error)
	CurrentUsersPlaylists(ctx context.Context, opts ...spotify.RequestOption) (*spotify.SimplePlaylistPage, error)
	PlayerDevices(ctx context.Context) ([]spotify.PlayerDevice, error)
	GetPlaylist(ctx context.Context, playlistID spotify.ID, opts ...spotify.RequestOption) (*spotify.FullPlaylist, error)
	PlayOpt(ctx context.Context, opts *spotify.PlayOptions) error
	Pause(ctx context.Context) error
	Shuffle(ctx context.Context, shuffle bool) error
}

// APIResponse represents a standard JSON response for the API.
type APIResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message,omitempty"`
	Error   string `json:"error,omitempty"`
}
