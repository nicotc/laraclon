// Package vcredist checks for and installs Microsoft's Visual C++ x64 runtime (v14, shared by
// Visual Studio 2015 to 2026), which the Windows builds of PHP, Apache and MySQL need. Nido only
// runs Microsoft's own signed installer.
package vcredist

import (
	"context"
	"debug/pe"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"github.com/nicotc/laraclon/internal/download"
	"github.com/nicotc/laraclon/internal/paths"
	"github.com/nicotc/laraclon/internal/versions"
	"github.com/nicotc/laraclon/internal/winx"
)

// URL is Microsoft's permanent link to the latest Visual C++ v14 x64 redistributable (Visual Studio
// 2017-2026). The older link of Visual Studio 2022 stays on 14.44, older than what programs built
// with Visual Studio 2026 (Apache Lounge's current Apache, for example) ask for.
const URL = "https://aka.ms/vc14/vc_redist.x64.exe"

// ErrRestart means the runtime was installed but Windows wants a restart to finish, usually because
// running programs had its DLLs open.
var ErrRestart = errors.New("Visual C++ quedó instalado pero Windows pide reiniciar para terminar")

var required = []string{"vcruntime140.dll", "vcruntime140_1.dll", "msvcp140.dll"}

func system32() string { return filepath.Join(os.Getenv("SystemRoot"), "System32") }

// Missing lists the runtime DLLs that are not in System32 (empty when the runtime is installed).
func Missing() []string {
	var out []string
	for _, dll := range required {
		if _, err := os.Stat(filepath.Join(system32(), dll)); err != nil {
			out = append(out, dll)
		}
	}
	return out
}

// Version is the file version of vcruntime140.dll, e.g. "14.44.35211.0" ("" when absent).
func Version() string {
	file := filepath.Join(system32(), "vcruntime140.dll")
	size, err := windows.GetFileVersionInfoSize(file, nil)
	if err != nil || size == 0 {
		return ""
	}
	buf := make([]byte, size)
	if err := windows.GetFileVersionInfo(file, 0, size, unsafe.Pointer(&buf[0])); err != nil {
		return ""
	}
	var info *windows.VS_FIXEDFILEINFO
	var n uint32
	if err := windows.VerQueryValue(unsafe.Pointer(&buf[0]), `\`, unsafe.Pointer(&info), &n); err != nil || info == nil {
		return ""
	}
	return fmt.Sprintf("%d.%d.%d.%d", info.FileVersionMS>>16, info.FileVersionMS&0xffff, info.FileVersionLS>>16, info.FileVersionLS&0xffff)
}

// Older reports whether the runtime is missing or older than min ("14.51").
func Older(min string) bool {
	have := Version()
	return have == "" || versions.Compare(have, min) < 0
}

// RestartPending reports whether an update of the runtime waits for Windows to restart: its
// installer could not replace DLLs that running programs had open.
func RestartPending() bool {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Control\Session Manager`, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	renames, _, err := k.GetStringsValue("PendingFileRenameOperations")
	if err != nil {
		return false
	}
	for _, r := range renames {
		r = strings.ToLower(r)
		if strings.Contains(r, "vcruntime140") || strings.Contains(r, "msvcp140") {
			return true
		}
	}
	return false
}

// Needs is the runtime a program was built for ("14.51"), or "" when it does not matter. Microsoft
// asks for a runtime at least as new as the compiler that built the program, which the program
// records as its linker version; in practice C programs such as Apache and PHP also run on older
// ones. Programs that do not load the runtime from Windows, or carry their own copy, need nothing.
func Needs(exe string) string {
	f, err := pe.Open(exe)
	if err != nil {
		return ""
	}
	defer f.Close()
	var major, minor uint8
	switch h := f.OptionalHeader.(type) {
	case *pe.OptionalHeader64:
		major, minor = h.MajorLinkerVersion, h.MinorLinkerVersion
	case *pe.OptionalHeader32:
		major, minor = h.MajorLinkerVersion, h.MinorLinkerVersion
	}
	if major != 14 || !importsRuntime(f) {
		return ""
	}
	// Windows looks next to the program before System32.
	if _, err := os.Stat(filepath.Join(filepath.Dir(exe), "vcruntime140.dll")); err == nil {
		return ""
	}
	return fmt.Sprintf("14.%d", minor)
}

func importsRuntime(f *pe.File) bool {
	syms, err := f.ImportedSymbols() // "memcpy:VCRUNTIME140.dll"
	if err != nil {
		return false
	}
	for _, s := range syms {
		lib := strings.ToLower(s[strings.LastIndex(s, ":")+1:])
		if strings.HasPrefix(lib, "vcruntime140") || strings.HasPrefix(lib, "msvcp140") {
			return true
		}
	}
	return false
}

// Install runs Microsoft's redistributable installer: the one given, or a fresh download of URL.
// The file must carry a valid Microsoft Authenticode signature. Windows asks for administrator
// permission (UAC) because the installer writes to System32. status, when not nil, gets one line
// as each phase ends, with how long it took (on a clean Windows the installer takes minutes).
func Install(ctx context.Context, home paths.Home, installer string, progress download.Progress, status func(string)) error {
	report := func(format string, a ...any) {
		if status != nil {
			status(fmt.Sprintf(format, a...))
		}
	}
	if installer == "" {
		installer = home.Cache("downloads", "vc_redist.x64.exe")
		if _, err := download.File(ctx, URL, installer, "", progress); err != nil {
			return fmt.Errorf("no se pudo descargar el runtime de Visual C++: %w", err)
		}
	}
	start := time.Now()
	if err := winx.VerifySignature(installer, "Microsoft Corporation"); err != nil {
		return err
	}
	report("Firma de Microsoft comprobada en %.1f s. Instalando; en un Windows recién instalado tarda unos minutos…", time.Since(start).Seconds())
	start = time.Now()
	code, err := winx.RunElevated(installer, "/install /passive /norestart", true)
	if err != nil {
		return fmt.Errorf("instalador de Visual C++: %w", err)
	}
	report("El instalador de Microsoft terminó en %.1f s (código %d).", time.Since(start).Seconds(), code)
	switch code {
	case 0, 1638: // installed, or a newer version was already present
		return nil
	case 3010, 1641:
		return ErrRestart
	}
	return fmt.Errorf("el instalador de Visual C++ terminó con código %d", code)
}
