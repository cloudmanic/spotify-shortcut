//
// Date: 2026-10-07
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Copyright (c) 2026 Cloudmanic Labs, LLC. All rights reserved.
//
// Description: Finding and starting music for /api/v1/ask. Searches
// Spotify's catalog, lets Jev pick the result that matches what the person
// said, finds an artist's newest release, and starts playback on a named
// speaker (claiming the speaker first when another account owns it).
//

package spotify

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"strings"
	"time"

	spotifyLib "github.com/zmb3/spotify/v2"
)

const (
	// searchLimit is the most results Spotify's search endpoint returns.
	searchLimit = 10

	// artistAlbumsPageSize is the most albums Spotify returns per page, and
	// artistAlbumsMaxPages bounds how many pages we read for "newest".
	artistAlbumsPageSize = 10
	artistAlbumsMaxPages = 5

	// noneOption is the choice key meaning "none of the above".
	noneOption = "NONE"
)

// playItem is something we can start on a speaker.
type playItem struct {
	// Spoken is how we describe the item in the reply, e.g.
	// "99 Red Balloons by Goldfinger".
	Spoken string
	// Kind is "track", "album", "artist" or "playlist".
	Kind string
	URI  spotifyLib.URI
	// TotalTracks is used to pick a random start for shuffled albums and
	// playlists. Zero when unknown.
	TotalTracks int
	// ArtistID is set for artists so we can look up their releases.
	ArtistID spotifyLib.ID
}

// searchCatalog searches Spotify for one kind of item and returns the
// results as play items, in Spotify's relevance order.
func searchCatalog(ctx context.Context, kind, query string) ([]playItem, error) {
	types := map[string]spotifyLib.SearchType{
		"track":    spotifyLib.SearchTypeTrack,
		"artist":   spotifyLib.SearchTypeArtist,
		"album":    spotifyLib.SearchTypeAlbum,
		"playlist": spotifyLib.SearchTypePlaylist,
	}
	st, ok := types[kind]
	if !ok {
		return nil, fmt.Errorf("unknown search kind %q", kind)
	}

	// MarketFromToken limits results to music playable in the account's
	// country, so the play call doesn't fail on a region-locked track.
	res, err := spotifyClient.Search(ctx, query, st,
		spotifyLib.Limit(searchLimit), spotifyLib.Market(spotifyLib.MarketFromToken))
	if err != nil {
		return nil, fmt.Errorf("spotify search: %w", err)
	}

	var items []playItem
	switch kind {
	case "track":
		if res.Tracks != nil {
			for _, t := range res.Tracks.Tracks {
				items = append(items, playItem{
					Spoken: fmt.Sprintf("%s by %s", strings.TrimSpace(t.Name), artistNames(t.Artists)),
					Kind:   "track",
					URI:    t.URI,
				})
			}
		}
	case "artist":
		if res.Artists != nil {
			for _, a := range res.Artists.Artists {
				items = append(items, playItem{Spoken: strings.TrimSpace(a.Name), Kind: "artist", URI: a.URI, ArtistID: a.ID})
			}
		}
	case "album":
		if res.Albums != nil {
			for _, a := range res.Albums.Albums {
				items = append(items, albumItem(a))
			}
		}
	case "playlist":
		if res.Playlists != nil {
			for _, p := range res.Playlists.Playlists {
				// Spotify returns null entries in playlist search results.
				if p.ID == "" {
					continue
				}
				items = append(items, playItem{
					Spoken:      strings.TrimSpace(p.Name),
					Kind:        "playlist",
					URI:         p.URI,
					TotalTracks: int(p.Tracks.Total),
				})
			}
		}
	}
	return items, nil
}

// albumItem turns an album into a play item described as
// "the album <name> by <artist>".
func albumItem(a spotifyLib.SimpleAlbum) playItem {
	return playItem{
		Spoken:      fmt.Sprintf("the album %s by %s", strings.TrimSpace(a.Name), artistNames(a.Artists)),
		Kind:        "album",
		URI:         a.URI,
		TotalTracks: int(a.TotalTracks),
	}
}

// artistNames joins artist names as "A, B and C".
func artistNames(artists []spotifyLib.SimpleArtist) string {
	names := make([]string, 0, len(artists))
	for _, a := range artists {
		names = append(names, strings.TrimSpace(a.Name))
	}
	return joinAnd(names)
}

// pickItem asks Jev which search result is the one the person asked for.
// It returns false when Jev says none of them is. Results are offered in
// Spotify's order because Jev leans toward earlier options and Spotify's
// first result is usually the best guess.
func pickItem(ctx context.Context, text, instructions, noneMeans string, items []playItem) (playItem, bool, error) {
	if len(items) == 0 {
		return playItem{}, false, nil
	}

	options := make(JevOptions, 0, len(items)+1)
	byKey := map[string]playItem{}
	for _, it := range items {
		key := uniqueKey(it.Spoken, byKey)
		byKey[key] = it
		options = append(options, JevOption{Key: key})
	}
	options = append(options, JevOption{Key: noneOption, Description: noneMeans})

	answers, err := jevClient.Ask(ctx, text, map[string]JevQuestion{
		"pick": JevChoice(instructions, options),
	})
	if err != nil {
		return playItem{}, false, fmt.Errorf("jev pick: %w", err)
	}
	choice, p := answers["pick"].Top()
	log.Printf("ask: pick %q (p=%.2f) from %d results", choice, p, len(items))
	it, ok := byKey[choice]
	return it, ok, nil
}

// uniqueKey returns base, or base with a counter when another option
// already uses it. Choice option keys must be unique.
func uniqueKey(base string, taken map[string]playItem) string {
	key := base
	for n := 2; ; n++ {
		if _, dup := taken[key]; !dup {
			return key
		}
		key = fmt.Sprintf("%s (%d)", base, n)
	}
}

// newestRelease returns the artist's most recent album, or most recent
// album or single when includeSingles is set. Dates are compared here in
// code because Jev is not reliable at ordering dates.
func newestRelease(ctx context.Context, artistID spotifyLib.ID, includeSingles bool) (*spotifyLib.SimpleAlbum, error) {
	types := []spotifyLib.AlbumType{spotifyLib.AlbumTypeAlbum}
	if includeSingles {
		types = append(types, spotifyLib.AlbumTypeSingle)
	}

	var newest *spotifyLib.SimpleAlbum
	var newestDate time.Time
	for page := 0; page < artistAlbumsMaxPages; page++ {
		res, err := spotifyClient.GetArtistAlbums(ctx, artistID, types,
			spotifyLib.Limit(artistAlbumsPageSize),
			spotifyLib.Offset(page*artistAlbumsPageSize),
			spotifyLib.Market(spotifyLib.MarketFromToken))
		if err != nil {
			return nil, fmt.Errorf("artist albums: %w", err)
		}
		for i := range res.Albums {
			a := res.Albums[i]
			if d := a.ReleaseDateTime(); newest == nil || d.After(newestDate) {
				newest, newestDate = &a, d
			}
		}
		if len(res.Albums) < artistAlbumsPageSize {
			break
		}
	}
	if newest == nil {
		return nil, fmt.Errorf("artist has no releases")
	}
	return newest, nil
}

// playOnSpeaker starts an item on the named speaker. It never falls back to
// another speaker: playing in the wrong room is worse than saying no. With
// shuffle off, albums and playlists start at track one and shuffle is
// turned off; with it on, they start at a random track with shuffle on.
func playOnSpeaker(ctx context.Context, speakerName string, item playItem, shuffle bool) error {
	deviceID, err := resolveSpeakerDevice(ctx, speakerName)
	if err != nil {
		return err
	}

	opts := &spotifyLib.PlayOptions{DeviceID: &deviceID}
	switch item.Kind {
	case "track":
		opts.URIs = []spotifyLib.URI{item.URI}
	default:
		uri := item.URI
		opts.PlaybackContext = &uri
	}

	// Spotify only accepts a start offset for albums and playlists.
	if item.Kind == "album" || item.Kind == "playlist" {
		start := 0
		if shuffle && item.TotalTracks > 0 {
			start = rand.Intn(item.TotalTracks)
		}
		opts.PlaybackOffset = &spotifyLib.PlaybackOffset{Position: &start}
	}

	if err := spotifyClient.PlayOpt(ctx, opts); err != nil {
		return fmt.Errorf("start playback: %w", err)
	}

	// Give the device a moment to take the session before changing its
	// shuffle state; Spotify rejects the call on a device that isn't
	// active yet.
	time.Sleep(shuffleSettleDelay)
	if err := spotifyClient.ShuffleOpt(ctx, shuffle, &spotifyLib.PlayOptions{DeviceID: &deviceID}); err != nil {
		log.Printf("ask: set shuffle=%v on %s: %v", shuffle, speakerName, err)
	}
	return nil
}

// shuffleSettleDelay is how long playOnSpeaker waits before setting
// shuffle. A variable so tests can skip the wait.
var shuffleSettleDelay = 500 * time.Millisecond

// resolveSpeakerDevice returns the Spotify device ID for a speaker name.
// When the speaker isn't linked to our account, it claims it over the LAN
// first.
func resolveSpeakerDevice(ctx context.Context, speakerName string) (spotifyLib.ID, error) {
	devices, err := spotifyClient.PlayerDevices(ctx)
	if err != nil {
		return "", fmt.Errorf("get devices: %w", err)
	}
	if id, ok := findDeviceID(devices, speakerName); ok {
		return id, nil
	}

	log.Printf("ask: %q not in Spotify devices, claiming it", speakerName)
	claim, err := ClaimDevice(ctx, speakerName)
	if err != nil {
		return "", errSpeakerUnreachable{speaker: speakerName, cause: err}
	}
	return spotifyLib.ID(claim.DeviceID), nil
}

// findDeviceID matches a speaker name against Spotify's devices, by name or
// by the device ID the speaker directory knows for that name.
func findDeviceID(devices []spotifyLib.PlayerDevice, speakerName string) (spotifyLib.ID, bool) {
	knownID := ""
	if s, ok := speakerDirectory.Find(speakerName); ok {
		knownID = s.SpotifyID
	}
	for _, d := range devices {
		if deviceMatches(d, speakerName) || (knownID != "" && strings.EqualFold(string(d.ID), knownID)) {
			return d.ID, true
		}
	}
	return "", false
}

// errSpeakerUnreachable means a speaker couldn't be linked to our account,
// usually because it is off or unplugged.
type errSpeakerUnreachable struct {
	speaker string
	cause   error
}

// Error describes the failure, including the underlying claim error.
func (e errSpeakerUnreachable) Error() string {
	return fmt.Sprintf("speaker %q unreachable: %v", e.speaker, e.cause)
}
