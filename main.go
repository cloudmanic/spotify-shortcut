//
// Date: 2025-12-08
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Copyright (c) 2025 Cloudmanic Labs, LLC. All rights reserved.
//
// Description: Spotify playlist player for Spotify Connect devices.
// This application authenticates with Spotify, enables shuffle mode,
// and starts playback of a specified playlist on a target device.
//

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/fatih/color"
	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/joho/godotenv"
	"github.com/zmb3/spotify/v2"
	"golang.org/x/oauth2"

	spotifyauth "github.com/zmb3/spotify/v2/auth"
)

const (
	defaultRedirectURI = "http://127.0.0.1:8080/callback"
	defaultTokenFile   = ".spotify_token.json"
)

var (
	auth           *spotifyauth.Authenticator
	ch             = make(chan *spotify.Client)
	state          = "spotify-shortcut-state"
	spotifyClient  *spotify.Client
	apiAccessToken string
	tokenFile      string
)

// main is the entry point for the application. It handles authentication,
// device selection, and playlist playback with shuffle enabled.
func main() {
	// Parse command line flags
	listDevices := flag.Bool("devices", false, "List available Spotify Connect devices and exit")
	listPlaylists := flag.Bool("playlists", false, "List your Spotify playlists and exit")
	debug := flag.Bool("debug", false, "Print raw API responses for debugging")
	shuffle := flag.Bool("shuffle", false, "Enable shuffle mode and start at random track")
	deviceFlag := flag.String("device", "", "Device name or ID to play on")
	playlistFlag := flag.String("playlist", "", "Playlist ID or URL to play")
	serverMode := flag.Bool("server", false, "Start as HTTP API server")
	flag.Parse()

	// Load .env file if it exists (ignore error if not found)
	_ = godotenv.Load()

	// Get credentials from environment variables
	clientID := os.Getenv("SPOTIFY_CLIENT_ID")
	clientSecret := os.Getenv("SPOTIFY_CLIENT_SECRET")
	redirectURI := os.Getenv("SPOTIFY_REDIRECT_URI")
	if redirectURI == "" {
		redirectURI = defaultRedirectURI
	}

	tokenFile = os.Getenv("SPOTIFY_TOKEN_FILE")
	if tokenFile == "" {
		tokenFile = defaultTokenFile
	}

	// Playlist ID from flag takes priority over env var
	playlistID := *playlistFlag
	if playlistID == "" {
		playlistID = os.Getenv("SPOTIFY_PLAYLIST_ID")
	}

	// Device name from flag takes priority over env var
	deviceName := *deviceFlag
	if deviceName == "" {
		deviceName = os.Getenv("SPOTIFY_DEVICE_NAME")
	}

	if clientID == "" || clientSecret == "" {
		log.Fatal("SPOTIFY_CLIENT_ID and SPOTIFY_CLIENT_SECRET environment variables are required")
	}

	// Only require playlist ID if not listing devices, playlists, or running in server mode
	if playlistID == "" && !*listDevices && !*listPlaylists && !*serverMode {
		log.Fatal("SPOTIFY_PLAYLIST_ID is required. Use -playlist flag or set in .env")
	}

	// Get API access token for server mode
	apiAccessToken = os.Getenv("API_ACCESS_TOKEN")
	if *serverMode && apiAccessToken == "" {
		log.Fatal("API_ACCESS_TOKEN environment variable is required for server mode")
	}

	// Initialize the authenticator with required scopes
	auth = spotifyauth.New(
		spotifyauth.WithClientID(clientID),
		spotifyauth.WithClientSecret(clientSecret),
		spotifyauth.WithRedirectURL(redirectURI),
		spotifyauth.WithScopes(
			spotifyauth.ScopeUserReadPlaybackState,
			spotifyauth.ScopeUserModifyPlaybackState,
			spotifyauth.ScopeUserReadCurrentlyPlaying,
			spotifyauth.ScopePlaylistReadPrivate,
			spotifyauth.ScopePlaylistReadCollaborative,
		),
	)

	// If --server flag is set, start HTTP API server
	// In server mode, we try to load an existing token but don't require it
	// Users can authenticate via /auth endpoint
	if *serverMode {
		client, err := loadToken()
		if err == nil {
			ctx := context.Background()
			user, err := client.CurrentUser(ctx)
			if err == nil {
				fmt.Printf("Authenticated as: %s\n", user.DisplayName)
				spotifyClient = client
			} else {
				fmt.Println("Existing token expired. Visit /auth to re-authenticate.")
			}
		} else {
			fmt.Println("No Spotify token found. Visit /auth to authenticate.")
		}
		startAPIServer()
		return
	}

	// For CLI mode, require authentication
	client, err := loadToken()
	if err != nil {
		// No valid token, need to authenticate
		client = authenticate()
	}

	ctx := context.Background()

	// Get user info to verify authentication
	user, err := client.CurrentUser(ctx)
	if err != nil {
		log.Printf("Token may be expired, re-authenticating: %v", err)
		client = authenticate()
		user, err = client.CurrentUser(ctx)
		if err != nil {
			log.Fatalf("Failed to get user info: %v", err)
		}
	}

	fmt.Printf("Authenticated as: %s\n", user.DisplayName)

	// Store client globally
	spotifyClient = client

	// If --playlists flag is set, list playlists and exit
	if *listPlaylists {
		// Fetch all playlists (handling pagination)
		var allPlaylists []spotify.SimplePlaylist
		limit := 50
		offset := 0

		for {
			playlists, err := client.CurrentUsersPlaylists(ctx, spotify.Limit(limit), spotify.Offset(offset))
			if err != nil {
				log.Fatalf("Failed to get playlists: %v", err)
			}

			allPlaylists = append(allPlaylists, playlists.Playlists...)

			// Check if there are more playlists to fetch
			if len(playlists.Playlists) < limit {
				break
			}
			offset += limit
		}

		// If --debug flag is set, print raw playlist data
		if *debug {
			fmt.Println("\n=== Raw Playlist Data ===")
			rawJSON, err := json.MarshalIndent(allPlaylists, "", "  ")
			if err != nil {
				log.Printf("Warning: Failed to marshal playlists: %v", err)
			} else {
				fmt.Println(string(rawJSON))
			}
			fmt.Println("=== End Raw Data ===")
		}

		printPlaylistsTable(allPlaylists)
		return
	}

	// Get available devices
	devices, err := client.PlayerDevices(ctx)
	if err != nil {
		log.Fatalf("Failed to get devices: %v", err)
	}

	if len(devices) == 0 {
		log.Fatal("No Spotify Connect devices found. Make sure a device is active.")
	}

	// If --debug flag is set, print raw device data
	if *debug {
		fmt.Println("\n=== Raw Device Data ===")
		rawJSON, err := json.MarshalIndent(devices, "", "  ")
		if err != nil {
			log.Printf("Warning: Failed to marshal devices: %v", err)
		} else {
			fmt.Println(string(rawJSON))
		}
		fmt.Println("=== End Raw Data ===")
	}

	// If --devices flag is set, list devices and exit
	if *listDevices {
		printDevicesTable(devices)
		return
	}

	// Find the target device by name or ID
	var targetDevice *spotify.PlayerDevice
	fmt.Println("\nAvailable devices:")
	for i, device := range devices {
		fmt.Printf("  %d. %s (%s) - Active: %v\n", i+1, device.Name, device.Type, device.Active)
		if deviceName != "" && (device.Name == deviceName || string(device.ID) == deviceName) {
			targetDevice = &devices[i]
		}
	}

	// If no device name/ID specified or not found, use the first active device or first device
	if targetDevice == nil {
		if deviceName != "" {
			fmt.Printf("\nDevice '%s' not found. ", deviceName)
		}
		for i, device := range devices {
			if device.Active {
				targetDevice = &devices[i]
				break
			}
		}
		if targetDevice == nil {
			targetDevice = &devices[0]
		}
		fmt.Printf("Using device: %s\n", targetDevice.Name)
	} else {
		fmt.Printf("\nUsing specified device: %s\n", targetDevice.Name)
	}

	// Resolve playlist by URL, name, or ID
	playlistID, err = resolvePlaylistID(ctx, client, playlistID)
	if err != nil {
		log.Fatalf("Failed to resolve playlist: %v", err)
	}

	// Get playlist info
	playlist, err := client.GetPlaylist(ctx, spotify.ID(playlistID))
	if err != nil {
		log.Fatalf("Failed to get playlist (ID: %s): %v\nMake sure the playlist ID is correct and the playlist is accessible.", playlistID, err)
	}

	trackCount := int(playlist.Tracks.Total)
	playlistURI := spotify.URI("spotify:playlist:" + playlistID)

	// Build play options
	opts := &spotify.PlayOptions{
		DeviceID:        &targetDevice.ID,
		PlaybackContext: &playlistURI,
	}

	// If shuffle is enabled, pick a random starting track
	if *shuffle {
		randomOffset := rand.Intn(trackCount)
		offset := &spotify.PlaybackOffset{Position: &randomOffset}
		opts.PlaybackOffset = offset

		err = client.PlayOpt(ctx, opts)
		if err != nil {
			log.Fatalf("Failed to start playback: %v", err)
		}

		fmt.Printf("Now playing playlist \"%s\" on %s (starting at track %d of %d)\n",
			playlist.Name, targetDevice.Name, randomOffset+1, trackCount)

		// Wait for playback to initialize before setting shuffle
		time.Sleep(500 * time.Millisecond)

		// Enable shuffle mode
		err = client.Shuffle(ctx, true)
		if err != nil {
			log.Printf("Warning: Failed to enable shuffle: %v", err)
		} else {
			fmt.Println("Shuffle mode enabled")
		}
	} else {
		// Start from the beginning (track 1) without shuffle
		startPosition := 0
		opts.PlaybackOffset = &spotify.PlaybackOffset{Position: &startPosition}

		err = client.PlayOpt(ctx, opts)
		if err != nil {
			log.Fatalf("Failed to start playback: %v", err)
		}

		fmt.Printf("Now playing playlist \"%s\" on %s (starting at track 1)\n", playlist.Name, targetDevice.Name)
	}
}

// authenticate starts the OAuth flow and returns an authenticated Spotify client.
// It starts a local HTTP server to handle the callback from Spotify.
func authenticate() *spotify.Client {
	http.HandleFunc("/callback", completeAuth)
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		log.Println("Got request for:", r.URL.String())
	})

	go func() {
		err := http.ListenAndServe(":8080", nil)
		if err != nil {
			log.Fatal(err)
		}
	}()

	url := auth.AuthURL(state)
	fmt.Println("Please visit this URL to authenticate:")
	fmt.Println(url)

	// Wait for auth to complete
	client := <-ch
	return client
}

// completeAuth handles the OAuth callback from Spotify, exchanges the code
// for a token, saves it for future use, and sends the client to the channel.
func completeAuth(w http.ResponseWriter, r *http.Request) {
	tok, err := auth.Token(r.Context(), state, r)
	if err != nil {
		http.Error(w, "Couldn't get token", http.StatusForbidden)
		log.Fatal(err)
	}

	if st := r.FormValue("state"); st != state {
		http.NotFound(w, r)
		log.Fatalf("State mismatch: %s != %s\n", st, state)
	}

	// Save token for future use
	saveToken(tok)

	client := spotify.New(auth.Client(r.Context(), tok))
	fmt.Fprintf(w, "Authentication successful! You can close this window.")
	ch <- client
}

// saveToken saves the OAuth token to a file for reuse in future sessions.
func saveToken(token *oauth2.Token) {
	file, err := os.Create(tokenFile)
	if err != nil {
		log.Printf("Warning: Failed to save token: %v", err)
		return
	}
	defer file.Close()

	err = json.NewEncoder(file).Encode(token)
	if err != nil {
		log.Printf("Warning: Failed to encode token: %v", err)
	}
}

// loadToken attempts to load a previously saved OAuth token from disk
// and returns a Spotify client if the token is still valid.
func loadToken() (*spotify.Client, error) {
	file, err := os.Open(tokenFile)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var token oauth2.Token
	err = json.NewDecoder(file).Decode(&token)
	if err != nil {
		return nil, err
	}

	// Create a new authenticator and client with the saved token
	ctx := context.Background()
	client := spotify.New(auth.Client(ctx, &token))

	return client, nil
}

// printDevicesTable displays available Spotify devices in a formatted table
// with colors to indicate active status.
func printDevicesTable(devices []spotify.PlayerDevice) {
	green := color.New(color.FgGreen, color.Bold)
	cyan := color.New(color.FgCyan)

	fmt.Println()
	cyan.Println("🎵 Available Spotify Connect Devices")
	fmt.Println()

	t := table.NewWriter()
	t.SetOutputMirror(os.Stdout)
	t.AppendHeader(table.Row{"#", "Name", "Type", "Status", "Device ID"})

	for i, device := range devices {
		status := "Inactive"
		if device.Active {
			status = color.GreenString("● Active")
		}

		t.AppendRow(table.Row{
			i + 1,
			color.New(color.Bold).Sprint(device.Name),
			device.Type,
			status,
			color.HiBlackString(string(device.ID)),
		})
	}

	t.SetStyle(table.StyleRounded)
	t.Render()

	fmt.Println()
	green.Printf("Total devices: %d\n", len(devices))
}

// extractPlaylistID extracts the playlist ID from a Spotify URL or returns
// the input as-is if it's already just an ID.
func extractPlaylistID(input string) string {
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

// resolvePlaylistID resolves a playlist input (URL, name, or ID) to a playlist ID.
// It first checks if it's a URL, then searches the user's playlists by name,
// and finally assumes it's an ID if no match is found.
func resolvePlaylistID(ctx context.Context, client *spotify.Client, input string) (string, error) {
	// First, check if it's a URL and extract the ID
	if strings.Contains(input, "spotify.com/playlist/") {
		return extractPlaylistID(input), nil
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
		playlists, err := client.CurrentUsersPlaylists(ctx, spotify.Limit(limit), spotify.Offset(offset))
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

// printPlaylistsTable displays the user's Spotify playlists in a formatted table.
func printPlaylistsTable(playlists []spotify.SimplePlaylist) {
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

// APIResponse represents a standard JSON response for the API.
type APIResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message,omitempty"`
	Error   string `json:"error,omitempty"`
}

// startAPIServer starts the HTTP API server for remote control.
func startAPIServer() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", handleRootRequest)
	mux.HandleFunc("/auth", handleAuthRequest)
	mux.HandleFunc("/callback", handleAuthCallback)
	mux.HandleFunc("/api/v1/play", handlePlayRequest)

	fmt.Printf("Starting API server on port %s...\n", port)
	fmt.Println("Endpoints:")
	fmt.Println("  GET /api/v1/play?device=<name>&playlist=<name|id|url>&shuffle=<true|false>")

	err := http.ListenAndServe(":"+port, mux)
	if err != nil {
		log.Fatalf("Failed to start API server: %v", err)
	}
}

// handleRootRequest handles requests to the root path with a simple message.
func handleRootRequest(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/plain")
	fmt.Fprint(w, "app coming soon....")
}

// handleAuthRequest redirects the user to Spotify's authorization page.
// Requires the API access token for security.
func handleAuthRequest(w http.ResponseWriter, r *http.Request) {
	// Verify access token
	token := r.URL.Query().Get("token")
	if token == "" {
		token = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	}

	if token != apiAccessToken {
		http.Error(w, "Unauthorized: Invalid or missing access token", http.StatusUnauthorized)
		return
	}

	url := auth.AuthURL(state)
	http.Redirect(w, r, url, http.StatusTemporaryRedirect)
}

// handleAuthCallback handles the OAuth callback from Spotify after user authorization.
func handleAuthCallback(w http.ResponseWriter, r *http.Request) {
	tok, err := auth.Token(r.Context(), state, r)
	if err != nil {
		http.Error(w, "Failed to get token: "+err.Error(), http.StatusForbidden)
		return
	}

	if st := r.FormValue("state"); st != state {
		http.Error(w, "State mismatch", http.StatusForbidden)
		return
	}

	// Save token for future use
	saveToken(tok)

	// Update the global client with the new token
	spotifyClient = spotify.New(auth.Client(r.Context(), tok))

	w.Header().Set("Content-Type", "text/plain")
	fmt.Fprint(w, "Authentication successful! You can close this window.")
}

// handlePlayRequest handles the /api/v1/play endpoint to start playlist playback.
func handlePlayRequest(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Verify access token
	token := r.URL.Query().Get("token")
	if token == "" {
		token = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	}

	if token != apiAccessToken {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(APIResponse{
			Success: false,
			Error:   "Invalid or missing access token",
		})
		return
	}

	// Get query parameters
	deviceName := r.URL.Query().Get("device")
	playlistInput := r.URL.Query().Get("playlist")
	shuffleStr := r.URL.Query().Get("shuffle")

	if playlistInput == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(APIResponse{
			Success: false,
			Error:   "playlist parameter is required",
		})
		return
	}

	shuffle := strings.ToLower(shuffleStr) == "true"

	// Play the playlist
	result, err := playPlaylist(deviceName, playlistInput, shuffle)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(APIResponse{
			Success: false,
			Error:   err.Error(),
		})
		return
	}

	json.NewEncoder(w).Encode(APIResponse{
		Success: true,
		Message: result,
	})
}

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

// resolvePlaylistIDQuiet resolves a playlist input without printing to stdout.
// Used by the API server to avoid cluttering logs.
func resolvePlaylistIDQuiet(ctx context.Context, client *spotify.Client, input string) (string, error) {
	// First, check if it's a URL and extract the ID
	if strings.Contains(input, "spotify.com/playlist/") {
		return extractPlaylistID(input), nil
	}

	// Check if it looks like a Spotify ID (22 alphanumeric characters)
	if len(input) == 22 && !strings.Contains(input, " ") {
		return input, nil
	}

	// Search user's playlists by name
	limit := 50
	offset := 0

	for {
		playlists, err := client.CurrentUsersPlaylists(ctx, spotify.Limit(limit), spotify.Offset(offset))
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
