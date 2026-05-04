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
