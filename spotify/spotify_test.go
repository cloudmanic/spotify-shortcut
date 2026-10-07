//
// Date: 2025-12-15
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Copyright (c) 2025 Cloudmanic Labs, LLC. All rights reserved.
//
// Description: Unit tests for Spotify Shortcut application.
//

package spotify

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	spotifyLib "github.com/zmb3/spotify/v2"
	"golang.org/x/oauth2"
)

// createFullPlaylist creates a FullPlaylist with the Total field set via JSON unmarshaling.
// This is necessary because the basePage struct is unexported.
func createFullPlaylist(id spotifyLib.ID, name string, total int) *spotifyLib.FullPlaylist {
	jsonData := []byte(`{
		"id": "` + string(id) + `",
		"name": "` + name + `",
		"tracks": {
			"total": ` + string(rune('0'+total/10)) + string(rune('0'+total%10)) + `
		}
	}`)
	var playlist spotifyLib.FullPlaylist
	json.Unmarshal(jsonData, &playlist)
	return &playlist
}

// createFullPlaylistWithTotal creates a FullPlaylist with a specific total using JSON.
func createFullPlaylistWithTotal(id string, name string, total int) *spotifyLib.FullPlaylist {
	jsonStr := `{"id":"` + id + `","name":"` + name + `","tracks":{"total":` + itoa(total) + `}}`
	var playlist spotifyLib.FullPlaylist
	json.Unmarshal([]byte(jsonStr), &playlist)
	return &playlist
}

// itoa converts an int to a string (simple implementation for test helper).
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	result := ""
	for i > 0 {
		result = string(rune('0'+i%10)) + result
		i /= 10
	}
	return result
}

// MockSpotifyClient is a mock implementation of the Client interface for testing.
type MockSpotifyClient struct {
	// CurrentUser mock
	CurrentUserFunc func(ctx context.Context) (*spotifyLib.PrivateUser, error)

	// CurrentUsersPlaylists mock
	CurrentUsersPlaylistsFunc func(ctx context.Context, opts ...spotifyLib.RequestOption) (*spotifyLib.SimplePlaylistPage, error)

	// PlayerDevices mock
	PlayerDevicesFunc func(ctx context.Context) ([]spotifyLib.PlayerDevice, error)

	// GetPlaylist mock
	GetPlaylistFunc func(ctx context.Context, playlistID spotifyLib.ID, opts ...spotifyLib.RequestOption) (*spotifyLib.FullPlaylist, error)

	// PlayOpt mock
	PlayOptFunc func(ctx context.Context, opts *spotifyLib.PlayOptions) error

	// Pause mock
	PauseFunc func(ctx context.Context) error

	// Shuffle mock
	ShuffleFunc func(ctx context.Context, shuffle bool) error

	// Token mock — returns the current OAuth access token.
	TokenFunc func() (*oauth2.Token, error)

	// Volume / VolumeOpt mocks — let tests assert on the percent and
	// device id passed without actually hitting Spotify.
	VolumeFunc    func(ctx context.Context, percent int) error
	VolumeOptFunc func(ctx context.Context, percent int, opt *spotifyLib.PlayOptions) error

	// Next mock — invoked by SkipToNext.
	NextFunc func(ctx context.Context) error

	// Device-targeted controls, player state and catalog lookups used by
	// /api/v1/ask.
	NextOptFunc         func(ctx context.Context, opt *spotifyLib.PlayOptions) error
	PauseOptFunc        func(ctx context.Context, opt *spotifyLib.PlayOptions) error
	ShuffleOptFunc      func(ctx context.Context, shuffle bool, opt *spotifyLib.PlayOptions) error
	PlayerStateFunc     func(ctx context.Context) (*spotifyLib.PlayerState, error)
	SearchFunc          func(ctx context.Context, query string, st spotifyLib.SearchType) (*spotifyLib.SearchResult, error)
	GetArtistAlbumsFunc func(ctx context.Context, artistID spotifyLib.ID, ts []spotifyLib.AlbumType) (*spotifyLib.SimpleAlbumPage, error)
}

// Next forwards to the supplied func or no-ops.
func (m *MockSpotifyClient) Next(ctx context.Context) error {
	if m.NextFunc != nil {
		return m.NextFunc(ctx)
	}
	return nil
}

// Token returns the current OAuth token, falling back to a stub for tests
// that don't care about the value.
func (m *MockSpotifyClient) Token() (*oauth2.Token, error) {
	if m.TokenFunc != nil {
		return m.TokenFunc()
	}
	return &oauth2.Token{AccessToken: "test-access-token"}, nil
}

// Volume forwards to the supplied func or no-ops.
func (m *MockSpotifyClient) Volume(ctx context.Context, percent int) error {
	if m.VolumeFunc != nil {
		return m.VolumeFunc(ctx, percent)
	}
	return nil
}

// VolumeOpt forwards to the supplied func or no-ops.
func (m *MockSpotifyClient) VolumeOpt(ctx context.Context, percent int, opt *spotifyLib.PlayOptions) error {
	if m.VolumeOptFunc != nil {
		return m.VolumeOptFunc(ctx, percent, opt)
	}
	return nil
}

// CurrentUser returns the current user.
func (m *MockSpotifyClient) CurrentUser(ctx context.Context) (*spotifyLib.PrivateUser, error) {
	if m.CurrentUserFunc != nil {
		return m.CurrentUserFunc(ctx)
	}
	return &spotifyLib.PrivateUser{
		User: spotifyLib.User{
			DisplayName: "Test User",
			ID:          "testuser123",
		},
	}, nil
}

// CurrentUsersPlaylists returns the user's playlists.
func (m *MockSpotifyClient) CurrentUsersPlaylists(ctx context.Context, opts ...spotifyLib.RequestOption) (*spotifyLib.SimplePlaylistPage, error) {
	if m.CurrentUsersPlaylistsFunc != nil {
		return m.CurrentUsersPlaylistsFunc(ctx, opts...)
	}
	return &spotifyLib.SimplePlaylistPage{
		Playlists: []spotifyLib.SimplePlaylist{
			{
				ID:   "playlist123",
				Name: "Test Playlist",
			},
			{
				ID:   "playlist456",
				Name: "Another Playlist",
			},
		},
	}, nil
}

// PlayerDevices returns available devices.
func (m *MockSpotifyClient) PlayerDevices(ctx context.Context) ([]spotifyLib.PlayerDevice, error) {
	if m.PlayerDevicesFunc != nil {
		return m.PlayerDevicesFunc(ctx)
	}
	return []spotifyLib.PlayerDevice{
		{
			ID:     "device123",
			Name:   "Living Room Speaker",
			Type:   "Speaker",
			Active: true,
		},
		{
			ID:     "device456",
			Name:   "Kitchen Speaker",
			Type:   "Speaker",
			Active: false,
		},
	}, nil
}

// GetPlaylist returns a playlist by ID.
func (m *MockSpotifyClient) GetPlaylist(ctx context.Context, playlistID spotifyLib.ID, opts ...spotifyLib.RequestOption) (*spotifyLib.FullPlaylist, error) {
	if m.GetPlaylistFunc != nil {
		return m.GetPlaylistFunc(ctx, playlistID, opts...)
	}
	return createFullPlaylistWithTotal(string(playlistID), "Test Playlist", 50), nil
}

// PlayOpt starts playback with options.
func (m *MockSpotifyClient) PlayOpt(ctx context.Context, opts *spotifyLib.PlayOptions) error {
	if m.PlayOptFunc != nil {
		return m.PlayOptFunc(ctx, opts)
	}
	return nil
}

// Pause pauses playback.
func (m *MockSpotifyClient) Pause(ctx context.Context) error {
	if m.PauseFunc != nil {
		return m.PauseFunc(ctx)
	}
	return nil
}

// Shuffle sets shuffle mode.
func (m *MockSpotifyClient) Shuffle(ctx context.Context, shuffle bool) error {
	if m.ShuffleFunc != nil {
		return m.ShuffleFunc(ctx, shuffle)
	}
	return nil
}

// NextOpt forwards to the supplied func or no-ops.
func (m *MockSpotifyClient) NextOpt(ctx context.Context, opt *spotifyLib.PlayOptions) error {
	if m.NextOptFunc != nil {
		return m.NextOptFunc(ctx, opt)
	}
	return nil
}

// PauseOpt forwards to the supplied func or no-ops.
func (m *MockSpotifyClient) PauseOpt(ctx context.Context, opt *spotifyLib.PlayOptions) error {
	if m.PauseOptFunc != nil {
		return m.PauseOptFunc(ctx, opt)
	}
	return nil
}

// ShuffleOpt forwards to the supplied func or no-ops.
func (m *MockSpotifyClient) ShuffleOpt(ctx context.Context, shuffle bool, opt *spotifyLib.PlayOptions) error {
	if m.ShuffleOptFunc != nil {
		return m.ShuffleOptFunc(ctx, shuffle, opt)
	}
	return nil
}

// PlayerState forwards to the supplied func, or reports nothing playing.
func (m *MockSpotifyClient) PlayerState(ctx context.Context, opts ...spotifyLib.RequestOption) (*spotifyLib.PlayerState, error) {
	if m.PlayerStateFunc != nil {
		return m.PlayerStateFunc(ctx)
	}
	return &spotifyLib.PlayerState{}, nil
}

// Search forwards to the supplied func, or returns no results.
func (m *MockSpotifyClient) Search(ctx context.Context, query string, st spotifyLib.SearchType, opts ...spotifyLib.RequestOption) (*spotifyLib.SearchResult, error) {
	if m.SearchFunc != nil {
		return m.SearchFunc(ctx, query, st)
	}
	return &spotifyLib.SearchResult{}, nil
}

// GetArtistAlbums forwards to the supplied func, or returns no albums.
func (m *MockSpotifyClient) GetArtistAlbums(ctx context.Context, artistID spotifyLib.ID, ts []spotifyLib.AlbumType, opts ...spotifyLib.RequestOption) (*spotifyLib.SimpleAlbumPage, error) {
	if m.GetArtistAlbumsFunc != nil {
		return m.GetArtistAlbumsFunc(ctx, artistID, ts)
	}
	return &spotifyLib.SimpleAlbumPage{}, nil
}

// TestExtractPlaylistID tests the ExtractPlaylistID function.
func TestExtractPlaylistID(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "full URL with query params",
			input:    "https://open.spotify.com/playlist/37i9dQZF1DXcBWIGoYBM5M?si=abc123",
			expected: "37i9dQZF1DXcBWIGoYBM5M",
		},
		{
			name:     "full URL without query params",
			input:    "https://open.spotify.com/playlist/37i9dQZF1DXcBWIGoYBM5M",
			expected: "37i9dQZF1DXcBWIGoYBM5M",
		},
		{
			name:     "just playlist ID",
			input:    "37i9dQZF1DXcBWIGoYBM5M",
			expected: "37i9dQZF1DXcBWIGoYBM5M",
		},
		{
			name:     "URL with http",
			input:    "http://open.spotify.com/playlist/abc123def456",
			expected: "abc123def456",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ExtractPlaylistID(tt.input)
			if result != tt.expected {
				t.Errorf("ExtractPlaylistID(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

// TestResolvePlaylistIDQuiet_URL tests ResolvePlaylistIDQuiet with URL input.
func TestResolvePlaylistIDQuiet_URL(t *testing.T) {
	mock := &MockSpotifyClient{}
	ctx := context.Background()

	result, err := ResolvePlaylistIDQuiet(ctx, mock, "https://open.spotify.com/playlist/37i9dQZF1DXcBWIGoYBM5M")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "37i9dQZF1DXcBWIGoYBM5M" {
		t.Errorf("expected 37i9dQZF1DXcBWIGoYBM5M, got %s", result)
	}
}

// TestResolvePlaylistIDQuiet_ID tests ResolvePlaylistIDQuiet with a 22-char ID.
func TestResolvePlaylistIDQuiet_ID(t *testing.T) {
	mock := &MockSpotifyClient{}
	ctx := context.Background()

	// 22 character ID
	result, err := ResolvePlaylistIDQuiet(ctx, mock, "37i9dQZF1DXcBWIGoYBM5M")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "37i9dQZF1DXcBWIGoYBM5M" {
		t.Errorf("expected 37i9dQZF1DXcBWIGoYBM5M, got %s", result)
	}
}

// TestResolvePlaylistIDQuiet_Name tests ResolvePlaylistIDQuiet with playlist name.
func TestResolvePlaylistIDQuiet_Name(t *testing.T) {
	mock := &MockSpotifyClient{
		CurrentUsersPlaylistsFunc: func(ctx context.Context, opts ...spotifyLib.RequestOption) (*spotifyLib.SimplePlaylistPage, error) {
			return &spotifyLib.SimplePlaylistPage{
				Playlists: []spotifyLib.SimplePlaylist{
					{ID: "found123playlistid00", Name: "My Awesome Playlist"},
					{ID: "other456playlistid00", Name: "Other Playlist"},
				},
			}, nil
		},
	}
	ctx := context.Background()

	result, err := ResolvePlaylistIDQuiet(ctx, mock, "My Awesome Playlist")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "found123playlistid00" {
		t.Errorf("expected found123playlistid00, got %s", result)
	}
}

// TestResolvePlaylistIDQuiet_NameCaseInsensitive tests case-insensitive name matching.
func TestResolvePlaylistIDQuiet_NameCaseInsensitive(t *testing.T) {
	mock := &MockSpotifyClient{
		CurrentUsersPlaylistsFunc: func(ctx context.Context, opts ...spotifyLib.RequestOption) (*spotifyLib.SimplePlaylistPage, error) {
			return &spotifyLib.SimplePlaylistPage{
				Playlists: []spotifyLib.SimplePlaylist{
					{ID: "found123playlistid00", Name: "My Awesome Playlist"},
				},
			}, nil
		},
	}
	ctx := context.Background()

	result, err := ResolvePlaylistIDQuiet(ctx, mock, "my awesome playlist")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "found123playlistid00" {
		t.Errorf("expected found123playlistid00, got %s", result)
	}
}

// TestResolvePlaylistIDQuiet_NotFound tests when playlist is not found by name.
func TestResolvePlaylistIDQuiet_NotFound(t *testing.T) {
	mock := &MockSpotifyClient{
		CurrentUsersPlaylistsFunc: func(ctx context.Context, opts ...spotifyLib.RequestOption) (*spotifyLib.SimplePlaylistPage, error) {
			return &spotifyLib.SimplePlaylistPage{
				Playlists: []spotifyLib.SimplePlaylist{},
			}, nil
		},
	}
	ctx := context.Background()

	// When not found, it returns the input as-is (assuming it's an ID)
	result, err := ResolvePlaylistIDQuiet(ctx, mock, "Unknown Playlist")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "Unknown Playlist" {
		t.Errorf("expected 'Unknown Playlist', got %s", result)
	}
}

// TestResolvePlaylistIDQuiet_APIError tests API error handling.
func TestResolvePlaylistIDQuiet_APIError(t *testing.T) {
	mock := &MockSpotifyClient{
		CurrentUsersPlaylistsFunc: func(ctx context.Context, opts ...spotifyLib.RequestOption) (*spotifyLib.SimplePlaylistPage, error) {
			return nil, errors.New("API error")
		},
	}
	ctx := context.Background()

	_, err := ResolvePlaylistIDQuiet(ctx, mock, "Some Playlist")
	if err == nil {
		t.Error("expected error, got nil")
	}
}

// TestSaveAndLoadToken tests token persistence.
func TestSaveAndLoadToken(t *testing.T) {
	// Create a temporary directory for the test
	tmpDir := t.TempDir()
	testTokenFile := filepath.Join(tmpDir, "test_token.json")

	// Save the original tokenFile and restore after test
	originalTokenFile := tokenFile
	tokenFile = testTokenFile
	defer func() { tokenFile = originalTokenFile }()

	// Create a test token
	testToken := &oauth2.Token{
		AccessToken:  "test-access-token",
		TokenType:    "Bearer",
		RefreshToken: "test-refresh-token",
	}

	// Save the token
	SaveToken(testToken)

	// Verify the file was created
	if _, err := os.Stat(testTokenFile); os.IsNotExist(err) {
		t.Fatal("token file was not created")
	}

	// Read and verify the saved token
	file, err := os.Open(testTokenFile)
	if err != nil {
		t.Fatalf("failed to open token file: %v", err)
	}
	defer file.Close()

	var loadedToken oauth2.Token
	if err := json.NewDecoder(file).Decode(&loadedToken); err != nil {
		t.Fatalf("failed to decode token: %v", err)
	}

	if loadedToken.AccessToken != testToken.AccessToken {
		t.Errorf("expected access token %s, got %s", testToken.AccessToken, loadedToken.AccessToken)
	}
	if loadedToken.RefreshToken != testToken.RefreshToken {
		t.Errorf("expected refresh token %s, got %s", testToken.RefreshToken, loadedToken.RefreshToken)
	}
}

// TestPausePlayback_Success tests successful pause.
func TestPausePlayback_Success(t *testing.T) {
	mock := &MockSpotifyClient{
		PauseFunc: func(ctx context.Context) error {
			return nil
		},
	}

	// Save and restore original client
	originalClient := spotifyClient
	spotifyClient = mock
	defer func() { spotifyClient = originalClient }()

	result, err := PausePlayback()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "Playback paused" {
		t.Errorf("expected 'Playback paused', got %s", result)
	}
}

// TestPausePlayback_Error tests pause with API error.
func TestPausePlayback_Error(t *testing.T) {
	mock := &MockSpotifyClient{
		PauseFunc: func(ctx context.Context) error {
			return errors.New("playback error")
		},
	}

	originalClient := spotifyClient
	spotifyClient = mock
	defer func() { spotifyClient = originalClient }()

	_, err := PausePlayback()
	if err == nil {
		t.Error("expected error, got nil")
	}
}

// TestPausePlayback_NotAuthenticated tests pause without authentication.
func TestPausePlayback_NotAuthenticated(t *testing.T) {
	originalClient := spotifyClient
	spotifyClient = nil
	defer func() { spotifyClient = originalClient }()

	_, err := PausePlayback()
	if err == nil {
		t.Error("expected error, got nil")
	}
	if err.Error() != "Spotify not authenticated. Visit /auth to authenticate" {
		t.Errorf("unexpected error message: %v", err)
	}
}

// TestPlayPlaylist_Success tests successful playlist playback.
func TestPlayPlaylist_Success(t *testing.T) {
	playOptCalled := false
	mock := &MockSpotifyClient{
		PlayerDevicesFunc: func(ctx context.Context) ([]spotifyLib.PlayerDevice, error) {
			return []spotifyLib.PlayerDevice{
				{ID: "device123", Name: "Test Speaker", Active: true},
			}, nil
		},
		GetPlaylistFunc: func(ctx context.Context, playlistID spotifyLib.ID, opts ...spotifyLib.RequestOption) (*spotifyLib.FullPlaylist, error) {
			return createFullPlaylistWithTotal(string(playlistID), "Test Playlist", 10), nil
		},
		PlayOptFunc: func(ctx context.Context, opts *spotifyLib.PlayOptions) error {
			playOptCalled = true
			return nil
		},
	}

	originalClient := spotifyClient
	spotifyClient = mock
	defer func() { spotifyClient = originalClient }()

	result, err := PlayPlaylist("Test Speaker", "37i9dQZF1DXcBWIGoYBM5M", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !playOptCalled {
		t.Error("PlayOpt was not called")
	}
	if result == "" {
		t.Error("expected non-empty result")
	}
}

// TestPlayPlaylist_WithShuffle tests playlist playback with shuffle enabled.
func TestPlayPlaylist_WithShuffle(t *testing.T) {
	shuffleCalled := false
	mock := &MockSpotifyClient{
		PlayerDevicesFunc: func(ctx context.Context) ([]spotifyLib.PlayerDevice, error) {
			return []spotifyLib.PlayerDevice{
				{ID: "device123", Name: "Test Speaker", Active: true},
			}, nil
		},
		GetPlaylistFunc: func(ctx context.Context, playlistID spotifyLib.ID, opts ...spotifyLib.RequestOption) (*spotifyLib.FullPlaylist, error) {
			return createFullPlaylistWithTotal(string(playlistID), "Test Playlist", 10), nil
		},
		PlayOptFunc: func(ctx context.Context, opts *spotifyLib.PlayOptions) error {
			return nil
		},
		ShuffleFunc: func(ctx context.Context, shuffle bool) error {
			shuffleCalled = true
			if !shuffle {
				t.Error("expected shuffle to be true")
			}
			return nil
		},
	}

	originalClient := spotifyClient
	spotifyClient = mock
	defer func() { spotifyClient = originalClient }()

	result, err := PlayPlaylist("Test Speaker", "37i9dQZF1DXcBWIGoYBM5M", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !shuffleCalled {
		t.Error("Shuffle was not called")
	}
	if result == "" {
		t.Error("expected non-empty result")
	}
}

// TestPlayPlaylist_NoDevices tests playback when no devices are available.
func TestPlayPlaylist_NoDevices(t *testing.T) {
	mock := &MockSpotifyClient{
		PlayerDevicesFunc: func(ctx context.Context) ([]spotifyLib.PlayerDevice, error) {
			return []spotifyLib.PlayerDevice{}, nil
		},
	}

	originalClient := spotifyClient
	spotifyClient = mock
	defer func() { spotifyClient = originalClient }()

	_, err := PlayPlaylist("", "37i9dQZF1DXcBWIGoYBM5M", false)
	if err == nil {
		t.Error("expected error, got nil")
	}
}

// TestPlayPlaylist_NotAuthenticated tests playback without authentication.
func TestPlayPlaylist_NotAuthenticated(t *testing.T) {
	originalClient := spotifyClient
	spotifyClient = nil
	defer func() { spotifyClient = originalClient }()

	_, err := PlayPlaylist("", "37i9dQZF1DXcBWIGoYBM5M", false)
	if err == nil {
		t.Error("expected error, got nil")
	}
}

// TestPlayPlaylist_DeviceSelection tests device selection logic.
func TestPlayPlaylist_DeviceSelection(t *testing.T) {
	selectedDeviceID := ""
	mock := &MockSpotifyClient{
		PlayerDevicesFunc: func(ctx context.Context) ([]spotifyLib.PlayerDevice, error) {
			return []spotifyLib.PlayerDevice{
				{ID: "device1", Name: "First Speaker", Active: false},
				{ID: "device2", Name: "Second Speaker", Active: false},
				{ID: "device3", Name: "Target Speaker", Active: false},
			}, nil
		},
		GetPlaylistFunc: func(ctx context.Context, playlistID spotifyLib.ID, opts ...spotifyLib.RequestOption) (*spotifyLib.FullPlaylist, error) {
			return createFullPlaylistWithTotal(string(playlistID), "Test", 10), nil
		},
		PlayOptFunc: func(ctx context.Context, opts *spotifyLib.PlayOptions) error {
			if opts.DeviceID != nil {
				selectedDeviceID = string(*opts.DeviceID)
			}
			return nil
		},
	}

	originalClient := spotifyClient
	spotifyClient = mock
	defer func() { spotifyClient = originalClient }()

	_, err := PlayPlaylist("Target Speaker", "37i9dQZF1DXcBWIGoYBM5M", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if selectedDeviceID != "device3" {
		t.Errorf("expected device3, got %s", selectedDeviceID)
	}
}

// TestHandlePauseRequest_Success tests the pause API endpoint.
func TestHandlePauseRequest_Success(t *testing.T) {
	mock := &MockSpotifyClient{
		PauseFunc: func(ctx context.Context) error {
			return nil
		},
	}

	originalClient := spotifyClient
	originalToken := apiAccessToken
	spotifyClient = mock
	apiAccessToken = "test-token"
	defer func() {
		spotifyClient = originalClient
		apiAccessToken = originalToken
	}()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/pause?token=test-token", nil)
	w := httptest.NewRecorder()

	HandlePauseRequest(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	var response APIResponse
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if !response.Success {
		t.Error("expected success to be true")
	}
	if response.Message != "Playback paused" {
		t.Errorf("expected 'Playback paused', got %s", response.Message)
	}
}

// TestHandlePauseRequest_Unauthorized tests pause endpoint without token.
func TestHandlePauseRequest_Unauthorized(t *testing.T) {
	originalToken := apiAccessToken
	apiAccessToken = "test-token"
	defer func() { apiAccessToken = originalToken }()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/pause", nil)
	w := httptest.NewRecorder()

	HandlePauseRequest(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected status 401, got %d", w.Code)
	}
}

// TestHandlePauseRequest_InvalidToken tests pause endpoint with wrong token.
func TestHandlePauseRequest_InvalidToken(t *testing.T) {
	originalToken := apiAccessToken
	apiAccessToken = "correct-token"
	defer func() { apiAccessToken = originalToken }()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/pause?token=wrong-token", nil)
	w := httptest.NewRecorder()

	HandlePauseRequest(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected status 401, got %d", w.Code)
	}
}

// TestHandlePauseRequest_BearerToken tests pause endpoint with Bearer token.
func TestHandlePauseRequest_BearerToken(t *testing.T) {
	mock := &MockSpotifyClient{
		PauseFunc: func(ctx context.Context) error {
			return nil
		},
	}

	originalClient := spotifyClient
	originalToken := apiAccessToken
	spotifyClient = mock
	apiAccessToken = "test-token"
	defer func() {
		spotifyClient = originalClient
		apiAccessToken = originalToken
	}()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/pause", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	w := httptest.NewRecorder()

	HandlePauseRequest(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}
}

// TestHandleDevicesRequest_Success tests the devices API endpoint returns
// the device list as JSON when the token is valid and Spotify responds OK.
func TestHandleDevicesRequest_Success(t *testing.T) {
	mock := &MockSpotifyClient{
		PlayerDevicesFunc: func(ctx context.Context) ([]spotifyLib.PlayerDevice, error) {
			return []spotifyLib.PlayerDevice{
				{ID: "device123", Name: "Living Room", Type: "Speaker", Active: true},
				{ID: "device456", Name: "iPhone", Type: "Smartphone", Active: false},
			}, nil
		},
	}

	originalClient := spotifyClient
	originalToken := apiAccessToken
	spotifyClient = mock
	apiAccessToken = "test-token"
	defer func() {
		spotifyClient = originalClient
		apiAccessToken = originalToken
	}()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/devices?token=test-token", nil)
	w := httptest.NewRecorder()

	HandleDevicesRequest(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	var response APIResponse
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if !response.Success {
		t.Errorf("expected success, got error: %s", response.Error)
	}
	if len(response.Devices) != 2 {
		t.Fatalf("expected 2 devices, got %d", len(response.Devices))
	}
	if response.Devices[0].ID != "device123" || response.Devices[0].Name != "Living Room" {
		t.Errorf("unexpected first device: %+v", response.Devices[0])
	}
	if !response.Devices[0].Active {
		t.Error("expected first device to be active")
	}
	if response.Devices[1].Active {
		t.Error("expected second device to be inactive")
	}
}

// TestHandleDevicesRequest_Unauthorized verifies the endpoint rejects
// requests with no token.
func TestHandleDevicesRequest_Unauthorized(t *testing.T) {
	originalToken := apiAccessToken
	apiAccessToken = "test-token"
	defer func() { apiAccessToken = originalToken }()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/devices", nil)
	w := httptest.NewRecorder()

	HandleDevicesRequest(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected status 401, got %d", w.Code)
	}
}

// TestHandleDevicesRequest_InvalidToken verifies the endpoint rejects
// requests carrying a wrong token.
func TestHandleDevicesRequest_InvalidToken(t *testing.T) {
	originalToken := apiAccessToken
	apiAccessToken = "correct-token"
	defer func() { apiAccessToken = originalToken }()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/devices?token=wrong-token", nil)
	w := httptest.NewRecorder()

	HandleDevicesRequest(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected status 401, got %d", w.Code)
	}
}

// TestHandleDevicesRequest_BearerToken verifies the endpoint accepts the
// access token via the Authorization: Bearer header.
func TestHandleDevicesRequest_BearerToken(t *testing.T) {
	mock := &MockSpotifyClient{
		PlayerDevicesFunc: func(ctx context.Context) ([]spotifyLib.PlayerDevice, error) {
			return []spotifyLib.PlayerDevice{}, nil
		},
	}

	originalClient := spotifyClient
	originalToken := apiAccessToken
	spotifyClient = mock
	apiAccessToken = "test-token"
	defer func() {
		spotifyClient = originalClient
		apiAccessToken = originalToken
	}()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/devices", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	w := httptest.NewRecorder()

	HandleDevicesRequest(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}
}

// TestHandleDevicesRequest_SpotifyError verifies a Spotify API failure is
// surfaced as a 500 with the error message in the response body.
func TestHandleDevicesRequest_SpotifyError(t *testing.T) {
	mock := &MockSpotifyClient{
		PlayerDevicesFunc: func(ctx context.Context) ([]spotifyLib.PlayerDevice, error) {
			return nil, errors.New("spotify API down")
		},
	}

	originalClient := spotifyClient
	originalToken := apiAccessToken
	spotifyClient = mock
	apiAccessToken = "test-token"
	defer func() {
		spotifyClient = originalClient
		apiAccessToken = originalToken
	}()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/devices?token=test-token", nil)
	w := httptest.NewRecorder()

	HandleDevicesRequest(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", w.Code)
	}

	var response APIResponse
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if response.Success {
		t.Error("expected success to be false on upstream error")
	}
	if response.Error == "" {
		t.Error("expected error message in response")
	}
}

// TestListDevices_NotAuthenticated verifies ListDevices returns an error when
// no Spotify client is set, mirroring PausePlayback's behavior.
func TestListDevices_NotAuthenticated(t *testing.T) {
	originalClient := spotifyClient
	spotifyClient = nil
	defer func() { spotifyClient = originalClient }()

	_, err := ListDevices()
	if err == nil {
		t.Fatal("expected error when not authenticated, got nil")
	}
}

// TestHandlePlayRequest_Success tests the play API endpoint.
func TestHandlePlayRequest_Success(t *testing.T) {
	mock := &MockSpotifyClient{
		PlayerDevicesFunc: func(ctx context.Context) ([]spotifyLib.PlayerDevice, error) {
			return []spotifyLib.PlayerDevice{
				{ID: "device123", Name: "Test Speaker", Active: true},
			}, nil
		},
		GetPlaylistFunc: func(ctx context.Context, playlistID spotifyLib.ID, opts ...spotifyLib.RequestOption) (*spotifyLib.FullPlaylist, error) {
			return createFullPlaylistWithTotal(string(playlistID), "Test Playlist", 10), nil
		},
		PlayOptFunc: func(ctx context.Context, opts *spotifyLib.PlayOptions) error {
			return nil
		},
	}

	originalClient := spotifyClient
	originalToken := apiAccessToken
	spotifyClient = mock
	apiAccessToken = "test-token"
	defer func() {
		spotifyClient = originalClient
		apiAccessToken = originalToken
	}()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/play?token=test-token&playlist=37i9dQZF1DXcBWIGoYBM5M", nil)
	w := httptest.NewRecorder()

	HandlePlayRequest(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	var response APIResponse
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if !response.Success {
		t.Errorf("expected success, got error: %s", response.Error)
	}
}

// TestHandlePlayRequest_MissingPlaylist tests play endpoint without playlist.
func TestHandlePlayRequest_MissingPlaylist(t *testing.T) {
	originalToken := apiAccessToken
	apiAccessToken = "test-token"
	defer func() { apiAccessToken = originalToken }()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/play?token=test-token", nil)
	w := httptest.NewRecorder()

	HandlePlayRequest(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", w.Code)
	}

	var response APIResponse
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if response.Success {
		t.Error("expected success to be false")
	}
	if response.Error != "playlist parameter is required" {
		t.Errorf("unexpected error: %s", response.Error)
	}
}

// TestHandlePlayRequest_Unauthorized tests play endpoint without token.
func TestHandlePlayRequest_Unauthorized(t *testing.T) {
	originalToken := apiAccessToken
	apiAccessToken = "test-token"
	defer func() { apiAccessToken = originalToken }()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/play?playlist=test", nil)
	w := httptest.NewRecorder()

	HandlePlayRequest(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected status 401, got %d", w.Code)
	}
}

// TestHandleRootRequest tests the root endpoint.
func TestHandleRootRequest(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()

	HandleRootRequest(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	body := w.Body.String()
	if body != "app coming soon...." {
		t.Errorf("unexpected body: %s", body)
	}
}

// TestHandleRootRequest_NotFound tests non-root paths.
func TestHandleRootRequest_NotFound(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/unknown", nil)
	w := httptest.NewRecorder()

	HandleRootRequest(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected status 404, got %d", w.Code)
	}
}

// TestHandleAuthRequest_Unauthorized tests auth endpoint without token.
func TestHandleAuthRequest_Unauthorized(t *testing.T) {
	originalToken := apiAccessToken
	apiAccessToken = "test-token"
	defer func() { apiAccessToken = originalToken }()

	req := httptest.NewRequest(http.MethodGet, "/auth", nil)
	w := httptest.NewRecorder()

	HandleAuthRequest(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected status 401, got %d", w.Code)
	}
}

// TestHandleAuthRequest_WrongToken tests auth endpoint with invalid token.
func TestHandleAuthRequest_WrongToken(t *testing.T) {
	originalToken := apiAccessToken
	apiAccessToken = "correct-token"
	defer func() { apiAccessToken = originalToken }()

	req := httptest.NewRequest(http.MethodGet, "/auth?token=wrong-token", nil)
	w := httptest.NewRecorder()

	HandleAuthRequest(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected status 401, got %d", w.Code)
	}
}

// fakeDiscoverer is a stub Discoverer for tests. It returns a canned set of
// devices and counts how many times Discover() was invoked so we can assert
// the cache is doing its job.
type fakeDiscoverer struct {
	devices []LocalDevice
	calls   int
	err     error
}

// Discover returns the canned device list and increments the call counter.
func (f *fakeDiscoverer) Discover(ctx context.Context, timeout time.Duration) ([]LocalDevice, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return f.devices, nil
}

// TestFriendlyNameFromHostname covers the heuristic that turns mDNS
// hostnames into human-readable device names.
func TestFriendlyNameFromHostname(t *testing.T) {
	cases := []struct {
		hostname string
		want     string
	}{
		{"Living-Room-Speakers.local.", "Living Room Speakers"},
		{"Pool-Porch-Speakers.local.", "Pool Porch Speakers"},
		{"Sonos-F0F6C161C30E.local.", "Sonos-F0F6C161C30E"},
		{"Android-2.local.", "Android 2"},
		{"none.local.", "none"},
	}

	for _, tc := range cases {
		got := friendlyNameFromHostname(tc.hostname)
		if got != tc.want {
			t.Errorf("friendlyNameFromHostname(%q) = %q, want %q", tc.hostname, got, tc.want)
		}
	}
}

// TestDiscoveryCache_CachesWithinTTL verifies a second Devices() call within
// the TTL window does not re-trigger an mDNS browse.
func TestDiscoveryCache_CachesWithinTTL(t *testing.T) {
	fake := &fakeDiscoverer{
		devices: []LocalDevice{
			{InstanceName: "FF98", Hostname: "Living-Room-Speakers.local.", FriendlyName: "Living Room Speakers", IP: "192.168.1.3", Port: 5356},
		},
	}
	cache := NewDiscoveryCache(fake, 60*time.Second)
	ctx := context.Background()

	first, err := cache.Devices(ctx)
	if err != nil {
		t.Fatalf("first Devices: %v", err)
	}
	if len(first) != 1 {
		t.Fatalf("expected 1 device, got %d", len(first))
	}

	second, err := cache.Devices(ctx)
	if err != nil {
		t.Fatalf("second Devices: %v", err)
	}
	if len(second) != 1 {
		t.Fatalf("expected 1 device on second call, got %d", len(second))
	}
	if fake.calls != 1 {
		t.Errorf("expected 1 mDNS call, got %d", fake.calls)
	}
}

// TestDiscoveryCache_RefreshesAfterTTL verifies that once the TTL elapses,
// the next call re-runs discovery.
func TestDiscoveryCache_RefreshesAfterTTL(t *testing.T) {
	fake := &fakeDiscoverer{
		devices: []LocalDevice{
			{InstanceName: "FF98", FriendlyName: "Living Room Speakers", IP: "192.168.1.3"},
		},
	}
	cache := NewDiscoveryCache(fake, 1*time.Millisecond)
	ctx := context.Background()

	if _, err := cache.Devices(ctx); err != nil {
		t.Fatalf("first: %v", err)
	}
	time.Sleep(5 * time.Millisecond)
	if _, err := cache.Devices(ctx); err != nil {
		t.Fatalf("second: %v", err)
	}

	if fake.calls != 2 {
		t.Errorf("expected 2 mDNS calls (TTL elapsed), got %d", fake.calls)
	}
}

// TestDiscoveryCache_FindByName_FriendlyName matches the most common case
// where the caller gave the human-friendly room name.
func TestDiscoveryCache_FindByName_FriendlyName(t *testing.T) {
	fake := &fakeDiscoverer{
		devices: []LocalDevice{
			{InstanceName: "FF98F2F7F4AFE509F6466C48", Hostname: "Living-Room-Speakers.local.", FriendlyName: "Living Room Speakers", IP: "192.168.1.3", Port: 5356},
			{InstanceName: "sonosRINCON_F0F6C161C30E01400", Hostname: "Sonos-F0F6C161C30E.local.", FriendlyName: "Sonos-F0F6C161C30E", IP: "192.168.1.7", Port: 1400},
		},
	}
	cache := NewDiscoveryCache(fake, time.Minute)

	device, ok, err := cache.FindByName(context.Background(), "living room speakers")
	if err != nil {
		t.Fatalf("FindByName: %v", err)
	}
	if !ok {
		t.Fatal("expected hit, got miss")
	}
	if device.IP != "192.168.1.3" {
		t.Errorf("wrong device matched: %+v", device)
	}
}

// TestDiscoveryCache_FindByName_Hostname allows callers to look up devices
// by their `.local` hostname when the friendly name is not enough (e.g.
// Sonos units share a generic name).
func TestDiscoveryCache_FindByName_Hostname(t *testing.T) {
	fake := &fakeDiscoverer{
		devices: []LocalDevice{
			{Hostname: "Sonos-F0F6C161C30E.local.", FriendlyName: "Sonos-F0F6C161C30E", IP: "192.168.1.7"},
		},
	}
	cache := NewDiscoveryCache(fake, time.Minute)

	device, ok, err := cache.FindByName(context.Background(), "Sonos-F0F6C161C30E.local")
	if err != nil {
		t.Fatalf("FindByName: %v", err)
	}
	if !ok || device.IP != "192.168.1.7" {
		t.Errorf("expected Sonos hit, got ok=%v device=%+v", ok, device)
	}
}

// TestDiscoveryCache_FindByName_Miss verifies a clean miss returns ok=false
// without an error.
func TestDiscoveryCache_FindByName_Miss(t *testing.T) {
	fake := &fakeDiscoverer{
		devices: []LocalDevice{
			{FriendlyName: "Pool Speakers"},
		},
	}
	cache := NewDiscoveryCache(fake, time.Minute)

	_, ok, err := cache.FindByName(context.Background(), "Garage Speakers")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Error("expected miss, got hit")
	}
}

// TestDiscoveryCache_DiscoveryError surfaces the underlying error when the
// cache is empty and discovery fails.
func TestDiscoveryCache_DiscoveryError(t *testing.T) {
	fake := &fakeDiscoverer{err: errors.New("mdns down")}
	cache := NewDiscoveryCache(fake, time.Minute)

	_, err := cache.Devices(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

// TestDiscoveryCache_Invalidate verifies Invalidate forces the next Devices
// call to re-browse mDNS instead of serving the previously-cached list.
// This is the contract the claim-retry path depends on: when a claim fails
// because the LAN cache held a stale empty (or wrong) result, retrying after
// Invalidate must actually re-query the network.
func TestDiscoveryCache_Invalidate(t *testing.T) {
	fake := &fakeDiscoverer{
		devices: []LocalDevice{
			{InstanceName: "FF98", Hostname: "Living-Room-Speakers.local.", FriendlyName: "Living Room Speakers", IP: "192.168.1.3", Port: 5356},
		},
	}
	cache := NewDiscoveryCache(fake, time.Minute)
	ctx := context.Background()

	if _, err := cache.Devices(ctx); err != nil {
		t.Fatalf("first Devices: %v", err)
	}
	if fake.calls != 1 {
		t.Fatalf("expected 1 mDNS call before invalidate, got %d", fake.calls)
	}

	cache.Invalidate()

	if _, err := cache.Devices(ctx); err != nil {
		t.Fatalf("post-invalidate Devices: %v", err)
	}
	if fake.calls != 2 {
		t.Errorf("expected 2 mDNS calls after invalidate, got %d", fake.calls)
	}
}

// TestZeroconfClient_GetInfo_AndAddUser_AccessTokenPath spins up an httptest
// server impersonating a WiiM-style Spotify Connect device that advertises
// tokenType=accesstoken. It verifies the client sends the unencrypted
// accesstoken flavor of addUser (raw token in blob, empty clientKey, plus
// tokenType=accesstoken form field).
func TestZeroconfClient_GetInfo_AndAddUser_AccessTokenPath(t *testing.T) {
	var (
		gotUserName, gotBlob, gotClientKey, gotTokenType, gotDeviceName string
		gotAction                                                       string
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		action := r.URL.Query().Get("action")
		if r.Method == http.MethodGet && action == "getInfo" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(GetInfoResponse{
				Status: 101, StatusString: "OK",
				DeviceID:   "fake-device-id",
				PublicKey:  "AA==", // unused on accesstoken path
				DeviceType: "AVR",
				TokenType:  "accesstoken",
			})
			return
		}
		if r.Method == http.MethodPost {
			gotAction = r.PostForm.Get("action")
			gotUserName = r.PostForm.Get("userName")
			gotBlob = r.PostForm.Get("blob")
			gotClientKey = r.PostForm.Get("clientKey")
			gotTokenType = r.PostForm.Get("tokenType")
			gotDeviceName = r.PostForm.Get("deviceName")
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(AddUserResponse{Status: 101, StatusString: "OK"})
			return
		}
		t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
	}))
	defer srv.Close()

	// Strip "http://" off the test server URL and split host/port for
	// NewZeroconfClient — it expects them separately.
	hostPort := strings.TrimPrefix(srv.URL, "http://")
	host, port, _ := net.SplitHostPort(hostPort)
	portInt := 0
	fmt.Sscanf(port, "%d", &portInt)
	zc := NewZeroconfClient(host, portInt, "/")

	ctx := context.Background()
	info, err := zc.GetInfo(ctx)
	if err != nil {
		t.Fatalf("GetInfo: %v", err)
	}
	if info.TokenType != "accesstoken" {
		t.Fatalf("expected tokenType=accesstoken, got %q", info.TokenType)
	}

	resp, err := zc.AddUser(ctx, info, "spotify-user-id", "test-app", []byte("RAW-ACCESS-TOKEN"))
	if err != nil {
		t.Fatalf("AddUser: %v", err)
	}
	if resp.Status != 101 {
		t.Errorf("status=%d", resp.Status)
	}

	if gotAction != "addUser" {
		t.Errorf("action=%q", gotAction)
	}
	if gotUserName != "spotify-user-id" {
		t.Errorf("userName=%q", gotUserName)
	}
	if gotBlob != "RAW-ACCESS-TOKEN" {
		t.Errorf("blob=%q (expected raw token, no encryption on accesstoken path)", gotBlob)
	}
	if gotClientKey != "" {
		t.Errorf("clientKey should be empty on accesstoken path, got %q", gotClientKey)
	}
	if gotTokenType != "accesstoken" {
		t.Errorf("tokenType=%q", gotTokenType)
	}
	if gotDeviceName != "test-app" {
		t.Errorf("deviceName=%q", gotDeviceName)
	}
}

// TestZeroconfClient_AddUser_LegacyEncryptedPath verifies that when the
// device does NOT advertise tokenType=accesstoken, the client falls back
// to the DH/AES/HMAC envelope and sends a non-empty clientKey. We don't
// re-implement the decrypt to keep the test from coupling to crypto
// constants; just check the wire-format invariants.
func TestZeroconfClient_AddUser_LegacyEncryptedPath(t *testing.T) {
	var gotBlob, gotClientKey, gotTokenType string
	const fakePub = "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8gISIjJCUmJygpKissLS4vMDEyMzQ1Njc4OTo7PD0+P0BBQkNERUZHSElKS0xNTk9QUVJTVFVWV1hZWltcXV5fYGFiY2Q="

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(GetInfoResponse{
				Status: 101, StatusString: "OK",
				DeviceID:  "fake",
				PublicKey: fakePub,
				TokenType: "default",
			})
			return
		}
		gotBlob = r.PostForm.Get("blob")
		gotClientKey = r.PostForm.Get("clientKey")
		gotTokenType = r.PostForm.Get("tokenType")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(AddUserResponse{Status: 101, StatusString: "OK"})
	}))
	defer srv.Close()

	hostPort := strings.TrimPrefix(srv.URL, "http://")
	host, port, _ := net.SplitHostPort(hostPort)
	portInt := 0
	fmt.Sscanf(port, "%d", &portInt)
	zc := NewZeroconfClient(host, portInt, "/")

	ctx := context.Background()
	info, err := zc.GetInfo(ctx)
	if err != nil {
		t.Fatalf("GetInfo: %v", err)
	}

	if _, err := zc.AddUser(ctx, info, "user", "test", []byte("plaintext-credentials-blob")); err != nil {
		t.Fatalf("AddUser: %v", err)
	}

	if gotBlob == "plaintext-credentials-blob" {
		t.Error("blob should be encrypted on legacy path, got plaintext")
	}
	if gotClientKey == "" {
		t.Error("clientKey should be non-empty on legacy path")
	}
	if gotTokenType != "" {
		t.Errorf("tokenType should be unset on legacy path, got %q", gotTokenType)
	}
	// Quick sanity: blob is base64 and at least IV(16)+payload+HMAC(20) bytes.
	decoded, err := base64.StdEncoding.DecodeString(gotBlob)
	if err != nil {
		t.Fatalf("blob not valid base64: %v", err)
	}
	if len(decoded) < 16+len("plaintext-credentials-blob")+20 {
		t.Errorf("blob too short: %d bytes", len(decoded))
	}
}

// TestHandleWakeRequest_MissingDevice verifies the wake endpoint rejects
// requests without a `device` parameter.
func TestHandleWakeRequest_MissingDevice(t *testing.T) {
	originalToken := apiAccessToken
	apiAccessToken = "test-token"
	defer func() { apiAccessToken = originalToken }()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/wake?token=test-token", nil)
	w := httptest.NewRecorder()

	HandleWakeRequest(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

// TestHandleWakeRequest_Unauthorized rejects requests without the API token.
func TestHandleWakeRequest_Unauthorized(t *testing.T) {
	originalToken := apiAccessToken
	apiAccessToken = "test-token"
	defer func() { apiAccessToken = originalToken }()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/wake?device=Foo", nil)
	w := httptest.NewRecorder()

	HandleWakeRequest(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

// TestClaimDevice_AlreadyInCloud verifies the fast-path: if the device is
// already in Spotify cloud's device list, ClaimDevice short-circuits and
// returns AlreadyActive=true without touching the LAN.
func TestClaimDevice_AlreadyInCloud(t *testing.T) {
	mock := &MockSpotifyClient{
		PlayerDevicesFunc: func(ctx context.Context) ([]spotifyLib.PlayerDevice, error) {
			return []spotifyLib.PlayerDevice{
				{ID: "abc123", Name: "Living Room Speakers", Active: false},
			}, nil
		},
	}
	originalClient := spotifyClient
	spotifyClient = mock
	defer func() { spotifyClient = originalClient }()

	result, err := ClaimDevice(context.Background(), "Living Room Speakers")
	if err != nil {
		t.Fatalf("ClaimDevice: %v", err)
	}
	if !result.AlreadyActive {
		t.Error("expected AlreadyActive=true for device already in cloud list")
	}
	if result.DeviceID != "abc123" {
		t.Errorf("DeviceID=%s", result.DeviceID)
	}
}

// TestHandleLANDevicesRequest_Success verifies the endpoint returns the
// mDNS-cached devices as JSON. We swap the package-level discovery cache's
// discoverer for a fake so the test doesn't touch the network.
func TestHandleLANDevicesRequest_Success(t *testing.T) {
	originalToken := apiAccessToken
	apiAccessToken = "test-token"
	defer func() { apiAccessToken = originalToken }()

	originalCache := defaultDiscoveryCache
	defaultDiscoveryCache = NewDiscoveryCache(&fakeDiscoverer{
		devices: []LocalDevice{
			{InstanceName: "FF98", Hostname: "Living-Room-Speakers.local.", FriendlyName: "Living Room Speakers", IP: "192.168.1.3", Port: 5356},
			{InstanceName: "sonosRINCON", Hostname: "Sonos-X.local.", FriendlyName: "Sonos-X", IP: "192.168.1.7", Port: 1400},
		},
	}, time.Minute)
	defer func() { defaultDiscoveryCache = originalCache }()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/lan-devices?token=test-token", nil)
	w := httptest.NewRecorder()
	HandleLANDevicesRequest(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}

	var resp LANDevicesResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.Success {
		t.Errorf("expected success, got error %q", resp.Error)
	}
	if len(resp.Devices) != 2 {
		t.Fatalf("expected 2 devices, got %d", len(resp.Devices))
	}
	if resp.Devices[0].Name != "Living Room Speakers" || resp.Devices[0].IP != "192.168.1.3" {
		t.Errorf("unexpected first device: %+v", resp.Devices[0])
	}
}

// TestHandleNextRequest_Success exercises the happy path: a valid token
// triggers a Next() call on the underlying client.
func TestHandleNextRequest_Success(t *testing.T) {
	called := false
	mock := &MockSpotifyClient{
		NextFunc: func(ctx context.Context) error {
			called = true
			return nil
		},
	}
	originalClient := spotifyClient
	originalToken := apiAccessToken
	spotifyClient = mock
	apiAccessToken = "test-token"
	defer func() {
		spotifyClient = originalClient
		apiAccessToken = originalToken
	}()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/next?token=test-token", nil)
	w := httptest.NewRecorder()
	HandleNextRequest(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if !called {
		t.Error("expected Next to be called")
	}
}

// TestHandleNextRequest_Unauthorized rejects requests without the API token.
func TestHandleNextRequest_Unauthorized(t *testing.T) {
	originalToken := apiAccessToken
	apiAccessToken = "test-token"
	defer func() { apiAccessToken = originalToken }()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/next", nil)
	w := httptest.NewRecorder()
	HandleNextRequest(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

// TestHandleNextRequest_SpotifyError surfaces the upstream error when
// Spotify rejects the skip (e.g. nothing playing).
func TestHandleNextRequest_SpotifyError(t *testing.T) {
	mock := &MockSpotifyClient{
		NextFunc: func(ctx context.Context) error {
			return errors.New("nothing currently playing")
		},
	}
	originalClient := spotifyClient
	originalToken := apiAccessToken
	spotifyClient = mock
	apiAccessToken = "test-token"
	defer func() {
		spotifyClient = originalClient
		apiAccessToken = originalToken
	}()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/next?token=test-token", nil)
	w := httptest.NewRecorder()
	HandleNextRequest(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", w.Code)
	}
}

// TestHandleVolumeRequest_NoDevice routes through the active-device path —
// no `device` param means we call Volume(), not VolumeOpt().
func TestHandleVolumeRequest_NoDevice(t *testing.T) {
	called := false
	mock := &MockSpotifyClient{
		VolumeFunc: func(ctx context.Context, percent int) error {
			called = true
			if percent != 60 {
				t.Errorf("percent=%d, want 60", percent)
			}
			return nil
		},
	}
	originalClient := spotifyClient
	originalToken := apiAccessToken
	spotifyClient = mock
	apiAccessToken = "test-token"
	defer func() {
		spotifyClient = originalClient
		apiAccessToken = originalToken
	}()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/volume?token=test-token&level=60", nil)
	w := httptest.NewRecorder()
	HandleVolumeRequest(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if !called {
		t.Error("expected Volume to be called")
	}
}

// TestHandleVolumeRequest_WithDevice resolves the device by name from the
// cloud devices list and calls VolumeOpt with the matched ID.
func TestHandleVolumeRequest_WithDevice(t *testing.T) {
	var capturedDeviceID spotifyLib.ID
	mock := &MockSpotifyClient{
		PlayerDevicesFunc: func(ctx context.Context) ([]spotifyLib.PlayerDevice, error) {
			return []spotifyLib.PlayerDevice{
				{ID: "dev-living", Name: "Living Room Speakers"},
				{ID: "dev-pool", Name: "Pool Speakers"},
			}, nil
		},
		VolumeOptFunc: func(ctx context.Context, percent int, opt *spotifyLib.PlayOptions) error {
			if opt != nil && opt.DeviceID != nil {
				capturedDeviceID = *opt.DeviceID
			}
			return nil
		},
	}
	originalClient := spotifyClient
	originalToken := apiAccessToken
	spotifyClient = mock
	apiAccessToken = "test-token"
	defer func() {
		spotifyClient = originalClient
		apiAccessToken = originalToken
	}()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/volume?token=test-token&level=80&device=Living+Room+Speakers", nil)
	w := httptest.NewRecorder()
	HandleVolumeRequest(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if capturedDeviceID != "dev-living" {
		t.Errorf("VolumeOpt called with deviceID=%q, want dev-living", capturedDeviceID)
	}
}

// TestHandleVolumeRequest_InvalidLevel covers both missing and out-of-range.
func TestHandleVolumeRequest_InvalidLevel(t *testing.T) {
	originalToken := apiAccessToken
	apiAccessToken = "test-token"
	defer func() { apiAccessToken = originalToken }()

	cases := []struct {
		query  string
		status int
	}{
		{"token=test-token", http.StatusBadRequest},                // missing
		{"token=test-token&level=abc", http.StatusBadRequest},      // not int
		{"token=test-token&level=200", http.StatusInternalServerError}, // > 100
		{"token=test-token&level=-1", http.StatusInternalServerError},  // < 0
	}
	for _, tc := range cases {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/volume?"+tc.query, nil)
		w := httptest.NewRecorder()
		HandleVolumeRequest(w, req)
		if w.Code != tc.status {
			t.Errorf("query=%q: got status %d, want %d", tc.query, w.Code, tc.status)
		}
	}
}

// TestHandleVolumeRequest_DeviceNotInCloud returns a clear error if the
// requested device isn't currently linked to the user's account.
func TestHandleVolumeRequest_DeviceNotInCloud(t *testing.T) {
	mock := &MockSpotifyClient{
		PlayerDevicesFunc: func(ctx context.Context) ([]spotifyLib.PlayerDevice, error) {
			return []spotifyLib.PlayerDevice{
				{ID: "dev-pool", Name: "Pool Speakers"},
			}, nil
		},
	}
	originalClient := spotifyClient
	originalToken := apiAccessToken
	spotifyClient = mock
	apiAccessToken = "test-token"
	defer func() {
		spotifyClient = originalClient
		apiAccessToken = originalToken
	}()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/volume?token=test-token&level=50&device=Master+Bedroom+Speakers", nil)
	w := httptest.NewRecorder()
	HandleVolumeRequest(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", w.Code)
	}

	var resp APIResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if !strings.Contains(resp.Error, "wake first") {
		t.Errorf("expected hint to /wake, got: %s", resp.Error)
	}
}

// TestHandleVolumeRequest_Unauthorized rejects requests without the API token.
func TestHandleVolumeRequest_Unauthorized(t *testing.T) {
	originalToken := apiAccessToken
	apiAccessToken = "test-token"
	defer func() { apiAccessToken = originalToken }()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/volume?level=50", nil)
	w := httptest.NewRecorder()
	HandleVolumeRequest(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

// TestHandlePlaylistsRequest_Success exercises the happy path with a mock
// that returns a single page of playlists.
func TestHandlePlaylistsRequest_Success(t *testing.T) {
	mock := &MockSpotifyClient{
		CurrentUsersPlaylistsFunc: func(ctx context.Context, opts ...spotifyLib.RequestOption) (*spotifyLib.SimplePlaylistPage, error) {
			// Build a page directly by JSON unmarshal so we can set
			// Tracks.Total without poking unexported fields.
			page := &spotifyLib.SimplePlaylistPage{
				Playlists: []spotifyLib.SimplePlaylist{
					{
						ID:    spotifyLib.ID("plid1"),
						Name:  "Dance",
						Owner: spotifyLib.User{DisplayName: "Spicer"},
					},
				},
			}
			page.Playlists[0].Tracks.Total = 42
			return page, nil
		},
	}

	originalClient := spotifyClient
	originalToken := apiAccessToken
	spotifyClient = mock
	apiAccessToken = "test-token"
	defer func() {
		spotifyClient = originalClient
		apiAccessToken = originalToken
	}()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/playlists?token=test-token", nil)
	w := httptest.NewRecorder()
	HandlePlaylistsRequest(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}

	var resp PlaylistsResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.Success {
		t.Errorf("expected success, got %q", resp.Error)
	}
	if len(resp.Playlists) != 1 {
		t.Fatalf("expected 1 playlist, got %d", len(resp.Playlists))
	}
	if resp.Playlists[0].ID != "plid1" || resp.Playlists[0].Name != "Dance" || resp.Playlists[0].Owner != "Spicer" || resp.Playlists[0].Tracks != 42 {
		t.Errorf("unexpected playlist: %+v", resp.Playlists[0])
	}
}

// TestHandlePlaylistsRequest_Unauthorized rejects requests without the API token.
func TestHandlePlaylistsRequest_Unauthorized(t *testing.T) {
	originalToken := apiAccessToken
	apiAccessToken = "test-token"
	defer func() { apiAccessToken = originalToken }()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/playlists", nil)
	w := httptest.NewRecorder()
	HandlePlaylistsRequest(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

// TestHandleLANDevicesRequest_Unauthorized rejects requests without the API token.
func TestHandleLANDevicesRequest_Unauthorized(t *testing.T) {
	originalToken := apiAccessToken
	apiAccessToken = "test-token"
	defer func() { apiAccessToken = originalToken }()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/lan-devices", nil)
	w := httptest.NewRecorder()
	HandleLANDevicesRequest(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

// TestClaimDevice_NotAuthenticated returns a clear error if no Spotify
// client has been set yet.
func TestClaimDevice_NotAuthenticated(t *testing.T) {
	originalClient := spotifyClient
	spotifyClient = nil
	defer func() { spotifyClient = originalClient }()

	_, err := ClaimDevice(context.Background(), "Anything")
	if err == nil {
		t.Fatal("expected error when not authenticated")
	}
}

// ---------------------------------------------------------------------------
// /api/v1/ask: Jev client, WiiM client, speaker directory, text-to-action.
// ---------------------------------------------------------------------------

// fakeJev returns canned answers by question key. Questions without a canned
// answer get the last option (NONE / other) for choices and 0 for yes/no.
type fakeJev struct {
	mu      sync.Mutex
	answers map[string]JevAnswer
	// picks, when set, answers successive "pick" questions in order.
	picks []JevAnswer
	calls []map[string]JevQuestion
	err   error
}

// Ask records the questions and returns the canned answers.
func (f *fakeJev) Ask(ctx context.Context, state string, questions map[string]JevQuestion) (map[string]JevAnswer, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, questions)
	if f.err != nil {
		return nil, f.err
	}
	out := map[string]JevAnswer{}
	for key, q := range questions {
		if key == "pick" && len(f.picks) > 0 {
			out[key] = f.picks[0]
			f.picks = f.picks[1:]
			continue
		}
		if a, ok := f.answers[key]; ok {
			out[key] = a
			continue
		}
		if q.Type == "noul" {
			out[key] = JevAnswer{Type: "noul"}
			continue
		}
		out[key] = jevPick(q.Options[len(q.Options)-1].Key, 1)
	}
	return out, nil
}

// jevPick builds a choice answer with one option at probability p.
func jevPick(choice string, p float64) JevAnswer {
	return JevAnswer{Type: "choice", Choice: choice, Probabilities: map[string]float64{choice: p}}
}

// jevYes builds a yes/no answer.
func jevYes(p float64) JevAnswer {
	return JevAnswer{Type: "noul", Noul: p}
}

// fakeWiiM is an in-memory WiiMClient keyed by host.
type fakeWiiM struct {
	mu       sync.Mutex
	names    map[string]string
	statuses map[string]WiiMStatus
	commands []string
	failCmd  map[string]bool
}

// newFakeWiiM returns an empty fake.
func newFakeWiiM() *fakeWiiM {
	return &fakeWiiM{names: map[string]string{}, statuses: map[string]WiiMStatus{}, failCmd: map[string]bool{}}
}

// DeviceName returns the configured name, or an error for non-WiiM hosts.
func (f *fakeWiiM) DeviceName(ctx context.Context, host string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	name, ok := f.names[host]
	if !ok {
		return "", errors.New("not a wiim")
	}
	return name, nil
}

// Status returns the configured status, or an error for unknown hosts.
func (f *fakeWiiM) Status(ctx context.Context, host string) (WiiMStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	st, ok := f.statuses[host]
	if !ok {
		return WiiMStatus{}, errors.New("no status")
	}
	return st, nil
}

// Command records "host cmd" and fails for hosts marked in failCmd.
func (f *fakeWiiM) Command(ctx context.Context, host, cmd string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.commands = append(f.commands, host+" "+cmd)
	if f.failCmd[host] {
		return errors.New("speaker said no")
	}
	return nil
}

// askTestEnv holds the fakes that stand in for every global Ask uses.
type askTestEnv struct {
	spotify *MockSpotifyClient
	jev     *fakeJev
	wiim    *fakeWiiM
	dir     *SpeakerDirectory
}

// newAskTestEnv swaps the Spotify client, Jev client, WiiM client, speaker
// directory and playlist cache for fakes, and restores them after the test.
func newAskTestEnv(t *testing.T, speakers ...Speaker) *askTestEnv {
	t.Helper()
	env := &askTestEnv{
		spotify: &MockSpotifyClient{},
		jev:     &fakeJev{answers: map[string]JevAnswer{}},
		wiim:    newFakeWiiM(),
		dir: NewSpeakerDirectory(
			func(ctx context.Context) ([]LocalDevice, error) { return nil, nil },
			func(ctx context.Context, d LocalDevice) (*GetInfoResponse, error) { return nil, errors.New("offline") },
		),
	}
	for _, s := range speakers {
		env.dir.speakers[speakerKey(s)] = knownSpeaker{Speaker: s, LastSeen: time.Now()}
	}
	env.dir.refreshed = true

	origSpotify, origJev, origWiiM, origDir, origDelay := spotifyClient, jevClient, wiimClient, speakerDirectory, shuffleSettleDelay
	spotifyClient, jevClient, wiimClient, speakerDirectory, shuffleSettleDelay = env.spotify, env.jev, env.wiim, env.dir, 0
	resetPlaylistCache()
	t.Cleanup(func() {
		spotifyClient, jevClient, wiimClient, speakerDirectory, shuffleSettleDelay = origSpotify, origJev, origWiiM, origDir, origDelay
		resetPlaylistCache()
	})
	return env
}

// resetPlaylistCache empties the playlist cache so each test fetches fresh.
func resetPlaylistCache() {
	playlistCache.mu.Lock()
	playlistCache.items, playlistCache.expires = nil, time.Time{}
	playlistCache.mu.Unlock()
}

// testSpeakers is a small house: three WiiM speakers and a web player.
var testSpeakers = []Speaker{
	{Name: "Living Room Speakers", SpotifyID: "b17f0fc4ad18d8b905f2fd820475af7dce958dd0", IP: "10.0.0.2", WiiM: true},
	{Name: "Pool Speakers", SpotifyID: "3b116b85e9c3d8aeb9ab5d04794260762e3b0d0a", IP: "10.0.0.1", WiiM: true},
	{Name: "Pool Porch Speakers", SpotifyID: "b3aa73e13ba4d562791bede9deff67e9ce7635b2", IP: "10.0.0.3", WiiM: true},
	{Name: "Web Player (Chrome)", SpotifyID: "webplayer1"},
}

// TestJevOptions_MarshalKeepsOrder checks options keep their listed order
// and that empty descriptions are sent as null.
func TestJevOptions_MarshalKeepsOrder(t *testing.T) {
	raw, err := json.Marshal(JevOptions{{Key: "zebra"}, {Key: "apple", Description: "a fruit"}, {Key: noneOption, Description: "none"}})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"zebra":null,"apple":"a fruit","NONE":"none"}`
	if string(raw) != want {
		t.Errorf("got %s, want %s", raw, want)
	}

	q, _ := json.Marshal(JevYesNo("Is it loud?"))
	if strings.Contains(string(q), "criteria") {
		t.Errorf("yes/no question should not send criteria: %s", q)
	}
}

// TestJevAnswer_RunnerUp returns the second most likely option.
func TestJevAnswer_RunnerUp(t *testing.T) {
	a := JevAnswer{Choice: "Pool Speakers", Probabilities: map[string]float64{
		"Pool Speakers": 0.55, "Pool Porch Speakers": 0.4, "NONE": 0.05,
	}}
	if key, p := a.Top(); key != "Pool Speakers" || p != 0.55 {
		t.Errorf("Top = %s %.2f", key, p)
	}
	if key, p := a.RunnerUp(); key != "Pool Porch Speakers" || p != 0.4 {
		t.Errorf("RunnerUp = %s %.2f", key, p)
	}
	if key, _ := (JevAnswer{Choice: "x", Probabilities: map[string]float64{"x": 1}}).RunnerUp(); key != "" {
		t.Errorf("RunnerUp with one option = %q", key)
	}
}

// TestTypesafeClient_Ask_Success sends the key, model and questions and
// decodes the answers.
func TestTypesafeClient_Ask_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("Authorization = %q", got)
		}
		var body struct {
			State     string                     `json:"state"`
			Model     string                     `json:"model"`
			Questions map[string]json.RawMessage `json:"questions"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		want := `{"type":"choice","instructions":"What?","criteria":{"play":null,"other":null}}`
		if body.Model != DefaultJevModel || body.State != "Play Green Day" || string(body.Questions["intent"]) != want {
			t.Errorf("unexpected body: %+v", body)
		}
		fmt.Fprint(w, `{"model":"jev-1.13.0","answers":{"intent":{"type":"choice","choice":"play","probabilities":{"play":0.9,"other":0.1},"confidence":0.8}}}`)
	}))
	defer srv.Close()

	c := &typesafeClient{apiKey: "test-key", model: DefaultJevModel, endpoint: srv.URL, http: srv.Client()}
	answers, err := c.Ask(context.Background(), "Play Green Day", map[string]JevQuestion{
		"intent": JevChoice("What?", JevOptions{{Key: "play"}, {Key: "other"}}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if key, p := answers["intent"].Top(); key != "play" || p != 0.9 {
		t.Errorf("intent = %s %.2f", key, p)
	}
}

// TestTypesafeClient_Ask_RetriesWhenBusy retries a 429 and then succeeds.
func TestTypesafeClient_Ask_RetriesWhenBusy(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.Header().Set("Retry-After", "0.01")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		fmt.Fprint(w, `{"answers":{"q":{"type":"noul","noul":0.9}}}`)
	}))
	defer srv.Close()

	c := &typesafeClient{apiKey: "k", model: "m", endpoint: srv.URL, http: srv.Client()}
	answers, err := c.Ask(context.Background(), "x", map[string]JevQuestion{"q": JevYesNo("?")})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || answers["q"].Noul != 0.9 {
		t.Errorf("calls=%d answer=%+v", calls, answers["q"])
	}
}

// TestTypesafeClient_Ask_ValidationError does not retry a 422.
func TestTypesafeClient_Ask_ValidationError(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusUnprocessableEntity)
		fmt.Fprint(w, `{"detail":"bad question"}`)
	}))
	defer srv.Close()

	c := &typesafeClient{apiKey: "k", model: "m", endpoint: srv.URL, http: srv.Client()}
	_, err := c.Ask(context.Background(), "x", map[string]JevQuestion{"q": JevYesNo("?")})
	if err == nil || !strings.Contains(err.Error(), "422") {
		t.Fatalf("expected 422 error, got %v", err)
	}
	if calls != 1 {
		t.Errorf("expected 1 call, got %d", calls)
	}
}

// TestDecodeLinkPlayText decodes hex text and drops the "Unknown" filler.
func TestDecodeLinkPlayText(t *testing.T) {
	cases := map[string]string{
		"4C657373205468616E204A616B65": "Less Than Jake",
		"556E6B6E6F776E":               "",
		"Plain Title":                  "Plain Title",
		"":                             "",
	}
	for in, want := range cases {
		if got := decodeLinkPlayText(in); got != want {
			t.Errorf("decodeLinkPlayText(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestLinkPlayClient talks to a fake WiiM speaker over HTTPS with a
// self-signed certificate, like the real ones.
func TestLinkPlayClient(t *testing.T) {
	var commands []string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cmd := r.URL.Query().Get("command")
		switch {
		case cmd == "getStatusEx":
			fmt.Fprint(w, `{"DeviceName":"Pool Speakers","firmware":"Linkplay.5.2"}`)
		case cmd == "getPlayerStatus":
			fmt.Fprint(w, `{"status":"play","Title":"4A6F686E6E79","Artist":"4C657373205468616E204A616B65","vol":"41"}`)
		case cmd == "setPlayerCmd:bogus":
			fmt.Fprint(w, "unknown command")
		case strings.HasPrefix(cmd, "setPlayerCmd:"):
			commands = append(commands, cmd)
			fmt.Fprint(w, "OK")
		}
	}))
	defer srv.Close()

	host := srv.Listener.Addr().String()
	c := newLinkPlayClient()
	ctx := context.Background()

	name, err := c.DeviceName(ctx, host)
	if err != nil || name != "Pool Speakers" {
		t.Errorf("DeviceName = %q, %v", name, err)
	}

	st, err := c.Status(ctx, host)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Playing() || st.Title != "Johnny" || st.Artist != "Less Than Jake" || st.Volume != 41 {
		t.Errorf("Status = %+v", st)
	}

	if err := c.Command(ctx, host, "vol:30"); err != nil {
		t.Errorf("Command: %v", err)
	}
	if len(commands) != 1 || commands[0] != "setPlayerCmd:vol:30" {
		t.Errorf("commands = %v", commands)
	}
	if err := c.Command(ctx, host, "bogus"); err == nil {
		t.Error("expected an error when the speaker doesn't answer OK")
	}
}

// TestSpeakerDirectory_Refresh names speakers from LAN probes, skips
// devices that only have an ID, and merges in named cloud devices.
func TestSpeakerDirectory_Refresh(t *testing.T) {
	env := newAskTestEnv(t)
	env.wiim.names["10.0.0.1"] = "Pool Speakers"

	poolID := "3b116b85e9c3d8aeb9ab5d04794260762e3b0d0a"
	infos := map[string]*GetInfoResponse{
		"10.0.0.1":   {DeviceID: poolID},
		"10.0.0.94":  {DeviceID: "9c7bf5813899b81ad823d09f775f629d497ba3ae"},
		"10.0.2.111": {DeviceID: "25efbf002f8d7c51c8b98001728059868529510a", RemoteName: "Philips 4K A1"},
	}
	dir := NewSpeakerDirectory(
		func(ctx context.Context) ([]LocalDevice, error) {
			return []LocalDevice{
				{FriendlyName: "Pool Speakers", IP: "10.0.0.1", Port: 5356},
				{FriendlyName: "532500127182", IP: "10.0.0.94", Port: 46529},
				{FriendlyName: "8dd325374c176f2f", IP: "10.0.2.111", Port: 42563},
				{FriendlyName: "none 2", IP: "10.0.2.110", Port: 4070},
			}, nil
		},
		func(ctx context.Context, d LocalDevice) (*GetInfoResponse, error) {
			if info, ok := infos[d.IP]; ok {
				return info, nil
			}
			return nil, errors.New("no zeroconf")
		},
	)
	speakerDirectory = dir

	env.spotify.PlayerDevicesFunc = func(ctx context.Context) ([]spotifyLib.PlayerDevice, error) {
		return []spotifyLib.PlayerDevice{
			{ID: spotifyLib.ID(poolID), Name: poolID},
			{ID: "webplayer1", Name: "Web Player (Chrome)"},
			{ID: "bab6dbc5d7f732c9ad4ff68675134c04facba32a", Name: "bab6dbc5d7f732c9ad4ff68675134c04facba32a"},
		}, nil
	}

	if err := dir.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}

	var names []string
	for _, s := range dir.List() {
		names = append(names, s.Name)
	}
	want := "Philips 4K A1, Pool Speakers, Web Player (Chrome)"
	if got := strings.Join(names, ", "); got != want {
		t.Errorf("speakers = %s, want %s", got, want)
	}

	pool, ok := dir.Find("pool speakers")
	if !ok || pool.SpotifyID != poolID || pool.WiiMHost() != "10.0.0.1" || pool.ZeroconfPort != 5356 {
		t.Errorf("pool = %+v", pool)
	}

	hexDevice := spotifyLib.PlayerDevice{ID: spotifyLib.ID(poolID), Name: poolID}
	if got := deviceDisplayName(hexDevice); got != "Pool Speakers" {
		t.Errorf("deviceDisplayName = %q", got)
	}
	if !deviceMatches(hexDevice, "Pool Speakers") {
		t.Error("deviceMatches should match the hex device by its friendly name")
	}
}

// TestSpeakerDirectory_RechecksKnownSpeakers keeps a saved speaker that the
// scan missed when it still answers at its address, and drops one whose
// address now belongs to a different device.
func TestSpeakerDirectory_RechecksKnownSpeakers(t *testing.T) {
	newAskTestEnv(t)
	dir := NewSpeakerDirectory(
		func(ctx context.Context) ([]LocalDevice, error) { return nil, errors.New("mdns missed everything") },
		func(ctx context.Context, d LocalDevice) (*GetInfoResponse, error) {
			switch d.IP {
			case "10.0.0.1":
				return &GetInfoResponse{DeviceID: "aaaaaaaaaaaa"}, nil
			case "10.0.0.2":
				return &GetInfoResponse{DeviceID: "cccccccccccc"}, nil
			}
			return nil, errors.New("offline")
		},
	)
	stale := time.Now().Add(-8 * 24 * time.Hour)
	dir.speakers["aaaaaaaaaaaa"] = knownSpeaker{Speaker: Speaker{Name: "Pool Speakers", SpotifyID: "aaaaaaaaaaaa", IP: "10.0.0.1", ZeroconfPort: 5356}, LastSeen: stale}
	dir.speakers["bbbbbbbbbbbb"] = knownSpeaker{Speaker: Speaker{Name: "Garage Speakers", SpotifyID: "bbbbbbbbbbbb", IP: "10.0.0.2", ZeroconfPort: 5356}, LastSeen: stale}

	_ = dir.Refresh(context.Background())

	if _, ok := dir.Find("Pool Speakers"); !ok {
		t.Error("Pool Speakers answered at its saved address and should be kept")
	}
	if _, ok := dir.Find("Garage Speakers"); ok {
		t.Error("Garage Speakers' address now holds another device; it should age out")
	}
}

// TestSpeakerDirectory_CacheRoundTrip saves the directory and loads it
// into a fresh one.
func TestSpeakerDirectory_CacheRoundTrip(t *testing.T) {
	newAskTestEnv(t)
	path := filepath.Join(t.TempDir(), "speakers.json")

	dir := NewSpeakerDirectory(
		func(ctx context.Context) ([]LocalDevice, error) {
			return []LocalDevice{{FriendlyName: "Pool Speakers", IP: "10.0.0.1", Port: 5356}}, nil
		},
		func(ctx context.Context, d LocalDevice) (*GetInfoResponse, error) {
			return &GetInfoResponse{DeviceID: "3b116b85e9c3d8aeb9ab5d04794260762e3b0d0a"}, nil
		},
	)
	dir.enableCache(path)
	if err := dir.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}

	loaded := NewSpeakerDirectory(nil, nil)
	loaded.enableCache(path)
	got, ok := loaded.Find("Pool Speakers")
	if !ok || got.SpotifyID != "3b116b85e9c3d8aeb9ab5d04794260762e3b0d0a" || got.IP != "10.0.0.1" {
		t.Errorf("loaded = %+v, %v", got, ok)
	}
	if name := loaded.NameForID("3b116b85e9c3d8aeb9ab5d04794260762e3b0d0a"); name != "Pool Speakers" {
		t.Errorf("NameForID = %q", name)
	}
}

// TestRequestSpans offers every short run of words, without punctuation
// or duplicates, and stays inside Jev's option limit.
func TestRequestSpans(t *testing.T) {
	spans := requestSpans(`Play "99 Red Balloons" by Green Day, please!`)
	has := map[string]bool{}
	for _, s := range spans {
		if has[strings.ToLower(s)] {
			t.Errorf("duplicate span %q", s)
		}
		has[strings.ToLower(s)] = true
	}
	for _, want := range []string{"99 Red Balloons", "Green Day", "please"} {
		if !has[strings.ToLower(want)] {
			t.Errorf("missing span %q in %v", want, spans)
		}
	}
	if has[`"99`] || has["day,"] {
		t.Error("punctuation should be trimmed from words")
	}

	long := strings.Repeat("word another thing ", 40)
	if n := len(requestSpans(long + "unique1 unique2 unique3 unique4 unique5 unique6 unique7")); n > maxSpans {
		t.Errorf("got %d spans, max is %d", n, maxSpans)
	}
}

// TestReadSpeaker picks a clear winner, asks on a close call between two
// speakers, and treats NONE as no speaker.
func TestReadSpeaker(t *testing.T) {
	clear := JevAnswer{Choice: "Pool Speakers", Probabilities: map[string]float64{"Pool Speakers": 0.9, "Pool Porch Speakers": 0.1}}
	if s, unsure := readSpeaker(clear); s != "Pool Speakers" || unsure != nil {
		t.Errorf("clear: %q %v", s, unsure)
	}

	close := JevAnswer{Choice: "Pool Speakers", Probabilities: map[string]float64{"Pool Speakers": 0.61, "Pool Porch Speakers": 0.31, "NONE": 0.08}}
	if s, unsure := readSpeaker(close); s != "" || len(unsure) != 2 || unsure[1] != "Pool Porch Speakers" {
		t.Errorf("close: %q %v", s, unsure)
	}

	none := JevAnswer{Choice: noneOption, Probabilities: map[string]float64{noneOption: 0.6, "Pool Speakers": 0.4}}
	if s, unsure := readSpeaker(none); s != "" || unsure != nil {
		t.Errorf("none: %q %v", s, unsure)
	}
}

// TestInterpret_ControlBecomesPlayWhenMusicIsNamed reads "Play less than
// Jake" as play even when Jev's intent leans toward "volume down".
func TestInterpret_ControlBecomesPlayWhenMusicIsNamed(t *testing.T) {
	env := newAskTestEnv(t)
	env.jev.answers = map[string]JevAnswer{
		"intent":      jevPick(intentVolumeDown, 0.5),
		"wants_music": jevYes(0.55),
		"artist":      jevPick("less than Jake", 1),
		"kind":        jevPick(kindArtist, 0.9),
	}
	req, err := interpret(context.Background(), "Play less than Jake", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if req.Intent != intentPlay || req.Artist != "less than Jake" {
		t.Errorf("req = %+v", req)
	}

	env.jev.answers = map[string]JevAnswer{
		"intent":      jevPick(intentVolumeDown, 0.9),
		"wants_music": jevYes(0.03),
	}
	req, _ = interpret(context.Background(), "Turn it down a little", nil, nil)
	if req.Intent != intentVolumeDown {
		t.Errorf("plain volume request became %s", req.Intent)
	}
}

// TestInterpret_DedupesNamesAndLowConfidence clears a name given as both
// artist and song, and treats a weak intent as not understood.
func TestInterpret_DedupesNamesAndLowConfidence(t *testing.T) {
	env := newAskTestEnv(t)
	env.jev.answers = map[string]JevAnswer{
		"intent": jevPick(intentPlay, 0.95),
		"kind":   jevPick(kindArtist, 0.9),
		"artist": jevPick("Green Day", 1),
		"song":   jevPick("Green Day", 0.7),
	}
	req, _ := interpret(context.Background(), "Play Green Day", nil, nil)
	if req.Artist != "Green Day" || req.Song != "" {
		t.Errorf("artist=%q song=%q", req.Artist, req.Song)
	}

	env.jev.answers = map[string]JevAnswer{"intent": jevPick(intentPlay, 0.3)}
	req, _ = interpret(context.Background(), "Blorp", nil, nil)
	if req.Intent != intentOther {
		t.Errorf("weak intent = %s", req.Intent)
	}
}

// TestAsk_PlayWithoutSpeaker refuses to guess the room.
func TestAsk_PlayWithoutSpeaker(t *testing.T) {
	env := newAskTestEnv(t, testSpeakers...)
	env.jev.answers = map[string]JevAnswer{
		"intent": jevPick(intentPlay, 0.9),
		"kind":   jevPick(kindArtist, 0.9),
		"artist": jevPick("less than Jake", 1),
	}
	env.spotify.PlayOptFunc = func(ctx context.Context, opts *spotifyLib.PlayOptions) error {
		t.Error("nothing should play without a speaker")
		return nil
	}

	res, err := Ask(context.Background(), "Play less than Jake", false)
	if err != nil {
		t.Fatal(err)
	}
	want := "I can't play less than Jake because you didn't say which speaker."
	if res.ActionTaken || res.Message != want || res.Intent != intentPlay {
		t.Errorf("res = %+v", res)
	}
}

// TestAsk_PlayAmbiguousSpeaker asks which of two speakers was meant.
func TestAsk_PlayAmbiguousSpeaker(t *testing.T) {
	env := newAskTestEnv(t, testSpeakers...)
	env.jev.answers = map[string]JevAnswer{
		"intent": jevPick(intentPlay, 0.9),
		"speaker": {Choice: "Pool Speakers", Probabilities: map[string]float64{
			"Pool Speakers": 0.55, "Pool Porch Speakers": 0.4, noneOption: 0.05,
		}},
	}
	res, _ := Ask(context.Background(), "Play Green Day on the pull house speakers", false)
	if res.ActionTaken || res.Message != "Did you mean Pool Speakers or Pool Porch Speakers?" {
		t.Errorf("res = %+v", res)
	}
}

// playlistAnswers is the Jev reading of "play my coding mix in the living room".
func playlistAnswers() map[string]JevAnswer {
	return map[string]JevAnswer{
		"intent":   jevPick(intentPlay, 1),
		"speaker":  jevPick("Living Room Speakers", 1),
		"kind":     jevPick(kindPlaylist, 0.98),
		"playlist": jevPick("Coding Mix", 1),
	}
}

// codingMixPlaylists is the mock account's playlist list.
func codingMixPlaylists(ctx context.Context, opts ...spotifyLib.RequestOption) (*spotifyLib.SimplePlaylistPage, error) {
	return &spotifyLib.SimplePlaylistPage{Playlists: []spotifyLib.SimplePlaylist{
		{ID: "punk", Name: "Punk Mix", URI: "spotify:playlist:punk"},
		{ID: "coding", Name: "Coding Mix", URI: "spotify:playlist:coding"},
	}}, nil
}

// TestAsk_PlayPlaylistStartsAtBeginning plays track one with shuffle off
// on the speaker's device, found by the ID behind its hex name.
func TestAsk_PlayPlaylistStartsAtBeginning(t *testing.T) {
	env := newAskTestEnv(t, testSpeakers...)
	env.jev.answers = playlistAnswers()
	env.spotify.CurrentUsersPlaylistsFunc = codingMixPlaylists
	lrID := spotifyLib.ID(testSpeakers[0].SpotifyID)
	env.spotify.PlayerDevicesFunc = func(ctx context.Context) ([]spotifyLib.PlayerDevice, error) {
		return []spotifyLib.PlayerDevice{{ID: lrID, Name: string(lrID)}}, nil
	}

	var played *spotifyLib.PlayOptions
	env.spotify.PlayOptFunc = func(ctx context.Context, opts *spotifyLib.PlayOptions) error {
		played = opts
		return nil
	}
	shuffleSet := map[bool]bool{}
	env.spotify.ShuffleOptFunc = func(ctx context.Context, shuffle bool, opt *spotifyLib.PlayOptions) error {
		shuffleSet[shuffle] = true
		return nil
	}

	res, err := Ask(context.Background(), "Play my coding mix on the living room speakers", false)
	if err != nil {
		t.Fatal(err)
	}
	if !res.ActionTaken || res.Message != "Playing Coding Mix on Living Room Speakers." {
		t.Errorf("res = %+v", res)
	}
	if played == nil || *played.DeviceID != lrID || *played.PlaybackContext != "spotify:playlist:coding" {
		t.Fatalf("played = %+v", played)
	}
	if played.PlaybackOffset == nil || *played.PlaybackOffset.Position != 0 {
		t.Error("playlist should start at track one")
	}
	if !shuffleSet[false] || shuffleSet[true] {
		t.Errorf("shuffle calls = %v, want only false", shuffleSet)
	}
}

// TestAsk_ShuffleWordTurnsShuffleOn shuffles only when the person says so.
func TestAsk_ShuffleWordTurnsShuffleOn(t *testing.T) {
	env := newAskTestEnv(t, testSpeakers...)
	env.jev.answers = playlistAnswers()
	env.spotify.CurrentUsersPlaylistsFunc = codingMixPlaylists
	env.spotify.PlayerDevicesFunc = func(ctx context.Context) ([]spotifyLib.PlayerDevice, error) {
		return []spotifyLib.PlayerDevice{{ID: spotifyLib.ID(testSpeakers[0].SpotifyID), Name: "Living Room Speakers"}}, nil
	}
	gotShuffle := false
	env.spotify.ShuffleOptFunc = func(ctx context.Context, shuffle bool, opt *spotifyLib.PlayOptions) error {
		gotShuffle = shuffle
		return nil
	}

	res, _ := Ask(context.Background(), "Shuffle my coding mix in the living room", false)
	if !gotShuffle || res.Message != "Shuffling Coding Mix on Living Room Speakers." {
		t.Errorf("shuffle=%v res=%+v", gotShuffle, res)
	}
}

// trackResults returns search results for "99 Red Balloons".
func trackResults(ctx context.Context, query string, st spotifyLib.SearchType) (*spotifyLib.SearchResult, error) {
	track := func(name, artist, uri string) spotifyLib.FullTrack {
		return spotifyLib.FullTrack{SimpleTrack: spotifyLib.SimpleTrack{
			Name: name, URI: spotifyLib.URI(uri), Artists: []spotifyLib.SimpleArtist{{Name: artist}},
		}}
	}
	return &spotifyLib.SearchResult{Tracks: &spotifyLib.FullTrackPage{Tracks: []spotifyLib.FullTrack{
		track("99 Luftballons", "Nena", "spotify:track:nena"),
		track("99 Red Balloons", "Goldfinger", "spotify:track:goldfinger"),
	}}}, nil
}

// TestAsk_PlaySongPicksSearchResult plays the search result Jev picks.
func TestAsk_PlaySongPicksSearchResult(t *testing.T) {
	env := newAskTestEnv(t, testSpeakers...)
	env.jev.answers = map[string]JevAnswer{
		"intent":  jevPick(intentPlay, 1),
		"speaker": jevPick("Pool Speakers", 1),
		"kind":    jevPick(kindSong, 1),
		"song":    jevPick("99 Red Balloons", 1),
		"artist":  jevPick("Goldfinger", 1),
		"pick":    jevPick("99 Red Balloons by Goldfinger", 0.9),
	}
	env.spotify.SearchFunc = trackResults
	env.spotify.PlayerDevicesFunc = func(ctx context.Context) ([]spotifyLib.PlayerDevice, error) {
		return []spotifyLib.PlayerDevice{{ID: spotifyLib.ID(testSpeakers[1].SpotifyID), Name: "Pool Speakers"}}, nil
	}
	var uris []spotifyLib.URI
	env.spotify.PlayOptFunc = func(ctx context.Context, opts *spotifyLib.PlayOptions) error {
		uris = opts.URIs
		return nil
	}

	res, err := Ask(context.Background(), "Play 99 Red Balloons by Goldfinger on the pool speakers", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(uris) != 1 || uris[0] != "spotify:track:goldfinger" {
		t.Errorf("played %v", uris)
	}
	if res.Message != "Playing 99 Red Balloons by Goldfinger on Pool Speakers." {
		t.Errorf("message = %q", res.Message)
	}
}

// TestAsk_SongNotFound says which version it couldn't find.
func TestAsk_SongNotFound(t *testing.T) {
	env := newAskTestEnv(t, testSpeakers...)
	env.jev.answers = map[string]JevAnswer{
		"intent":  jevPick(intentPlay, 1),
		"speaker": jevPick("Pool Speakers", 1),
		"kind":    jevPick(kindSong, 1),
		"song":    jevPick("99 Red Balloons", 1),
		"artist":  jevPick("Green Day", 1),
		"pick":    jevPick(noneOption, 0.98),
	}
	env.spotify.SearchFunc = trackResults

	res, _ := Ask(context.Background(), `Play the Green Day version of "99 Red Balloons" on the pool speakers`, false)
	if res.ActionTaken || res.Message != "I couldn't find a Green Day version of 99 Red Balloons." {
		t.Errorf("res = %+v", res)
	}
}

// TestAsk_TitleFallsBackToAlbum finds "Dookie" as an album after the song
// search turns up nothing, since a lone title can be either.
func TestAsk_TitleFallsBackToAlbum(t *testing.T) {
	env := newAskTestEnv(t, testSpeakers...)
	env.jev.answers = map[string]JevAnswer{
		"intent":  jevPick(intentPlay, 1),
		"speaker": jevPick("Living Room Speakers", 1),
		"kind":    jevPick(kindSong, 0.6),
		"song":    jevPick("Dookie", 1),
	}
	env.jev.picks = []JevAnswer{jevPick(noneOption, 0.9), jevPick("the album Dookie by Green Day", 0.95)}
	var searched []spotifyLib.SearchType
	env.spotify.SearchFunc = func(ctx context.Context, query string, st spotifyLib.SearchType) (*spotifyLib.SearchResult, error) {
		searched = append(searched, st)
		if st == spotifyLib.SearchTypeAlbum {
			return &spotifyLib.SearchResult{Albums: &spotifyLib.SimpleAlbumPage{Albums: []spotifyLib.SimpleAlbum{
				{Name: "Dookie", URI: "spotify:album:dookie", Artists: []spotifyLib.SimpleArtist{{Name: "Green Day"}}},
			}}}, nil
		}
		return trackResults(ctx, query, st)
	}

	res, _ := Ask(context.Background(), "Play Dookie in the living room", true)
	if res.Message != "Dry run: Playing the album Dookie by Green Day on Living Room Speakers." {
		t.Errorf("message = %q", res.Message)
	}
	if len(searched) != 2 || searched[0] != spotifyLib.SearchTypeTrack || searched[1] != spotifyLib.SearchTypeAlbum {
		t.Errorf("searched = %v, want track then album", searched)
	}
}

// TestAsk_LatestAlbumPicksNewestByDate compares release dates in code.
func TestAsk_LatestAlbumPicksNewestByDate(t *testing.T) {
	env := newAskTestEnv(t, testSpeakers...)
	env.jev.answers = map[string]JevAnswer{
		"intent":  jevPick(intentPlay, 1),
		"speaker": jevPick("Living Room Speakers", 1),
		"kind":    jevPick(kindAlbum, 1),
		"artist":  jevPick("Less Than Jake", 1),
		"latest":  jevYes(0.97),
		"pick":    jevPick("Less Than Jake", 0.95),
	}
	env.spotify.SearchFunc = func(ctx context.Context, query string, st spotifyLib.SearchType) (*spotifyLib.SearchResult, error) {
		return &spotifyLib.SearchResult{Artists: &spotifyLib.FullArtistPage{Artists: []spotifyLib.FullArtist{
			{SimpleArtist: spotifyLib.SimpleArtist{ID: "ltj", Name: "Less Than Jake", URI: "spotify:artist:ltj"}},
		}}}, nil
	}
	ltj := []spotifyLib.SimpleArtist{{Name: "Less Than Jake"}}
	env.spotify.GetArtistAlbumsFunc = func(ctx context.Context, id spotifyLib.ID, ts []spotifyLib.AlbumType) (*spotifyLib.SimpleAlbumPage, error) {
		if id != "ltj" || len(ts) != 1 || ts[0] != spotifyLib.AlbumTypeAlbum {
			t.Errorf("albums for %s %v", id, ts)
		}
		return &spotifyLib.SimpleAlbumPage{Albums: []spotifyLib.SimpleAlbum{
			{Name: "Losing Streak", URI: "spotify:album:old", ReleaseDate: "1996-11-12", ReleaseDatePrecision: "day", Artists: ltj},
			{Name: "Silver Linings", URI: "spotify:album:new", ReleaseDate: "2020-02-14", ReleaseDatePrecision: "day", Artists: ltj},
			{Name: "See the Light", URI: "spotify:album:mid", ReleaseDate: "2013", ReleaseDatePrecision: "year", Artists: ltj},
		}}, nil
	}
	env.spotify.PlayerDevicesFunc = func(ctx context.Context) ([]spotifyLib.PlayerDevice, error) {
		return []spotifyLib.PlayerDevice{{ID: spotifyLib.ID(testSpeakers[0].SpotifyID), Name: "Living Room Speakers"}}, nil
	}
	var played spotifyLib.URI
	env.spotify.PlayOptFunc = func(ctx context.Context, opts *spotifyLib.PlayOptions) error {
		played = *opts.PlaybackContext
		return nil
	}

	res, err := Ask(context.Background(), "Play the latest Less Than Jake album in the living room", false)
	if err != nil {
		t.Fatal(err)
	}
	if played != "spotify:album:new" {
		t.Errorf("played %s", played)
	}
	want := "Playing the newest Less Than Jake album, Silver Linings, on Living Room Speakers."
	if res.Message != want {
		t.Errorf("message = %q, want %q", res.Message, want)
	}
}

// TestAsk_DryRunDoesNotPlay decides but changes nothing.
func TestAsk_DryRunDoesNotPlay(t *testing.T) {
	env := newAskTestEnv(t, testSpeakers...)
	env.jev.answers = playlistAnswers()
	env.spotify.CurrentUsersPlaylistsFunc = codingMixPlaylists
	env.spotify.PlayOptFunc = func(ctx context.Context, opts *spotifyLib.PlayOptions) error {
		t.Error("dry run should not start playback")
		return nil
	}

	res, _ := Ask(context.Background(), "Play my coding mix on the living room speakers", true)
	if res.ActionTaken || res.Message != "Dry run: Playing Coding Mix on Living Room Speakers." {
		t.Errorf("res = %+v", res)
	}
}

// TestAsk_StopAllPausesEverySpeaker pauses every playing WiiM speaker and
// our own Spotify session, and leaves paused speakers alone.
func TestAsk_StopAllPausesEverySpeaker(t *testing.T) {
	env := newAskTestEnv(t, testSpeakers...)
	env.jev.answers = map[string]JevAnswer{"intent": jevPick(intentStopAll, 1)}
	env.wiim.statuses["10.0.0.1"] = WiiMStatus{State: "play"}
	env.wiim.statuses["10.0.0.2"] = WiiMStatus{State: "play"}
	env.wiim.statuses["10.0.0.3"] = WiiMStatus{State: "pause"}
	env.spotify.PlayerStateFunc = func(ctx context.Context) (*spotifyLib.PlayerState, error) {
		return &spotifyLib.PlayerState{
			CurrentlyPlaying: spotifyLib.CurrentlyPlaying{Playing: true},
			Device:           spotifyLib.PlayerDevice{ID: "webplayer1", Name: "Web Player (Chrome)"},
		}, nil
	}
	var pausedID spotifyLib.ID
	env.spotify.PauseOptFunc = func(ctx context.Context, opt *spotifyLib.PlayOptions) error {
		pausedID = *opt.DeviceID
		return nil
	}

	res, err := Ask(context.Background(), "Kill all music", false)
	if err != nil {
		t.Fatal(err)
	}
	want := "Stopped the music on Web Player (Chrome), Living Room Speakers and Pool Speakers."
	if !res.ActionTaken || res.Message != want {
		t.Errorf("res = %+v, want %q", res, want)
	}
	if pausedID != "webplayer1" {
		t.Errorf("Spotify paused %q", pausedID)
	}
	cmds := strings.Join(env.wiim.commands, "; ")
	if !strings.Contains(cmds, "10.0.0.1 pause") || !strings.Contains(cmds, "10.0.0.2 pause") || strings.Contains(cmds, "10.0.0.3") {
		t.Errorf("wiim commands = %s", cmds)
	}
}

// TestAsk_StopAllNothingPlaying takes no action.
func TestAsk_StopAllNothingPlaying(t *testing.T) {
	env := newAskTestEnv(t, testSpeakers...)
	env.jev.answers = map[string]JevAnswer{"intent": jevPick(intentStopAll, 1)}
	res, _ := Ask(context.Background(), "Kill all music", false)
	if res.ActionTaken || res.Message != "Nothing is playing anywhere." {
		t.Errorf("res = %+v", res)
	}
}

// TestAsk_PauseWithoutSpeaker pauses the one speaker that is playing, and
// asks when several are.
func TestAsk_PauseWithoutSpeaker(t *testing.T) {
	env := newAskTestEnv(t, testSpeakers...)
	env.jev.answers = map[string]JevAnswer{"intent": jevPick(intentPause, 1)}
	env.wiim.statuses["10.0.0.1"] = WiiMStatus{State: "play"}

	res, _ := Ask(context.Background(), "Pause the music", false)
	if !res.ActionTaken || res.Message != "Paused Pool Speakers." {
		t.Errorf("res = %+v", res)
	}
	if len(env.wiim.commands) != 1 || env.wiim.commands[0] != "10.0.0.1 pause" {
		t.Errorf("commands = %v", env.wiim.commands)
	}

	env.wiim.statuses["10.0.0.2"] = WiiMStatus{State: "play"}
	res, _ = Ask(context.Background(), "Pause the music", false)
	if res.ActionTaken || res.Message != "Music is playing on Living Room Speakers and Pool Speakers. Which speaker?" {
		t.Errorf("res = %+v", res)
	}
}

// TestAsk_SkipOurSessionUsesSpotify skips through Spotify when the speaker
// is our own active session.
func TestAsk_SkipOurSessionUsesSpotify(t *testing.T) {
	env := newAskTestEnv(t, testSpeakers...)
	env.jev.answers = map[string]JevAnswer{"intent": jevPick(intentSkip, 1)}
	lrID := spotifyLib.ID(testSpeakers[0].SpotifyID)
	env.wiim.statuses["10.0.0.2"] = WiiMStatus{State: "play"}
	env.spotify.PlayerStateFunc = func(ctx context.Context) (*spotifyLib.PlayerState, error) {
		return &spotifyLib.PlayerState{
			CurrentlyPlaying: spotifyLib.CurrentlyPlaying{Playing: true},
			Device:           spotifyLib.PlayerDevice{ID: lrID, Name: string(lrID)},
		}, nil
	}
	var skipped spotifyLib.ID
	env.spotify.NextOptFunc = func(ctx context.Context, opt *spotifyLib.PlayOptions) error {
		skipped = *opt.DeviceID
		return nil
	}

	res, _ := Ask(context.Background(), "Skip this song", false)
	if skipped != lrID || res.Message != "Skipped to the next song on Living Room Speakers." {
		t.Errorf("skipped=%s res=%+v", skipped, res)
	}
	if len(env.wiim.commands) != 0 {
		t.Errorf("WiiM should not get a command: %v", env.wiim.commands)
	}
}

// TestAsk_Volume moves the volume by a step from the current level, sets a
// spoken number, and asks when no number is given.
func TestAsk_Volume(t *testing.T) {
	env := newAskTestEnv(t, testSpeakers...)
	env.wiim.statuses["10.0.0.1"] = WiiMStatus{State: "stop", Volume: 41}

	env.jev.answers = map[string]JevAnswer{"intent": jevPick(intentVolumeUp, 1), "speaker": jevPick("Pool Speakers", 1)}
	res, _ := Ask(context.Background(), "Turn up the pool speakers", false)
	if res.Message != "Turned Pool Speakers up to 51." || env.wiim.commands[0] != "10.0.0.1 vol:51" {
		t.Errorf("up: res=%+v commands=%v", res, env.wiim.commands)
	}

	env.jev.answers["intent"] = jevPick(intentVolumeSet, 1)
	res, _ = Ask(context.Background(), "Set the pool speakers to 140 percent", false)
	if res.Message != "Set the volume on Pool Speakers to 100." {
		t.Errorf("set: res=%+v", res)
	}

	res, _ = Ask(context.Background(), "Set the volume on the pool speakers", false)
	if res.ActionTaken || res.Message != "What volume should I set it to?" {
		t.Errorf("no number: res=%+v", res)
	}
}

// TestAsk_NowPlaying lists every speaker playing, merging our Spotify
// session with the same speaker's WiiM report.
func TestAsk_NowPlaying(t *testing.T) {
	env := newAskTestEnv(t, testSpeakers...)
	env.jev.answers = map[string]JevAnswer{"intent": jevPick(intentNowPlaying, 1)}
	lrID := spotifyLib.ID(testSpeakers[0].SpotifyID)
	env.wiim.statuses["10.0.0.2"] = WiiMStatus{State: "play", Title: "ignored", Artist: "ignored"}
	env.wiim.statuses["10.0.0.1"] = WiiMStatus{State: "play", Title: "Johnny Quest Thinks We're Sellouts", Artist: "Less Than Jake"}
	env.spotify.PlayerStateFunc = func(ctx context.Context) (*spotifyLib.PlayerState, error) {
		return &spotifyLib.PlayerState{
			CurrentlyPlaying: spotifyLib.CurrentlyPlaying{Playing: true, Item: &spotifyLib.FullTrack{SimpleTrack: spotifyLib.SimpleTrack{
				Name: "Basket Case", Artists: []spotifyLib.SimpleArtist{{Name: "Green Day"}},
			}}},
			Device: spotifyLib.PlayerDevice{ID: lrID, Name: string(lrID)},
		}, nil
	}

	res, _ := Ask(context.Background(), "What's playing right now on what speakers?", false)
	want := "Here's what's playing:\n- Living Room Speakers: Basket Case by Green Day\n- Pool Speakers: Johnny Quest Thinks We're Sellouts by Less Than Jake"
	if !res.ActionTaken || res.Message != want {
		t.Errorf("message = %q", res.Message)
	}

	env.wiim.statuses = map[string]WiiMStatus{}
	env.spotify.PlayerStateFunc = nil
	res, _ = Ask(context.Background(), "What's playing?", false)
	if res.Message != "Nothing is playing right now." {
		t.Errorf("message = %q", res.Message)
	}
}

// TestAsk_ListsAndUnknown covers listing speakers and playlists and a
// request Jev doesn't recognize.
func TestAsk_ListsAndUnknown(t *testing.T) {
	env := newAskTestEnv(t, testSpeakers...)
	env.spotify.CurrentUsersPlaylistsFunc = codingMixPlaylists

	env.jev.answers = map[string]JevAnswer{"intent": jevPick(intentListSpeakers, 1)}
	res, _ := Ask(context.Background(), "What are my possible speakers?", false)
	want := "Here are your speakers:\n- Living Room Speakers\n- Pool Porch Speakers\n- Pool Speakers\n- Web Player (Chrome)"
	if res.Message != want {
		t.Errorf("speakers = %q", res.Message)
	}

	env.jev.answers = map[string]JevAnswer{"intent": jevPick(intentListPlaylists, 1)}
	res, _ = Ask(context.Background(), "What are all my playlists?", false)
	if res.Message != "Here's a list of all your possible playlists:\n- Punk Mix\n- Coding Mix" {
		t.Errorf("playlists = %q", res.Message)
	}

	env.jev.answers = map[string]JevAnswer{}
	res, _ = Ask(context.Background(), "What's the weather like?", false)
	if res.ActionTaken || res.Message != "Sorry, I didn't understand that." {
		t.Errorf("unknown = %+v", res)
	}
}

// TestAsk_RequiresJev errors clearly when TYPESAFE_API_KEY isn't set.
func TestAsk_RequiresJev(t *testing.T) {
	newAskTestEnv(t)
	jevClient = nil
	if _, err := Ask(context.Background(), "Play something", false); err == nil || !strings.Contains(err.Error(), "TYPESAFE_API_KEY") {
		t.Errorf("err = %v", err)
	}
}

// TestHandleAskRequest covers auth, a missing request, and GET and POST.
func TestHandleAskRequest(t *testing.T) {
	env := newAskTestEnv(t, testSpeakers...)
	env.jev.answers = map[string]JevAnswer{"intent": jevPick(intentListSpeakers, 1)}
	originalToken := apiAccessToken
	apiAccessToken = "test-token"
	defer func() { apiAccessToken = originalToken }()

	w := httptest.NewRecorder()
	HandleAskRequest(w, httptest.NewRequest(http.MethodGet, "/api/v1/ask?text=hi", nil))
	if w.Code != http.StatusUnauthorized {
		t.Errorf("no token: %d", w.Code)
	}

	w = httptest.NewRecorder()
	HandleAskRequest(w, httptest.NewRequest(http.MethodGet, "/api/v1/ask?token=test-token", nil))
	if w.Code != http.StatusBadRequest {
		t.Errorf("no text: %d", w.Code)
	}

	w = httptest.NewRecorder()
	HandleAskRequest(w, httptest.NewRequest(http.MethodGet, "/api/v1/ask?token=test-token&text=what+speakers&dry_run=true", nil))
	var resp AskResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if w.Code != http.StatusOK || !resp.ActionTaken || resp.Intent != intentListSpeakers || !resp.DryRun ||
		!strings.HasPrefix(resp.Message, "Here are your speakers:") {
		t.Errorf("GET: %d %+v", w.Code, resp)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/ask", strings.NewReader(`{"text":"what speakers"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-token")
	w = httptest.NewRecorder()
	HandleAskRequest(w, req)
	resp = AskResponse{}
	json.NewDecoder(w.Body).Decode(&resp)
	if w.Code != http.StatusOK || resp.Intent != intentListSpeakers || resp.DryRun {
		t.Errorf("POST: %d %+v", w.Code, resp)
	}

	env.jev.err = errors.New("jev down")
	w = httptest.NewRecorder()
	HandleAskRequest(w, httptest.NewRequest(http.MethodGet, "/api/v1/ask?token=test-token&text=play", nil))
	resp = AskResponse{}
	json.NewDecoder(w.Body).Decode(&resp)
	if w.Code != http.StatusInternalServerError || resp.ActionTaken || resp.Message == "" || resp.Error == "" {
		t.Errorf("error: %d %+v", w.Code, resp)
	}
}

// TestHandleDevicesRequest_UsesSpeakerNames shows the friendly name for a
// device Spotify lists only by its hex ID.
func TestHandleDevicesRequest_UsesSpeakerNames(t *testing.T) {
	env := newAskTestEnv(t, testSpeakers...)
	originalToken := apiAccessToken
	apiAccessToken = "test-token"
	defer func() { apiAccessToken = originalToken }()

	poolID := testSpeakers[1].SpotifyID
	env.spotify.PlayerDevicesFunc = func(ctx context.Context) ([]spotifyLib.PlayerDevice, error) {
		return []spotifyLib.PlayerDevice{{ID: spotifyLib.ID(poolID), Name: poolID}}, nil
	}

	w := httptest.NewRecorder()
	HandleDevicesRequest(w, httptest.NewRequest(http.MethodGet, "/api/v1/devices?token=test-token", nil))
	var resp APIResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if len(resp.Devices) != 1 || resp.Devices[0].Name != "Pool Speakers" || resp.Devices[0].ID != poolID {
		t.Errorf("devices = %+v", resp.Devices)
	}
}

// TestPlayPlaylist_MatchesSpeakerNameByDeviceID finds a speaker Spotify
// lists by hex ID when /play is given its friendly name, without claiming.
func TestPlayPlaylist_MatchesSpeakerNameByDeviceID(t *testing.T) {
	env := newAskTestEnv(t, testSpeakers...)
	poolID := spotifyLib.ID(testSpeakers[1].SpotifyID)
	env.spotify.PlayerDevicesFunc = func(ctx context.Context) ([]spotifyLib.PlayerDevice, error) {
		return []spotifyLib.PlayerDevice{
			{ID: "other", Name: "Kitchen", Active: true},
			{ID: poolID, Name: string(poolID)},
		}, nil
	}
	var played spotifyLib.ID
	env.spotify.PlayOptFunc = func(ctx context.Context, opts *spotifyLib.PlayOptions) error {
		played = *opts.DeviceID
		return nil
	}

	msg, err := PlayPlaylist("Pool Speakers", "37i9dQZF1DXcBWIGoYBM5M", false)
	if err != nil {
		t.Fatal(err)
	}
	if played != poolID || !strings.Contains(msg, "on Pool Speakers") {
		t.Errorf("played=%s msg=%q", played, msg)
	}
}
