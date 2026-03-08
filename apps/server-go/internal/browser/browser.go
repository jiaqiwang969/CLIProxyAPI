package browser

import (
	"fmt"
	"os/exec"
	"runtime"

	log "github.com/sirupsen/logrus"
	"github.com/skratchdot/open-golang/open"
)

// OpenURL opens a URL in the default system browser, with a platform fallback
// when the helper library cannot launch it directly.
func OpenURL(url string) error {
	fmt.Printf("Attempting to open URL in browser: %s\n", url)

	if err := open.Run(url); err == nil {
		log.Debug("Successfully opened URL using open-golang library")
		return nil
	}

	return openURLPlatformSpecific(url)
}

func openURLPlatformSpecific(url string) error {
	var cmd *exec.Cmd

	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "linux":
		browsers := []string{"xdg-open", "x-www-browser", "www-browser", "firefox", "chromium", "google-chrome"}
		for _, browser := range browsers {
			if _, err := exec.LookPath(browser); err == nil {
				cmd = exec.Command(browser, url)
				break
			}
		}
		if cmd == nil {
			return fmt.Errorf("no suitable browser found on Linux system")
		}
	default:
		return fmt.Errorf("unsupported operating system: %s", runtime.GOOS)
	}

	log.Debugf("Running command: %s %v", cmd.Path, cmd.Args[1:])
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start browser command: %w", err)
	}

	return nil
}

// IsAvailable reports whether the current host can open a browser.
func IsAvailable() bool {
	if err := open.Run("about:blank"); err == nil {
		return true
	}

	switch runtime.GOOS {
	case "darwin":
		_, err := exec.LookPath("open")
		return err == nil
	case "windows":
		_, err := exec.LookPath("rundll32")
		return err == nil
	case "linux":
		browsers := []string{"xdg-open", "x-www-browser", "www-browser", "firefox", "chromium", "google-chrome"}
		for _, browser := range browsers {
			if _, err := exec.LookPath(browser); err == nil {
				return true
			}
		}
		return false
	default:
		return false
	}
}

// GetPlatformInfo reports browser-launch support details for diagnostics.
func GetPlatformInfo() map[string]interface{} {
	info := map[string]interface{}{
		"os":        runtime.GOOS,
		"arch":      runtime.GOARCH,
		"available": IsAvailable(),
	}

	switch runtime.GOOS {
	case "darwin":
		info["default_command"] = "open"
	case "windows":
		info["default_command"] = "rundll32"
	case "linux":
		browsers := []string{"xdg-open", "x-www-browser", "www-browser", "firefox", "chromium", "google-chrome"}
		var availableBrowsers []string
		for _, browser := range browsers {
			if _, err := exec.LookPath(browser); err == nil {
				availableBrowsers = append(availableBrowsers, browser)
			}
		}
		info["available_browsers"] = availableBrowsers
		if len(availableBrowsers) > 0 {
			info["default_command"] = availableBrowsers[0]
		}
	}

	return info
}
