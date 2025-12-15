//
// Date: 2025-12-15
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Copyright (c) 2025 Cloudmanic Labs, LLC. All rights reserved.
//
// Description: Configuration constants and global variables for the application.
//

package spotify

import (
	spotifyLib "github.com/zmb3/spotify/v2"

	spotifyauth "github.com/zmb3/spotify/v2/auth"
)

const (
	DefaultRedirectURI = "http://127.0.0.1:8080/callback"
	DefaultTokenFile   = ".spotify_token.json"
)

var (
	auth           *spotifyauth.Authenticator
	ch             = make(chan *spotifyLib.Client)
	state          = "spotify-shortcut-state"
	spotifyClient  Client
	apiAccessToken string
	tokenFile      string
)

// SetTokenFile sets the token file path.
func SetTokenFile(path string) {
	tokenFile = path
}

// GetTokenFile returns the token file path.
func GetTokenFile() string {
	return tokenFile
}

// SetAPIAccessToken sets the API access token.
func SetAPIAccessToken(token string) {
	apiAccessToken = token
}

// GetAPIAccessToken returns the API access token.
func GetAPIAccessToken() string {
	return apiAccessToken
}

// SetClient sets the Spotify client.
func SetClient(client Client) {
	spotifyClient = client
}

// GetClient returns the Spotify client.
func GetClient() Client {
	return spotifyClient
}

// GetAuthenticator returns the authenticator.
func GetAuthenticator() *spotifyauth.Authenticator {
	return auth
}
