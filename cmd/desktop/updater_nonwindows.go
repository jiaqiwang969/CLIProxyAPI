//go:build !windows
// +build !windows

package main

import log "github.com/sirupsen/logrus"

// checkForUpdates is only implemented on Windows for the desktop self-update flow.
func (a *App) checkForUpdates() {
	log.Debug("desktop updater is skipped on non-windows platforms")
}
