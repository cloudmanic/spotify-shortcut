//
// Date: 2026-10-07
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Copyright (c) 2026 Cloudmanic Labs, LLC. All rights reserved.
//
// Description: Playback controls for /api/v1/ask: what's playing where,
// stop everything, pause, resume, skip and volume. Spotify's API only sees
// our own account, so this combines it with each WiiM speaker's own report,
// which covers music started from any account or app.
//

package spotify

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"

	spotifyLib "github.com/zmb3/spotify/v2"
)

// volumeStep is how far "turn it up" or "turn it down" moves the volume.
const volumeStep = 10

// speakerPlayback is what one speaker is doing right now.
type speakerPlayback struct {
	Speaker string
	Title   string
	Artist  string
	Playing bool
	// Paused is true when there is something to resume.
	Paused bool
	// SpotifyID is set when Spotify knows the device.
	SpotifyID string
	// WiiMHost is set when the speaker answers the WiiM API.
	WiiMHost string
	// OurSession is true when this is our account's active Spotify device,
	// so Spotify's API can control it directly.
	OurSession bool
}

// Describe returns "Title by Artist", or whichever part is known.
func (p speakerPlayback) Describe() string {
	switch {
	case p.Title != "" && p.Artist != "":
		return fmt.Sprintf("%s by %s", p.Title, p.Artist)
	case p.Title != "":
		return p.Title
	case p.Artist != "":
		return p.Artist
	}
	return "music"
}

// playbackSnapshot reads our account's Spotify state and every WiiM
// speaker's player status at the same time, and merges them per speaker.
func playbackSnapshot(ctx context.Context) []speakerPlayback {
	speakers := speakerDirectory.List()

	var (
		state    *spotifyLib.PlayerState
		stateErr error
		mu       sync.Mutex
		wiim     = map[string]WiiMStatus{}
		wg       sync.WaitGroup
	)

	wg.Add(1)
	go func() {
		defer wg.Done()
		state, stateErr = spotifyClient.PlayerState(ctx)
	}()
	for _, s := range speakers {
		if s.WiiMHost() == "" {
			continue
		}
		wg.Add(1)
		go func(s Speaker) {
			defer wg.Done()
			st, err := wiimClient.Status(ctx, s.WiiMHost())
			if err != nil {
				log.Printf("ask: status of %s: %v", s.Name, err)
				return
			}
			mu.Lock()
			wiim[s.Name] = st
			mu.Unlock()
		}(s)
	}
	wg.Wait()

	// Without Spotify's view we can still see and stop every WiiM speaker,
	// which is most of the house, so carry on.
	if stateErr != nil {
		log.Printf("ask: spotify player state: %v", stateErr)
	}

	var out []speakerPlayback
	ourName := ""
	if state != nil && state.Device.ID != "" {
		ourName = deviceDisplayName(state.Device)
		p := speakerPlayback{
			Speaker:    ourName,
			Playing:    state.Playing,
			Paused:     !state.Playing,
			SpotifyID:  string(state.Device.ID),
			OurSession: true,
		}
		if state.Item != nil {
			p.Title = state.Item.Name
			p.Artist = artistNames(state.Item.Artists)
		}
		if s, ok := speakerDirectory.Find(ourName); ok {
			p.WiiMHost = s.WiiMHost()
		}
		out = append(out, p)
	}

	for _, s := range speakers {
		st, ok := wiim[s.Name]
		if !ok || strings.EqualFold(s.Name, ourName) {
			continue
		}
		out = append(out, speakerPlayback{
			Speaker:   s.Name,
			Title:     st.Title,
			Artist:    st.Artist,
			Playing:   st.Playing(),
			Paused:    st.State == "pause",
			SpotifyID: s.SpotifyID,
			WiiMHost:  s.WiiMHost(),
		})
	}
	return out
}

// playingNow filters a snapshot to speakers that are playing.
func playingNow(snap []speakerPlayback) []speakerPlayback {
	var out []speakerPlayback
	for _, p := range snap {
		if p.Playing {
			out = append(out, p)
		}
	}
	return out
}

// pausedNow filters a snapshot to speakers with something to resume.
func pausedNow(snap []speakerPlayback) []speakerPlayback {
	var out []speakerPlayback
	for _, p := range snap {
		if p.Paused {
			out = append(out, p)
		}
	}
	return out
}

// findPlayback returns the snapshot entry for a speaker. A speaker that is
// silent and not on our session still gets an entry from the directory so
// commands like volume can reach it.
func findPlayback(snap []speakerPlayback, speaker string) (speakerPlayback, bool) {
	for _, p := range snap {
		if strings.EqualFold(p.Speaker, speaker) {
			return p, true
		}
	}
	if s, ok := speakerDirectory.Find(speaker); ok {
		return speakerPlayback{Speaker: s.Name, SpotifyID: s.SpotifyID, WiiMHost: s.WiiMHost()}, true
	}
	return speakerPlayback{}, false
}

// pauseSpeaker pauses one speaker. Our own Spotify session goes through
// Spotify; anything else goes to the WiiM speaker directly.
func pauseSpeaker(ctx context.Context, p speakerPlayback) error {
	if p.OurSession || p.WiiMHost == "" {
		return spotifyClient.PauseOpt(ctx, deviceOpt(p))
	}
	return wiimClient.Command(ctx, p.WiiMHost, "pause")
}

// resumeSpeaker resumes one paused speaker.
func resumeSpeaker(ctx context.Context, p speakerPlayback) error {
	if p.OurSession || p.WiiMHost == "" {
		return spotifyClient.PlayOpt(ctx, deviceOpt(p))
	}
	return wiimClient.Command(ctx, p.WiiMHost, "resume")
}

// skipSpeaker skips to the next song on one speaker.
func skipSpeaker(ctx context.Context, p speakerPlayback) error {
	if p.OurSession || p.WiiMHost == "" {
		return spotifyClient.NextOpt(ctx, deviceOpt(p))
	}
	return wiimClient.Command(ctx, p.WiiMHost, "next")
}

// setSpeakerVolume sets one speaker's volume (0-100). WiiM speakers are set
// directly so this works whichever account is playing on them.
func setSpeakerVolume(ctx context.Context, p speakerPlayback, level int) error {
	if p.WiiMHost != "" {
		return wiimClient.Command(ctx, p.WiiMHost, fmt.Sprintf("vol:%d", level))
	}
	return spotifyClient.VolumeOpt(ctx, level, deviceOpt(p))
}

// currentVolume reads a speaker's volume from the same place
// setSpeakerVolume writes it: the WiiM speaker itself, or Spotify's
// devices list for anything else.
func currentVolume(ctx context.Context, p speakerPlayback) (int, error) {
	if p.WiiMHost != "" {
		st, err := wiimClient.Status(ctx, p.WiiMHost)
		if err != nil {
			return 0, err
		}
		return st.Volume, nil
	}
	devices, err := spotifyClient.PlayerDevices(ctx)
	if err != nil {
		return 0, err
	}
	for _, d := range devices {
		if strings.EqualFold(string(d.ID), p.SpotifyID) {
			return int(d.Volume), nil
		}
	}
	return 0, fmt.Errorf("no volume reported for %s", p.Speaker)
}

// stopEverywhere pauses every speaker that is playing, all at once. It
// returns the speakers it stopped and the ones that failed, in the order
// given, so the spoken reply doesn't shuffle from one request to the next.
func stopEverywhere(ctx context.Context, playing []speakerPlayback) (stopped, failed []string) {
	errs := make([]error, len(playing))
	var wg sync.WaitGroup
	for i, p := range playing {
		wg.Add(1)
		go func(i int, p speakerPlayback) {
			defer wg.Done()
			errs[i] = pauseSpeaker(ctx, p)
		}(i, p)
	}
	wg.Wait()

	for i, p := range playing {
		if errs[i] != nil {
			log.Printf("ask: stop %s: %v", p.Speaker, errs[i])
			failed = append(failed, p.Speaker)
			continue
		}
		stopped = append(stopped, p.Speaker)
	}
	return stopped, failed
}

// deviceOpt targets a Spotify call at the speaker's device, or at our
// active device when the ID is unknown.
func deviceOpt(p speakerPlayback) *spotifyLib.PlayOptions {
	if p.SpotifyID == "" {
		return &spotifyLib.PlayOptions{}
	}
	id := spotifyLib.ID(p.SpotifyID)
	return &spotifyLib.PlayOptions{DeviceID: &id}
}

// clampVolume keeps a volume inside 0-100.
func clampVolume(v int) int {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}
