package dns

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/nicotc/laraclon/internal/procutil"
	"github.com/nicotc/laraclon/internal/winx"
)

// ruleComment marks the name resolution policy rules Nido creates, so it only ever removes its own.
const ruleComment = "Nido: dominios locales"

// validTLD keeps what goes into a PowerShell command to plain domain letters.
func validTLD(tld string) error {
	if tld == "" || strings.Trim(tld, "abcdefghijklmnopqrstuvwxyz0123456789-") != "" {
		return fmt.Errorf("dominio local no válido: %q", tld)
	}
	return nil
}

// script is the PowerShell that adds (on) or removes Nido's rule for .<tld>, then empties the DNS
// cache so the change counts at once.
func script(tld string, on bool) string {
	remove := fmt.Sprintf("Get-DnsClientNrptRule | Where-Object { $_.Comment -eq '%s' -or ($_.Namespace -contains '.%s' -and $_.NameServers -contains '127.0.0.1') } | ForEach-Object { Remove-DnsClientNrptRule -Name $_.Name -Force }", ruleComment, tld)
	s := "$ErrorActionPreference = 'Stop'; " + remove + "; "
	if on {
		s += fmt.Sprintf("Add-DnsClientNrptRule -Namespace '.%s' -NameServers '127.0.0.1' -Comment '%s' | Out-Null; ", tld, ruleComment)
	}
	return s + "Clear-DnsClientCache"
}

// Apply is run by the elevated helper ('nido dns-apply on|off <tld>').
func Apply(tld string, on bool) error {
	if err := validTLD(tld); err != nil {
		return err
	}
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script(tld, on))
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("PowerShell: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// RequestElevated asks Windows (UAC) to run 'exe dns-apply on|off tld' as administrator: the rule is a
// system setting.
func RequestElevated(exe, tld string, on bool) error {
	if err := validTLD(tld); err != nil {
		return err
	}
	verb := "off"
	if on {
		verb = "on"
	}
	code, err := winx.RunElevated(exe, "dns-apply "+verb+" "+tld, false)
	if errors.Is(err, winx.ErrElevationCancelled) {
		return fmt.Errorf("se canceló el permiso de administrador; Windows sigue resolviendo .%s como antes", tld)
	}
	if err != nil {
		return fmt.Errorf("no se pudo pedir permiso de administrador: %w", err)
	}
	if code != 0 {
		return fmt.Errorf("el ayudante de DNS terminó con código %d", code)
	}
	return nil
}

// RuleInstalled reports whether Windows sends .<tld> to 127.0.0.1 (reading the rules needs no
// administrator rights).
func RuleInstalled(tld string) bool {
	if validTLD(tld) != nil {
		return false
	}
	q := fmt.Sprintf("@(Get-DnsClientNrptRule | Where-Object { $_.Namespace -contains '.%s' -and $_.NameServers -contains '127.0.0.1' }).Count", tld)
	out, err := procutil.Output(15*time.Second, "", "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", q)
	return err == nil && strings.TrimSpace(out) != "" && strings.TrimSpace(out) != "0"
}
