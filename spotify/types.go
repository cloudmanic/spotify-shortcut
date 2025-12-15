//
// Date: 2025-12-15
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Copyright (c) 2025 Cloudmanic Labs, LLC. All rights reserved.
//
// Description: Type definitions and interfaces for the Spotify Shortcut application.
//

package spotify

import (
	"context"

	spotifyLib "github.com/zmb3/spotify/v2"
)

// Client defines the interface for Spotify API operations.
// This allows for mocking in tests.
type Client interface {
	CurrentUser(ctx context.Context) (*spotifyLib.PrivateUser, error)
	CurrentUsersPlaylists(ctx context.Context, opts ...spotifyLib.RequestOption) (*spotifyLib.SimplePlaylistPage, error)
	PlayerDevices(ctx context.Context) ([]spotifyLib.PlayerDevice, error)
	GetPlaylist(ctx context.Context, playlistID spotifyLib.ID, opts ...spotifyLib.RequestOption) (*spotifyLib.FullPlaylist, error)
	PlayOpt(ctx context.Context, opts *spotifyLib.PlayOptions) error
	Pause(ctx context.Context) error
	Shuffle(ctx context.Context, shuffle bool) error
}

// APIResponse represents a standard JSON response for the API.
type APIResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message,omitempty"`
	Error   string `json:"error,omitempty"`
}
