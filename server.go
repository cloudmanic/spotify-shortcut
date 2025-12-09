//
// Date: 2025-12-09
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Copyright (c) 2025 Cloudmanic Labs, LLC. All rights reserved.
//
// Description: HTTP API server and request handlers.
//

package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/zmb3/spotify/v2"
)

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
	mux.HandleFunc("/api/v1/pause", handlePauseRequest)

	fmt.Printf("Starting API server on port %s...\n", port)
	fmt.Println("Endpoints:")
	fmt.Println("  GET /api/v1/play?device=<name>&playlist=<name|id|url>&shuffle=<true|false>")
	fmt.Println("  GET /api/v1/pause")

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

// handlePauseRequest handles the /api/v1/pause endpoint to pause playback.
func handlePauseRequest(w http.ResponseWriter, r *http.Request) {
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

	// Pause playback
	result, err := pausePlayback()
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
