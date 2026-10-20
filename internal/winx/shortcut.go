package winx

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// Shortcut is a Windows shortcut (.lnk), like the ones installers leave in the Start menu.
type Shortcut struct {
	Path        string // the .lnk file
	Target      string
	Args        string
	Dir         string // working folder
	Icon        string // an .ico file ("" uses the target's)
	Description string // shown as its tooltip
}

// shortcutScript writes a .lnk with the shell's own object, as installers do. The values travel in
// environment variables, so no quoting can break the script.
const shortcutScript = `$s = (New-Object -ComObject WScript.Shell).CreateShortcut($env:NIDO_LNK)
$s.TargetPath = $env:NIDO_TARGET
$s.Arguments = $env:NIDO_ARGS
$s.WorkingDirectory = $env:NIDO_DIR
if ($env:NIDO_ICON) { $s.IconLocation = $env:NIDO_ICON + ',0' }
$s.Description = $env:NIDO_DESC
$s.Save()`

// CreateShortcut writes (or rewrites) a shortcut.
func CreateShortcut(s Shortcut) error {
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o755); err != nil {
		return err
	}
	ps := filepath.Join(os.Getenv("SystemRoot"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
	cmd := exec.Command(ps, "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", shortcutScript)
	cmd.Env = append(os.Environ(), "NIDO_LNK="+s.Path, "NIDO_TARGET="+s.Target, "NIDO_ARGS="+s.Args,
		"NIDO_DIR="+s.Dir, "NIDO_ICON="+s.Icon, "NIDO_DESC="+s.Description)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("no se pudo crear el acceso directo %s: %v %s", s.Path, err, strings.TrimSpace(string(out)))
	}
	if _, err := os.Stat(s.Path); err != nil {
		return fmt.Errorf("no se pudo crear el acceso directo %s", s.Path)
	}
	return nil
}

// StartMenuPrograms is the user's Start menu folder of programs.
func StartMenuPrograms() (string, error) {
	return windows.KnownFolderPath(windows.FOLDERID_Programs, 0)
}

// Desktop is the user's desktop folder (it may live in OneDrive).
func Desktop() (string, error) {
	return windows.KnownFolderPath(windows.FOLDERID_Desktop, 0)
}

const runKey = `Software\Microsoft\Windows\CurrentVersion\Run`

// SetRunAtLogon makes a command start when the user signs in to Windows ("" removes it): the same
// list Windows shows in Task Manager's startup apps.
func SetRunAtLogon(name, command string) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	if command == "" {
		if err := k.DeleteValue(name); err != nil && err != registry.ErrNotExist {
			return err
		}
		return nil
	}
	return k.SetStringValue(name, command)
}

// RunAtLogon is the command that starts under name when the user signs in ("" when none).
func RunAtLogon(name string) string {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer k.Close()
	v, _, err := k.GetStringValue(name)
	if err != nil {
		return ""
	}
	return v
}
