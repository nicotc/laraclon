// Package vcredist checks for and installs the Microsoft Visual C++ 2015-2022 x64 runtime, which the
// Windows builds of PHP, Apache and MySQL need. clon only runs Microsoft's own signed installer.
package vcredist

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/nicotc/laraclon/internal/download"
	"github.com/nicotc/laraclon/internal/paths"
	"github.com/nicotc/laraclon/internal/winx"
)

// URL is Microsoft's permanent link to the latest Visual C++ 2015-2022 x64 redistributable.
const URL = "https://aka.ms/vs/17/release/vc_redist.x64.exe"

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
		return fmt.Errorf("Visual C++ quedó instalado pero Windows pide reiniciar para terminar")
	}
	return fmt.Errorf("el instalador de Visual C++ terminó con código %d", code)
}
