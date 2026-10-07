//
// Date: 2026-10-07
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Copyright (c) 2026 Cloudmanic Labs, LLC. All rights reserved.
//
// Description: The speaker directory: one list of every place we can play
// music, with its friendly name, its Spotify Connect device ID and its LAN
// address. Spotify's devices list often shows a speaker only by its hex
// device ID; each speaker reports that same ID from its zeroconf getInfo
// endpoint, so the directory can put the right name on it. mDNS scans miss
// speakers now and then, so known speakers are saved to disk and re-checked
// at their last address on every refresh. A background loop keeps the list
// fresh because a scan takes several seconds and spoken requests can't wait.
//

package spotify

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	spotifyLib "github.com/zmb3/spotify/v2"
)

const (
	// speakerRefreshInterval is how often the background loop re-scans.
	speakerRefreshInterval = 60 * time.Second

	// speakerForgetAfter is how long a speaker stays listed after it last
	// answered. Speakers in a house rarely change; a week rides out one
	// being unplugged for a few days.
	speakerForgetAfter = 7 * 24 * time.Hour

	// speakerProbeTimeout bounds the getInfo and WiiM checks per device.
	speakerProbeTimeout = 3 * time.Second

	// DefaultSpeakerCacheFile is where the server saves known speakers.
	DefaultSpeakerCacheFile = ".speakers.json"
)

// speakerDirectory is the package-level directory used by the API server.
var speakerDirectory = NewSpeakerDirectory(
	func(ctx context.Context) ([]LocalDevice, error) { return defaultDiscoveryCache.Devices(ctx) },
	func(ctx context.Context, d LocalDevice) (*GetInfoResponse, error) {
		return NewZeroconfClient(d.IP, d.Port, "/zc").GetInfo(ctx)
	},
)

// EnableSpeakerCache loads saved speakers from path and saves the directory
// there after every refresh.
func EnableSpeakerCache(path string) {
	speakerDirectory.enableCache(path)
}

// Speaker is one place music can play.
type Speaker struct {
	// Name is the friendly name people say, e.g. "Master Bedroom Speakers".
	Name string `json:"name"`
	// SpotifyID is the Spotify Connect device ID. Empty if unknown.
	SpotifyID string `json:"spotify_id,omitempty"`
	// IP and ZeroconfPort locate the speaker on the LAN. Empty for devices
	// only Spotify knows about, like a web player.
	IP           string `json:"ip,omitempty"`
	ZeroconfPort int    `json:"zeroconf_port,omitempty"`
	// WiiM is true when the speaker answers the WiiM API at IP.
	WiiM bool `json:"wiim,omitempty"`
}

// WiiMHost returns the address for WiiM API calls, or "" when the speaker
// isn't a WiiM.
func (s Speaker) WiiMHost() string {
	if s.WiiM {
		return s.IP
	}
	return ""
}

// knownSpeaker is a directory entry plus when it last answered.
type knownSpeaker struct {
	Speaker
	LastSeen time.Time `json:"last_seen"`
}

// SpeakerDirectory merges LAN discovery with Spotify's device list.
type SpeakerDirectory struct {
	mu        sync.RWMutex
	speakers  map[string]knownSpeaker
	refreshed bool
	cacheFile string

	// refreshMu makes sure only one refresh runs at a time.
	refreshMu sync.Mutex

	discover func(ctx context.Context) ([]LocalDevice, error)
	getInfo  func(ctx context.Context, d LocalDevice) (*GetInfoResponse, error)
}

// NewSpeakerDirectory builds a directory from a LAN discovery function and
// a zeroconf getInfo function. Tests pass fakes for both.
func NewSpeakerDirectory(
	discover func(ctx context.Context) ([]LocalDevice, error),
	getInfo func(ctx context.Context, d LocalDevice) (*GetInfoResponse, error),
) *SpeakerDirectory {
	return &SpeakerDirectory{
		speakers: map[string]knownSpeaker{},
		discover: discover,
		getInfo:  getInfo,
	}
}

// enableCache loads saved speakers from path, if the file exists, and
// turns on saving after each refresh.
func (d *SpeakerDirectory) enableCache(path string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.cacheFile = path

	raw, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var saved []knownSpeaker
	if err := json.Unmarshal(raw, &saved); err != nil {
		log.Printf("speaker cache %s: %v", path, err)
		return
	}
	for _, s := range saved {
		d.speakers[speakerKey(s.Speaker)] = s
	}
}

// saveLocked writes the directory to the cache file. Callers hold d.mu.
func (d *SpeakerDirectory) saveLocked() {
	if d.cacheFile == "" {
		return
	}
	list := make([]knownSpeaker, 0, len(d.speakers))
	for _, s := range d.speakers {
		list = append(list, s)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	raw, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return
	}
	if err := os.WriteFile(d.cacheFile, raw, 0o600); err != nil {
		log.Printf("speaker cache %s: %v", d.cacheFile, err)
	}
}

// Run refreshes the directory now and then on every interval until ctx ends.
func (d *SpeakerDirectory) Run(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if err := d.Refresh(ctx); err != nil {
			log.Printf("speaker directory refresh: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// EnsureLoaded runs a first refresh if the directory has never refreshed
// and has nothing saved, so the very first request still sees speakers.
func (d *SpeakerDirectory) EnsureLoaded(ctx context.Context) {
	d.mu.RLock()
	ready := d.refreshed || len(d.speakers) > 0
	d.mu.RUnlock()
	if ready {
		return
	}
	if err := d.Refresh(ctx); err != nil {
		log.Printf("speaker directory refresh: %v", err)
	}
}

// Refresh scans the LAN, re-checks known speakers the scan missed at their
// last address, merges in Spotify's device list, and drops speakers that
// haven't answered for speakerForgetAfter. A failed scan still re-checks
// known speakers, so one bad mDNS browse doesn't lose them.
func (d *SpeakerDirectory) Refresh(ctx context.Context) error {
	d.refreshMu.Lock()
	defer d.refreshMu.Unlock()

	locals, lanErr := d.discover(ctx)
	probes := d.probeTargets(locals)
	lanSpeakers := d.probeAll(ctx, probes)

	var cloud []spotifyLib.PlayerDevice
	if spotifyClient != nil {
		if devices, err := spotifyClient.PlayerDevices(ctx); err == nil {
			cloud = devices
		}
	}

	now := time.Now()
	d.mu.Lock()
	defer d.mu.Unlock()

	for _, s := range lanSpeakers {
		key := speakerKey(s)
		// Drop an older entry for the same speaker stored under another key,
		// e.g. by name before its getInfo answered, so it isn't listed twice.
		for other, existing := range d.speakers {
			if other != key && strings.EqualFold(existing.Name, s.Name) {
				delete(d.speakers, other)
			}
		}
		d.speakers[key] = knownSpeaker{Speaker: s, LastSeen: now}
	}

	for _, c := range cloud {
		id := string(c.ID)
		if id == "" {
			continue
		}
		// A LAN entry with this ID already has the better name and address.
		if existing, ok := d.speakers[id]; ok {
			existing.LastSeen = now
			d.speakers[id] = existing
			continue
		}
		if looksLikeDeviceID(c.Name) {
			continue
		}
		d.speakers[id] = knownSpeaker{Speaker: Speaker{Name: c.Name, SpotifyID: id}, LastSeen: now}
	}

	for key, s := range d.speakers {
		if now.Sub(s.LastSeen) > speakerForgetAfter {
			delete(d.speakers, key)
		}
	}
	d.refreshed = true
	d.saveLocked()
	return lanErr
}

// probeTarget is one LAN address to check, plus the speaker we expect to
// find there when it comes from the saved list.
type probeTarget struct {
	local    LocalDevice
	expected *Speaker
}

// probeTargets returns the devices found by the scan plus every known LAN
// speaker the scan missed, at its last address.
func (d *SpeakerDirectory) probeTargets(locals []LocalDevice) []probeTarget {
	targets := make([]probeTarget, 0, len(locals))
	scanned := map[string]bool{}
	for _, l := range locals {
		if l.IP == "" {
			continue
		}
		scanned[l.IP] = true
		targets = append(targets, probeTarget{local: l})
	}

	d.mu.RLock()
	defer d.mu.RUnlock()
	for _, s := range d.speakers {
		if s.IP == "" || scanned[s.IP] {
			continue
		}
		known := s.Speaker
		targets = append(targets, probeTarget{
			local:    LocalDevice{FriendlyName: s.Name, IP: s.IP, Port: s.ZeroconfPort},
			expected: &known,
		})
	}
	return targets
}

// probeAll probes every target at the same time and returns the speakers
// that answered.
func (d *SpeakerDirectory) probeAll(ctx context.Context, targets []probeTarget) []Speaker {
	var (
		mu  sync.Mutex
		wg  sync.WaitGroup
		out []Speaker
	)
	for _, t := range targets {
		wg.Add(1)
		go func(t probeTarget) {
			defer wg.Done()
			s, ok := d.probe(ctx, t.local)
			if !ok || !sameSpeaker(t.expected, s) {
				return
			}
			mu.Lock()
			out = append(out, s)
			mu.Unlock()
		}(t)
	}
	wg.Wait()
	return out
}

// sameSpeaker reports whether a re-checked address still holds the speaker
// we saved for it. The router may have given the address to another device.
func sameSpeaker(expected *Speaker, found Speaker) bool {
	if expected == nil {
		return true
	}
	if expected.SpotifyID != "" {
		return strings.EqualFold(expected.SpotifyID, found.SpotifyID)
	}
	return strings.EqualFold(expected.Name, found.Name)
}

// probe asks one LAN device for its Spotify device ID and checks whether
// it is a WiiM speaker. Devices that answer neither are skipped: we could
// not play to them or control them anyway.
func (d *SpeakerDirectory) probe(ctx context.Context, local LocalDevice) (Speaker, bool) {
	pctx, cancel := context.WithTimeout(ctx, speakerProbeTimeout)
	defer cancel()

	var (
		info    *GetInfoResponse
		infoErr = errNoZeroconfPort
		wiim    string
		wiimErr error
		wg      sync.WaitGroup
	)
	if local.Port > 0 {
		wg.Add(1)
		go func() { defer wg.Done(); info, infoErr = d.getInfo(pctx, local) }()
	}
	wg.Add(1)
	go func() { defer wg.Done(); wiim, wiimErr = wiimClient.DeviceName(pctx, local.IP) }()
	wg.Wait()

	if infoErr != nil && wiimErr != nil {
		return Speaker{}, false
	}

	s := Speaker{IP: local.IP}
	remoteName := ""
	if infoErr == nil && info != nil {
		s.SpotifyID = info.DeviceID
		s.ZeroconfPort = local.Port
		remoteName = info.RemoteName
	}
	s.WiiM = wiimErr == nil
	s.Name = firstUsableName(local.FriendlyName, wiim, remoteName)
	if s.Name == "" {
		return Speaker{}, false
	}
	return s, true
}

// errNoZeroconfPort stands in for a getInfo call we skip because the
// device's zeroconf port is unknown.
var errNoZeroconfPort = errors.New("no zeroconf port")

// List returns every known speaker, sorted by name.
func (d *SpeakerDirectory) List() []Speaker {
	d.mu.RLock()
	defer d.mu.RUnlock()
	out := make([]Speaker, 0, len(d.speakers))
	for _, s := range d.speakers {
		out = append(out, s.Speaker)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Find looks a speaker up by name or Spotify device ID, ignoring case.
func (d *SpeakerDirectory) Find(nameOrID string) (Speaker, bool) {
	target := strings.TrimSpace(nameOrID)
	if target == "" {
		return Speaker{}, false
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	for _, s := range d.speakers {
		if strings.EqualFold(s.Name, target) || strings.EqualFold(s.SpotifyID, target) {
			return s.Speaker, true
		}
	}
	return Speaker{}, false
}

// NameForID returns the friendly name for a Spotify device ID, or "".
func (d *SpeakerDirectory) NameForID(id string) string {
	if id == "" {
		return ""
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	for _, s := range d.speakers {
		if strings.EqualFold(s.SpotifyID, id) {
			return s.Name
		}
	}
	return ""
}

// speakerKey keys the directory by Spotify device ID so the LAN and cloud
// entries for one speaker merge. Speakers without an ID key by name.
func speakerKey(s Speaker) string {
	if s.SpotifyID != "" {
		return s.SpotifyID
	}
	return "name:" + strings.ToLower(s.Name)
}

// firstUsableName returns the first candidate that is a real name and not a
// hex device ID or serial number.
func firstUsableName(candidates ...string) string {
	for _, c := range candidates {
		c = strings.TrimSpace(c)
		if c != "" && !looksLikeDeviceID(c) {
			return c
		}
	}
	return ""
}

// looksLikeDeviceID reports whether a name is really an identifier, like
// Spotify's 40-character hex IDs or "Sonos-F0F6C161C30E".
func looksLikeDeviceID(name string) bool {
	return hasHexFragment(strings.ReplaceAll(strings.TrimSpace(name), " ", "-"))
}

// deviceMatches reports whether a Spotify device is the one the caller
// named, by its Spotify name, its ID, or the friendly name the directory
// knows for its ID.
func deviceMatches(d spotifyLib.PlayerDevice, target string) bool {
	target = strings.TrimSpace(target)
	if target == "" {
		return false
	}
	if strings.EqualFold(d.Name, target) || strings.EqualFold(string(d.ID), target) {
		return true
	}
	name := speakerDirectory.NameForID(string(d.ID))
	return name != "" && strings.EqualFold(name, target)
}

// deviceDisplayName returns the friendly name for a Spotify device, using
// the directory when Spotify only gives us the hex ID.
func deviceDisplayName(d spotifyLib.PlayerDevice) string {
	if looksLikeDeviceID(d.Name) {
		if name := speakerDirectory.NameForID(string(d.ID)); name != "" {
			return name
		}
	}
	return d.Name
}
