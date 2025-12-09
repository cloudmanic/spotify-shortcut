//
// Date: 2025-12-08
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Copyright (c) 2025 Cloudmanic Labs, LLC. All rights reserved.
//
// Description: Entry point for the Spotify Shortcut application.
// Handles command-line flag parsing and delegates to appropriate handlers.
//

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"os"
	"time"

	"github.com/joho/godotenv"
	"github.com/zmb3/spotify/v2"

	spotifyauth "github.com/zmb3/spotify/v2/auth"
)

// main is the entry point for the application. It handles flag parsing
// and delegates to the appropriate mode (CLI or server).
func main() {
	// Parse command line flags
	listDevices := flag.Bool("devices", false, "List available Spotify Connect devices and exit")
	listPlaylists := flag.Bool("playlists", false, "List your Spotify playlists and exit")
	debug := flag.Bool("debug", false, "Print raw API responses for debugging")
	shuffle := flag.Bool("shuffle", false, "Enable shuffle mode and start at random track")
	deviceFlag := flag.String("device", "", "Device name or ID to play on")
	playlistFlag := flag.String("playlist", "", "Playlist ID or URL to play")
	serverMode := flag.Bool("server", false, "Start as HTTP API server")
	pauseMode := flag.Bool("pause", false, "Pause playback on all devices")
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

	// Only require playlist ID if not listing devices, playlists, pausing, or running in server mode
	if playlistID == "" && !*listDevices && !*listPlaylists && !*serverMode && !*pauseMode {
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
	if *serverMode {
		runServerMode()
		return
	}

	// Run CLI mode
	runCLIMode(listDevices, listPlaylists, debug, shuffle, pauseMode, deviceName, playlistID)
}

// runServerMode starts the HTTP API server.
// In server mode, we try to load an existing token but don't require it.
// Users can authenticate via /auth endpoint.
func runServerMode() {
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
}

// runCLIMode handles all command-line interface operations.
func runCLIMode(listDevices, listPlaylists, debug, shuffle, pauseMode *bool, deviceName, playlistID string) {
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

	// Handle --playlists flag
	if *listPlaylists {
		handleListPlaylists(ctx, client, debug)
		return
	}

	// Handle --pause flag
	if *pauseMode {
		result, err := pausePlayback()
		if err != nil {
			log.Fatalf("Failed to pause: %v", err)
		}
		fmt.Println(result)
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

	// Handle --debug flag for devices
	if *debug {
		printDebugJSON("Device", devices)
	}

	// Handle --devices flag
	if *listDevices {
		printDevicesTable(devices)
		return
	}

	// Play the playlist
	handlePlayPlaylist(ctx, client, devices, deviceName, playlistID, shuffle)
}

// handleListPlaylists fetches and displays all user playlists.
func handleListPlaylists(ctx context.Context, client *spotify.Client, debug *bool) {
	var allPlaylists []spotify.SimplePlaylist
	limit := 50
	offset := 0

	for {
		playlists, err := client.CurrentUsersPlaylists(ctx, spotify.Limit(limit), spotify.Offset(offset))
		if err != nil {
			log.Fatalf("Failed to get playlists: %v", err)
		}

		allPlaylists = append(allPlaylists, playlists.Playlists...)

		if len(playlists.Playlists) < limit {
			break
		}
		offset += limit
	}

	if *debug {
		printDebugJSON("Playlist", allPlaylists)
	}

	printPlaylistsTable(allPlaylists)
}

// handlePlayPlaylist starts playback on the specified device.
func handlePlayPlaylist(ctx context.Context, client *spotify.Client, devices []spotify.PlayerDevice, deviceName, playlistID string, shuffle *bool) {
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
	resolvedPlaylistID, err := resolvePlaylistID(ctx, client, playlistID)
	if err != nil {
		log.Fatalf("Failed to resolve playlist: %v", err)
	}

	// Get playlist info
	playlist, err := client.GetPlaylist(ctx, spotify.ID(resolvedPlaylistID))
	if err != nil {
		log.Fatalf("Failed to get playlist (ID: %s): %v\nMake sure the playlist ID is correct and the playlist is accessible.", resolvedPlaylistID, err)
	}

	trackCount := int(playlist.Tracks.Total)
	playlistURI := spotify.URI("spotify:playlist:" + resolvedPlaylistID)

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

// printDebugJSON prints raw JSON data for debugging.
func printDebugJSON(label string, data interface{}) {
	fmt.Printf("\n=== Raw %s Data ===\n", label)
	rawJSON, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		log.Printf("Warning: Failed to marshal %s data: %v", label, err)
	} else {
		fmt.Println(string(rawJSON))
	}
	fmt.Println("=== End Raw Data ===")
}
