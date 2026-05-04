//
// Date: 2026-05-04
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Copyright (c) 2026 Cloudmanic Labs, LLC. All rights reserved.
//
// Description: High-level "claim a Spotify Connect device for the current
// user" routine. Combines mDNS discovery, the zeroconf addUser handshake,
// and a short post-claim wait for the device to register with Spotify
// cloud. Exposed to the API server via HandleWakeRequest.
//

package spotify

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// ClaimResult summarizes the outcome of a ClaimDevice call. We return the
// resolved LocalDevice so callers can log details (IP, hostname) and the
// claimed Spotify device ID for use in subsequent /me/player/play calls.
type ClaimResult struct {
	Local         LocalDevice
	DeviceID      string
	AlreadyActive bool
}

// ClaimDevice resolves a device by friendly name on the LAN, performs the
// zeroconf addUser handshake against it using the current OAuth access
// token, and waits briefly for Spotify cloud to acknowledge the new device.
//
// It is idempotent: claiming a device that already belongs to the user
// just refreshes the registration. It is also safe to call when the device
// has never been seen before, as long as it is currently powered on and
// reachable on the LAN.
//
// Returns an error if the device can't be found on the LAN, the zeroconf
// handshake fails, or the device never appears in the Spotify cloud
// devices list within the wait window.
func ClaimDevice(ctx context.Context, deviceName string) (*ClaimResult, error) {
	if spotifyClient == nil {
		return nil, fmt.Errorf("Spotify not authenticated. Visit /auth to authenticate")
	}

	// Skip the full handshake if Spotify cloud already lists the device —
	// no need to disturb a working session. This is the common path for
	// devices that are still actively logged in to our account.
	if existing, ok := findCloudDevice(ctx, deviceName); ok {
		return &ClaimResult{DeviceID: string(existing.ID), AlreadyActive: true}, nil
	}

	// Resolve the local IP/port via mDNS.
	local, found, err := defaultDiscoveryCache.FindByName(ctx, deviceName)
	if err != nil {
		return nil, fmt.Errorf("mDNS discovery failed: %w", err)
	}
	if !found {
		return nil, fmt.Errorf("device %q not found on LAN — make sure it is powered on and on the same network", deviceName)
	}
	if local.IP == "" {
		return nil, fmt.Errorf("device %q resolved with no IP address", deviceName)
	}

	user, err := spotifyClient.CurrentUser(ctx)
	if err != nil {
		return nil, fmt.Errorf("get current user: %w", err)
	}

	tok, err := spotifyClient.Token()
	if err != nil {
		return nil, fmt.Errorf("get access token: %w", err)
	}

	// Build the zeroconf client targeting the device's HTTP API.
	zc := NewZeroconfClient(local.IP, local.Port, "/zc")

	info, err := zc.GetInfo(ctx)
	if err != nil {
		return nil, fmt.Errorf("zeroconf getInfo: %w", err)
	}

	if _, err := zc.AddUser(ctx, info, user.ID, "spotify-shortcut", []byte(tok.AccessToken)); err != nil {
		return nil, fmt.Errorf("zeroconf addUser: %w", err)
	}

	// Poll Spotify cloud for the new device to appear. We poll instead of
	// sleeping because some devices register in <1s while others take 5-10s.
	deviceID, err := waitForCloudRegistration(ctx, info.DeviceID, deviceName, 12*time.Second)
	if err != nil {
		return nil, err
	}

	return &ClaimResult{Local: local, DeviceID: deviceID}, nil
}

// findCloudDevice looks up a device in the Spotify cloud devices list by
// either its friendly name or its hex device ID. Returns a copy of the
// matched device and true on hit.
func findCloudDevice(ctx context.Context, target string) (PlayerDeviceLite, bool) {
	devices, err := spotifyClient.PlayerDevices(ctx)
	if err != nil {
		return PlayerDeviceLite{}, false
	}
	for _, d := range devices {
		if strings.EqualFold(d.Name, target) || strings.EqualFold(string(d.ID), target) {
			return PlayerDeviceLite{ID: string(d.ID), Name: d.Name}, true
		}
	}
	return PlayerDeviceLite{}, false
}

// PlayerDeviceLite is a tiny copy of the bits of spotifyLib.PlayerDevice we
// need internally — keeping our claim/play helpers from leaking the
// upstream struct around.
type PlayerDeviceLite struct {
	ID   string
	Name string
}

// waitForCloudRegistration polls /me/player/devices until the newly-claimed
// device appears, or the timeout elapses. Matches by either the deviceID
// (preferred — guaranteed unique) or the friendly name (fallback for cases
// where the deviceID Spotify reports differs from the local zeroconf ID).
func waitForCloudRegistration(ctx context.Context, expectedID, friendlyName string, timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	for {
		devices, err := spotifyClient.PlayerDevices(ctx)
		if err == nil {
			for _, d := range devices {
				if strings.EqualFold(string(d.ID), expectedID) ||
					strings.EqualFold(d.Name, friendlyName) ||
					strings.EqualFold(d.Name, expectedID) {
					return string(d.ID), nil
				}
			}
		}

		if time.Now().After(deadline) {
			return "", fmt.Errorf("device claimed but did not appear in Spotify cloud within %s", timeout)
		}

		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(1 * time.Second):
		}
	}
}
