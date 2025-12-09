//
// Date: 2025-12-09
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Copyright (c) 2025 Cloudmanic Labs, LLC. All rights reserved.
//
// Description: Configuration constants and global variables for the application.
//

package main

import (
	"github.com/zmb3/spotify/v2"

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
	spotifyClient  SpotifyClient
	apiAccessToken string
	tokenFile      string
)
