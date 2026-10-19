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
	"github.com/nicotc/laraclon/internal/vcredist"
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
	// SumsURL is a SHASUMS-style listing the SHA-256 is read from at install time (Node).
	SumsURL string `json:"sums_url,omitempty"`
	Size    int64  `json:"size,omitempty"`
	Note    string `json:"note,omitempty"` // "LTS", "Innovation", "estable"…
	Source  string `json:"source"`
	// Extras are installed with the release into a subfolder (MongoDB's shell, for example).
	Extras []Extra `json:"extras,omitempty"`
	// Binary is set when the download is the program itself rather than a zip (Meilisearch).
	Binary string `json:"binary,omitempty"`
	// Skip lists folders of the zip clon does not need (PostgreSQL's pgAdmin, for example).
	Skip []string `json:"skip,omitempty"`
}

// Extra is a companion download of a release.
type Extra struct {
	Name   string `json:"name"`
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
	Dir    string `json:"dir"`
}

// Installable lists what the catalog can install.
var Installable = []versions.Component{versions.MySQL, versions.MariaDB, versions.PostgreSQL, versions.MongoDB,
	versions.Redis, versions.Mailpit, versions.Meilisearch, versions.Apache, versions.Nginx, versions.FrankenPHP,
	versions.Adminer}

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
	case versions.Apache:
		list, err = apacheCatalog(ctx)
	case versions.Nginx:
		list, err = nginxCatalog(ctx)
	case versions.FrankenPHP:
		list, err = frankenphpCatalog(ctx)
	case versions.MongoDB:
		list, err = mongodbCatalog(ctx)
	case versions.PostgreSQL:
		list, err = postgresCatalog(ctx)
	case versions.Redis:
		list, err = githubCatalog(ctx, redisReleasesURL, versions.Redis, "github.com/redis-windows/redis-windows", redisAsset)
		for i := range list {
			list[i].Note = "compilación comunitaria"
		}
	case versions.Mailpit:
		list, err = githubCatalog(ctx, mailpitReleasesURL, versions.Mailpit, "github.com/axllent/mailpit", tagAsset("mailpit-windows-amd64.zip"))
	case versions.Meilisearch:
		list, err = githubCatalog(ctx, meiliReleasesURL, versions.Meilisearch, "github.com/meilisearch/meilisearch", tagAsset("meilisearch-windows-amd64.exe"))
		for i := range list {
			list[i].Binary = "meilisearch.exe"
		}
	case versions.Adminer:
		list, err = githubCatalog(ctx, adminerReleasesURL, versions.Adminer, "github.com/vrana/adminer", adminerAsset)
		for i := range list {
			list[i].Binary = "adminer.php"
		}
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

// ---- Apache ----

// The Apache Software Foundation publishes no Windows builds; Apache Lounge's are the ones Windows
// stacks use (Laragon's among them). Its download page links the current build with a file of
// checksums next to the zip.
const apacheLoungeURL = "https://www.apachelounge.com/download/"

var (
	apacheZipRe = regexp.MustCompile(`(?i)href="(/download/(VS\d+)/binaries/httpd-(\d+\.\d+\.\d+)-\d+-win64-VS\d+\.zip)"`)
	apacheSumRe = regexp.MustCompile(`(?i)SHA256-Checksum for:[^\n]*\n\s*([0-9a-f]{64})`)
)

func apacheCatalog(ctx context.Context) ([]Release, error) {
	page, err := download.Text(ctx, apacheLoungeURL)
	if err != nil {
		return nil, fmt.Errorf("no se pudo leer la página de descargas de Apache Lounge: %w", err)
	}
	rel, ok := pickApache(page)
	if !ok {
		return nil, fmt.Errorf("la página de descargas de Apache Lounge no enlaza ningún Apache de 64 bits")
	}
	sums, err := download.Text(ctx, rel.URL+".txt")
	if err != nil {
		return nil, fmt.Errorf("no se pudieron leer las sumas de comprobación de Apache Lounge: %w", err)
	}
	m := apacheSumRe.FindStringSubmatch(sums)
	if m == nil {
		return nil, fmt.Errorf("%s.txt no trae el SHA-256 del zip", path.Base(rel.URL))
	}
	rel.SHA256 = strings.ToLower(m[1])
	if size, err := download.Head(ctx, rel.URL); err == nil {
		rel.Size = size
	}
	return []Release{rel}, nil
}

// pickApache takes the newest 64-bit build the download page links. The zip holds Apache24\ (the
// manual, 10 MB of HTML, is left out) next to a few text files.
func pickApache(page string) (Release, bool) {
	var best Release
	for _, m := range apacheZipRe.FindAllStringSubmatch(page, -1) {
		if best.Version == "" || versions.Compare(m[3], best.Version) > 0 {
			best = Release{Kind: versions.Apache, Series: versions.MajorMinor(m[3]), Version: m[3],
				URL: "https://www.apachelounge.com" + m[1], Note: "compilación " + strings.ToUpper(m[2]),
				Source: "apachelounge.com", Skip: []string{"Apache24/manual/"}}
		}
	}
	return best, best.Version != ""
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

var releaseTagRe = regexp.MustCompile(`^v?(\d+\.\d+\.\d+)$`)

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
		m := releaseTagRe.FindStringSubmatch(r.Tag)
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

// ---- MongoDB ----

// MongoDB lists every release with its files and SHA-256 in one JSON document; its shell (mongosh)
// is published in the releases of its own official repository.
const (
	mongoFullURL       = "https://downloads.mongodb.org/full.json"
	mongoshReleasesURL = "https://api.github.com/repos/mongodb-js/mongosh/releases?per_page=10"
)

type mongoFull struct {
	Versions []struct {
		Version    string `json:"version"`
		Production bool   `json:"production_release"`
		Downloads  []struct {
			Target  string `json:"target"`
			Arch    string `json:"arch"`
			Edition string `json:"edition"`
			Archive struct {
				URL    string `json:"url"`
				SHA256 string `json:"sha256"`
			} `json:"archive"`
		} `json:"downloads"`
	} `json:"versions"`
}

var plainVersionRe = regexp.MustCompile(`^\d+\.\d+\.\d+$`)

func mongodbCatalog(ctx context.Context) ([]Release, error) {
	var full mongoFull
	if err := download.JSON(ctx, mongoFullURL, &full); err != nil {
		return nil, fmt.Errorf("no se pudo leer la lista de versiones de MongoDB (downloads.mongodb.org): %w", err)
	}
	list := pickMongoDB(full)
	if len(list) == 0 {
		return nil, fmt.Errorf("downloads.mongodb.org no devolvió versiones para Windows")
	}
	var rels []githubRelease
	if err := download.JSON(ctx, mongoshReleasesURL, &rels); err == nil {
		if shell, ok := pickMongosh(rels); ok {
			for i := range list {
				list[i].Extras = []Extra{shell}
			}
		}
	}
	return list, nil
}

// pickMongoDB keeps the newest release of the three newest major series (9.0, 8.0…), plus the
// newest rapid release (8.3…) when it is newer than all of them.
func pickMongoDB(full mongoFull) []Release {
	newest := map[string]Release{}
	for _, v := range full.Versions {
		if !v.Production || !plainVersionRe.MatchString(v.Version) {
			continue
		}
		for _, dl := range v.Downloads {
			if dl.Target != "windows" || dl.Arch != "x86_64" || dl.Edition != "base" || dl.Archive.URL == "" || len(dl.Archive.SHA256) != 64 {
				continue
			}
			s := versions.MajorMinor(v.Version)
			if cur, seen := newest[s]; !seen || versions.Compare(v.Version, cur.Version) > 0 {
				newest[s] = Release{Kind: versions.MongoDB, Series: s, Version: v.Version, URL: dl.Archive.URL, SHA256: dl.Archive.SHA256, Source: "mongodb.org"}
			}
		}
	}
	var series []string
	for s := range newest {
		series = append(series, s)
	}
	sort.Slice(series, func(i, j int) bool { return versions.Compare(series[i], series[j]) > 0 })
	var out []Release
	majors := 0
	for _, s := range series {
		r := newest[s]
		isMajor := strings.HasSuffix(s, ".0")
		switch {
		case !isMajor && len(out) == 0:
			r.Note = "rapid release"
		case isMajor && majors < 3:
			r.Note = "versión mayor"
			majors++
		default:
			continue
		}
		out = append(out, r)
	}
	if len(out) > 0 && out[0].Note == "versión mayor" {
		out[0].Note = "la más reciente"
	}
	return out
}

func pickMongosh(rels []githubRelease) (Extra, bool) {
	for _, r := range rels {
		if r.Draft || r.Prerelease {
			continue
		}
		for _, a := range r.Assets {
			sum, ok := strings.CutPrefix(a.Digest, "sha256:")
			if ok && len(sum) == 64 && strings.HasSuffix(a.Name, "-win32-x64.zip") {
				return Extra{Name: "mongosh", URL: a.URL, SHA256: sum, Dir: "mongosh"}, true
			}
		}
	}
	return Extra{}, false
}

// ---- Releases on GitHub (Redis, Mailpit, Meilisearch, Adminer) ----

const (
	// Redis publishes no Windows build; this community project builds every release with MSYS2.
	redisReleasesURL   = "https://api.github.com/repos/redis-windows/redis-windows/releases?per_page=20"
	mailpitReleasesURL = "https://api.github.com/repos/axllent/mailpit/releases?per_page=20"
	meiliReleasesURL   = "https://api.github.com/repos/meilisearch/meilisearch/releases?per_page=30"
	// Adminer is one PHP file; the release has it with every database driver and language.
	adminerReleasesURL = "https://api.github.com/repos/vrana/adminer/releases?per_page=10"
)

// adminerAsset matches "adminer-6.1.2.php" in the release tagged v6.1.2.
func adminerAsset(tag, asset string) (string, bool) {
	m := releaseTagRe.FindStringSubmatch(tag)
	if m == nil || asset != "adminer-"+m[1]+".php" {
		return "", false
	}
	return m[1], true
}

var redisAssetRe = regexp.MustCompile(`^Redis-(\d+\.\d+\.\d+)-Windows-x64-msys2\.zip$`)

func redisAsset(_, asset string) (string, bool) {
	if m := redisAssetRe.FindStringSubmatch(asset); m != nil {
		return m[1], true
	}
	return "", false
}

// tagAsset matches one asset name and takes the version from the release tag ("v1.31.6").
func tagAsset(name string) func(tag, asset string) (string, bool) {
	return func(tag, asset string) (string, bool) {
		m := releaseTagRe.FindStringSubmatch(tag)
		if m == nil || asset != name {
			return "", false
		}
		return m[1], true
	}
}

func githubCatalog(ctx context.Context, url string, kind versions.Component, source string, match func(tag, asset string) (string, bool)) ([]Release, error) {
	var rels []githubRelease
	if err := download.JSON(ctx, url, &rels); err != nil {
		return nil, fmt.Errorf("no se pudo leer la lista de versiones de %s (%s): %w", kind.Name(), source, err)
	}
	list := pickGitHub(rels, kind, source, match)
	if len(list) == 0 {
		return nil, fmt.Errorf("%s no devolvió versiones para Windows", source)
	}
	return list, nil
}

// pickGitHub keeps the newest release of the two newest series that have a matching asset with its
// SHA-256.
func pickGitHub(rels []githubRelease, kind versions.Component, source string, match func(tag, asset string) (string, bool)) []Release {
	newest := map[string]Release{}
	for _, r := range rels {
		if r.Draft || r.Prerelease {
			continue
		}
		for _, a := range r.Assets {
			v, ok := match(r.Tag, a.Name)
			sum, hasSum := strings.CutPrefix(a.Digest, "sha256:")
			if !ok || !hasSum || len(sum) != 64 {
				continue
			}
			s := versions.Series(kind, v)
			if cur, seen := newest[s]; !seen || versions.Compare(v, cur.Version) > 0 {
				newest[s] = Release{Kind: kind, Series: s, Version: v, URL: a.URL, SHA256: sum, Size: a.Size, Source: source}
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

// ---- PostgreSQL ----

// postgresql.org lists every major version with its newest release and end of life; the Windows
// binaries are the zips EnterpriseDB builds (linked from postgresql.org); EnterpriseDB publishes no
// checksum for them.
const pgVersionsURL = "https://www.postgresql.org/versions.json"

// pgVersion is one entry of versions.json, where numbers come either as numbers or as text.
type pgVersion struct {
	Major       json.Number `json:"major"`
	LatestMinor json.Number `json:"latestMinor"`
	Supported   bool        `json:"supported"`
	EOL         string      `json:"eolDate"`
}

func postgresCatalog(ctx context.Context) ([]Release, error) {
	var all []pgVersion
	if err := download.JSON(ctx, pgVersionsURL, &all); err != nil {
		return nil, fmt.Errorf("no se pudo leer la lista de versiones de PostgreSQL (postgresql.org): %w", err)
	}
	majors := supportedPostgres(all, time.Now())
	out := make([]Release, len(majors))
	found := make([]bool, len(majors))
	var wg sync.WaitGroup
	for i, m := range majors {
		wg.Add(1)
		go func(i int, m pgVersion) {
			defer wg.Done()
			v := m.Major.String() + "." + m.LatestMinor.String()
			for build := 3; build >= 1; build-- {
				url := fmt.Sprintf("https://get.enterprisedb.com/postgresql/postgresql-%s-%d-windows-x64-binaries.zip", v, build)
				if size, err := download.Head(ctx, url); err == nil {
					note := "soporte hasta " + m.EOL
					if len(m.EOL) >= 7 {
						note = "soporte hasta " + m.EOL[:7]
					}
					out[i] = Release{Kind: versions.PostgreSQL, Series: m.Major.String(), Version: v, URL: url, Size: size, Note: note,
						Source: "get.enterprisedb.com (enlazado desde postgresql.org)", Skip: []string{"pgAdmin 4/", "StackBuilder/", "doc/", "symbols/"}}
					found[i] = true
					return
				}
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
		return nil, fmt.Errorf("no se encontraron los binarios de PostgreSQL para Windows en get.enterprisedb.com")
	}
	list[0].Note = "la más reciente · " + list[0].Note
	return list, nil
}

// supportedPostgres keeps the four newest supported major versions.
func supportedPostgres(all []pgVersion, now time.Time) []pgVersion {
	today := now.Format("2006-01-02")
	var out []pgVersion
	for _, m := range all {
		if m.Supported && !strings.Contains(m.Major.String(), ".") && (m.EOL == "" || m.EOL >= today) {
			out = append(out, m)
		}
	}
	sort.Slice(out, func(i, j int) bool { return versions.Compare(out[i].Major.String(), out[j].Major.String()) > 0 })
	if len(out) > 4 {
		out = out[:4]
	}
	return out
}

// ---- Install ----

// signers are the vendors whose signature a program must carry when no checksum is published.
// PostgreSQL has neither: EnterpriseDB signs its installer but not the programs in the zip, so that
// zip is only checked for integrity (each entry's CRC) and for the version it contains.
var signers = map[versions.Component]struct{ file, org string }{
	versions.MySQL: {filepath.Join("bin", "mysqld.exe"), "Oracle America, Inc."},
}

// InstallRelease downloads a release, checks it and unpacks it into bin\<kind>\<version>. Downloads
// must match the published SHA-256 (MariaDB, MongoDB, GitHub); MySQL's server must carry Oracle's
// signature. phase receives a short description of each step.
func InstallRelease(ctx context.Context, home paths.Home, rel Release, phase func(string), progress download.Progress) (versions.Version, error) {
	say := func(s string) {
		if phase != nil {
			phase(s)
		}
	}
	dest := home.Bin(string(rel.Kind), rel.Version)
	fail := func(err error) (versions.Version, error) {
		os.RemoveAll(dest)
		return versions.Version{}, err
	}
	say("descargando")
	if rel.SHA256 == "" && rel.SumsURL != "" {
		sums, err := download.Text(ctx, rel.SumsURL)
		if err != nil {
			return versions.Version{}, err
		}
		if rel.SHA256 = checksumFor(sums, path.Base(rel.URL)); rel.SHA256 == "" {
			return versions.Version{}, fmt.Errorf("%s no incluye %s", path.Base(rel.SumsURL), path.Base(rel.URL))
		}
	}
	if rel.Binary != "" {
		os.RemoveAll(dest)
		if err := os.MkdirAll(dest, 0o755); err != nil {
			return versions.Version{}, err
		}
		if _, err := download.File(ctx, rel.URL, filepath.Join(dest, rel.Binary), rel.SHA256, progress); err != nil {
			return fail(err)
		}
	} else {
		zipPath := home.Cache("downloads", path.Base(rel.URL))
		if _, err := download.File(ctx, rel.URL, zipPath, rel.SHA256, progress); err != nil {
			return versions.Version{}, err
		}
		defer os.Remove(zipPath) // hundreds of MB that nothing reuses
		say("descomprimiendo")
		if err := unpackFreshSkip(zipPath, dest, rel.Skip); err != nil {
			return fail(err)
		}
	}
	if s, ok := signers[rel.Kind]; ok && rel.SHA256 == "" {
		say("comprobando la firma de " + s.org)
		if err := winx.VerifySignature(filepath.Join(dest, s.file), s.org); err != nil {
			return fail(err)
		}
	}
	say("comprobando la instalación")
	v, err := versions.Detect(rel.Kind, dest)
	if err != nil {
		// A program built with a newer Visual Studio than the runtime Windows has may not start.
		if need := vcredist.Needs(versions.Program(rel.Kind, dest)); need != "" && vcredist.Older(need) {
			have := vcredist.Version()
			if have == "" {
				have = "ninguno"
			}
			err = fmt.Errorf("%w. %s %s se compiló para el runtime de Visual C++ %s o más nuevo y este equipo tiene %s: actualízalo con 'clon vcredist install' y vuelve a intentarlo", err, rel.Kind.Name(), rel.Version, need, have)
		}
		return fail(err)
	}
	if v.Component != rel.Kind || v.Version != rel.Version {
		return fail(fmt.Errorf("el zip contiene %s y se esperaba %s %s", v.Label(), rel.Kind.Name(), rel.Version))
	}
	for _, x := range rel.Extras {
		say("descargando " + x.Name)
		zip := home.Cache("downloads", path.Base(x.URL))
		if _, err := download.File(ctx, x.URL, zip, x.SHA256, progress); err != nil {
			return fail(fmt.Errorf("%s: %w", x.Name, err))
		}
		err := unpackFresh(zip, filepath.Join(dest, x.Dir))
		os.Remove(zip)
		if err != nil {
			return fail(fmt.Errorf("%s: %w", x.Name, err))
		}
	}
	v.Source = "download"
	return v, nil
}
