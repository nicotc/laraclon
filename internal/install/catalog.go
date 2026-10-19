package install

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/nicotc/laraclon/internal/config"
	"github.com/nicotc/laraclon/internal/download"
	"github.com/nicotc/laraclon/internal/paths"
	"github.com/nicotc/laraclon/internal/versions"
	"github.com/nicotc/laraclon/internal/winx"
)

// Release is a version that can be installed from the catalog (official 64-bit zip for Windows).
type Release struct {
	Kind    versions.Component `json:"kind"`
	Series  string             `json:"series"`
	Version string             `json:"version"`
	URL     string             `json:"url"`
	SHA256  string             `json:"sha256,omitempty"` // published by MariaDB and GitHub; MySQL is checked by Oracle's signature
	Size    int64              `json:"size,omitempty"`
	Note    string             `json:"note,omitempty"` // "LTS", "Innovation", "estable"…
	Source  string             `json:"source"`
}

// Installable lists what the catalog can install.
var Installable = []versions.Component{versions.MySQL, versions.MariaDB, versions.Nginx, versions.FrankenPHP}

const catalogTTL = 6 * time.Hour

type cachedCatalog struct {
	Fetched  time.Time `json:"fetched"`
	Releases []Release `json:"releases"`
}

// Catalog lists the versions of a component that can be installed, newest series first. The list is
// kept in state\catalog-<kind>.json for a few hours; refresh asks the official sources again.
func Catalog(ctx context.Context, home paths.Home, kind versions.Component, refresh bool) ([]Release, error) {
	file := home.State("catalog-" + string(kind) + ".json")
	var cached cachedCatalog
	if data, err := os.ReadFile(file); err == nil && json.Unmarshal(data, &cached) == nil {
		if !refresh && time.Since(cached.Fetched) < catalogTTL && len(cached.Releases) > 0 {
			return cached.Releases, nil
		}
	}
	var list []Release
	var err error
	switch kind {
	case versions.MySQL:
		list, err = mysqlCatalog(ctx)
	case versions.MariaDB:
		list, err = mariadbCatalog(ctx)
	case versions.Nginx:
		list, err = nginxCatalog(ctx)
	case versions.FrankenPHP:
		list, err = frankenphpCatalog(ctx)
	default:
		return nil, fmt.Errorf("%s no se puede instalar desde el catálogo", kind.Name())
	}
	if err != nil {
		if len(cached.Releases) > 0 {
			return cached.Releases, nil // an older list beats none while offline
		}
		return nil, err
	}
	if data, err := json.MarshalIndent(cachedCatalog{Fetched: time.Now(), Releases: list}, "", "  "); err == nil {
		config.WriteFileAtomic(file, data)
	}
	return list, nil
}

// FindRelease picks the catalog entry of a series ("8.4"), an exact version ("8.4.11") or, with
// "latest", the first one (the newest series).
func FindRelease(list []Release, want string) (Release, error) {
	want = strings.TrimSpace(want)
	if (want == "latest" || want == "") && len(list) > 0 {
		return list[0], nil
	}
	for _, r := range list {
		if r.Series == want || r.Version == want {
			return r, nil
		}
	}
	var names []string
	for _, r := range list {
		names = append(names, r.Series)
	}
	if len(list) == 0 {
		return Release{}, fmt.Errorf("el catálogo está vacío")
	}
	return Release{}, fmt.Errorf("%s %s no está en el catálogo; disponibles: %s (otras versiones se importan desde una carpeta)", list[0].Kind.Name(), want, strings.Join(names, ", "))
}

// ---- MySQL ----

// MySQL has no machine-readable list of Windows builds (dev.mysql.com refuses scripts), so the
// versions come from the release tags of Oracle's official repository and each zip is confirmed on
// cdn.mysql.com: the newest release of a series sits in Downloads/, the older ones in archives/.
const mysqlTagsURL = "https://api.github.com/repos/mysql/mysql-server/git/matching-refs/tags/mysql-"

var mysqlTagRe = regexp.MustCompile(`^refs/tags/mysql-(\d+)\.(\d+)\.(\d+)$`)

// mysqlKnown is used when GitHub cannot be reached (newest releases as of October 2026).
var mysqlKnown = []string{"26.7.0", "9.7.2", "8.4.11", "8.0.46", "5.7.44"}

// mysqlNotes describes the support of each series (MySQL 9.7 became the second LTS in April 2026;
// 8.0 reached its end of life in April 2026 and 5.7 in October 2023).
var mysqlNotes = map[string]string{"9.7": "LTS", "8.4": "LTS", "8.0": "sin soporte", "5.7": "sin soporte"}

func mysqlCatalog(ctx context.Context) ([]Release, error) {
	bySeries := map[string][]string{}
	var refs []struct {
		Ref string `json:"ref"`
	}
	tags := mysqlKnown
	if err := download.JSON(ctx, mysqlTagsURL, &refs); err == nil && len(refs) > 0 {
		tags = nil
		for _, r := range refs {
			if m := mysqlTagRe.FindStringSubmatch(r.Ref); m != nil {
				tags = append(tags, m[1]+"."+m[2]+"."+m[3])
			}
		}
	}
	for _, v := range tags {
		s := versions.MajorMinor(v)
		bySeries[s] = append(bySeries[s], v)
	}
	series := pickMySQLSeries(bySeries)

	out := make([]Release, len(series))
	found := make([]bool, len(series))
	var wg sync.WaitGroup
	for i, s := range series {
		wg.Add(1)
		go func(i int, s string) {
			defer wg.Done()
			list := bySeries[s]
			sort.Slice(list, func(a, b int) bool { return versions.Compare(list[a], list[b]) > 0 })
			// A release can be tagged before its Windows build is published: try the previous ones too.
			for n, v := range list {
				if n == 3 {
					break
				}
				if url, size, err := mysqlZip(ctx, s, v); err == nil {
					out[i] = Release{Kind: versions.MySQL, Series: s, Version: v, URL: url, Size: size, Note: mysqlNotes[s], Source: "cdn.mysql.com"}
					found[i] = true
					return
				}
			}
		}(i, s)
	}
	wg.Wait()
	var list []Release
	for i := range out {
		if found[i] {
			list = append(list, out[i])
		}
	}
	if len(list) == 0 {
		return nil, fmt.Errorf("no se pudo consultar cdn.mysql.com")
	}
	if list[0].Note == "" {
		list[0].Note = "Innovation"
	}
	return list, nil
}

// pickMySQLSeries keeps the newest series of each major version plus 8.0, which many projects
// still use. Short-lived innovation series and anything older than 5.7 are left out (old versions
// can still be imported from a folder).
func pickMySQLSeries(bySeries map[string][]string) []string {
	newestOfMajor := map[string]string{}
	for s := range bySeries {
		major, _, _ := strings.Cut(s, ".")
		if cur, ok := newestOfMajor[major]; !ok || versions.Compare(s, cur) > 0 {
			newestOfMajor[major] = s
		}
	}
	keep := map[string]bool{}
	for _, s := range newestOfMajor {
		keep[s] = true
	}
	if _, ok := bySeries["8.0"]; ok {
		keep["8.0"] = true
	}
	var out []string
	for s := range keep {
		if versions.Compare(s, "5.7") >= 0 {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return versions.Compare(out[i], out[j]) > 0 })
	return out
}

func mysqlZip(ctx context.Context, series, version string) (string, int64, error) {
	name := fmt.Sprintf("mysql-%s-winx64.zip", version)
	for _, u := range []string{
		fmt.Sprintf("https://cdn.mysql.com/Downloads/MySQL-%s/%s", series, name),
		fmt.Sprintf("https://cdn.mysql.com/archives/mysql-%s/%s", series, name),
	} {
		if size, err := download.Head(ctx, u); err == nil {
			return u, size, nil
		}
	}
	return "", 0, fmt.Errorf("cdn.mysql.com no tiene %s", name)
}

// ---- MariaDB ----

// The MariaDB Foundation publishes its releases, files and SHA-256 checksums in a REST API.
const mariadbAPI = "https://downloads.mariadb.org/rest-api/mariadb/"

type mariadbMajor struct {
	ID      string `json:"release_id"`
	Status  string `json:"release_status"`
	Support string `json:"release_support_type"`
	EOL     string `json:"release_eol_date"`
}

type mariadbFile struct {
	Name     string `json:"file_name"`
	Checksum struct {
		SHA256 string `json:"sha256sum"`
	} `json:"checksum"`
	URL string `json:"file_download_url"`
}

type mariadbReleases struct {
	Releases map[string]struct {
		ID    string        `json:"release_id"`
		Files []mariadbFile `json:"files"`
	} `json:"releases"`
}

var mariadbZipRe = regexp.MustCompile(`^mariadb-(\d+\.\d+\.\d+)-winx64\.zip$`)

func mariadbCatalog(ctx context.Context) ([]Release, error) {
	var root struct {
		Major []mariadbMajor `json:"major_releases"`
	}
	if err := download.JSON(ctx, mariadbAPI, &root); err != nil {
		return nil, fmt.Errorf("no se pudo leer la lista de versiones de MariaDB (downloads.mariadb.org): %w", err)
	}
	majors := supportedMariaDB(root.Major, time.Now())
	out := make([]Release, len(majors))
	found := make([]bool, len(majors))
	var wg sync.WaitGroup
	for i, m := range majors {
		wg.Add(1)
		go func(i int, m mariadbMajor) {
			defer wg.Done()
			var rel mariadbReleases
			if err := download.JSON(ctx, mariadbAPI+m.ID+"/latest/", &rel); err != nil {
				return
			}
			if r, ok := mariadbWindowsZip(m, rel); ok {
				out[i], found[i] = r, true
			}
		}(i, m)
	}
	wg.Wait()
	var list []Release
	for i := range out {
		if found[i] {
			list = append(list, out[i])
		}
	}
	if len(list) == 0 {
		return nil, fmt.Errorf("downloads.mariadb.org no devolvió versiones para Windows")
	}
	return list, nil
}

// supportedMariaDB keeps the stable series that are still supported, newest first.
func supportedMariaDB(all []mariadbMajor, now time.Time) []mariadbMajor {
	today := now.Format("2006-01-02")
	var out []mariadbMajor
	for _, m := range all {
		if m.Status == "Stable" && (m.EOL == "" || m.EOL >= today) {
			out = append(out, m)
		}
	}
	sort.Slice(out, func(i, j int) bool { return versions.Compare(out[i].ID, out[j].ID) > 0 })
	return out
}

func mariadbWindowsZip(m mariadbMajor, rel mariadbReleases) (Release, bool) {
	for _, r := range rel.Releases {
		for _, f := range r.Files {
			mm := mariadbZipRe.FindStringSubmatch(f.Name)
			if mm == nil || f.Checksum.SHA256 == "" {
				continue
			}
			note := "estable"
			if strings.Contains(strings.ToLower(m.Support), "long term") {
				note = "LTS"
				if len(m.EOL) >= 7 {
					note += " hasta " + m.EOL[:7]
				}
			}
			// The API lists http:// links; the same path is served over HTTPS.
			url := strings.Replace(f.URL, "http://", "https://", 1)
			return Release{Kind: versions.MariaDB, Series: m.ID, Version: mm[1], URL: url, SHA256: f.Checksum.SHA256, Note: note, Source: "mariadb.org"}, true
		}
	}
	return Release{}, false
}

// ---- Nginx ----

// nginx.org signs its Windows zips with PGP; the same files are published in the releases of the
// official repository on GitHub, whose API also gives each file's SHA-256.
const nginxReleasesURL = "https://api.github.com/repos/nginx/nginx/releases?per_page=100"

type githubRelease struct {
	Tag        string `json:"tag_name"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
	Assets     []struct {
		Name   string `json:"name"`
		URL    string `json:"browser_download_url"`
		Digest string `json:"digest"` // "sha256:…"
		Size   int64  `json:"size"`
	} `json:"assets"`
}

var nginxZipRe = regexp.MustCompile(`^nginx-(\d+\.\d+\.\d+)\.zip$`)

func nginxCatalog(ctx context.Context) ([]Release, error) {
	var rels []githubRelease
	if err := download.JSON(ctx, nginxReleasesURL, &rels); err != nil {
		return nil, fmt.Errorf("no se pudo leer la lista de versiones de Nginx (github.com/nginx/nginx): %w", err)
	}
	list := pickNginx(rels)
	if len(list) == 0 {
		return nil, fmt.Errorf("github.com/nginx/nginx no devolvió versiones para Windows")
	}
	return list, nil
}

// pickNginx keeps the newest mainline release (odd minor version), the newest stable one (even)
// and the previous stable series.
func pickNginx(rels []githubRelease) []Release {
	newest := map[string]Release{}
	for _, r := range rels {
		if r.Draft || r.Prerelease {
			continue
		}
		for _, a := range r.Assets {
			m := nginxZipRe.FindStringSubmatch(a.Name)
			sum, ok := strings.CutPrefix(a.Digest, "sha256:")
			if m == nil || !ok || len(sum) != 64 {
				continue
			}
			s := versions.MajorMinor(m[1])
			if cur, seen := newest[s]; !seen || versions.Compare(m[1], cur.Version) > 0 {
				newest[s] = Release{Kind: versions.Nginx, Series: s, Version: m[1], URL: a.URL, SHA256: sum, Size: a.Size, Source: "github.com/nginx/nginx"}
			}
		}
	}
	var series []string
	for s := range newest {
		series = append(series, s)
	}
	sort.Slice(series, func(i, j int) bool { return versions.Compare(series[i], series[j]) > 0 })
	var out []Release
	stable := 0
	for _, s := range series {
		minor := 0
		fmt.Sscanf(strings.SplitN(s, ".", 2)[1], "%d", &minor)
		r := newest[s]
		switch {
		case minor%2 == 1 && len(out) == 0:
			r.Note = "mainline"
		case minor%2 == 0 && stable == 0:
			r.Note, stable = "estable", 1
		case minor%2 == 0 && stable == 1:
			r.Note, stable = "estable anterior", 2
		default:
			continue
		}
		out = append(out, r)
	}
	return out
}

// ---- FrankenPHP ----

// FrankenPHP publishes a Windows build (with its own PHP) in the releases of its official repository.
const frankenphpReleasesURL = "https://api.github.com/repos/php/frankenphp/releases?per_page=30"

const frankenphpAsset = "frankenphp-windows-x86_64.zip"

var frankenTagRe = regexp.MustCompile(`^v?(\d+\.\d+\.\d+)$`)

func frankenphpCatalog(ctx context.Context) ([]Release, error) {
	var rels []githubRelease
	if err := download.JSON(ctx, frankenphpReleasesURL, &rels); err != nil {
		return nil, fmt.Errorf("no se pudo leer la lista de versiones de FrankenPHP (github.com/php/frankenphp): %w", err)
	}
	list := pickFrankenPHP(rels)
	if len(list) == 0 {
		return nil, fmt.Errorf("github.com/php/frankenphp no devolvió versiones para Windows")
	}
	return list, nil
}

// pickFrankenPHP keeps the newest release and the newest one of the previous series.
func pickFrankenPHP(rels []githubRelease) []Release {
	newest := map[string]Release{}
	for _, r := range rels {
		m := frankenTagRe.FindStringSubmatch(r.Tag)
		if r.Draft || r.Prerelease || m == nil {
			continue
		}
		for _, a := range r.Assets {
			sum, ok := strings.CutPrefix(a.Digest, "sha256:")
			if a.Name != frankenphpAsset || !ok || len(sum) != 64 {
				continue
			}
			s := versions.MajorMinor(m[1])
			if cur, seen := newest[s]; !seen || versions.Compare(m[1], cur.Version) > 0 {
				newest[s] = Release{Kind: versions.FrankenPHP, Series: s, Version: m[1], URL: a.URL, SHA256: sum, Size: a.Size, Source: "github.com/php/frankenphp"}
			}
		}
	}
	var series []string
	for s := range newest {
		series = append(series, s)
	}
	sort.Slice(series, func(i, j int) bool { return versions.Compare(series[i], series[j]) > 0 })
	var out []Release
	for i, s := range series {
		if i == 2 {
			break
		}
		r := newest[s]
		r.Note = map[int]string{0: "la más reciente", 1: "serie anterior"}[i]
		out = append(out, r)
	}
	return out
}

// ---- Install ----

// InstallRelease downloads a release, checks it and unpacks it into bin\<kind>\<version>. Zips must
// match the published SHA-256 (MariaDB, GitHub); MySQL's server must carry Oracle's signature.
// phase receives a short description of each step.
func InstallRelease(ctx context.Context, home paths.Home, rel Release, phase func(string), progress download.Progress) (versions.Version, error) {
	say := func(s string) {
		if phase != nil {
			phase(s)
		}
	}
	zipPath := home.Cache("downloads", path.Base(rel.URL))
	say("descargando")
	if _, err := download.File(ctx, rel.URL, zipPath, rel.SHA256, progress); err != nil {
		return versions.Version{}, err
	}
	defer os.Remove(zipPath) // hundreds of MB that nothing reuses
	dest := home.Bin(string(rel.Kind), rel.Version)
	say("descomprimiendo")
	if err := unpackFresh(zipPath, dest); err != nil {
		os.RemoveAll(dest)
		return versions.Version{}, err
	}
	fail := func(err error) (versions.Version, error) {
		os.RemoveAll(dest)
		return versions.Version{}, err
	}
	if rel.Kind == versions.MySQL {
		say("comprobando la firma de Oracle")
		if err := winx.VerifySignature(filepath.Join(dest, "bin", "mysqld.exe"), "Oracle America, Inc."); err != nil {
			return fail(err)
		}
	}
	say("comprobando la instalación")
	v, err := versions.Detect(rel.Kind, dest)
	if err != nil {
		return fail(err)
	}
	if v.Component != rel.Kind || v.Version != rel.Version {
		return fail(fmt.Errorf("el zip contiene %s y se esperaba %s %s", v.Label(), rel.Kind.Name(), rel.Version))
	}
	v.Source = "download"
	return v, nil
}
