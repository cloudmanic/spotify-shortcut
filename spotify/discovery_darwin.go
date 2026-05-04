//
// Date: 2026-05-04
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Copyright (c) 2026 Cloudmanic Labs, LLC. All rights reserved.
//
// Description: macOS-specific Spotify Connect discovery that shells out to
// the system `dns-sd` tool and resolves hostnames via the system resolver
// instead of binding raw multicast sockets directly. This works around the
// macOS Sequoia (15+) Local Network privacy gate that blocks launchd-
// spawned processes from doing direct multicast — pure-Go zeroconf libs
// silently return zero results in that environment, while dns-sd talks to
// mDNSResponder over a Unix socket and is unaffected.
//

//go:build darwin

package spotify

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// dnsSDDiscoverer implements Discoverer by shelling out to the system
// `dns-sd` tool. It is registered as the default discoverer at package init
// time on darwin so the rest of the codebase doesn't have to know about
// the platform split.
type dnsSDDiscoverer struct{}

// init swaps the default zeroconf-backed discoverer for the dns-sd one on
// darwin. Tests that supply their own Discoverer continue to work because
// they construct a NewDiscoveryCache with an explicit discoverer.
func init() {
	defaultDiscoveryCache = NewDiscoveryCache(&dnsSDDiscoverer{}, 60*time.Second)
}

// dnsSDInstanceLine matches the per-instance lines in `dns-sd -B` output.
// Example line:
//
//	15:47:49.201  Add        3   6 local.               _spotify-connect._tcp. SpotifyConnect (2)
var dnsSDInstanceLine = regexp.MustCompile(`^\S+\s+(Add|Rmv)\s+\S+\s+\S+\s+\S+\s+(_\S+\._tcp\.?)\s+(.+?)\s*$`)

// dnsSDLookupLine matches the SRV-target line from `dns-sd -L`. Example:
//
//	can be reached at Pool-Speakers.local.:5356 (interface 6)
var dnsSDLookupLine = regexp.MustCompile(`reached at\s+(\S+):(\d+)`)

// Discover runs `dns-sd -B` for ~timeout seconds to collect Spotify Connect
// instance names, then resolves each via `dns-sd -L` (host+port) and
// net.LookupHost (host->IP). All host lookups go through mDNSResponder
// over a Unix socket — no multicast bind in our process.
func (d *dnsSDDiscoverer) Discover(ctx context.Context, timeout time.Duration) ([]LocalDevice, error) {
	instances, err := browseInstances(ctx, timeout)
	if err != nil {
		return nil, err
	}

	var devices []LocalDevice
	seen := make(map[string]bool)

	// Resolution per-instance is independent — could parallelize, but our
	// input set is small (<20 typical) and serial keeps the code simple.
	// Total wallclock stays under ~timeout + N*lookup_budget.
	resolveCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	for _, instance := range instances {
		if seen[instance] {
			continue
		}
		seen[instance] = true

		host, port, err := lookupServiceEndpoint(resolveCtx, instance)
		if err != nil || host == "" {
			// Skip instances that don't resolve — common for transient
			// devices that disappear between -B and -L calls.
			continue
		}

		ip, _ := net.LookupHost(host)
		var ipv4 string
		for _, a := range ip {
			parsed := net.ParseIP(a)
			if parsed != nil && parsed.To4() != nil {
				ipv4 = a
				break
			}
		}

		devices = append(devices, LocalDevice{
			InstanceName: instance,
			Hostname:     host,
			FriendlyName: friendlyNameFromHostname(host),
			IP:           ipv4,
			Port:         port,
		})
	}

	return devices, nil
}

// browseInstances runs `dns-sd -B _spotify-connect._tcp local.` for the
// supplied duration and returns the unique instance names that responded.
func browseInstances(ctx context.Context, timeout time.Duration) ([]string, error) {
	browseCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(browseCtx, "dns-sd", "-B", "_spotify-connect._tcp", "local.")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("dns-sd stdout: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("dns-sd start: %w", err)
	}

	var instances []string
	seen := make(map[string]bool)

	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		line := scanner.Text()
		match := dnsSDInstanceLine.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		// match[1] = Add or Rmv; we only care about Add events.
		if match[1] != "Add" {
			continue
		}
		// match[3] is the instance name, possibly containing escaped
		// spaces (e.g. "SpotifyConnect\032(2)" or "SpotifyConnect (2)").
		instance := unescapeDNSSDName(match[3])
		if !seen[instance] {
			seen[instance] = true
			instances = append(instances, instance)
		}
	}

	// dns-sd runs until killed by the context timeout — that's expected.
	_ = cmd.Wait()

	return instances, nil
}

// lookupServiceEndpoint resolves a service instance to its host and port
// via `dns-sd -L`. We give it a short per-instance budget so a flaky
// device can't stall the whole discovery.
func lookupServiceEndpoint(ctx context.Context, instance string) (string, int, error) {
	lookupCtx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
	defer cancel()

	cmd := exec.CommandContext(lookupCtx, "dns-sd", "-L", instance, "_spotify-connect._tcp", "local.")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", 0, err
	}
	if err := cmd.Start(); err != nil {
		return "", 0, err
	}

	var host string
	var port int

	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		line := scanner.Text()
		match := dnsSDLookupLine.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		host = strings.TrimSuffix(match[1], ".") + "."
		port, _ = strconv.Atoi(match[2])
		// Got what we need — kill dns-sd early.
		_ = cmd.Process.Kill()
		break
	}

	_ = cmd.Wait()

	if host == "" {
		return "", 0, fmt.Errorf("no SRV target for %q", instance)
	}
	return host, port, nil
}

// unescapeDNSSDName converts dns-sd's escaped output back to the literal
// instance name. The tool emits `\032` for spaces and similar octal
// escapes for other special characters.
func unescapeDNSSDName(s string) string {
	// Quick path — most names don't contain escapes.
	if !strings.Contains(s, `\`) {
		return s
	}

	var b strings.Builder
	i := 0
	for i < len(s) {
		if s[i] == '\\' && i+3 < len(s) {
			// Try to parse a 3-digit octal.
			n, err := strconv.ParseInt(s[i+1:i+4], 8, 32)
			if err == nil {
				b.WriteRune(rune(n))
				i += 4
				continue
			}
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}
