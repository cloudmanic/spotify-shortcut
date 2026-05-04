//
// Date: 2026-05-04
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Copyright (c) 2026 Cloudmanic Labs, LLC. All rights reserved.
//
// Description: Local network discovery of Spotify Connect devices via mDNS.
// Used to find a device's IP address so we can speak the zeroconf addUser
// protocol against it (port 5356, /zc) to claim it for the user's account.
//

package spotify

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/grandcat/zeroconf"
)

// LocalDevice is a Spotify Connect device discovered on the LAN via mDNS.
// We keep this distinct from the spotifyLib.PlayerDevice type because the
// Spotify cloud API does not give us the local IP — only mDNS does.
type LocalDevice struct {
	// InstanceName is the raw mDNS service instance name (e.g. "FF98F2F7..."
	// for WiiM amps, or "sonosRINCON_..." for Sonos).
	InstanceName string

	// Hostname is the .local hostname advertised in the SRV record
	// (e.g. "Living-Room-Speakers.local").
	Hostname string

	// FriendlyName is a human-readable name we derive from the hostname,
	// e.g. "Living Room Speakers". This is what users typically know the
	// device by and is what /api/v1/devices returns.
	FriendlyName string

	// IP is the IPv4 address resolved from the AAAA/A record.
	IP string

	// Port is the TCP port the zeroconf API listens on (5356 for WiiM,
	// 1400 for Sonos, 4070 for librespot/desktop).
	Port int
}

// Discoverer abstracts mDNS browsing so tests can supply a fake
// implementation. The real implementation uses grandcat/zeroconf.
type Discoverer interface {
	// Discover browses _spotify-connect._tcp on the LAN for up to `timeout`
	// and returns every device that responded.
	Discover(ctx context.Context, timeout time.Duration) ([]LocalDevice, error)
}

// zeroconfDiscoverer is the production implementation of Discoverer that
// drives github.com/grandcat/zeroconf to do real mDNS queries.
type zeroconfDiscoverer struct{}

// Discover performs an mDNS browse for _spotify-connect._tcp and collects
// every responding service entry, converting each into a LocalDevice.
//
// We explicitly select multicast-capable interfaces with non-link-local
// IPv4 addresses rather than relying on the library default, because under
// macOS launchd the default interface enumeration silently picks unusable
// interfaces (tunnels, awdl, etc.) and returns zero results — the same
// binary running under an SSH-foreground session works fine.
func (z *zeroconfDiscoverer) Discover(ctx context.Context, timeout time.Duration) ([]LocalDevice, error) {
	ifaces := lanMulticastInterfaces()

	var resolverOpts []zeroconf.ClientOption
	if len(ifaces) > 0 {
		resolverOpts = append(resolverOpts, zeroconf.SelectIfaces(ifaces))
	}

	resolver, err := zeroconf.NewResolver(resolverOpts...)
	if err != nil {
		return nil, fmt.Errorf("zeroconf resolver init failed: %w", err)
	}

	entries := make(chan *zeroconf.ServiceEntry, 16)
	browseCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	if err := resolver.Browse(browseCtx, "_spotify-connect._tcp", "local.", entries); err != nil {
		return nil, fmt.Errorf("zeroconf browse failed: %w", err)
	}

	// Drain the entries channel until the browse context times out.
	var devices []LocalDevice
	for entry := range entries {
		devices = append(devices, entryToDevice(entry))
	}

	return devices, nil
}

// lanMulticastInterfaces returns network interfaces suitable for mDNS:
// up, multicast-capable, not loopback, with at least one global IPv4
// address (i.e. not link-local 169.254.x.x). The grandcat/zeroconf default
// is unreliable under launchd on macOS where many synthetic interfaces
// (utun, awdl, gif/stf, anpi) get picked but can't actually carry the
// multicast traffic. Returning nil here means the library falls back to
// its default — we only do that if our filter found nothing.
func lanMulticastInterfaces() []net.Interface {
	all, err := net.Interfaces()
	if err != nil {
		return nil
	}

	var good []net.Interface
	for _, iface := range all {
		// Skip down or non-multicast interfaces.
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagMulticast == 0 {
			continue
		}
		// Skip loopback and point-to-point (tunnels, VPNs).
		if iface.Flags&net.FlagLoopback != 0 || iface.Flags&net.FlagPointToPoint != 0 {
			continue
		}

		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}

		// Require at least one non-link-local IPv4 address.
		hasGlobalV4 := false
		for _, addr := range addrs {
			ipNet, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}
			ip4 := ipNet.IP.To4()
			if ip4 == nil {
				continue
			}
			if ip4.IsLoopback() || ip4.IsLinkLocalUnicast() {
				continue
			}
			hasGlobalV4 = true
			break
		}
		if !hasGlobalV4 {
			continue
		}

		good = append(good, iface)
	}

	return good
}

// entryToDevice converts a zeroconf ServiceEntry into our LocalDevice type.
// Picks the first IPv4 address (we don't currently care about IPv6).
func entryToDevice(entry *zeroconf.ServiceEntry) LocalDevice {
	var ip string
	if len(entry.AddrIPv4) > 0 {
		ip = entry.AddrIPv4[0].String()
	}

	return LocalDevice{
		InstanceName: entry.Instance,
		Hostname:     entry.HostName,
		FriendlyName: friendlyNameFromHostname(entry.HostName),
		IP:           ip,
		Port:         entry.Port,
	}
}

// friendlyNameFromHostname turns a `.local` hostname into the human-readable
// device name. Examples:
//
//	"Living-Room-Speakers.local." -> "Living Room Speakers"
//	"Sonos-F0F6C161C30E.local."   -> "Sonos-F0F6C161C30E"
//	"Android-2.local."            -> "Android-2"
//
// We strip a trailing dot and the ".local" suffix, then replace dashes with
// spaces only when the hostname looks like a friendly room name (contains
// no hex-style identifiers). Keeping device-id style hostnames intact
// preserves uniqueness for matching.
func friendlyNameFromHostname(hostname string) string {
	name := strings.TrimSuffix(hostname, ".")
	name = strings.TrimSuffix(name, ".local")

	// Heuristic: if any segment looks like a hex MAC fragment (>=6 hex chars),
	// keep the hostname as-is so we don't mangle device identifiers.
	if hasHexFragment(name) {
		return name
	}

	return strings.ReplaceAll(name, "-", " ")
}

// hasHexFragment returns true if any dash-separated segment of `name`
// looks like a hex identifier (at least 6 chars, all hex).
func hasHexFragment(name string) bool {
	for _, part := range strings.Split(name, "-") {
		if len(part) >= 6 && isHex(part) {
			return true
		}
	}
	return false
}

// isHex reports whether every rune in s is a hex digit.
func isHex(s string) bool {
	for _, r := range s {
		isDigit := r >= '0' && r <= '9'
		isLowerHex := r >= 'a' && r <= 'f'
		isUpperHex := r >= 'A' && r <= 'F'
		if !isDigit && !isLowerHex && !isUpperHex {
			return false
		}
	}
	return true
}

// DiscoveryCache caches the result of mDNS discovery for a short TTL so we
// don't re-broadcast on every request. Discovery takes ~3-5 seconds so we
// definitely want a cache in front of the play path.
type DiscoveryCache struct {
	mu         sync.Mutex
	discoverer Discoverer
	ttl        time.Duration
	devices    []LocalDevice
	expiresAt  time.Time
}

// NewDiscoveryCache builds a cache backed by the supplied Discoverer.
// Pass `nil` to use the default zeroconf-backed discoverer.
func NewDiscoveryCache(d Discoverer, ttl time.Duration) *DiscoveryCache {
	if d == nil {
		d = &zeroconfDiscoverer{}
	}
	return &DiscoveryCache{discoverer: d, ttl: ttl}
}

// Devices returns the current set of discovered devices, refreshing the
// cache via mDNS if the TTL has expired. Returns whatever was last seen on
// error so callers can degrade gracefully.
func (c *DiscoveryCache) Devices(ctx context.Context) ([]LocalDevice, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if time.Now().Before(c.expiresAt) && c.devices != nil {
		return c.devices, nil
	}

	// 4s is enough for most household speakers to respond.
	devices, err := c.discoverer.Discover(ctx, 4*time.Second)
	if err != nil {
		return c.devices, err
	}

	c.devices = devices
	c.expiresAt = time.Now().Add(c.ttl)
	return c.devices, nil
}

// FindByName looks up a discovered device by its friendly name (case
// insensitive). Returns the matched device and true on hit. Falls back to
// matching the raw hostname or instance name if the friendly name doesn't
// match — useful for hex-id devices like Sonos.
func (c *DiscoveryCache) FindByName(ctx context.Context, name string) (LocalDevice, bool, error) {
	devices, err := c.Devices(ctx)
	if err != nil && len(devices) == 0 {
		return LocalDevice{}, false, err
	}

	target := strings.TrimSpace(name)
	hostnameNoDot := func(h string) string { return strings.TrimSuffix(h, ".") }

	for _, d := range devices {
		if strings.EqualFold(d.FriendlyName, target) ||
			strings.EqualFold(d.Hostname, target) ||
			strings.EqualFold(hostnameNoDot(d.Hostname), target) ||
			strings.EqualFold(strings.TrimSuffix(d.Hostname, ".local."), target) ||
			strings.EqualFold(d.InstanceName, target) {
			return d, true, nil
		}
	}
	return LocalDevice{}, false, nil
}

// defaultDiscoveryCache is the package-level cache used by the API server.
// 60s TTL: long enough that a play burst (multiple calls in quick succession)
// shares a single mDNS round-trip, short enough that newly-booted speakers
// appear within a minute.
var defaultDiscoveryCache = NewDiscoveryCache(nil, 60*time.Second)

// DefaultDiscoveryCache returns the package-level discovery cache. Exposed
// so tests can override its discoverer via swapping `defaultDiscoveryCache`.
func DefaultDiscoveryCache() *DiscoveryCache {
	return defaultDiscoveryCache
}
