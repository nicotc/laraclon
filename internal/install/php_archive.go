package install

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/nicotc/laraclon/internal/download"
	"github.com/nicotc/laraclon/internal/paths"
	"github.com/nicotc/laraclon/internal/versions"
)

// Branches windows.php.net no longer maintains (8.1, 8.0, 7.4… down to 5.x) keep every build in
// releases/archives. sha256sum.txt lists the last release of the recent ones (7.4, 8.0, 8.1) with
// its SHA-256; for older branches php.net publishes no sums, so the zip is checked to be whole and
// to hold the version expected, as with PostgreSQL's.
const phpArchives = phpReleases + "archives/"

// php-7.2.34-nts-Win32-VC15-x64.zip, php-8.1.34-nts-Win32-vs16-x64.zip
var archivedPHPRe = regexp.MustCompile(`php-(\d+\.\d+)\.(\d+)-nts-Win32-(?i:vc|vs)\d+-x64\.zip`)

// newestArchived picks the last 64-bit NTS release of a branch from the archives' listing.
func newestArchived(listing, branch string) (file, version string) {
	best := -1
	for _, m := range archivedPHPRe.FindAllStringSubmatch(listing, -1) {
		if m[1] != branch {
			continue
		}
		if p, err := strconv.Atoi(m[2]); err == nil && p > best {
			best, file, version = p, m[0], m[1]+"."+m[2]
		}
	}
	return file, version
}

// sumFor finds a file's SHA-256 in a sha256sum.txt ("<sum> *<file>" lines); "" when it is not there.
func sumFor(sums, file string) string {
	for _, line := range strings.Split(sums, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && len(fields[0]) == 64 && strings.TrimPrefix(fields[1], "*") == file {
			return fields[0]
		}
	}
	return ""
}

// installArchivedPHP downloads the last release of an old branch ("7.2") from the archives.
func installArchivedPHP(ctx context.Context, home paths.Home, branch string, progress download.Progress) (versions.Version, error) {
	listing, err := download.Text(ctx, phpArchives)
	if err != nil {
		return versions.Version{}, fmt.Errorf("no se pudo leer el archivo de versiones anteriores de PHP (windows.php.net): %w", err)
	}
	file, version := newestArchived(listing, branch)
	if file == "" {
		return versions.Version{}, fmt.Errorf("windows.php.net no tiene PHP %s para Windows de 64 bits", branch)
	}
	sum := ""
	if sums, err := download.Text(ctx, phpReleases+"sha256sum.txt"); err == nil {
		sum = sumFor(sums, file)
	}
	zipPath := home.Cache("downloads", file)
	if _, err := download.File(ctx, phpArchives+file, zipPath, sum, progress); err != nil {
		return versions.Version{}, err
	}
	defer os.Remove(zipPath)
	dest := home.Bin("php", version+"-nts-x64")
	if err := unpackFresh(zipPath, dest); err != nil { // a damaged zip fails its CRC here
		return versions.Version{}, err
	}
	v, err := versions.DetectPHP(dest)
	if err != nil {
		return versions.Version{}, err
	}
	if v.Version != version {
		os.RemoveAll(dest)
		return versions.Version{}, fmt.Errorf("el zip %s trae PHP %s y se esperaba %s", file, v.Version, version)
	}
	v.Source = "download"
	return v, nil
}
