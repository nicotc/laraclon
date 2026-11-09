package install

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nicotc/laraclon/internal/paths"
	"github.com/nicotc/laraclon/internal/versions"
)

func TestArchivedBranches(t *testing.T) {
	listing := `<a href="php-7.2.33-nts-Win32-VC15-x64.zip">x</a><a href="php-7.2.34-nts-Win32-VC15-x64.zip">x</a>
<a href="php-8.1.34-nts-Win32-vs16-x64.zip">x</a><a href="php-7.2.34-Win32-VC15-x86.zip">x</a>`
	got := strings.Join(archivedBranches(listing), ",")
	if got != "7.2,8.1" {
		t.Fatalf("archivedBranches = %q", got)
	}
}

func TestZipFolder(t *testing.T) {
	ts, nts := true, false
	cases := []struct {
		v    versions.Version
		want string
	}{
		{versions.Version{Component: versions.PHP, Version: "7.1.33", ThreadSafe: &nts}, "7.1.33-nts-x64"},
		{versions.Version{Component: versions.PHP, Version: "8.4.16", ThreadSafe: &ts}, "8.4.16-ts-x64"},
		{versions.Version{Component: versions.Apache, Version: "2.4.62"}, "2.4.62"},
	}
	for _, c := range cases {
		if got := ZipFolder(c.v); got != c.want {
			t.Errorf("ZipFolder(%s) = %q, want %q", c.v.Label(), got, c.want)
		}
	}
}

func TestOwnedByNido(t *testing.T) {
	home := paths.Home{Root: `C:\nido`}
	cases := []struct {
		v    versions.Version
		want bool
	}{
		{versions.Version{Source: "download", Path: `C:\nido\bin\php\8.4.16-nts-x64`}, true},
		{versions.Version{Source: SourceZip, Path: `c:\NIDO\bin\apache\2.4.62\Apache24`}, true},
		{versions.Version{Source: "import", Path: `C:\nido\bin\php\8.3`}, false},
		{versions.Version{Source: "download", Path: `C:\php\8.4`}, false},
		{versions.Version{Source: "download", Path: `C:\nido\binario\php`}, false},
	}
	for _, c := range cases {
		if got := OwnedByNido(home, c.v); got != c.want {
			t.Errorf("OwnedByNido(%s %s) = %v", c.v.Source, c.v.Path, got)
		}
	}
}

func TestImportZipRejectsWhatIsNotTheProgram(t *testing.T) {
	home := paths.Home{Root: t.TempDir()}
	zipPath := filepath.Join(t.TempDir(), "x.zip")
	f, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(f)
	fw, _ := w.Create("php-8.4/readme.txt")
	fw.Write([]byte("no soy PHP"))
	w.Close()
	f.Close()
	if _, err := ImportZip(home, versions.PHP, zipPath, nil); err == nil || !strings.Contains(err.Error(), "no contiene PHP") {
		t.Fatalf("ImportZip = %v, want «no contiene PHP»", err)
	}
	if _, err := ImportZip(home, versions.Adminer, zipPath, nil); err == nil {
		t.Fatal("Adminer should not be importable")
	}
	// Nothing is left behind in bin\php.
	entries, _ := os.ReadDir(home.Bin("php"))
	if len(entries) != 0 {
		t.Fatalf("bin\\php keeps %d entries", len(entries))
	}
}
