//
// Date: 2025-12-15
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Copyright (c) 2025 Cloudmanic Labs, LLC. All rights reserved.
//
// Description: HTTP API server and request handlers.
//

package spotify

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	spotifyLib "github.com/zmb3/spotify/v2"
)

// loggingResponseWriter wraps http.ResponseWriter to capture the status code.
type loggingResponseWriter struct {
	http.ResponseWriter
	statusCode int
}

// WriteHeader captures the status code before writing it.
func (lrw *loggingResponseWriter) WriteHeader(code int) {
	lrw.statusCode = code
	lrw.ResponseWriter.WriteHeader(code)
}

// loggingMiddleware wraps an http.Handler and logs each request.
func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// Wrap the response writer to capture status code
		lrw := &loggingResponseWriter{ResponseWriter: w, statusCode: http.StatusOK}

		// Process the request
		next.ServeHTTP(lrw, r)

		// Log the request
		log.Printf("%s %s %d %s", r.Method, r.URL.Path, lrw.statusCode, time.Since(start))
	})
}

// StartAPIServer starts the HTTP API server for remote control.
func StartAPIServer() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", HandleRootRequest)
	mux.HandleFunc("/auth", HandleAuthRequest)
	mux.HandleFunc("/callback", HandleAuthCallback)
	mux.HandleFunc("/api/v1/play", HandlePlayRequest)
	mux.HandleFunc("/api/v1/pause", HandlePauseRequest)

	fmt.Printf("Starting API server on port %s...\n", port)
	fmt.Println("Endpoints:")
	fmt.Println("  GET /api/v1/play?device=<name>&playlist=<name|id|url>&shuffle=<true|false>")
	fmt.Println("  GET /api/v1/pause")

	// Wrap mux with logging middleware
	handler := loggingMiddleware(mux)

	err := http.ListenAndServe(":"+port, handler)
	if err != nil {
		log.Fatalf("Failed to start API server: %v", err)
	}
}

// HandleRootRequest handles requests to the root path with a simple message.
func HandleRootRequest(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/plain")
	fmt.Fprint(w, "app coming soon....")
}

// HandleAuthRequest redirects the user to Spotify's authorization page.
// Requires the API access token for security.
func HandleAuthRequest(w http.ResponseWriter, r *http.Request) {
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

// HandleAuthCallback handles the OAuth callback from Spotify after user authorization.
func HandleAuthCallback(w http.ResponseWriter, r *http.Request) {
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
	SaveToken(tok)

	// Update the global client with the new token
	spotifyClient = spotifyLib.New(auth.Client(r.Context(), tok))

	w.Header().Set("Content-Type", "text/plain")
	fmt.Fprint(w, "Authentication successful! You can close this window.")
}

// HandlePlayRequest handles the /api/v1/play endpoint to start playlist playback.
func HandlePlayRequest(w http.ResponseWriter, r *http.Request) {
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
	result, err := PlayPlaylist(deviceName, playlistInput, shuffle)
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

// HandlePauseRequest handles the /api/v1/pause endpoint to pause playback.
func HandlePauseRequest(w http.ResponseWriter, r *http.Request) {
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
	result, err := PausePlayback()
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
