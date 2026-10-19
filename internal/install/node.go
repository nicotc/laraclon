// Package install downloads official builds (Node, PHP, Composer, CA bundle) into the installation.
package install

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"time"

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
	Security bool     `json:"security"`
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
	file := fmt.Sprintf("node-%s-win-x64.zip", rel.Version)
	sums, err := download.Text(ctx, fmt.Sprintf("%s/%s/SHASUMS256.txt", nodeDist, rel.Version))
	if err != nil {
		return versions.Version{}, err
	}
	want := checksumFor(sums, file)
	if want == "" {
		return versions.Version{}, fmt.Errorf("SHASUMS256.txt no incluye %s", file)
	}
	zipPath := home.Cache("downloads", file)
	if _, err := download.File(ctx, fmt.Sprintf("%s/%s/%s", nodeDist, rel.Version, file), zipPath, want, progress); err != nil {
		return versions.Version{}, err
	}
	dest := home.Bin("node", strings.TrimPrefix(rel.Version, "v"))
	if err := unpackFresh(zipPath, dest); err != nil {
		return versions.Version{}, err
	}
	v, err := versions.DetectNode(dest)
	if err != nil {
		return versions.Version{}, err
	}
	v.Source = "download"
	return v, nil
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
func unpackFresh(zipPath, dest string) error {
	tmp := dest + ".unpack"
	os.RemoveAll(tmp)
	if err := download.ExtractZip(zipPath, tmp, true); err != nil {
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
