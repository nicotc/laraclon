package winx

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// OpenURL opens a URL or file with its default program.
func OpenURL(target string) error {
	verb, _ := windows.UTF16PtrFromString("open")
	file, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	return windows.ShellExecute(0, verb, file, nil, nil, windows.SW_SHOWNORMAL)
}

// OpenAppWindow opens url in its own Edge window without tabs or address bar (falls back to the
// default browser).
func OpenAppWindow(url string) error {
	for _, base := range []string{os.Getenv("ProgramFiles(x86)"), os.Getenv("ProgramFiles")} {
		edge := filepath.Join(base, "Microsoft", "Edge", "Application", "msedge.exe")
		if _, err := os.Stat(edge); err == nil {
			return exec.Command(edge, "--app="+url).Start()
		}
	}
	return OpenURL(url)
}

const envKey = `Environment`

// AddToUserPath appends dirs to the current user's PATH (HKCU\Environment) if missing.
func AddToUserPath(dirs ...string) error {
	return editUserPath(func(parts []string) []string {
		for _, d := range dirs {
			if !containsFold(parts, d) {
				parts = append(parts, d)
			}
		}
		return parts
	})
}

// RemoveFromUserPath removes dirs from the current user's PATH.
func RemoveFromUserPath(dirs ...string) error {
	return editUserPath(func(parts []string) []string {
		out := parts[:0]
		for _, p := range parts {
			if !containsFold(dirs, p) {
				out = append(out, p)
			}
		}
		return out
	})
}

func containsFold(list []string, s string) bool {
	for _, x := range list {
		if strings.EqualFold(strings.TrimRight(x, `\`), strings.TrimRight(s, `\`)) {
			return true
		}
	}
	return false
}

func editUserPath(change func([]string) []string) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, envKey, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	cur, valType, err := k.GetStringValue("Path")
	if err != nil && err != registry.ErrNotExist {
		return err
	}
	var parts []string
	for _, p := range strings.Split(cur, ";") {
		if strings.TrimSpace(p) != "" {
			parts = append(parts, p)
		}
	}
	next := strings.Join(change(parts), ";")
	if next == cur {
		return nil
	}
	if valType == registry.EXPAND_SZ || strings.Contains(next, "%") {
		err = k.SetExpandStringValue("Path", next)
	} else {
		err = k.SetStringValue("Path", next)
	}
	if err != nil {
		return err
	}
	broadcastEnvironmentChange()
	return nil
}

// MessageBox shows a simple error dialog (used when the tray program cannot start).
func MessageBox(title, text string) {
	t, _ := windows.UTF16PtrFromString(title)
	m, _ := windows.UTF16PtrFromString(text)
	windows.MessageBox(0, m, t, windows.MB_ICONERROR|windows.MB_OK)
}

// broadcastEnvironmentChange tells Explorer and other programs that the environment changed, so new
// terminals pick up the PATH without signing out.
func broadcastEnvironmentChange() {
	user32 := windows.NewLazySystemDLL("user32.dll")
	send := user32.NewProc("SendMessageTimeoutW")
	env, _ := windows.UTF16PtrFromString("Environment")
	const hwndBroadcast, wmSettingChange, smtoAbortIfHung = 0xffff, 0x001A, 0x0002
	var result uintptr
	send.Call(hwndBroadcast, wmSettingChange, 0, uintptr(unsafe.Pointer(env)), smtoAbortIfHung, 5000, uintptr(unsafe.Pointer(&result)))
}
