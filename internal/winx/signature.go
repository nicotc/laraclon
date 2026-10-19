package winx

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"
)

// VerifySignature checks with Windows' own verifier (revocation included) that file carries a valid
// Authenticode signature from the organisation org, e.g. "Microsoft Corporation".
func VerifySignature(file, org string) error {
	script := `$s = Get-AuthenticodeSignature -LiteralPath $env:CLON_FILE; "{0}|{1}" -f $s.Status, $s.SignerCertificate.Subject`
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script)
	cmd.Env = append(os.Environ(), "CLON_FILE="+file)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("no se pudo comprobar la firma de %s: %w", file, err)
	}
	status, subject, _ := strings.Cut(strings.TrimSpace(string(out)), "|")
	if status != "Valid" || !signedBy(subject, org) {
		return fmt.Errorf("%s no tiene una firma válida de %s (%s %s); no se usa", filepath.Base(file), org, status, subject)
	}
	return nil
}

// signedBy looks for the organisation (O=) in a certificate subject; values with commas are quoted.
func signedBy(subject, org string) bool {
	return strings.Contains(subject+",", "O="+org+",") || strings.Contains(subject, `O="`+org+`"`)
}
