package desktop

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
)

// FindBrowserAppExecutable locates an Edge, Chrome, or Chromium executable capable of running in --app mode
func FindBrowserAppExecutable() string {
	var candidates []string
	switch runtime.GOOS {
	case "windows":
		candidates = []string{
			`C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe`,
			`C:\Program Files\Microsoft\Edge\Application\msedge.exe`,
			`C:\Program Files\Google\Chrome\Application\chrome.exe`,
			`C:\Program Files (x86)\Google\Chrome\Application\chrome.exe`,
			filepath.Join(os.Getenv("LOCALAPPDATA"), `Microsoft\Edge\Application\msedge.exe`),
			filepath.Join(os.Getenv("LOCALAPPDATA"), `Google\Chrome\Application\chrome.exe`),
		}
	case "darwin":
		candidates = []string{
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
			"/Applications/Brave Browser.app/Contents/MacOS/Brave Browser",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
		}
	default: // linux, bsd
		candidates = []string{
			"/usr/bin/google-chrome",
			"/usr/bin/google-chrome-stable",
			"/usr/bin/chromium-browser",
			"/usr/bin/chromium",
			"/usr/bin/microsoft-edge",
			"/usr/bin/brave-browser",
		}
	}

	for _, path := range candidates {
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return ""
}

// OpenDefaultBrowser opens the given URL in the user's default browser across OS platforms
func OpenDefaultBrowser(appURL string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", "", appURL)
	case "darwin":
		cmd = exec.Command("open", appURL)
	default:
		cmd = exec.Command("xdg-open", appURL)
	}
	return cmd.Start()
}

// LaunchAppWindow opens the application URL in a dedicated chromeless native desktop window
func LaunchAppWindow(appURL string) (*exec.Cmd, error) {
	exe := FindBrowserAppExecutable()
	if exe == "" {
		return nil, OpenDefaultBrowser(appURL)
	}

	args := []string{
		fmt.Sprintf("--app=%s", appURL),
		"--window-size=1280,850",
	}

	cmd := exec.Command(exe, args...)
	if err := cmd.Start(); err != nil {
		return nil, OpenDefaultBrowser(appURL)
	}
	return cmd, nil
}

// EnsureDesktopShortcut creates a Desktop launcher shortcut pointing to this binary across Windows and Linux
func EnsureDesktopShortcut(targetExe string) error {
	if targetExe == "" {
		var err error
		targetExe, err = os.Executable()
		if err != nil {
			return err
		}
	}

	targetExe, _ = filepath.Abs(targetExe)
	workDir := filepath.Dir(targetExe)

	if runtime.GOOS == "windows" {
		script := fmt.Sprintf(`
$desktop = [Environment]::GetFolderPath('Desktop')
if ($desktop -and (Test-Path $desktop)) {
    $shortcutPath = Join-Path $desktop "Web Auto.lnk"
    $ws = New-Object -ComObject WScript.Shell
    $s = $ws.CreateShortcut($shortcutPath)
    $s.TargetPath = "%s"
    $s.Arguments = "app"
    $s.WorkingDirectory = "%s"
    $s.Description = "Web Auto"
    $s.Save()
}
`, targetExe, workDir)

		cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", script)
		return cmd.Run()
	}

	if runtime.GOOS == "linux" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return nil
		}
		desktopDir := filepath.Join(homeDir, "Desktop")
		appsDir := filepath.Join(homeDir, ".local", "share", "applications")
		_ = os.MkdirAll(appsDir, 0755)

		entryContent := fmt.Sprintf(`[Desktop Entry]
Type=Application
Name=Web Auto
Comment=WhatsApp Automation Platform
Exec=%s app
Path=%s
Terminal=false
Categories=Network;InstantMessaging;
`, targetExe, workDir)

		appFile := filepath.Join(appsDir, "whatsapp-auto.desktop")
		_ = os.WriteFile(appFile, []byte(entryContent), 0755)

		if fi, err := os.Stat(desktopDir); err == nil && fi.IsDir() {
			deskFile := filepath.Join(desktopDir, "whatsapp-auto.desktop")
			_ = os.WriteFile(deskFile, []byte(entryContent), 0755)
		}
		return nil
	}

	return nil
}

// WaitForServer waits for the HTTP server to respond before opening the app window
func WaitForServer(url string, timeout time.Duration) bool {
	client := &http.Client{Timeout: 1 * time.Second}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := client.Get(url)
		if err == nil {
			resp.Body.Close()
			return true
		}
		time.Sleep(150 * time.Millisecond)
	}
	return false
}
