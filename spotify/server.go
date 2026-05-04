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
	"strconv"
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
	mux.HandleFunc("/api/v1/devices", HandleDevicesRequest)
	mux.HandleFunc("/api/v1/lan-devices", HandleLANDevicesRequest)
	mux.HandleFunc("/api/v1/wake", HandleWakeRequest)
	mux.HandleFunc("/api/v1/playlists", HandlePlaylistsRequest)
	mux.HandleFunc("/api/v1/volume", HandleVolumeRequest)

	fmt.Printf("Starting API server on port %s...\n", port)
	fmt.Println("Endpoints:")
	fmt.Println("  GET /api/v1/play?device=<name>&playlist=<name|id|url>&shuffle=<true|false>")
	fmt.Println("  GET /api/v1/pause")
	fmt.Println("  GET /api/v1/devices")
	fmt.Println("  GET /api/v1/lan-devices")
	fmt.Println("  GET /api/v1/wake?device=<name>")
	fmt.Println("  GET /api/v1/playlists")
	fmt.Println("  GET /api/v1/volume?level=0-100&device=<optional name>")

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

// HandleVolumeRequest handles GET /api/v1/volume?level=0-100&device=<name>.
// Sets playback volume on the named device, or on the current active
// device if no name is given. Premium-only on Spotify's side.
func HandleVolumeRequest(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	token := r.URL.Query().Get("token")
	if token == "" {
		token = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	}
	if token != apiAccessToken {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(APIResponse{Success: false, Error: "Invalid or missing access token"})
		return
	}

	levelStr := r.URL.Query().Get("level")
	if levelStr == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(APIResponse{Success: false, Error: "level parameter is required (0-100)"})
		return
	}

	level, err := strconv.Atoi(levelStr)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(APIResponse{Success: false, Error: "level must be an integer"})
		return
	}

	deviceName := r.URL.Query().Get("device")

	msg, err := SetVolume(level, deviceName)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(APIResponse{Success: false, Error: err.Error()})
		return
	}

	json.NewEncoder(w).Encode(APIResponse{Success: true, Message: msg})
}

// HandlePlaylistsRequest handles GET /api/v1/playlists. Returns every
// playlist owned or followed by the authenticated Spotify user. The server
// paginates through Spotify's API so clients receive a single flat list.
func HandlePlaylistsRequest(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	token := r.URL.Query().Get("token")
	if token == "" {
		token = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	}
	if token != apiAccessToken {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(APIResponse{Success: false, Error: "Invalid or missing access token"})
		return
	}

	playlists, err := ListPlaylists(r.Context())
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(APIResponse{Success: false, Error: err.Error()})
		return
	}

	out := make([]PlaylistInfo, 0, len(playlists))
	for _, p := range playlists {
		out = append(out, PlaylistInfo{
			ID:     string(p.ID),
			Name:   p.Name,
			Owner:  p.Owner.DisplayName,
			Tracks: uint(p.Tracks.Total),
		})
	}

	json.NewEncoder(w).Encode(PlaylistsResponse{
		Success:   true,
		Message:   fmt.Sprintf("Found %d playlist(s)", len(out)),
		Playlists: out,
	})
}

// HandleLANDevicesRequest handles GET /api/v1/lan-devices. Returns every
// Spotify Connect device currently advertising itself on the local network
// via mDNS — i.e. every speaker that *could* be played to, including ones
// linked to other accounts. Useful for picking a wake target when the
// device isn't yet in the Spotify cloud devices list.
func HandleLANDevicesRequest(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	token := r.URL.Query().Get("token")
	if token == "" {
		token = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	}
	if token != apiAccessToken {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(APIResponse{Success: false, Error: "Invalid or missing access token"})
		return
	}

	locals, err := defaultDiscoveryCache.Devices(r.Context())
	if err != nil && len(locals) == 0 {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(APIResponse{Success: false, Error: err.Error()})
		return
	}

	out := make([]LANDeviceInfo, 0, len(locals))
	for _, d := range locals {
		out = append(out, LANDeviceInfo{
			Name:     d.FriendlyName,
			Hostname: d.Hostname,
			IP:       d.IP,
			Port:     d.Port,
			Instance: d.InstanceName,
		})
	}

	json.NewEncoder(w).Encode(LANDevicesResponse{
		Success: true,
		Message: fmt.Sprintf("Found %d device(s) on LAN", len(out)),
		Devices: out,
	})
}

// HandleWakeRequest handles GET /api/v1/wake?device=<name>. It runs the
// zeroconf addUser handshake against the named device on the LAN, claiming
// it for the current Spotify account, and waits for the device to appear
// in Spotify cloud. Useful when a household member played to the speaker
// from their account and the device is no longer linked to ours.
func HandleWakeRequest(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Verify access token (query param takes priority over header)
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

	deviceName := r.URL.Query().Get("device")
	if deviceName == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(APIResponse{
			Success: false,
			Error:   "device parameter is required",
		})
		return
	}

	// Use a generous request context — the claim flow polls for up to ~12s.
	result, err := ClaimDevice(r.Context(), deviceName)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(APIResponse{
			Success: false,
			Error:   err.Error(),
		})
		return
	}

	msg := fmt.Sprintf("Claimed %q (deviceID=%s)", deviceName, result.DeviceID)
	if result.AlreadyActive {
		msg = fmt.Sprintf("%q already linked to current account (deviceID=%s)", deviceName, result.DeviceID)
	}

	json.NewEncoder(w).Encode(APIResponse{
		Success: true,
		Message: msg,
	})
}

// HandleDevicesRequest handles the /api/v1/devices endpoint, returning the
// list of Spotify Connect devices visible to the authenticated user as JSON.
// Requires the API access token (query param `token` or Bearer header).
func HandleDevicesRequest(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Verify access token (query param takes priority over header)
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

	// Fetch devices from Spotify
	devices, err := ListDevices()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(APIResponse{
			Success: false,
			Error:   err.Error(),
		})
		return
	}

	// Convert to JSON-friendly DeviceInfo slice so we control the contract
	infos := make([]DeviceInfo, 0, len(devices))
	for _, d := range devices {
		infos = append(infos, DeviceInfo{
			ID:     string(d.ID),
			Name:   d.Name,
			Type:   d.Type,
			Active: d.Active,
		})
	}

	json.NewEncoder(w).Encode(APIResponse{
		Success: true,
		Message: fmt.Sprintf("Found %d device(s)", len(infos)),
		Devices: infos,
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
