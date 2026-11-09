// Package phpreq reads which PHP versions a Composer project accepts: its own "php" requirement and
// the one of every package its composer.lock pins, and checks a version against them with
// Composer's constraint syntax (^8.1, ~8.2.0, >=7.4 <8.5, 8.1.*, ||).
package phpreq

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Requirement is one "php" constraint and who asks for it.
type Requirement struct {
	From       string // "composer.json" or the package ("phpoffice/phpspreadsheet 1.30.4")
	Constraint string
}

// Read lists a project's PHP requirements. pinned is true when composer.json fakes the platform
// (config.platform.php): Composer then never checks the real PHP, so neither should Nido.
func Read(dir string) (reqs []Requirement, pinned bool, err error) {
	var cj struct {
		Require map[string]string `json:"require"`
		Config  struct {
			Platform map[string]any `json:"platform"`
		} `json:"config"`
	}
	data, err := os.ReadFile(filepath.Join(dir, "composer.json"))
	if err != nil {
		return nil, false, err
	}
	if err := json.Unmarshal(data, &cj); err != nil {
		return nil, false, fmt.Errorf("composer.json: %w", err)
	}
	if _, ok := cj.Config.Platform["php"]; ok {
		return nil, true, nil
	}
	if c := cj.Require["php"]; c != "" {
		reqs = append(reqs, Requirement{From: "composer.json", Constraint: c})
	}
	type pkg struct {
		Name    string            `json:"name"`
		Version string            `json:"version"`
		Require map[string]string `json:"require"`
	}
	var lock struct {
		Packages    []pkg `json:"packages"`
		PackagesDev []pkg `json:"packages-dev"`
	}
	if data, err := os.ReadFile(filepath.Join(dir, "composer.lock")); err == nil && json.Unmarshal(data, &lock) == nil {
		for _, p := range append(lock.Packages, lock.PackagesDev...) {
			if c := p.Require["php"]; c != "" {
				reqs = append(reqs, Requirement{From: p.Name + " " + p.Version, Constraint: c})
			}
		}
	}
	return reqs, false, nil
}

// Unmet is the first requirement a PHP version does not satisfy (nil when it satisfies them all).
func Unmet(version string, reqs []Requirement) *Requirement {
	for i := range reqs {
		if !Satisfies(version, reqs[i].Constraint) {
			return &reqs[i]
		}
	}
	return nil
}

// Satisfies reports whether a version ("8.4.27") meets a Composer constraint. A constraint it cannot
// read is taken as met: Composer itself will say what is wrong.
func Satisfies(version, constraint string) bool {
	v, ok := parse(version)
	if !ok {
		return true
	}
	for _, alt := range regexp.MustCompile(`\s*\|\|?\s*`).Split(strings.TrimSpace(constraint), -1) {
		if alt == "" {
			continue
		}
		all, readable := true, true
		for _, term := range terms(alt) {
			ok, known := matchTerm(v, term)
			if !known {
				readable = false
				break
			}
			if !ok {
				all = false
				break
			}
		}
		if !readable || all {
			return true
		}
	}
	return false
}

type ver [3]int

func cmp(a, b ver) int {
	for i := 0; i < 3; i++ {
		if a[i] != b[i] {
			if a[i] < b[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}

// parse reads "8.4.27", "v8.1", "8.1.0-dev"; parts it lacks are 0. n is how many parts it had.
func parse(s string) (ver, bool) {
	v, _, ok := parseN(s)
	return v, ok
}

func parseN(s string) (v ver, n int, ok bool) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	if i := strings.IndexAny(s, "-@+"); i >= 0 {
		s = s[:i]
	}
	parts := strings.Split(s, ".")
	if len(parts) == 0 || len(parts) > 4 {
		return v, 0, false
	}
	for i, p := range parts {
		if i == 3 {
			break
		}
		num, err := strconv.Atoi(p)
		if err != nil {
			return v, 0, false
		}
		v[i] = num
		n++
	}
	return v, n, true
}

// terms splits an AND list ("<8.5.0 >=7.4", ">=7.4, <8.5"), keeping hyphen ranges ("7.4 - 8.3") whole.
func terms(s string) []string {
	s = regexp.MustCompile(`\s+-\s+`).ReplaceAllString(s, "~RANGE~")
	s = regexp.MustCompile(`(>=|<=|!=|==|>|<|=|\^|~)\s+`).ReplaceAllString(s, "$1")
	var out []string
	for _, t := range regexp.MustCompile(`[\s,]+`).Split(s, -1) {
		if t != "" {
			out = append(out, t)
		}
	}
	return out
}

func matchTerm(v ver, t string) (ok, known bool) {
	if t == "*" {
		return true, true
	}
	if lo, hi, isRange := strings.Cut(t, "~RANGE~"); isRange {
		a, _, ok1 := parseN(lo)
		b, n, ok2 := parseN(hi)
		if !ok1 || !ok2 {
			return false, false
		}
		upper := bump(b, n) // "8.3" as an upper bound means anything 8.3.x
		return cmp(v, a) >= 0 && cmp(v, upper) < 0, true
	}
	for _, op := range []string{">=", "<=", "!=", "==", ">", "<", "=", "^", "~"} {
		if !strings.HasPrefix(t, op) {
			continue
		}
		rest := t[len(op):]
		b, n, ok := parseN(strings.TrimSuffix(strings.TrimSuffix(rest, ".*"), "*"))
		if !ok {
			return false, false
		}
		switch op {
		case ">=":
			return cmp(v, b) >= 0, true
		case "<=":
			return cmp(v, b) <= 0, true
		case ">":
			return cmp(v, b) > 0, true
		case "<":
			return cmp(v, b) < 0, true
		case "!=":
			return cmp(v, b) != 0, true
		case "=", "==":
			return cmp(v, b) == 0, true
		case "^":
			upper := ver{b[0] + 1}
			if b[0] == 0 {
				upper = ver{0, b[1] + 1}
			}
			return cmp(v, b) >= 0 && cmp(v, upper) < 0, true
		case "~":
			upper := ver{b[0] + 1}
			if n >= 3 {
				upper = ver{b[0], b[1] + 1}
			}
			return cmp(v, b) >= 0 && cmp(v, upper) < 0, true
		}
	}
	// "8.1.*", "8.*" or a bare version ("8.1": exactly 8.1.0, as Composer reads it).
	if strings.HasSuffix(t, ".*") {
		b, n, ok := parseN(strings.TrimSuffix(t, ".*"))
		if !ok {
			return false, false
		}
		return cmp(v, b) >= 0 && cmp(v, bump(b, n)) < 0, true
	}
	b, _, ok := parseN(t)
	if !ok {
		return false, false
	}
	return cmp(v, b) == 0, true
}

// bump is the first version after every one that starts like b's first n parts.
func bump(b ver, n int) ver {
	switch n {
	case 1:
		return ver{b[0] + 1}
	case 2:
		return ver{b[0], b[1] + 1}
	}
	return ver{b[0], b[1], b[2] + 1}
}
