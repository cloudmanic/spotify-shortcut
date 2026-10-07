//
// Date: 2026-10-07
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Copyright (c) 2026 Cloudmanic Labs, LLC. All rights reserved.
//
// Description: Client for the local HTTP API on WiiM (LinkPlay) speakers.
// Spotify's Web API only sees playback on our own account, but every WiiM
// speaker reports and controls whatever it is playing, whichever account or
// app started it. /api/v1/ask uses this to stop music everywhere, report
// what is playing on each speaker, and pause, skip or change volume.
//

package spotify

import (
	"context"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// wiimClient is the package-level WiiM client. Tests swap it for a fake.
var wiimClient WiiMClient = newLinkPlayClient()

// WiiMStatus is what a WiiM speaker reports about its player.
type WiiMStatus struct {
	// State is the raw player state: "play", "pause", "stop", "load" or "none".
	State  string
	Title  string
	Artist string
	Volume int
}

// Playing reports whether the speaker is playing (or loading) audio.
func (s WiiMStatus) Playing() bool {
	return s.State == "play" || s.State == "load"
}

// WiiMClient talks to WiiM speakers on the LAN. host is an IP, or
// "IP:port" in tests.
type WiiMClient interface {
	// DeviceName returns the speaker's name, which also confirms the host
	// is a WiiM speaker.
	DeviceName(ctx context.Context, host string) (string, error)
	// Status returns what the speaker is playing and its volume.
	Status(ctx context.Context, host string) (WiiMStatus, error)
	// Command sends a player command such as "pause", "resume", "next",
	// "stop" or "vol:40".
	Command(ctx context.Context, host, cmd string) error
}

// linkPlayClient is the HTTP implementation of WiiMClient.
type linkPlayClient struct {
	http *http.Client
}

// newLinkPlayClient builds a client that accepts the speakers' self-signed
// certificates. WiiM firmware only serves this API over HTTPS with a
// certificate no CA signed, and the traffic never leaves the LAN.
func newLinkPlayClient() *linkPlayClient {
	return &linkPlayClient{
		http: &http.Client{
			Timeout: 3 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			},
		},
	}
}

// get runs one httpapi.asp command and returns the raw response body. The
// command goes in unescaped because the firmware expects literal colons.
func (c *linkPlayClient) get(ctx context.Context, host, command string) (string, error) {
	u := fmt.Sprintf("https://%s/httpapi.asp?command=%s", host, command)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("wiim %s: %w", host, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("wiim %s: read: %w", host, err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("wiim %s: HTTP %d", host, resp.StatusCode)
	}
	return string(body), nil
}

// DeviceName reads the speaker's configured name from getStatusEx.
func (c *linkPlayClient) DeviceName(ctx context.Context, host string) (string, error) {
	body, err := c.get(ctx, host, "getStatusEx")
	if err != nil {
		return "", err
	}
	var parsed struct {
		DeviceName string `json:"DeviceName"`
	}
	if err := json.Unmarshal([]byte(body), &parsed); err != nil {
		return "", fmt.Errorf("wiim %s: not a WiiM status response", host)
	}
	return parsed.DeviceName, nil
}

// Status reads getPlayerStatus. The speaker returns every value as a
// string, and title/artist as hex-encoded UTF-8.
func (c *linkPlayClient) Status(ctx context.Context, host string) (WiiMStatus, error) {
	body, err := c.get(ctx, host, "getPlayerStatus")
	if err != nil {
		return WiiMStatus{}, err
	}
	var parsed struct {
		Status string `json:"status"`
		Title  string `json:"Title"`
		Artist string `json:"Artist"`
		Vol    string `json:"vol"`
	}
	if err := json.Unmarshal([]byte(body), &parsed); err != nil {
		return WiiMStatus{}, fmt.Errorf("wiim %s: bad player status: %w", host, err)
	}
	vol, _ := strconv.Atoi(parsed.Vol)
	return WiiMStatus{
		State:  parsed.Status,
		Title:  decodeLinkPlayText(parsed.Title),
		Artist: decodeLinkPlayText(parsed.Artist),
		Volume: vol,
	}, nil
}

// Command sends setPlayerCmd:<cmd>. The speaker answers "OK" on success.
func (c *linkPlayClient) Command(ctx context.Context, host, cmd string) error {
	body, err := c.get(ctx, host, "setPlayerCmd:"+cmd)
	if err != nil {
		return err
	}
	if strings.TrimSpace(body) != "OK" {
		return fmt.Errorf("wiim %s: %s returned %q", host, cmd, truncate(body, 100))
	}
	return nil
}

// decodeLinkPlayText turns the speaker's hex-encoded text into a string.
// Some firmware sends plain text, so anything that isn't valid hex UTF-8 is
// returned as-is. "Unknown" is the speaker's placeholder for no value.
func decodeLinkPlayText(s string) string {
	out := s
	if decoded, err := hex.DecodeString(s); err == nil && utf8.Valid(decoded) {
		out = string(decoded)
	}
	if strings.EqualFold(out, "unknown") {
		return ""
	}
	return out
}
