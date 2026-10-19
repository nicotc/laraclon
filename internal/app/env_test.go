package app

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/nicotc/laraclon/internal/ca"
	"github.com/nicotc/laraclon/internal/install"
	"github.com/nicotc/laraclon/internal/paths"
)

// A CA created before the public list was downloaded (nido setup's Node step does) must not leave
// PHP with a bundle that only trusts Nido's CA.
func TestPHPBundleGetsThePublicRootsDownloadedAfterTheCA(t *testing.T) {
	home := paths.Home{Root: t.TempDir()}
	if err := os.MkdirAll(home.SSL(), 0o755); err != nil {
		t.Fatal(err)
	}
	e := &Env{Home: home}
	authority, created, err := ca.LoadOrCreate(home.SSL())
	if err != nil || !created {
		t.Fatalf("CA: %v %v", created, err)
	}
	if err := e.RefreshCABundle(authority, created); err != nil {
		t.Fatal(err)
	}
	bundle := home.SSL("cacert-bundle.pem")
	before, _ := os.ReadFile(bundle)
	if strings.Contains(string(before), "PUBLICA") {
		t.Fatal("there is no public list yet")
	}
	// The public list arrives later.
	later := time.Now().Add(2 * time.Second)
	if err := os.WriteFile(install.MozillaBundle(home), []byte("# PUBLICA\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	os.Chtimes(install.MozillaBundle(home), later, later)
	if err := e.RefreshCABundleFile(); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(bundle)
	if !strings.Contains(string(after), "PUBLICA") || !strings.Contains(string(after), "Nido CA local") {
		t.Fatalf("the bundle must hold the public list and Nido's CA, got:\n%s", after)
	}
}
