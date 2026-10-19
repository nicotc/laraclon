// Package install downloads official builds (Node, PHP, Composer, CA bundle) into the installation.
package install

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/nicotc/laraclon/internal/config"
	"github.com/nicotc/laraclon/internal/download"
	"github.com/nicotc/laraclon/internal/paths"
	"github.com/nicotc/laraclon/internal/versions"
)

const nodeDist = "https://nodejs.org/dist"

// NodeRelease is one entry of https://nodejs.org/dist/index.json.
type NodeRelease struct {
	Version  string   `json:"version"` // "v24.22.0"
	Date     string   `json:"date"`
	Files    []string `json:"files"`
	LTS      any      `json:"lts"` // false or the codename
	Security bool     `json:"security,omitempty"`
}

// Major is the release line ("v24.22.0" → 24).
func (r NodeRelease) Major() int {
	n, _ := strconv.Atoi(strings.SplitN(strings.TrimPrefix(r.Version, "v"), ".", 2)[0])
	return n
}

// Release describes its Windows zip for InstallRelease; the SHA-256 comes from the release's
// SHASUMS256.txt when it is installed.
func (r NodeRelease) Release() Release {
	note := "actual"
	if r.IsLTS() {
		note = "LTS " + r.Codename()
	}
	return Release{Kind: versions.Node, Series: strconv.Itoa(r.Major()), Version: strings.TrimPrefix(r.Version, "v"),
		URL:     fmt.Sprintf("%s/%s/node-%s-win-x64.zip", nodeDist, r.Version, r.Version),
		SumsURL: fmt.Sprintf("%s/%s/SHASUMS256.txt", nodeDist, r.Version), Note: note, Source: "nodejs.org"}
}

func (r NodeRelease) IsLTS() bool {
	switch v := r.LTS.(type) {
	case bool:
		return v
	case string:
		return v != ""
	}
	return false
}

// Codename is the LTS name ("Krypton") or "".
func (r NodeRelease) Codename() string {
	if s, ok := r.LTS.(string); ok {
		return s
	}
	return ""
}

func (r NodeRelease) hasWindowsZip() bool {
	for _, f := range r.Files {
		if f == "win-x64-zip" {
			return true
		}
	}
	return false
}

// NodeReleases returns the official release list, newest first.
func NodeReleases(ctx context.Context) ([]NodeRelease, error) {
	var rels []NodeRelease
	if err := download.JSON(ctx, nodeDist+"/index.json", &rels); err != nil {
		return nil, fmt.Errorf("no se pudo leer la lista de versiones de Node: %w", err)
	}
	return rels, nil
}

// NodeIndex is NodeReleases limited to the Windows builds, kept in state\node-index.json for a few
// hours like the catalogs (the full list weighs a third of a megabyte); refresh asks nodejs.org again.
func NodeIndex(ctx context.Context, home paths.Home, refresh bool) ([]NodeRelease, error) {
	file := home.State("node-index.json")
	var cached struct {
		Fetched  time.Time     `json:"fetched"`
		Releases []NodeRelease `json:"releases"`
	}
	if data, err := os.ReadFile(file); err == nil && json.Unmarshal(data, &cached) == nil {
		if !refresh && time.Since(cached.Fetched) < catalogTTL && len(cached.Releases) > 0 {
			return cached.Releases, nil
		}
	}
	all, err := NodeReleases(ctx)
	if err != nil {
		if len(cached.Releases) > 0 {
			return cached.Releases, nil // an older list beats none while offline
		}
		return nil, err
	}
	var rels []NodeRelease
	for _, r := range all {
		if r.hasWindowsZip() {
			r.Files = []string{"win-x64-zip"}
			rels = append(rels, r)
		}
	}
	cached.Fetched, cached.Releases = time.Now(), rels
	if data, err := json.Marshal(cached); err == nil {
		config.WriteFileAtomic(file, data)
	}
	return rels, nil
}

// nodeSupportWindow: an LTS line without a release for this long has reached its end of life (the
// lines in maintenance still get security releases every few months).
const nodeSupportWindow = 180 * 24 * time.Hour

// NodeLines is the newest release of each line worth installing, newest line first: the current
// one when it is ahead of the LTS lines, and the LTS lines that still get releases.
func NodeLines(rels []NodeRelease, now time.Time) []NodeRelease {
	newest := map[int]NodeRelease{}
	var majors []int
	for _, r := range rels { // nodejs.org lists the newest version first
		if _, ok := newest[r.Major()]; !ok && r.hasWindowsZip() {
			newest[r.Major()] = r
			majors = append(majors, r.Major())
		}
	}
	latestLTS := 0
	for m, r := range newest {
		if r.IsLTS() && m > latestLTS {
			latestLTS = m
		}
	}
	var out []NodeRelease
	for i, m := range majors {
		r := newest[m]
		date, _ := time.Parse("2006-01-02", r.Date)
		switch {
		case i == 0 && m > latestLTS: // the current line
			out = append(out, r)
		case r.IsLTS() && (m == latestLTS || now.Sub(date) < nodeSupportWindow):
			out = append(out, r)
		}
	}
	return out
}

// NodeUpdate is a newer Node than the one in use.
type NodeUpdate struct {
	Version   string `json:"version"`             // "24.22.1"
	Line      bool   `json:"line,omitempty"`      // a newer LTS line rather than a release of the one in use
	LTS       string `json:"lts,omitempty"`       // codename of the line
	Security  bool   `json:"security,omitempty"`  // fixes vulnerabilities of the version in use
	Installed bool   `json:"installed,omitempty"` // already downloaded, just not in use
}

// NodeUpdates compares the Node in use with the release list: a newer release of its line, and a
// newer LTS line nobody installed yet. installed are the versions Nido knows.
func NodeUpdates(rels []NodeRelease, current string, installed []string) []NodeUpdate {
	cur := NodeRelease{Version: "v" + current}
	var out []NodeUpdate
	for _, r := range rels {
		if r.Major() != cur.Major() || !r.hasWindowsZip() {
			continue
		}
		v := strings.TrimPrefix(r.Version, "v")
		if versions.Compare(v, current) <= 0 {
			break
		}
		if len(out) == 0 {
			out = append(out, NodeUpdate{Version: v, LTS: r.Codename()})
			for _, have := range installed {
				out[0].Installed = out[0].Installed || have == v
			}
		}
		out[0].Security = out[0].Security || r.Security
	}
	var lts NodeRelease // the newest release of the newest LTS line
	for _, r := range rels {
		if r.IsLTS() && r.hasWindowsZip() && r.Major() > lts.Major() {
			lts = r
		}
	}
	if lts.Major() <= cur.Major() {
		return out
	}
	for _, v := range installed {
		if (NodeRelease{Version: "v" + v}).Major() >= lts.Major() {
			return out // already has it (or a newer one)
		}
	}
	return append(out, NodeUpdate{Version: strings.TrimPrefix(lts.Version, "v"), Line: true, LTS: lts.Codename()})
}

// NodeTools are the package managers Nido installs next to npm, as global npm packages (Node 25
// stopped shipping Corepack, which used to provide them).
var NodeTools = []string{"pnpm", "yarn"}

// NodePackage reads a global npm package: its version and its commands ("pnpm" → bin\pnpm.cjs),
// with ok false when it is not installed.
func NodePackage(prefix, name string) (version string, bins map[string]string, ok bool) {
	dir := filepath.Join(prefix, "node_modules", name)
	data, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		return "", nil, false
	}
	var pkg struct {
		Version string          `json:"version"`
		Bin     json.RawMessage `json:"bin"`
	}
	if json.Unmarshal(data, &pkg) != nil || pkg.Version == "" {
		return "", nil, false
	}
	bins = map[string]string{}
	var one string
	if json.Unmarshal(pkg.Bin, &one) == nil && one != "" {
		bins[name] = filepath.Join(dir, filepath.FromSlash(one))
	} else {
		var many map[string]string
		json.Unmarshal(pkg.Bin, &many)
		for cmd, p := range many {
			bins[cmd] = filepath.Join(dir, filepath.FromSlash(p))
		}
	}
	return pkg.Version, bins, true
}

// ResolveNode picks a release: "lts" (default), "latest", a major ("24") or an exact version.
func ResolveNode(rels []NodeRelease, want string) (NodeRelease, error) {
	want = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(want)), "v")
	for _, r := range rels {
		if !r.hasWindowsZip() {
			continue
		}
		v := strings.TrimPrefix(r.Version, "v")
		switch {
		case want == "" || want == "lts":
			if r.IsLTS() {
				return r, nil
			}
		case want == "latest" || want == "current":
			return r, nil
		case v == want || strings.HasPrefix(v, want+"."):
			return r, nil
		}
	}
	return NodeRelease{}, fmt.Errorf("no hay una versión de Node para Windows que coincida con %q", want)
}

// InstallNode downloads node-vX-win-x64.zip, verifies it against SHASUMS256.txt and unpacks it
// into bin\node\<version>. npm and npx come inside the official zip.
func InstallNode(ctx context.Context, home paths.Home, rel NodeRelease, progress download.Progress) (versions.Version, error) {
	return InstallRelease(ctx, home, rel.Release(), nil, progress)
}

// checksumFor finds "<sha256>  <file>" in a SHASUMS-style listing.
func checksumFor(listing, file string) string {
	sc := bufio.NewScanner(strings.NewReader(listing))
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == file {
			return fields[0]
		}
	}
	return ""
}

// unpackFresh extracts into dest, replacing a previous partial attempt. The final rename is retried:
// antivirus scanners briefly hold freshly written executables open, and Windows then refuses to
// move the folder that contains them.
func unpackFresh(zipPath, dest string) error { return unpackFreshSkip(zipPath, dest, nil) }

// unpackFreshSkip is unpackFresh leaving out the folders in skip ("pgAdmin 4/").
func unpackFreshSkip(zipPath, dest string, skip []string) error {
	tmp := dest + ".unpack"
	os.RemoveAll(tmp)
	keep := func(name string) bool {
		for _, s := range skip {
			if strings.HasPrefix(name, s) {
				return false
			}
		}
		return true
	}
	if err := download.ExtractZipFilter(zipPath, tmp, true, keep); err != nil {
		os.RemoveAll(tmp)
		return err
	}
	os.RemoveAll(dest)
	var err error
	for attempt := 0; attempt < 30; attempt++ {
		if err = os.Rename(tmp, dest); err == nil {
			return nil
		}
		time.Sleep(time.Duration(100+attempt*100) * time.Millisecond)
	}
	os.RemoveAll(tmp)
	return err
}
