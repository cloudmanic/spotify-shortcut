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
	"golang.org/x/oauth2"
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
	// Volume sets the playback volume on the user's current active device
	// to `percent` (0-100). Premium-only.
	Volume(ctx context.Context, percent int) error
	// VolumeOpt is the same as Volume but lets the caller target a
	// specific device via PlayOptions.DeviceID. Used by /api/v1/volume
	// when a device name is supplied.
	VolumeOpt(ctx context.Context, percent int, opt *spotifyLib.PlayOptions) error
	// Next skips to the next track in the current playback queue.
	Next(ctx context.Context) error
	// Token returns the current OAuth token, refreshing it if needed.
	// We need the access token to push to Spotify Connect devices via the
	// zeroconf addUser flow.
	Token() (*oauth2.Token, error)
}

// APIResponse represents a standard JSON response for the API.
type APIResponse struct {
	Success bool          `json:"success"`
	Message string        `json:"message,omitempty"`
	Error   string        `json:"error,omitempty"`
	Devices []DeviceInfo  `json:"devices,omitempty"`
}

// DeviceInfo is the JSON-friendly subset of a Spotify Connect device returned
// by the /api/v1/devices endpoint. We avoid leaking the upstream library type
// directly so the API contract stays under our control.
type DeviceInfo struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Type   string `json:"type"`
	Active bool   `json:"active"`
}

// LANDeviceInfo is one entry in the /api/v1/lan-devices response — a
// Spotify Connect speaker discovered on the local network via mDNS,
// regardless of whether it is currently linked to our Spotify account.
type LANDeviceInfo struct {
	Name     string `json:"name"`
	Hostname string `json:"hostname"`
	IP       string `json:"ip"`
	Port     int    `json:"port"`
	Instance string `json:"instance"`
}

// LANDevicesResponse is the shape returned by /api/v1/lan-devices. Kept
// separate from APIResponse so the device list can be typed.
type LANDevicesResponse struct {
	Success bool            `json:"success"`
	Message string          `json:"message,omitempty"`
	Error   string          `json:"error,omitempty"`
	Devices []LANDeviceInfo `json:"devices"`
}

// PlaylistInfo is the JSON-friendly subset of a Spotify playlist returned
// by the /api/v1/playlists endpoint. We expose just what clients (the iOS
// Shortcut) typically need: id, name, owner display name, track count.
type PlaylistInfo struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Owner  string `json:"owner"`
	Tracks uint   `json:"tracks"`
}

// PlaylistsResponse is the shape returned by /api/v1/playlists.
type PlaylistsResponse struct {
	Success   bool           `json:"success"`
	Message   string         `json:"message,omitempty"`
	Error     string         `json:"error,omitempty"`
	Playlists []PlaylistInfo `json:"playlists"`
}
