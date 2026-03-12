// events.go implements fsnotify event handling for config and auth file changes.
// It normalizes paths, debounces noisy events, and triggers reload/update logic.
package watcher

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
	log "github.com/sirupsen/logrus"
)

func matchProvider(provider string, targets []string) (string, bool) {
	p := strings.ToLower(strings.TrimSpace(provider))
	for _, t := range targets {
		if strings.EqualFold(p, strings.TrimSpace(t)) {
			return p, true
		}
	}
	return p, false
}

func (w *Watcher) start(ctx context.Context) error {
	if errAddConfig := w.watcher.Add(w.configPath); errAddConfig != nil {
		log.Errorf("failed to watch config file %s: %v", w.configPath, errAddConfig)
		return errAddConfig
	}
	log.Debugf("watching config file: %s", w.configPath)

	if errAddAuthDir := w.watcher.Add(w.authDir); errAddAuthDir != nil {
		log.Errorf("failed to watch auth directory %s: %v", w.authDir, errAddAuthDir)
		return errAddAuthDir
	}
	log.Debugf("watching auth directory: %s", w.authDir)

	w.ensureAuggieSessionSourceWatches()

	go w.processEvents(ctx)

	w.reloadClients(true, nil, false)
	return nil
}

func (w *Watcher) processEvents(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-w.watcher.Events:
			if !ok {
				return
			}
			w.handleEvent(event)
		case errWatch, ok := <-w.watcher.Errors:
			if !ok {
				return
			}
			log.Errorf("file watcher error: %v", errWatch)
		}
	}
}

func (w *Watcher) handleEvent(event fsnotify.Event) {
	// Filter only relevant events: config file, auth-dir JSON files, or the Auggie session file.
	configOps := fsnotify.Write | fsnotify.Create | fsnotify.Rename
	normalizedName := w.normalizeAuthPath(event.Name)
	normalizedConfigPath := w.normalizeAuthPath(w.configPath)
	normalizedAuthDir := w.normalizeAuthPath(w.authDir)
	isConfigEvent := normalizedName == normalizedConfigPath && event.Op&configOps != 0
	authOps := fsnotify.Create | fsnotify.Write | fsnotify.Remove | fsnotify.Rename
	isAuthJSON := strings.HasPrefix(normalizedName, normalizedAuthDir) && strings.HasSuffix(normalizedName, ".json") && event.Op&authOps != 0
	normalizedAuggieSessionPath := ""
	if auggieSessionPath, errResolve := resolveAuggieSessionPath(); errResolve == nil {
		normalizedAuggieSessionPath = w.normalizeAuthPath(auggieSessionPath)
	}
	normalizedAuggieSessionDir := ""
	if auggieSessionDir, errResolve := resolveAuggieSessionWatchDir(); errResolve == nil {
		normalizedAuggieSessionDir = w.normalizeAuthPath(auggieSessionDir)
	}
	isAuggieSessionEvent := normalizedAuggieSessionPath != "" && normalizedName == normalizedAuggieSessionPath && event.Op&authOps != 0
	isAuggieSessionDirEvent := normalizedAuggieSessionDir != "" && normalizedName == normalizedAuggieSessionDir && event.Op&(fsnotify.Create|fsnotify.Rename|fsnotify.Remove) != 0
	if !isConfigEvent && !isAuthJSON && !isAuggieSessionEvent && !isAuggieSessionDirEvent {
		// Ignore unrelated files (e.g., cookie snapshots *.cookie) and other noise.
		return
	}

	now := time.Now()
	log.Debugf("file system event detected: %s %s", event.Op.String(), event.Name)

	// Handle config file changes
	if isConfigEvent {
		log.Debugf("config file change details - operation: %s, timestamp: %s", event.Op.String(), now.Format("2006-01-02 15:04:05.000"))
		w.scheduleConfigReload()
		return
	}

	if isAuggieSessionEvent {
		log.Infof("Auggie session changed (%s): %s, refreshing auth state", event.Op.String(), filepath.Base(event.Name))
		w.refreshAuthState(false)
		return
	}

	if isAuggieSessionDirEvent {
		if event.Op&(fsnotify.Create|fsnotify.Rename) != 0 {
			w.ensureAuggieSessionDirWatch()
		}
		if event.Op&(fsnotify.Remove|fsnotify.Rename) != 0 {
			w.ensureAuggieHomeWatch()
		}
		log.Infof("Auggie session directory changed (%s): %s, refreshing auth state", event.Op.String(), filepath.Base(event.Name))
		w.refreshAuthState(false)
		return
	}

	// Handle auth directory changes incrementally (.json only)
	if event.Op&(fsnotify.Remove|fsnotify.Rename) != 0 {
		if w.shouldDebounceRemove(normalizedName, now) {
			log.Debugf("debouncing remove event for %s", filepath.Base(event.Name))
			return
		}
		// Atomic replace on some platforms may surface as Rename (or Remove) before the new file is ready.
		// Wait briefly; if the path exists again, treat as an update instead of removal.
		time.Sleep(replaceCheckDelay)
		if _, statErr := os.Stat(event.Name); statErr == nil {
			if unchanged, errSame := w.authFileUnchanged(event.Name); errSame == nil && unchanged {
				log.Debugf("auth file unchanged (hash match), skipping reload: %s", filepath.Base(event.Name))
				return
			}
			log.Infof("auth file changed (%s): %s, processing incrementally", event.Op.String(), filepath.Base(event.Name))
			w.addOrUpdateClient(event.Name)
			return
		}
		if !w.isKnownAuthFile(event.Name) {
			log.Debugf("ignoring remove for unknown auth file: %s", filepath.Base(event.Name))
			return
		}
		log.Infof("auth file changed (%s): %s, processing incrementally", event.Op.String(), filepath.Base(event.Name))
		w.removeClient(event.Name)
		return
	}
	if event.Op&(fsnotify.Create|fsnotify.Write) != 0 {
		if unchanged, errSame := w.authFileUnchanged(event.Name); errSame == nil && unchanged {
			log.Debugf("auth file unchanged (hash match), skipping reload: %s", filepath.Base(event.Name))
			return
		}
		log.Infof("auth file changed (%s): %s, processing incrementally", event.Op.String(), filepath.Base(event.Name))
		w.addOrUpdateClient(event.Name)
	}
}

func (w *Watcher) authFileUnchanged(path string) (bool, error) {
	data, errRead := os.ReadFile(path)
	if errRead != nil {
		return false, errRead
	}
	if len(data) == 0 {
		return false, nil
	}
	sum := sha256.Sum256(data)
	curHash := hex.EncodeToString(sum[:])

	normalized := w.normalizeAuthPath(path)
	w.clientsMutex.RLock()
	prevHash, ok := w.lastAuthHashes[normalized]
	w.clientsMutex.RUnlock()
	if ok && prevHash == curHash {
		return true, nil
	}
	return false, nil
}

func (w *Watcher) isKnownAuthFile(path string) bool {
	normalized := w.normalizeAuthPath(path)
	w.clientsMutex.RLock()
	defer w.clientsMutex.RUnlock()
	_, ok := w.lastAuthHashes[normalized]
	return ok
}

func (w *Watcher) normalizeAuthPath(path string) string {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return ""
	}
	cleaned := filepath.Clean(trimmed)
	if runtime.GOOS == "windows" {
		cleaned = strings.TrimPrefix(cleaned, `\\?\`)
		cleaned = strings.ToLower(cleaned)
	}
	return cleaned
}

func resolveAuggieSessionPath() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(homeDir, ".augment", "session.json"), nil
}

func resolveAuggieSessionWatchDir() (string, error) {
	sessionPath, err := resolveAuggieSessionPath()
	if err != nil {
		return "", err
	}
	return filepath.Dir(sessionPath), nil
}

func resolveAuggieHomeDir() (string, error) {
	return os.UserHomeDir()
}

func (w *Watcher) ensureAuggieHomeWatch() {
	homeDir, err := resolveAuggieHomeDir()
	if err != nil {
		log.WithError(err).Debug("failed to resolve Auggie home directory")
		return
	}
	w.ensureDirectoryWatch(homeDir, "Auggie home directory")
}

func (w *Watcher) ensureAuggieSessionSourceWatches() {
	sessionDir, err := resolveAuggieSessionWatchDir()
	if err != nil {
		log.WithError(err).Debug("failed to resolve Auggie session watch directory")
		return
	}
	if info, errStat := os.Stat(sessionDir); errStat == nil && info.IsDir() {
		w.ensureDirectoryWatch(sessionDir, "Auggie session directory")
		return
	} else if errStat != nil && !os.IsNotExist(errStat) {
		log.WithError(errStat).Debugf("failed to stat Auggie session directory %s", sessionDir)
	}
	w.ensureAuggieHomeWatch()
}

func (w *Watcher) ensureAuggieSessionDirWatch() {
	sessionDir, err := resolveAuggieSessionWatchDir()
	if err != nil {
		log.WithError(err).Debug("failed to resolve Auggie session watch directory")
		return
	}
	w.ensureDirectoryWatch(sessionDir, "Auggie session directory")
}

func (w *Watcher) ensureDirectoryWatch(path, label string) {
	if w == nil || w.watcher == nil {
		return
	}
	normalizedTarget := w.normalizeAuthPath(path)
	if normalizedTarget == "" {
		return
	}
	for _, watchedPath := range w.watcher.WatchList() {
		if w.normalizeAuthPath(watchedPath) == normalizedTarget {
			return
		}
	}
	info, err := os.Stat(path)
	if err != nil {
		if !os.IsNotExist(err) {
			log.WithError(err).Debugf("failed to stat %s %s", label, path)
		}
		return
	}
	if !info.IsDir() {
		return
	}
	if err = w.watcher.Add(path); err != nil {
		log.WithError(err).Warnf("failed to watch %s %s", label, path)
		return
	}
	log.Debugf("watching %s: %s", label, path)
}

func (w *Watcher) shouldDebounceRemove(normalizedPath string, now time.Time) bool {
	if normalizedPath == "" {
		return false
	}
	w.clientsMutex.Lock()
	if w.lastRemoveTimes == nil {
		w.lastRemoveTimes = make(map[string]time.Time)
	}
	if last, ok := w.lastRemoveTimes[normalizedPath]; ok {
		if now.Sub(last) < authRemoveDebounceWindow {
			w.clientsMutex.Unlock()
			return true
		}
	}
	w.lastRemoveTimes[normalizedPath] = now
	if len(w.lastRemoveTimes) > 128 {
		cutoff := now.Add(-2 * authRemoveDebounceWindow)
		for p, t := range w.lastRemoveTimes {
			if t.Before(cutoff) {
				delete(w.lastRemoveTimes, p)
			}
		}
	}
	w.clientsMutex.Unlock()
	return false
}
