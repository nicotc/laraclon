package main

import "github.com/nicotc/laraclon/internal/logo"

// The tray icons: running, running with something failing in the health check, and stopped.
var (
	iconRunning = logo.ICO(logo.Amber, logo.AmberDark, false)
	iconWarning = logo.ICO(logo.Amber, logo.AmberDark, true)
	iconStopped = logo.ICO(logo.Gray, logo.GrayDark, false)
)
