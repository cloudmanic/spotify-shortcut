//
// Date: 2026-10-07
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Copyright (c) 2026 Cloudmanic Labs, LLC. All rights reserved.
//
// Description: Text-to-action for /api/v1/ask. One Jev call answers every
// question about the request at once (what to do, which speaker, what to
// play, which words name the artist or song); code then reads only the
// answers that matter, does the action, and writes a sentence to read back.
// Jev can only pick from options we give it, so names are found by offering
// it every short run of words from the request, and Spotify results are
// matched by a second Jev call over the search results.
//

package spotify

import (
	"context"
	"errors"
	"fmt"
	"log"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	spotifyLib "github.com/zmb3/spotify/v2"
)

// Intents Jev chooses between. The order is the order Jev sees them in.
const (
	intentPlay          = "play"
	intentListPlaylists = "list_playlists"
	intentListSpeakers  = "list_speakers"
	intentNowPlaying    = "now_playing"
	intentStopAll       = "stop_all"
	intentPause         = "pause"
	intentResume        = "resume"
	intentSkip          = "skip"
	intentVolumeUp      = "volume_up"
	intentVolumeDown    = "volume_down"
	intentVolumeSet     = "volume_set"
	intentOther         = "other"
)

// Kinds of music a play request can ask for.
const (
	kindPlaylist  = "my_playlist"
	kindArtist    = "artist"
	kindAlbum     = "album"
	kindSong      = "song"
	kindGenre     = "genre"
	kindNotStated = "not_stated"
)

const (
	// minIntentProbability is the lowest top probability we act on. Below
	// it Jev is telling us it doesn't know what the person wants.
	minIntentProbability = 0.4

	// A speaker answer is a close call when the runner-up is another real
	// speaker with at least this probability and the winner is under
	// speakerSureProbability. We ask instead of guessing the room.
	speakerRunnerUpProbability = 0.25
	speakerSureProbability     = 0.7

	// genrePlaylistProbability is how sure Jev must be that one of the
	// person's own playlists fits a genre before we play it instead of
	// searching Spotify.
	genrePlaylistProbability = 0.6

	// latestThreshold is the yes-probability for "newest release".
	latestThreshold = 0.5

	// playCheckThreshold and namedMusicProbability gate the override that
	// turns a control intent into play. Band names like "Less Than Jake"
	// can read as "turn it down"; when Jev also says the person asked for
	// specific music and found a name for it, it is a play request.
	playCheckThreshold    = 0.5
	namedMusicProbability = 0.7

	// maxSpanWords and maxSpans keep the word-run options inside Jev's 255
	// options per choice question (one slot is kept for NONE).
	maxSpanWords = 6
	maxSpans     = 254

	// playlistCacheTTL is how long the playlist list is reused. Playlists
	// rarely change and fetching them adds a Spotify round trip.
	playlistCacheTTL = 5 * time.Minute

	// dryRunPrefix marks replies to requests that only pretended to act.
	dryRunPrefix = "Dry run: "
)

var (
	// shuffleWord matches "shuffle", "shuffled" and "shuffling". The person
	// asked for this to be the only way to turn shuffle on.
	shuffleWord = regexp.MustCompile(`(?i)\bshuffl`)

	// volumeNumber finds a volume level such as "40" or "40%".
	volumeNumber = regexp.MustCompile(`\b(\d{1,3})\s*(%|percent)?`)
)

// AskResult is the outcome of one request.
type AskResult struct {
	ActionTaken bool
	Message     string
	Intent      string
}

// askRequest is Jev's reading of a request, reduced to what code needs.
type askRequest struct {
	Text    string
	Intent  string
	Speaker string
	// SpeakerChoices holds the two speakers Jev couldn't pick between.
	SpeakerChoices []string
	Kind           string
	Playlist       *spotifyLib.SimplePlaylist
	// PlaylistProbability is how sure Jev is about Playlist.
	PlaylistProbability float64
	Artist              string
	Song                string
	Album               string
	Genre               string
	Latest              bool
	Shuffle             bool
}

// Ask turns a spoken request into an action and a sentence describing it.
// With dryRun set it decides everything, including which Spotify item it
// would play, but changes nothing.
func Ask(ctx context.Context, text string, dryRun bool) (AskResult, error) {
	if spotifyClient == nil {
		return AskResult{}, fmt.Errorf("Spotify not authenticated. Visit /auth to authenticate")
	}
	if jevClient == nil {
		return AskResult{}, fmt.Errorf("TYPESAFE_API_KEY is not set")
	}

	speakerDirectory.EnsureLoaded(ctx)
	playlists, err := cachedPlaylists(ctx)
	if err != nil {
		return AskResult{}, err
	}

	start := time.Now()
	req, err := interpret(ctx, text, speakerDirectory.List(), playlists)
	if err != nil {
		return AskResult{}, err
	}
	log.Printf("ask: %q -> intent=%s speaker=%q unsure=%v kind=%s playlist=%s artist=%q song=%q album=%q genre=%q latest=%v shuffle=%v (%s)",
		text, req.Intent, req.Speaker, req.SpeakerChoices, req.Kind, playlistName(req.Playlist),
		req.Artist, req.Song, req.Album, req.Genre, req.Latest, req.Shuffle, time.Since(start).Round(time.Millisecond))

	res, err := execute(ctx, req, dryRun)
	res.Intent = req.Intent
	return res, err
}

// interpret asks Jev every question about the request in one call. Most
// answers go unused for any one request; asking them all up front is
// faster than asking in rounds.
func interpret(ctx context.Context, text string, speakers []Speaker, playlists []spotifyLib.SimplePlaylist) (askRequest, error) {
	spans := requestSpans(text)
	playlistOptions, playlistByKey := playlistChoices(playlists)

	answers, err := jevClient.Ask(ctx, text, map[string]JevQuestion{
		"intent":      JevChoice("What does the person want the home music system to do?", intentOptions()),
		"speaker":     JevChoice("Which speaker or room does the person name? The text comes from speech-to-text, so a word may be replaced by a similar-sounding word (for example 'pull' for 'pool'). Pick the speaker whose name sounds most like what they said.", speakerOptions(speakers)),
		"kind":        JevChoice("What kind of music does the person ask to hear?", kindOptions()),
		"playlist":    JevChoice("Which of these saved playlists does the person ask to play?", playlistOptions),
		"artist":      JevChoice("Which words from the request are the name of the artist or band the person wants to hear?", spanOptions(spans, "The request names no artist or band")),
		"song":        JevChoice("Which words from the request are the title of the song the person wants to hear?", spanOptions(spans, "The request names no song")),
		"album":       JevChoice("Which words from the request are the title of the album the person wants to hear?", spanOptions(spans, "The request names no album title")),
		"genre":       JevChoice("Which words from the request describe the style or genre of music the person wants to hear?", spanOptions(spans, "The request names no style or genre")),
		"latest":      JevYesNo("Does the person ask for the newest or most recent release?"),
		"wants_music": JevYesNo("Does the person ask to start playing a specific playlist, artist, band, album, song or style of music?"),
	})
	if err != nil {
		return askRequest{}, fmt.Errorf("jev: %w", err)
	}

	req := askRequest{Text: text, Shuffle: shuffleWord.MatchString(text)}

	intent, p := answers["intent"].Top()
	req.Intent = intent
	if p < minIntentProbability {
		req.Intent = intentOther
	}

	req.Speaker, req.SpeakerChoices = readSpeaker(answers["speaker"])
	req.Kind = answers["kind"].Choice

	if key, p := answers["playlist"].Top(); key != noneOption {
		if pl, ok := playlistByKey[key]; ok {
			req.Playlist = &pl
			req.PlaylistProbability = p
		}
	}

	req.Artist = spanAnswer(answers["artist"])
	req.Song = spanAnswer(answers["song"])
	req.Album = spanAnswer(answers["album"])
	req.Genre = spanAnswer(answers["genre"])
	dedupeNames(&req)
	req.Latest = answers["latest"].Noul >= latestThreshold

	if isControlIntent(req.Intent) && answers["wants_music"].Noul >= playCheckThreshold && namesMusic(answers) {
		req.Intent = intentPlay
	}
	return req, nil
}

// dedupeNames clears a name Jev gave twice. With one name in the request
// ("Play Green Day"), Jev often offers it as both the artist and the song;
// the kind of music decides which one it is.
func dedupeNames(req *askRequest) {
	same := func(a, b string) bool { return a != "" && strings.EqualFold(a, b) }
	if same(req.Song, req.Artist) {
		if req.Kind == kindSong {
			req.Artist = ""
		} else {
			req.Song = ""
		}
	}
	if same(req.Album, req.Artist) {
		if req.Kind == kindAlbum {
			req.Artist = ""
		} else {
			req.Album = ""
		}
	}
	if same(req.Genre, req.Artist) {
		if req.Kind == kindGenre {
			req.Artist = ""
		} else {
			req.Genre = ""
		}
	}
}

// isControlIntent reports whether an intent changes what is already
// playing rather than starting something new.
func isControlIntent(intent string) bool {
	switch intent {
	case intentPause, intentResume, intentSkip, intentVolumeUp, intentVolumeDown, intentVolumeSet:
		return true
	}
	return false
}

// namesMusic reports whether Jev confidently found a playlist, artist,
// song or album name in the request.
func namesMusic(answers map[string]JevAnswer) bool {
	for _, key := range []string{"playlist", "artist", "song", "album"} {
		if choice, p := answers[key].Top(); choice != noneOption && choice != "" && p >= namedMusicProbability {
			return true
		}
	}
	return false
}

// readSpeaker returns the speaker Jev picked, or the top two when Jev is
// torn between two real speakers. "Pull house" could be the pool or the
// pool porch, and playing in the wrong room is worse than asking.
func readSpeaker(a JevAnswer) (string, []string) {
	top, pTop := a.Top()
	if top == noneOption || top == "" {
		return "", nil
	}
	second, pSecond := a.RunnerUp()
	if second != noneOption && second != "" && pSecond >= speakerRunnerUpProbability && pTop < speakerSureProbability {
		return "", []string{top, second}
	}
	return top, nil
}

// spanAnswer returns the words Jev picked, or "" for none.
func spanAnswer(a JevAnswer) string {
	if a.Choice == noneOption {
		return ""
	}
	return a.Choice
}

// execute carries out the request.
func execute(ctx context.Context, req askRequest, dryRun bool) (AskResult, error) {
	switch req.Intent {
	case intentListPlaylists:
		return listPlaylistsReply(ctx)
	case intentListSpeakers:
		return listSpeakersReply(), nil
	case intentNowPlaying:
		return nowPlayingReply(ctx, req)
	case intentPlay:
		return playReply(ctx, req, dryRun)
	case intentStopAll:
		return stopAllReply(ctx, dryRun)
	case intentPause, intentResume, intentSkip, intentVolumeUp, intentVolumeDown, intentVolumeSet:
		return controlReply(ctx, req, dryRun)
	}
	return noAction("Sorry, I didn't understand that."), nil
}

// listPlaylistsReply lists the person's playlists.
func listPlaylistsReply(ctx context.Context) (AskResult, error) {
	playlists, err := cachedPlaylists(ctx)
	if err != nil {
		return AskResult{}, err
	}
	if len(playlists) == 0 {
		return done("You don't have any playlists."), nil
	}
	names := make([]string, 0, len(playlists))
	for _, p := range playlists {
		names = append(names, strings.TrimSpace(p.Name))
	}
	return done("Here's a list of all your possible playlists:" + bulletList(names)), nil
}

// listSpeakersReply lists every speaker in the directory.
func listSpeakersReply() AskResult {
	speakers := speakerDirectory.List()
	if len(speakers) == 0 {
		return noAction("I couldn't find any speakers.")
	}
	names := make([]string, 0, len(speakers))
	for _, s := range speakers {
		names = append(names, s.Name)
	}
	return done("Here are your speakers:" + bulletList(names))
}

// nowPlayingReply says what is playing on each speaker, or on the one
// speaker the person asked about.
func nowPlayingReply(ctx context.Context, req askRequest) (AskResult, error) {
	if len(req.SpeakerChoices) > 0 {
		return didYouMean(req.SpeakerChoices), nil
	}
	snap := playbackSnapshot(ctx)
	playing := playingNow(snap)

	if req.Speaker != "" {
		for _, p := range playing {
			if strings.EqualFold(p.Speaker, req.Speaker) {
				return done(fmt.Sprintf("%s is playing on %s.", p.Describe(), p.Speaker)), nil
			}
		}
		return done(fmt.Sprintf("Nothing is playing on %s.", req.Speaker)), nil
	}

	switch len(playing) {
	case 0:
		return done("Nothing is playing right now."), nil
	case 1:
		return done(fmt.Sprintf("%s is playing on %s.", playing[0].Describe(), playing[0].Speaker)), nil
	}
	lines := make([]string, 0, len(playing))
	for _, p := range playing {
		lines = append(lines, fmt.Sprintf("%s: %s", p.Speaker, p.Describe()))
	}
	return done("Here's what's playing:" + bulletList(lines)), nil
}

// stopAllReply pauses every speaker that is playing, whichever account or
// app started it.
func stopAllReply(ctx context.Context, dryRun bool) (AskResult, error) {
	snap := playbackSnapshot(ctx)
	playing := playingNow(snap)
	if len(playing) == 0 {
		return noAction("Nothing is playing anywhere."), nil
	}

	names := make([]string, 0, len(playing))
	for _, p := range playing {
		names = append(names, p.Speaker)
	}
	if dryRun {
		return dryRunResult(fmt.Sprintf("Stopped the music on %s.", joinAnd(names))), nil
	}

	stopped, failed := stopEverywhere(ctx, playing)
	switch {
	case len(stopped) == 0:
		return noAction(fmt.Sprintf("I couldn't stop the music on %s.", joinAnd(failed))), nil
	case len(failed) > 0:
		return done(fmt.Sprintf("Stopped the music on %s. I couldn't stop %s.", joinAnd(stopped), joinAnd(failed))), nil
	}
	return done(fmt.Sprintf("Stopped the music on %s.", joinAnd(stopped))), nil
}

// controlReply handles pause, resume, skip and volume. With no speaker
// named, it acts on the one speaker that is playing (or paused, for
// resume) and asks which one when there are several.
func controlReply(ctx context.Context, req askRequest, dryRun bool) (AskResult, error) {
	if len(req.SpeakerChoices) > 0 {
		return didYouMean(req.SpeakerChoices), nil
	}

	level := -1
	if req.Intent == intentVolumeSet {
		m := volumeNumber.FindStringSubmatch(req.Text)
		if m == nil {
			return noAction("What volume should I set it to?"), nil
		}
		n, _ := strconv.Atoi(m[1])
		level = clampVolume(n)
	}

	snap := playbackSnapshot(ctx)

	var target speakerPlayback
	if req.Speaker != "" {
		p, ok := findPlayback(snap, req.Speaker)
		if !ok {
			return noAction(fmt.Sprintf("I couldn't find %s.", req.Speaker)), nil
		}
		target = p
	} else {
		candidates := playingNow(snap)
		if req.Intent == intentResume {
			candidates = pausedNow(snap)
		}
		switch len(candidates) {
		case 0:
			if req.Intent == intentResume {
				return noAction("Nothing is paused right now."), nil
			}
			return noAction("Nothing is playing right now."), nil
		case 1:
			target = candidates[0]
		default:
			names := make([]string, 0, len(candidates))
			for _, c := range candidates {
				names = append(names, c.Speaker)
			}
			return noAction(fmt.Sprintf("Music is playing on %s. Which speaker?", joinAnd(names))), nil
		}
	}

	var (
		msg    string
		action func() error
	)
	switch req.Intent {
	case intentPause:
		msg = fmt.Sprintf("Paused %s.", target.Speaker)
		action = func() error { return pauseSpeaker(ctx, target) }
	case intentResume:
		msg = fmt.Sprintf("Resumed %s.", target.Speaker)
		action = func() error { return resumeSpeaker(ctx, target) }
	case intentSkip:
		msg = fmt.Sprintf("Skipped to the next song on %s.", target.Speaker)
		action = func() error { return skipSpeaker(ctx, target) }
	case intentVolumeSet:
		msg = fmt.Sprintf("Set the volume on %s to %d.", target.Speaker, level)
		action = func() error { return setSpeakerVolume(ctx, target, level) }
	case intentVolumeUp, intentVolumeDown:
		current, err := currentVolume(ctx, target)
		if err != nil {
			return AskResult{}, fmt.Errorf("read volume of %s: %w", target.Speaker, err)
		}
		step, word := volumeStep, "up"
		if req.Intent == intentVolumeDown {
			step, word = -volumeStep, "down"
		}
		level = clampVolume(current + step)
		msg = fmt.Sprintf("Turned %s %s to %d.", target.Speaker, word, level)
		action = func() error { return setSpeakerVolume(ctx, target, level) }
	}

	if dryRun {
		return dryRunResult(msg), nil
	}
	if err := action(); err != nil {
		return AskResult{}, fmt.Errorf("%s on %s: %w", req.Intent, target.Speaker, err)
	}
	return done(msg), nil
}

// playReply finds what to play and starts it on the named speaker.
func playReply(ctx context.Context, req askRequest, dryRun bool) (AskResult, error) {
	if len(req.SpeakerChoices) > 0 {
		return didYouMean(req.SpeakerChoices), nil
	}
	if req.Speaker == "" {
		what := spokenWhat(req)
		if what == "" {
			return noAction("I can't play music because you didn't say which speaker."), nil
		}
		return noAction(fmt.Sprintf("I can't play %s because you didn't say which speaker.", what)), nil
	}

	item, found, notFound, err := findPlayItem(ctx, req)
	if err != nil {
		return AskResult{}, err
	}
	if !found {
		return noAction(notFound), nil
	}

	msg := fmt.Sprintf("Playing %s on %s.", item.Spoken, req.Speaker)
	if req.Shuffle {
		msg = fmt.Sprintf("Shuffling %s on %s.", item.Spoken, req.Speaker)
	}
	if dryRun {
		return dryRunResult(msg), nil
	}

	if err := playOnSpeaker(ctx, req.Speaker, item, req.Shuffle); err != nil {
		var unreachable errSpeakerUnreachable
		if errors.As(err, &unreachable) {
			log.Printf("ask: %v", err)
			return noAction(fmt.Sprintf("I couldn't reach %s. Make sure it's turned on.", req.Speaker)), nil
		}
		return AskResult{}, err
	}
	return done(msg), nil
}

// findPlayItem decides what to play. When nothing matches it returns
// found=false and a sentence saying what couldn't be found.
func findPlayItem(ctx context.Context, req askRequest) (item playItem, found bool, notFound string, err error) {
	// The person's own playlists come first: "play my coding mix", and a
	// genre that matches one of their mixes ("play some punk" -> Punk Mix).
	if req.Playlist != nil && (req.Kind == kindPlaylist ||
		(req.Kind == kindGenre && req.PlaylistProbability >= genrePlaylistProbability)) {
		return playItem{
			Spoken:      strings.TrimSpace(req.Playlist.Name),
			Kind:        "playlist",
			URI:         req.Playlist.URI,
			TotalTracks: int(req.Playlist.Tracks.Total),
		}, true, "", nil
	}

	switch {
	case req.Latest && req.Artist != "":
		return findNewest(ctx, req)

	case (req.Kind == kindSong || req.Kind == kindAlbum) && (req.Song != "" || req.Album != ""):
		// A lone title like "Dookie" can be read as a song or an album, so
		// search the kind Jev picked first and the other kind second.
		order := []string{"track", "album"}
		if req.Kind == kindAlbum {
			order = []string{"album", "track"}
		}
		for _, kind := range order {
			it, ok, err := findTitle(ctx, req, kind)
			if err != nil || ok {
				return it, ok, "", err
			}
		}
		if req.Kind == kindAlbum && req.Album != "" {
			return playItem{}, false, fmt.Sprintf("I couldn't find the album %s.", req.Album), nil
		}
		title := req.Song
		if title == "" {
			title = req.Album
		}
		if req.Artist != "" {
			return playItem{}, false, fmt.Sprintf("I couldn't find a %s version of %s.", req.Artist, title), nil
		}
		return playItem{}, false, fmt.Sprintf("I couldn't find the song %s.", title), nil

	case req.Artist != "" && req.Kind != kindGenre:
		it, ok, err := findArtist(ctx, req)
		if err != nil || ok {
			return it, ok, "", err
		}
		return playItem{}, false, fmt.Sprintf("I couldn't find an artist called %s.", req.Artist), nil

	case req.Genre != "":
		items, err := searchCatalog(ctx, "playlist", req.Genre)
		if err != nil {
			return playItem{}, false, "", err
		}
		if len(items) == 0 {
			return playItem{}, false, fmt.Sprintf("I couldn't find any %s music.", req.Genre), nil
		}
		it, ok, err := pickItem(ctx, req.Text,
			"Which playlist best fits the style of music the person asked for?",
			"None of these playlists fits", items)
		if err != nil {
			return playItem{}, false, "", err
		}
		// Any playlist from a genre search is a fair answer to "play some
		// jazz", so fall back to Spotify's top result.
		if !ok {
			it = items[0]
		}
		return it, true, "", nil

	case req.Kind == kindPlaylist:
		return playItem{}, false, "I couldn't find that playlist.", nil
	}

	return playItem{}, false, "What would you like me to play?", nil
}

// findTitle searches for a song ("track") or album by the title the person
// said, plus the artist if they named one, and lets Jev pick the match.
// Either title is used when the other is empty, since Jev often offers a
// lone title as only one of the two.
func findTitle(ctx context.Context, req askRequest, kind string) (playItem, bool, error) {
	title, other := req.Song, req.Album
	what := "song"
	if kind == "album" {
		title, other = req.Album, req.Song
		what = "album"
	}
	if title == "" {
		title = other
	}
	if title == "" {
		return playItem{}, false, nil
	}

	items, err := searchCatalog(ctx, kind, strings.TrimSpace(title+" "+req.Artist))
	if err != nil {
		return playItem{}, false, err
	}
	// Jev reads questions literally, so only mention the artist when the
	// person named one.
	question := fmt.Sprintf("Which search result is the %s the person asked for?", what)
	if req.Artist != "" {
		question = fmt.Sprintf("Which search result is the %s the person asked for, by the artist they named?", what)
	}
	return pickItem(ctx, req.Text, question, fmt.Sprintf("None of these is the %s the person asked for", what), items)
}

// findArtist searches for the artist the person named and lets Jev pick.
func findArtist(ctx context.Context, req askRequest) (playItem, bool, error) {
	items, err := searchCatalog(ctx, "artist", req.Artist)
	if err != nil {
		return playItem{}, false, err
	}
	return pickItem(ctx, req.Text,
		"Which search result is the artist or band the person asked for?",
		"None of these is the artist the person asked for", items)
}

// findNewest plays the artist's newest album, or newest album or single
// when the person didn't ask for an album.
func findNewest(ctx context.Context, req askRequest) (playItem, bool, string, error) {
	artist, ok, err := findArtist(ctx, req)
	if err != nil {
		return playItem{}, false, "", err
	}
	if !ok {
		return playItem{}, false, fmt.Sprintf("I couldn't find an artist called %s.", req.Artist), nil
	}
	album, err := newestRelease(ctx, artist.ArtistID, req.Kind != kindAlbum)
	if err != nil {
		return playItem{}, false, fmt.Sprintf("I couldn't find any releases by %s.", artist.Spoken), nil
	}
	word := "release"
	if req.Kind == kindAlbum {
		word = "album"
	}
	it := albumItem(*album)
	it.Spoken = fmt.Sprintf("the newest %s %s, %s,", artistNames(album.Artists), word, album.Name)
	return it, true, "", nil
}

// spokenWhat describes what the person asked to play, in their words, for
// replies like "I can't play less than Jake because ...".
func spokenWhat(req askRequest) string {
	switch {
	case req.Kind == kindPlaylist && req.Playlist != nil:
		return strings.TrimSpace(req.Playlist.Name)
	case req.Kind == kindSong && req.Song != "" && req.Artist != "":
		return fmt.Sprintf("%s by %s", req.Song, req.Artist)
	case req.Kind == kindSong && req.Song != "":
		return req.Song
	case req.Kind == kindAlbum && req.Album != "":
		return req.Album
	case req.Kind == kindGenre && req.Genre != "":
		return req.Genre
	}
	for _, words := range []string{req.Artist, req.Song, req.Album, req.Genre} {
		if words != "" {
			return words
		}
	}
	return ""
}

// intentOptions are the actions Jev chooses between. Band and song names
// can contain words like "less" or "stop", so the play option says so.
func intentOptions() JevOptions {
	return JevOptions{
		{intentPlay, "Start playing music: a playlist, artist, band, album, song or style of music. Band and song names can contain everyday words like 'less', 'up', 'down', 'stop' or 'skip'."},
		{intentListPlaylists, "Tell them which playlists they have"},
		{intentListSpeakers, "Tell them which speakers, rooms or devices they can play music on"},
		{intentNowPlaying, "Tell them what is playing right now, and on which speakers"},
		{intentStopAll, "Stop all music everywhere, on every speaker in the house, such as 'kill all music' or 'stop all the music'"},
		{intentPause, "Pause or stop the music that is playing"},
		{intentResume, "Resume or unpause music that was paused"},
		{intentSkip, "Skip to the next song"},
		{intentVolumeUp, "Make the music louder, such as 'turn it up' or 'volume up'"},
		{intentVolumeDown, "Make the music quieter, such as 'turn it down' or 'volume down'"},
		{intentVolumeSet, "Set the volume to a specific number or percent"},
		{intentOther, "Anything else"},
	}
}

// kindOptions are the kinds of music a play request can ask for.
func kindOptions() JevOptions {
	return JevOptions{
		{kindPlaylist, "One of their own saved playlists or mixes"},
		{kindArtist, "Music by an artist or band, with no specific song or album"},
		{kindAlbum, "An album by an artist"},
		{kindSong, "One specific song"},
		{kindGenre, "A style or mood of music, like punk, jazz or relaxing music"},
		{kindNotStated, "They do not say what to play"},
	}
}

// speakerOptions lists every known speaker plus "none named".
func speakerOptions(speakers []Speaker) JevOptions {
	opts := make(JevOptions, 0, len(speakers)+1)
	seen := map[string]bool{}
	for _, s := range speakers {
		if seen[s.Name] {
			continue
		}
		seen[s.Name] = true
		opts = append(opts, JevOption{Key: s.Name})
	}
	return append(opts, JevOption{Key: noneOption, Description: "No speaker or room is named"})
}

// playlistChoices lists the person's playlists plus "none of these", and
// maps each option key back to its playlist.
func playlistChoices(playlists []spotifyLib.SimplePlaylist) (JevOptions, map[string]spotifyLib.SimplePlaylist) {
	opts := make(JevOptions, 0, len(playlists)+1)
	byKey := map[string]spotifyLib.SimplePlaylist{}
	for _, p := range playlists {
		key := strings.TrimSpace(p.Name)
		if key == "" || key == noneOption {
			continue
		}
		for n := 2; ; n++ {
			if _, dup := byKey[key]; !dup {
				break
			}
			key = fmt.Sprintf("%s (%d)", strings.TrimSpace(p.Name), n)
		}
		byKey[key] = p
		opts = append(opts, JevOption{Key: key})
	}
	return append(opts, JevOption{Key: noneOption, Description: "They do not ask for one of these playlists"}), byKey
}

// spanOptions offers every run of words from the request plus "none".
func spanOptions(spans []string, noneMeans string) JevOptions {
	opts := make(JevOptions, 0, len(spans)+1)
	for _, s := range spans {
		opts = append(opts, JevOption{Key: s})
	}
	return append(opts, JevOption{Key: noneOption, Description: noneMeans})
}

// requestSpans returns every run of one to maxSpanWords words in the
// request, with punctuation trimmed from each word. Jev can't write out a
// name, but it can pick "Green Day" from this list.
func requestSpans(text string) []string {
	var words []string
	for _, w := range strings.Fields(text) {
		w = strings.Trim(w, ".,!?;:\"“”‘'’()[]")
		if w != "" {
			words = append(words, w)
		}
	}

	var spans []string
	seen := map[string]bool{}
	for n := 1; n <= maxSpanWords; n++ {
		for i := 0; i+n <= len(words); i++ {
			span := strings.Join(words[i:i+n], " ")
			key := strings.ToLower(span)
			if seen[key] || key == strings.ToLower(noneOption) {
				continue
			}
			seen[key] = true
			spans = append(spans, span)
			if len(spans) == maxSpans {
				return spans
			}
		}
	}
	return spans
}

// playlistCache holds the person's playlists between requests.
var playlistCache struct {
	mu      sync.Mutex
	items   []spotifyLib.SimplePlaylist
	expires time.Time
}

// cachedPlaylists returns the person's playlists, fetching them at most
// once per playlistCacheTTL.
func cachedPlaylists(ctx context.Context) ([]spotifyLib.SimplePlaylist, error) {
	playlistCache.mu.Lock()
	defer playlistCache.mu.Unlock()
	if time.Now().Before(playlistCache.expires) {
		return playlistCache.items, nil
	}
	items, err := ListPlaylists(ctx)
	if err != nil {
		return nil, err
	}
	playlistCache.items = items
	playlistCache.expires = time.Now().Add(playlistCacheTTL)
	return items, nil
}

// playlistName returns a playlist's name for logging, or "none".
func playlistName(p *spotifyLib.SimplePlaylist) string {
	if p == nil {
		return "none"
	}
	return strings.TrimSpace(p.Name)
}

// didYouMean asks the person to pick between two speakers.
func didYouMean(choices []string) AskResult {
	return noAction(fmt.Sprintf("Did you mean %s?", joinOr(choices)))
}

// done is a result for a request we carried out.
func done(msg string) AskResult {
	return AskResult{ActionTaken: true, Message: msg}
}

// noAction is a result for a request we did not carry out.
func noAction(msg string) AskResult {
	return AskResult{ActionTaken: false, Message: msg}
}

// dryRunResult reports what a dry run would have done.
func dryRunResult(msg string) AskResult {
	return AskResult{ActionTaken: false, Message: dryRunPrefix + msg}
}

// bulletList formats items as lines starting with "- ".
func bulletList(items []string) string {
	var b strings.Builder
	for _, it := range items {
		b.WriteString("\n- ")
		b.WriteString(it)
	}
	return b.String()
}

// joinAnd joins items as "A", "A and B" or "A, B and C".
func joinAnd(items []string) string {
	return joinWith(items, "and")
}

// joinOr joins items as "A", "A or B" or "A, B or C".
func joinOr(items []string) string {
	return joinWith(items, "or")
}

// joinWith joins items into an English list ending with the given word.
func joinWith(items []string, word string) string {
	switch len(items) {
	case 0:
		return ""
	case 1:
		return items[0]
	}
	return strings.Join(items[:len(items)-1], ", ") + " " + word + " " + items[len(items)-1]
}
