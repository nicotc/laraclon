package winx

import (
	"errors"
	"unsafe"

	"golang.org/x/sys/windows"
)

// ErrElevationCancelled means the user declined the UAC prompt.
var ErrElevationCancelled = errors.New("se canceló el permiso de administrador")

const (
	seeMaskNoCloseProcess = 0x00000040
	seeMaskNoAsync        = 0x00000100
)

var procShellExecuteEx = windows.NewLazySystemDLL("shell32.dll").NewProc("ShellExecuteExW")

// shellExecuteInfo mirrors SHELLEXECUTEINFOW.
type shellExecuteInfo struct {
	cbSize         uint32
	fMask          uint32
	hwnd           uintptr
	lpVerb         *uint16
	lpFile         *uint16
	lpParameters   *uint16
	lpDirectory    *uint16
	nShow          int32
	hInstApp       uintptr
	lpIDList       uintptr
	lpClass        *uint16
	hkeyClass      uintptr
	dwHotKey       uint32
	hIconOrMonitor uintptr
	hProcess       windows.Handle
}

// RunElevated asks Windows to run exe as administrator (Windows shows its UAC prompt unless the
// caller is already elevated), waits for it and returns its exit code.
func RunElevated(exe, params string, visible bool) (uint32, error) {
	verb, _ := windows.UTF16PtrFromString("runas")
	file, err := windows.UTF16PtrFromString(exe)
	if err != nil {
		return 0, err
	}
	args, err := windows.UTF16PtrFromString(params)
	if err != nil {
		return 0, err
	}
	show := int32(windows.SW_HIDE)
	if visible {
		show = windows.SW_SHOWNORMAL
	}
	info := shellExecuteInfo{
		fMask:        seeMaskNoCloseProcess | seeMaskNoAsync,
		lpVerb:       verb,
		lpFile:       file,
		lpParameters: args,
		nShow:        show,
	}
	info.cbSize = uint32(unsafe.Sizeof(info))
	r, _, callErr := procShellExecuteEx.Call(uintptr(unsafe.Pointer(&info)))
	if r == 0 {
		if callErr == windows.ERROR_CANCELLED {
			return 0, ErrElevationCancelled
		}
		return 0, callErr
	}
	defer windows.CloseHandle(info.hProcess)
	windows.WaitForSingleObject(info.hProcess, windows.INFINITE)
	var code uint32
	if err := windows.GetExitCodeProcess(info.hProcess, &code); err != nil {
		return 0, err
	}
	return code, nil
}
