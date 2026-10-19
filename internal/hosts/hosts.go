// Package hosts maintains one clearly marked block in the Windows hosts file with the local sites.
// Lines outside the block are never touched. Writing the file needs administrator rights, so Nido
// asks Windows to run a narrow helper (nido hosts-apply) with elevation; Windows shows the UAC prompt.
package hosts

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/nicotc/laraclon/internal/winx"
)

const (
	beginMarker = "# >>> Nido: sitios locales (bloque gestionado por Nido; no lo edites)"
	endMarker   = "# <<< Nido"
)

var validName = regexp.MustCompile(`^[a-z0-9]([a-z0-9.-]*[a-z0-9])?$`)

// Path is the system hosts file.
func Path() string {
	return filepath.Join(os.Getenv("SystemRoot"), "System32", "drivers", "etc", "hosts")
}

// Render returns content with the managed block replaced (or appended) for names.
func Render(content string, names []string) string {
	clean := cleanNames(names)
	eol := "\r\n"
	if !strings.Contains(content, "\r\n") && strings.Contains(content, "\n") {
		eol = "\n"
	}
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	var out []string
	inBlock := false
	for _, l := range lines {
		switch strings.TrimSpace(l) {
		case beginMarker:
			inBlock = true
			continue
		case endMarker:
			inBlock = false
			continue
		}
		if !inBlock {
			out = append(out, l)
		}
	}
	for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
		out = out[:len(out)-1]
	}
	if len(clean) > 0 {
		out = append(out, "", beginMarker)
		for _, n := range clean {
			out = append(out, fmt.Sprintf("127.0.0.1  %s", n), fmt.Sprintf("::1        %s", n))
		}
		out = append(out, endMarker)
	}
	return strings.Join(out, eol) + eol
}

func cleanNames(names []string) []string {
	set := map[string]bool{}
	for _, n := range names {
		n = strings.ToLower(strings.TrimSpace(n))
		if validName.MatchString(n) {
			set[n] = true
		}
	}
	out := make([]string, 0, len(set))
	for n := range set {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Missing lists names that the hosts file does not resolve (in or out of the managed block).
func Missing(content string, names []string) []string {
	known := map[string]bool{}
	for _, l := range strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n") {
		if i := strings.IndexByte(l, '#'); i >= 0 {
			l = l[:i]
		}
		f := strings.Fields(l)
		if len(f) < 2 {
			continue
		}
		for _, n := range f[1:] {
			known[strings.ToLower(n)] = true
		}
	}
	var out []string
	for _, n := range cleanNames(names) {
		if !known[n] {
			out = append(out, n)
		}
	}
	return out
}

// InSync reports whether the managed block already lists exactly names.
func InSync(content string, names []string) bool {
	return strings.ReplaceAll(Render(content, names), "\r\n", "\n") == strings.ReplaceAll(normalizeTrailing(content), "\r\n", "\n")
}

func normalizeTrailing(content string) string {
	eol := "\r\n"
	if !strings.Contains(content, "\r\n") && strings.Contains(content, "\n") {
		eol = "\n"
	}
	return strings.TrimRight(content, "\r\n") + eol
}

// Apply is run by the elevated helper: it reads the list of names, rewrites only the managed block
// and keeps a backup of the previous file in hosts.nido.bak.
func Apply(namesFile string) error {
	data, err := os.ReadFile(namesFile)
	if err != nil {
		return err
	}
	names := strings.Fields(string(data))
	current, err := os.ReadFile(Path())
	if err != nil {
		return err
	}
	next := Render(string(current), names)
	if next == string(current) {
		return nil
	}
	if err := os.WriteFile(Path()+".nido.bak", current, 0o644); err != nil {
		return err
	}
	return os.WriteFile(Path(), []byte(next), 0o644)
}

// RequestElevated asks Windows (UAC prompt) to run `exe hosts-apply namesFile` as administrator
// and waits for it.
func RequestElevated(exe, namesFile string) error {
	code, err := winx.RunElevated(exe, `hosts-apply "`+namesFile+`"`, false)
	if errors.Is(err, winx.ErrElevationCancelled) {
		return fmt.Errorf("se canceló el permiso de administrador; el archivo hosts no se modificó")
	}
	if err != nil {
		return fmt.Errorf("no se pudo pedir permiso de administrador: %w", err)
	}
	if code != 0 {
		return fmt.Errorf("el ayudante de hosts terminó con código %d", code)
	}
	return nil
}
