package install

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// nodeIndex is a trimmed nodejs.org/dist/index.json as of mid-October 2026: 26 is the current line,
// 24 and 22 are LTS, 20 had its last release in March and 25 was the previous current line.
var nodeIndex = []NodeRelease{
	{Version: "v26.12.0", Date: "2026-10-14", Files: []string{"win-x64-zip"}, LTS: false},
	{Version: "v25.9.0", Date: "2026-03-31", Files: []string{"win-x64-zip"}, LTS: false},
	{Version: "v24.22.0", Date: "2026-10-14", Files: []string{"win-x64-zip"}, LTS: "Krypton"},
	{Version: "v24.21.0", Date: "2026-09-30", Files: []string{"win-x64-zip"}, LTS: "Krypton", Security: true},
	{Version: "v24.20.0", Date: "2026-09-02", Files: []string{"win-x64-zip"}, LTS: "Krypton"},
	{Version: "v22.23.3", Date: "2026-09-23", Files: []string{"win-x64-zip"}, LTS: "Jod"},
	{Version: "v20.20.2", Date: "2026-03-24", Files: []string{"win-x64-zip"}, LTS: "Iron", Security: true},
}

func versionsOf(rels []NodeRelease) []string {
	var out []string
	for _, r := range rels {
		out = append(out, r.Version)
	}
	return out
}

func TestNodeLinesAreTheCurrentAndTheSupportedLTS(t *testing.T) {
	now := time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)
	got := versionsOf(NodeLines(nodeIndex, now))
	want := []string{"v26.12.0", "v24.22.0", "v22.23.3"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v (20 stopped getting releases, 25 is behind the current line)", got, want)
	}
	// Once 26 turns LTS it is listed as such, and it is still the first line.
	turned := append([]NodeRelease{{Version: "v26.13.0", Date: "2026-10-28", Files: []string{"win-x64-zip"}, LTS: "Lumen"}}, nodeIndex...)
	lines := NodeLines(turned, now)
	if lines[0].Version != "v26.13.0" || lines[0].Release().Note != "LTS Lumen" || lines[0].Release().Series != "26" {
		t.Fatalf("got %+v", lines[0].Release())
	}
}

func TestNodeUpdatesFlagSecurityReleasesAndNewLTSLines(t *testing.T) {
	got := NodeUpdates(nodeIndex, "24.20.0", []string{"24.20.0"})
	want := []NodeUpdate{{Version: "24.22.0", LTS: "Krypton", Security: true}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("24.20.0 misses a security release: got %+v", got)
	}
	if got := NodeUpdates(nodeIndex, "24.21.0", nil); len(got) != 1 || got[0].Security {
		t.Fatalf("24.21.0 only misses a regular release: got %+v", got)
	}
	if got := NodeUpdates(nodeIndex, "24.22.0", nil); len(got) != 0 {
		t.Fatalf("the newest Krypton needs nothing: got %+v", got)
	}
	got = NodeUpdates(nodeIndex, "22.23.3", []string{"22.23.3"})
	want = []NodeUpdate{{Version: "24.22.0", Line: true, LTS: "Krypton"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("22 should hear about 24 LTS: got %+v", got)
	}
	if got := NodeUpdates(nodeIndex, "22.23.3", []string{"22.23.3", "24.20.0"}); len(got) != 0 {
		t.Fatalf("with a 24 installed there is no news: got %+v", got)
	}
}

func TestNodePackageReadsVersionAndCommands(t *testing.T) {
	prefix := t.TempDir()
	write := func(name, pkg string) {
		dir := filepath.Join(prefix, "node_modules", name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(pkg), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, ok := NodePackage(prefix, "pnpm"); ok {
		t.Fatal("pnpm is not installed yet")
	}
	write("pnpm", `{"name": "pnpm", "version": "10.18.3", "bin": {"pnpm": "bin/pnpm.cjs", "pnpx": "bin/pnpx.cjs"}}`)
	write("yarn", `{"name": "yarn", "version": "1.22.22", "bin": {"yarn": "./bin/yarn.js", "yarnpkg": "./bin/yarn.js"}}`)
	write("only", `{"name": "only", "version": "1.0.0", "bin": "cli.js"}`)
	v, bins, ok := NodePackage(prefix, "pnpm")
	if !ok || v != "10.18.3" || bins["pnpx"] != filepath.Join(prefix, "node_modules", "pnpm", "bin", "pnpx.cjs") {
		t.Fatalf("got %s %v %v", v, bins, ok)
	}
	if _, bins, _ := NodePackage(prefix, "yarn"); bins["yarnpkg"] != filepath.Join(prefix, "node_modules", "yarn", "bin", "yarn.js") {
		t.Fatalf("got %v", bins)
	}
	if _, bins, _ := NodePackage(prefix, "only"); bins["only"] != filepath.Join(prefix, "node_modules", "only", "cli.js") {
		t.Fatalf("a single bin is named after the package, got %v", bins)
	}
}
