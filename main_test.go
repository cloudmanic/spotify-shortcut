//
// Date: 2025-12-09
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Copyright (c) 2025 Cloudmanic Labs, LLC. All rights reserved.
//
// Description: Unit tests for Spotify Shortcut application.
//

package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/zmb3/spotify/v2"
	"golang.org/x/oauth2"
)

// createFullPlaylist creates a FullPlaylist with the Total field set via JSON unmarshaling.
// This is necessary because the basePage struct is unexported.
func createFullPlaylist(id spotify.ID, name string, total int) *spotify.FullPlaylist {
	jsonData := []byte(`{
		"id": "` + string(id) + `",
		"name": "` + name + `",
		"tracks": {
			"total": ` + string(rune('0'+total/10)) + string(rune('0'+total%10)) + `
		}
	}`)
	var playlist spotify.FullPlaylist
	json.Unmarshal(jsonData, &playlist)
	return &playlist
}

// createFullPlaylistWithTotal creates a FullPlaylist with a specific total using JSON.
func createFullPlaylistWithTotal(id string, name string, total int) *spotify.FullPlaylist {
	jsonStr := `{"id":"` + id + `","name":"` + name + `","tracks":{"total":` + itoa(total) + `}}`
	var playlist spotify.FullPlaylist
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

// MockSpotifyClient is a mock implementation of the SpotifyClient interface for testing.
type MockSpotifyClient struct {
	// CurrentUser mock
	CurrentUserFunc func(ctx context.Context) (*spotify.PrivateUser, error)

	// CurrentUsersPlaylists mock
	CurrentUsersPlaylistsFunc func(ctx context.Context, opts ...spotify.RequestOption) (*spotify.SimplePlaylistPage, error)

	// PlayerDevices mock
	PlayerDevicesFunc func(ctx context.Context) ([]spotify.PlayerDevice, error)

	// GetPlaylist mock
	GetPlaylistFunc func(ctx context.Context, playlistID spotify.ID, opts ...spotify.RequestOption) (*spotify.FullPlaylist, error)

	// PlayOpt mock
	PlayOptFunc func(ctx context.Context, opts *spotify.PlayOptions) error

	// Pause mock
	PauseFunc func(ctx context.Context) error

	// Shuffle mock
	ShuffleFunc func(ctx context.Context, shuffle bool) error
}

// CurrentUser returns the current user.
func (m *MockSpotifyClient) CurrentUser(ctx context.Context) (*spotify.PrivateUser, error) {
	if m.CurrentUserFunc != nil {
		return m.CurrentUserFunc(ctx)
	}
	return &spotify.PrivateUser{
		User: spotify.User{
			DisplayName: "Test User",
			ID:          "testuser123",
		},
	}, nil
}

// CurrentUsersPlaylists returns the user's playlists.
func (m *MockSpotifyClient) CurrentUsersPlaylists(ctx context.Context, opts ...spotify.RequestOption) (*spotify.SimplePlaylistPage, error) {
	if m.CurrentUsersPlaylistsFunc != nil {
		return m.CurrentUsersPlaylistsFunc(ctx, opts...)
	}
	return &spotify.SimplePlaylistPage{
		Playlists: []spotify.SimplePlaylist{
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
func (m *MockSpotifyClient) PlayerDevices(ctx context.Context) ([]spotify.PlayerDevice, error) {
	if m.PlayerDevicesFunc != nil {
		return m.PlayerDevicesFunc(ctx)
	}
	return []spotify.PlayerDevice{
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
func (m *MockSpotifyClient) GetPlaylist(ctx context.Context, playlistID spotify.ID, opts ...spotify.RequestOption) (*spotify.FullPlaylist, error) {
	if m.GetPlaylistFunc != nil {
		return m.GetPlaylistFunc(ctx, playlistID, opts...)
	}
	return createFullPlaylistWithTotal(string(playlistID), "Test Playlist", 50), nil
}

// PlayOpt starts playback with options.
func (m *MockSpotifyClient) PlayOpt(ctx context.Context, opts *spotify.PlayOptions) error {
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

// TestExtractPlaylistID tests the extractPlaylistID function.
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
			result := extractPlaylistID(tt.input)
			if result != tt.expected {
				t.Errorf("extractPlaylistID(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

// TestResolvePlaylistIDQuiet_URL tests resolvePlaylistIDQuiet with URL input.
func TestResolvePlaylistIDQuiet_URL(t *testing.T) {
	mock := &MockSpotifyClient{}
	ctx := context.Background()

	result, err := resolvePlaylistIDQuiet(ctx, mock, "https://open.spotify.com/playlist/37i9dQZF1DXcBWIGoYBM5M")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "37i9dQZF1DXcBWIGoYBM5M" {
		t.Errorf("expected 37i9dQZF1DXcBWIGoYBM5M, got %s", result)
	}
}

// TestResolvePlaylistIDQuiet_ID tests resolvePlaylistIDQuiet with a 22-char ID.
func TestResolvePlaylistIDQuiet_ID(t *testing.T) {
	mock := &MockSpotifyClient{}
	ctx := context.Background()

	// 22 character ID
	result, err := resolvePlaylistIDQuiet(ctx, mock, "37i9dQZF1DXcBWIGoYBM5M")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "37i9dQZF1DXcBWIGoYBM5M" {
		t.Errorf("expected 37i9dQZF1DXcBWIGoYBM5M, got %s", result)
	}
}

// TestResolvePlaylistIDQuiet_Name tests resolvePlaylistIDQuiet with playlist name.
func TestResolvePlaylistIDQuiet_Name(t *testing.T) {
	mock := &MockSpotifyClient{
		CurrentUsersPlaylistsFunc: func(ctx context.Context, opts ...spotify.RequestOption) (*spotify.SimplePlaylistPage, error) {
			return &spotify.SimplePlaylistPage{
				Playlists: []spotify.SimplePlaylist{
					{ID: "found123playlistid00", Name: "My Awesome Playlist"},
					{ID: "other456playlistid00", Name: "Other Playlist"},
				},
			}, nil
		},
	}
	ctx := context.Background()

	result, err := resolvePlaylistIDQuiet(ctx, mock, "My Awesome Playlist")
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
		CurrentUsersPlaylistsFunc: func(ctx context.Context, opts ...spotify.RequestOption) (*spotify.SimplePlaylistPage, error) {
			return &spotify.SimplePlaylistPage{
				Playlists: []spotify.SimplePlaylist{
					{ID: "found123playlistid00", Name: "My Awesome Playlist"},
				},
			}, nil
		},
	}
	ctx := context.Background()

	result, err := resolvePlaylistIDQuiet(ctx, mock, "my awesome playlist")
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
		CurrentUsersPlaylistsFunc: func(ctx context.Context, opts ...spotify.RequestOption) (*spotify.SimplePlaylistPage, error) {
			return &spotify.SimplePlaylistPage{
				Playlists: []spotify.SimplePlaylist{},
			}, nil
		},
	}
	ctx := context.Background()

	// When not found, it returns the input as-is (assuming it's an ID)
	result, err := resolvePlaylistIDQuiet(ctx, mock, "Unknown Playlist")
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
		CurrentUsersPlaylistsFunc: func(ctx context.Context, opts ...spotify.RequestOption) (*spotify.SimplePlaylistPage, error) {
			return nil, errors.New("API error")
		},
	}
	ctx := context.Background()

	_, err := resolvePlaylistIDQuiet(ctx, mock, "Some Playlist")
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
	saveToken(testToken)

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

	result, err := pausePlayback()
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

	_, err := pausePlayback()
	if err == nil {
		t.Error("expected error, got nil")
	}
}

// TestPausePlayback_NotAuthenticated tests pause without authentication.
func TestPausePlayback_NotAuthenticated(t *testing.T) {
	originalClient := spotifyClient
	spotifyClient = nil
	defer func() { spotifyClient = originalClient }()

	_, err := pausePlayback()
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
		PlayerDevicesFunc: func(ctx context.Context) ([]spotify.PlayerDevice, error) {
			return []spotify.PlayerDevice{
				{ID: "device123", Name: "Test Speaker", Active: true},
			}, nil
		},
		GetPlaylistFunc: func(ctx context.Context, playlistID spotify.ID, opts ...spotify.RequestOption) (*spotify.FullPlaylist, error) {
			return createFullPlaylistWithTotal(string(playlistID), "Test Playlist", 10), nil
		},
		PlayOptFunc: func(ctx context.Context, opts *spotify.PlayOptions) error {
			playOptCalled = true
			return nil
		},
	}

	originalClient := spotifyClient
	spotifyClient = mock
	defer func() { spotifyClient = originalClient }()

	result, err := playPlaylist("Test Speaker", "37i9dQZF1DXcBWIGoYBM5M", false)
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
		PlayerDevicesFunc: func(ctx context.Context) ([]spotify.PlayerDevice, error) {
			return []spotify.PlayerDevice{
				{ID: "device123", Name: "Test Speaker", Active: true},
			}, nil
		},
		GetPlaylistFunc: func(ctx context.Context, playlistID spotify.ID, opts ...spotify.RequestOption) (*spotify.FullPlaylist, error) {
			return createFullPlaylistWithTotal(string(playlistID), "Test Playlist", 10), nil
		},
		PlayOptFunc: func(ctx context.Context, opts *spotify.PlayOptions) error {
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

	result, err := playPlaylist("Test Speaker", "37i9dQZF1DXcBWIGoYBM5M", true)
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
		PlayerDevicesFunc: func(ctx context.Context) ([]spotify.PlayerDevice, error) {
			return []spotify.PlayerDevice{}, nil
		},
	}

	originalClient := spotifyClient
	spotifyClient = mock
	defer func() { spotifyClient = originalClient }()

	_, err := playPlaylist("", "37i9dQZF1DXcBWIGoYBM5M", false)
	if err == nil {
		t.Error("expected error, got nil")
	}
}

// TestPlayPlaylist_NotAuthenticated tests playback without authentication.
func TestPlayPlaylist_NotAuthenticated(t *testing.T) {
	originalClient := spotifyClient
	spotifyClient = nil
	defer func() { spotifyClient = originalClient }()

	_, err := playPlaylist("", "37i9dQZF1DXcBWIGoYBM5M", false)
	if err == nil {
		t.Error("expected error, got nil")
	}
}

// TestPlayPlaylist_DeviceSelection tests device selection logic.
func TestPlayPlaylist_DeviceSelection(t *testing.T) {
	selectedDeviceID := ""
	mock := &MockSpotifyClient{
		PlayerDevicesFunc: func(ctx context.Context) ([]spotify.PlayerDevice, error) {
			return []spotify.PlayerDevice{
				{ID: "device1", Name: "First Speaker", Active: false},
				{ID: "device2", Name: "Second Speaker", Active: false},
				{ID: "device3", Name: "Target Speaker", Active: false},
			}, nil
		},
		GetPlaylistFunc: func(ctx context.Context, playlistID spotify.ID, opts ...spotify.RequestOption) (*spotify.FullPlaylist, error) {
			return createFullPlaylistWithTotal(string(playlistID), "Test", 10), nil
		},
		PlayOptFunc: func(ctx context.Context, opts *spotify.PlayOptions) error {
			if opts.DeviceID != nil {
				selectedDeviceID = string(*opts.DeviceID)
			}
			return nil
		},
	}

	originalClient := spotifyClient
	spotifyClient = mock
	defer func() { spotifyClient = originalClient }()

	_, err := playPlaylist("Target Speaker", "37i9dQZF1DXcBWIGoYBM5M", false)
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

	handlePauseRequest(w, req)

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

	handlePauseRequest(w, req)

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

	handlePauseRequest(w, req)

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

	handlePauseRequest(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}
}

// TestHandlePlayRequest_Success tests the play API endpoint.
func TestHandlePlayRequest_Success(t *testing.T) {
	mock := &MockSpotifyClient{
		PlayerDevicesFunc: func(ctx context.Context) ([]spotify.PlayerDevice, error) {
			return []spotify.PlayerDevice{
				{ID: "device123", Name: "Test Speaker", Active: true},
			}, nil
		},
		GetPlaylistFunc: func(ctx context.Context, playlistID spotify.ID, opts ...spotify.RequestOption) (*spotify.FullPlaylist, error) {
			return createFullPlaylistWithTotal(string(playlistID), "Test Playlist", 10), nil
		},
		PlayOptFunc: func(ctx context.Context, opts *spotify.PlayOptions) error {
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

	handlePlayRequest(w, req)

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

	handlePlayRequest(w, req)

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

	handlePlayRequest(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected status 401, got %d", w.Code)
	}
}

// TestHandleRootRequest tests the root endpoint.
func TestHandleRootRequest(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()

	handleRootRequest(w, req)

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

	handleRootRequest(w, req)

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

	handleAuthRequest(w, req)

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

	handleAuthRequest(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected status 401, got %d", w.Code)
	}
}
