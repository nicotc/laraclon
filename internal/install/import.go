package install

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nicotc/laraclon/internal/download"
	"github.com/nicotc/laraclon/internal/paths"
	"github.com/nicotc/laraclon/internal/versions"
)

// Importable lists what a .zip brought by the user can be installed as: the same programs the
// catalog downloads, for versions it does not offer (an old PHP, a build of your own).
var Importable = []versions.Component{versions.PHP, versions.Apache, versions.Nginx, versions.FrankenPHP,
	versions.MySQL, versions.MariaDB, versions.PostgreSQL, versions.MongoDB, versions.Redis, versions.Mailpit,
	versions.Meilisearch, versions.Node}

// CanImport reports whether a .zip can be installed as kind.
func CanImport(kind versions.Component) bool {
	for _, k := range Importable {
		if k == kind {
			return true
		}
	}
	return false
}

// SourceZip marks the versions installed from a .zip the user brought. Like the downloaded ones,
// they live in bin\ and Nido deletes them when they are uninstalled.
const SourceZip = "zip"

// ImportZip installs a .zip as kind: it unpacks it, asks the program its version (which also proves
// it is what it claims and that it runs here) and moves it to bin\<kind>\<version>. taken reports
// whether a version is already installed, so an existing one is never replaced.
func ImportZip(home paths.Home, kind versions.Component, zipPath string, taken func(version string) bool) (versions.Version, error) {
	if !CanImport(kind) {
		return versions.Version{}, fmt.Errorf("%s no se puede instalar desde un .zip", kind.Name())
	}
	tmp := home.Bin(string(kind), fmt.Sprintf(".zip-%d", time.Now().UnixNano()))
	defer os.RemoveAll(tmp)
	if err := download.ExtractZip(zipPath, tmp, true); err != nil {
		return versions.Version{}, fmt.Errorf("el .zip no se pudo descomprimir: %w", err)
	}
	v, err := versions.Detect(kind, tmp)
	if err != nil {
		// The temporary folder means nothing to the user: the zip is what they chose.
		return versions.Version{}, fmt.Errorf("el .zip no contiene %s para Windows: %s", kind.Name(), strings.ReplaceAll(err.Error(), " en "+tmp, " en el .zip"))
	}
	if v.Arch == "x86" {
		return versions.Version{}, fmt.Errorf("%s es de 32 bits: Nido necesita la versión de 64 bits (x64)", v.Label())
	}
	if taken != nil && taken(v.Version) {
		return versions.Version{}, fmt.Errorf("%s ya está instalado", v.Label())
	}
	dest := home.Bin(string(kind), ZipFolder(v))
	if _, err := os.Stat(dest); err == nil {
		return versions.Version{}, fmt.Errorf("ya existe %s: desinstala esa versión antes de subirla otra vez", dest)
	}
	// The program may sit in a subfolder of the zip (Apache Lounge's Apache24\).
	inner, err := filepath.Rel(tmp, v.Path)
	if err != nil || strings.HasPrefix(inner, "..") {
		inner = "."
	}
	if err := renameRetry(tmp, dest); err != nil {
		return versions.Version{}, fmt.Errorf("no se pudo mover %s a %s: %w", tmp, dest, err)
	}
	v.Path = filepath.Clean(filepath.Join(dest, inner))
	v.Source = SourceZip
	return v, nil
}

// ZipFolder is the folder name of an imported version: PHP's say which build they are, as the
// downloaded ones do ("8.4.16-nts-x64").
func ZipFolder(v versions.Version) string {
	if v.Component != versions.PHP {
		return v.Version
	}
	flavor := "nts"
	if v.ThreadSafe != nil && *v.ThreadSafe {
		flavor = "ts"
	}
	return v.Version + "-" + flavor + "-x64"
}

// renameRetry moves a folder, retrying while an antivirus scanner holds a file freshly run inside.
func renameRetry(src, dst string) error {
	var err error
	for attempt := 0; attempt < 30; attempt++ {
		if err = os.Rename(src, dst); err == nil {
			return nil
		}
		time.Sleep(time.Duration(100+attempt*100) * time.Millisecond)
	}
	return err
}

// OwnedByNido reports whether a version lives in Nido's bin\ because Nido put it there (downloaded
// or from a .zip), so uninstalling it deletes its folder; the others are only forgotten.
func OwnedByNido(home paths.Home, v versions.Version) bool {
	if v.Source != "download" && v.Source != SourceZip {
		return false
	}
	bin := strings.ToLower(filepath.Clean(home.Bin())) + `\`
	return strings.HasPrefix(strings.ToLower(filepath.Clean(v.Path))+`\`, bin)
}
