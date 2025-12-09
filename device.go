//
// Date: 2025-12-09
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Copyright (c) 2025 Cloudmanic Labs, LLC. All rights reserved.
//
// Description: Device listing and display functions.
//

package main

import (
	"fmt"
	"os"

	"github.com/fatih/color"
	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/zmb3/spotify/v2"
)

// printDevicesTable displays available Spotify devices in a formatted table
// with colors to indicate active status.
func printDevicesTable(devices []spotify.PlayerDevice) {
	green := color.New(color.FgGreen, color.Bold)
	cyan := color.New(color.FgCyan)

	fmt.Println()
	cyan.Println("🎵 Available Spotify Connect Devices")
	fmt.Println()

	t := table.NewWriter()
	t.SetOutputMirror(os.Stdout)
	t.AppendHeader(table.Row{"#", "Name", "Type", "Status", "Device ID"})

	for i, device := range devices {
		status := "Inactive"
		if device.Active {
			status = color.GreenString("● Active")
		}

		t.AppendRow(table.Row{
			i + 1,
			color.New(color.Bold).Sprint(device.Name),
			device.Type,
			status,
			color.HiBlackString(string(device.ID)),
		})
	}

	t.SetStyle(table.StyleRounded)
	t.Render()

	fmt.Println()
	green.Printf("Total devices: %d\n", len(devices))
}
