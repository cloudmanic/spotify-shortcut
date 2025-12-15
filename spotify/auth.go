//
// Date: 2025-12-15
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Copyright (c) 2025 Cloudmanic Labs, LLC. All rights reserved.
//
// Description: Authentication logic for Spotify OAuth flow.
//

package spotify

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"

	spotifyLib "github.com/zmb3/spotify/v2"
	"golang.org/x/oauth2"

	spotifyauth "github.com/zmb3/spotify/v2/auth"
)

// InitAuth initializes the Spotify authenticator with the provided credentials.
func InitAuth(clientID, clientSecret, redirectURI string) {
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
}

// Authenticate starts the OAuth flow and returns an authenticated Spotify client.
// It starts a local HTTP server to handle the callback from Spotify.
func Authenticate() *spotifyLib.Client {
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
	SaveToken(tok)

	client := spotifyLib.New(auth.Client(r.Context(), tok))
	fmt.Fprintf(w, "Authentication successful! You can close this window.")
	ch <- client
}

// SaveToken saves the OAuth token to a file for reuse in future sessions.
func SaveToken(token *oauth2.Token) {
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

// LoadToken attempts to load a previously saved OAuth token from disk
// and returns a Spotify client if the token is still valid.
func LoadToken() (*spotifyLib.Client, error) {
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
	client := spotifyLib.New(auth.Client(ctx, &token))

	return client, nil
}
