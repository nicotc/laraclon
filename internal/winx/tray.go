package winx

import (
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows/registry"
)

const notifyIconSettings = `Control Panel\NotifyIconSettings`

// PromoteTrayIcon makes Windows 11 show exe's tray icon next to the clock instead of hiding it under
// «^». Windows keeps one entry per program under HKCU\Control Panel\NotifyIconSettings, created once
// the icon has been shown, with IsPromoted when the user chose where it goes. Only an entry without
// that choice is changed, so hiding it again later is respected. It reports whether the entry exists
// yet (call it again a moment later when it does not).
func PromoteTrayIcon(exe string) (found bool) {
	root, err := registry.OpenKey(registry.CURRENT_USER, notifyIconSettings, registry.ENUMERATE_SUB_KEYS)
	if err != nil {
		return false // Windows 10 has no such list: its icons are placed by the user alone
	}
	defer root.Close()
	names, err := root.ReadSubKeyNames(-1)
	if err != nil {
		return false
	}
	want := strings.ToLower(filepath.Clean(exe))
	for _, n := range names {
		k, err := registry.OpenKey(root, n, registry.QUERY_VALUE|registry.SET_VALUE)
		if err != nil {
			continue
		}
		path, _, err := k.GetStringValue("ExecutablePath")
		if err != nil || !strings.EqualFold(filepath.Clean(expandKnownFolder(path)), want) {
			k.Close()
			continue
		}
		found = true
		if _, _, err := k.GetIntegerValue("IsPromoted"); err == registry.ErrNotExist {
			k.SetDWordValue("IsPromoted", 1)
		}
		k.Close()
	}
	return found
}

// expandKnownFolder turns the {GUID}\… form Windows uses for some paths into a plain one; other paths
// pass through.
func expandKnownFolder(p string) string {
	known := map[string]string{
		"{6D809377-6AF0-444B-8957-A3773F02200E}": `C:\Program Files`,
		"{7C5A40EF-A0FB-4BFC-874A-C0F2E0B9FA8E}": `C:\Program Files (x86)`,
		"{F38BF404-1D43-42F2-9305-67DE0B28FC23}": `C:\Windows`,
	}
	for guid, dir := range known {
		if strings.HasPrefix(strings.ToUpper(p), guid) {
			return dir + p[len(guid):]
		}
	}
	return p
}
