package desktop

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"whatsapp-automation/pkg/version"
)

// FindBrowserAppExecutable locates Edge or Chrome for a chromeless app window.
func FindBrowserAppExecutable() string {
	var candidates []string
	switch runtime.GOOS {
	case "windows":
		candidates = []string{
			filepath.Join(os.Getenv("ProgramFiles(x86)"), `Microsoft\Edge\Application\msedge.exe`),
			filepath.Join(os.Getenv("ProgramFiles"), `Microsoft\Edge\Application\msedge.exe`),
			filepath.Join(os.Getenv("ProgramFiles"), `Google\Chrome\Application\chrome.exe`),
			filepath.Join(os.Getenv("ProgramFiles(x86)"), `Google\Chrome\Application\chrome.exe`),
			filepath.Join(os.Getenv("LOCALAPPDATA"), `Microsoft\Edge\Application\msedge.exe`),
			filepath.Join(os.Getenv("LOCALAPPDATA"), `Google\Chrome\Application\chrome.exe`),
		}
	case "darwin":
		candidates = []string{
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
		}
	default:
		candidates = []string{"/usr/bin/google-chrome", "/usr/bin/google-chrome-stable", "/usr/bin/chromium-browser", "/usr/bin/chromium", "/usr/bin/microsoft-edge"}
	}
	for _, p := range candidates {
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p
		}
	}
	return ""
}

// OpenDefaultBrowser opens url in the default browser.
func OpenDefaultBrowser(url string) error {
	switch runtime.GOOS {
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	case "darwin":
		return exec.Command("open", url).Start()
	default:
		return exec.Command("xdg-open", url).Start()
	}
}

// LaunchAppWindow opens url in a dedicated app window, or the default browser.
func LaunchAppWindow(url string) (*exec.Cmd, error) {
	exe := FindBrowserAppExecutable()
	if exe == "" {
		return nil, OpenDefaultBrowser(url)
	}
	cmd := exec.Command(exe, "--app="+url, "--window-size=1280,860")
	if err := cmd.Start(); err != nil {
		return nil, OpenDefaultBrowser(url)
	}
	return cmd, nil
}

func targetExe() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.Abs(exe)
}

// EnsureDesktopShortcut creates a desktop launcher for the app.
func EnsureDesktopShortcut(exe string) error {
	if exe == "" {
		var err error
		if exe, err = targetExe(); err != nil {
			return err
		}
	}
	switch runtime.GOOS {
	case "windows":
		return createShortcut("Desktop", version.Product+".lnk", exe, "app", 1)
	case "linux":
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		entry := fmt.Sprintf("[Desktop Entry]\nType=Application\nName=%s\nComment=%s\nExec=\"%s\" app\nPath=%s\nTerminal=false\nCategories=Network;\n", version.Product, version.Tagline, exe, filepath.Dir(exe))
		apps := filepath.Join(home, ".local", "share", "applications")
		_ = os.MkdirAll(apps, 0755)
		return os.WriteFile(filepath.Join(apps, "pulse.desktop"), []byte(entry), 0755)
	}
	return nil
}

const startupLink = version.Product + " (background).lnk"

// AutostartSupported reports whether "start with Windows" can be offered.
func AutostartSupported() bool { return runtime.GOOS == "windows" }

// AutostartEnabled reports whether the app starts with Windows.
func AutostartEnabled() bool {
	if runtime.GOOS != "windows" {
		return false
	}
	_, err := os.Stat(filepath.Join(os.Getenv("APPDATA"), `Microsoft\Windows\Start Menu\Programs\Startup`, startupLink))
	return err == nil
}

// SetAutostart adds or removes a Startup-folder shortcut that runs the server minimised.
func SetAutostart(enabled bool) error {
	if runtime.GOOS != "windows" {
		return errors.New("Starting automatically is only available on Windows.")
	}
	path := filepath.Join(os.Getenv("APPDATA"), `Microsoft\Windows\Start Menu\Programs\Startup`, startupLink)
	if !enabled {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	exe, err := targetExe()
	if err != nil {
		return err
	}
	return createShortcut("Startup", startupLink, exe, "serve", 7)
}

// createShortcut writes a .lnk in a special folder. Paths are passed through the
// environment so they are never parsed as PowerShell code.
func createShortcut(folder, name, exe, args string, windowStyle int) error {
	const script = `$dir = [Environment]::GetFolderPath($env:PULSE_FOLDER)
if (-not $dir -or -not (Test-Path $dir)) { exit 2 }
$ws = New-Object -ComObject WScript.Shell
$s = $ws.CreateShortcut((Join-Path $dir $env:PULSE_NAME))
$s.TargetPath = $env:PULSE_EXE
$s.Arguments = $env:PULSE_ARGS
$s.WorkingDirectory = $env:PULSE_DIR
$s.WindowStyle = [int]$env:PULSE_STYLE
$s.Description = $env:PULSE_DESC
$s.Save()`
	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", script)
	cmd.Env = append(os.Environ(),
		"PULSE_FOLDER="+folder, "PULSE_NAME="+name, "PULSE_EXE="+exe, "PULSE_ARGS="+args,
		"PULSE_DIR="+filepath.Dir(exe), fmt.Sprintf("PULSE_STYLE=%d", windowStyle), "PULSE_DESC="+version.Product+" "+version.Tagline)
	hideWindow(cmd)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("could not create shortcut: %v %s", err, string(out))
	}
	return nil
}

// WaitForServer polls url until it answers or timeout passes.
func WaitForServer(url string, timeout time.Duration) bool {
	c := &http.Client{Timeout: time.Second}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if resp, err := c.Get(url); err == nil {
			resp.Body.Close()
			return true
		}
		time.Sleep(150 * time.Millisecond)
	}
	return false
}
