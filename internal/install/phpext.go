package install

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/nicotc/laraclon/internal/config"
	"github.com/nicotc/laraclon/internal/download"
	"github.com/nicotc/laraclon/internal/paths"
	"github.com/nicotc/laraclon/internal/versions"
)

// phpExtRepos are the PHP extensions clon can download. Their official repositories publish Windows
// builds in each release, named as PIE (PHP's extension installer) expects:
// php_<ext>-<version>-<php series>-<ts|nts>-<compiler>-x86_64.zip, with the SHA-256 GitHub reports.
// phpredis and APCu publish none, so Laravel uses predis and the file cache instead.
var phpExtRepos = map[string]string{
	"xdebug":  "xdebug/xdebug",
	"mongodb": "mongodb/mongo-php-driver",
}

// DownloadableExtensions lists the extensions clon can download, sorted.
func DownloadableExtensions() []string {
	var out []string
	for name := range phpExtRepos {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// PHPExtension is one downloadable build of an extension.
type PHPExtension struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	URL     string `json:"url"`
	SHA256  string `json:"sha256"`
	Size    int64  `json:"size"`
}

type cachedReleases struct {
	Fetched  time.Time       `json:"fetched"`
	Releases []githubRelease `json:"releases"`
}

// FindPHPExtension picks the newest stable build of an extension for a PHP series ("8.4") and
// thread safety. The list of releases is kept in state\catalog-ext-<name>.json for a few hours.
func FindPHPExtension(ctx context.Context, home paths.Home, name, series string, ts bool) (PHPExtension, error) {
	repo, ok := phpExtRepos[name]
	if !ok {
		return PHPExtension{}, fmt.Errorf("clon no sabe descargar la extensión %s (puede: %s)", name, strings.Join(DownloadableExtensions(), ", "))
	}
	file := home.State("catalog-ext-" + name + ".json")
	var cached cachedReleases
	if data, err := os.ReadFile(file); err == nil {
		json.Unmarshal(data, &cached)
	}
	rels := cached.Releases
	if time.Since(cached.Fetched) > catalogTTL || len(rels) == 0 {
		var fresh []githubRelease
		if err := download.JSON(ctx, "https://api.github.com/repos/"+repo+"/releases?per_page=20", &fresh); err == nil {
			rels = fresh
			if data, err := json.Marshal(cachedReleases{Fetched: time.Now(), Releases: fresh}); err == nil {
				config.WriteFileAtomic(file, data)
			}
		} else if len(rels) == 0 {
			return PHPExtension{}, fmt.Errorf("no se pudo leer las versiones de %s (github.com/%s): %w", name, repo, err)
		}
	}
	ext, ok := pickPHPExtension(rels, name, series, ts)
	if !ok {
		flavor := "NTS"
		if ts {
			flavor = "TS"
		}
		return PHPExtension{}, fmt.Errorf("github.com/%s no publica %s para PHP %s %s de 64 bits", repo, name, series, flavor)
	}
	return ext, nil
}

var extTagRe = regexp.MustCompile(`^v?\d+\.\d+\.\d+$`)

// pickPHPExtension takes the newest stable release with a build for the PHP series and flavour.
func pickPHPExtension(rels []githubRelease, name, series string, ts bool) (PHPExtension, bool) {
	flavor := "nts"
	if ts {
		flavor = "ts"
	}
	re := regexp.MustCompile(`^php_` + regexp.QuoteMeta(name) + `-(\d+\.\d+\.\d+)-` + regexp.QuoteMeta(series) + `-` + flavor + `-(?:vs|vc)\d+-x86_64\.zip$`)
	var best PHPExtension
	for _, r := range rels {
		if r.Draft || r.Prerelease || !extTagRe.MatchString(r.Tag) {
			continue
		}
		for _, a := range r.Assets {
			m := re.FindStringSubmatch(a.Name)
			sum, ok := strings.CutPrefix(a.Digest, "sha256:")
			if m == nil || !ok || len(sum) != 64 {
				continue
			}
			if best.Version == "" || versions.Compare(m[1], best.Version) > 0 {
				best = PHPExtension{Name: name, Version: m[1], URL: a.URL, SHA256: sum, Size: a.Size}
			}
		}
	}
	return best, best.Version != ""
}

// InstallPHPExtension downloads a build, checks its SHA-256 and leaves its DLL in dir as
// php_<name>.dll, the name PHP and clon's php.ini expect.
func InstallPHPExtension(ctx context.Context, home paths.Home, ext PHPExtension, dir string) (string, error) {
	zipPath := home.Cache("downloads", path.Base(ext.URL))
	if _, err := download.File(ctx, ext.URL, zipPath, ext.SHA256, nil); err != nil {
		return "", err
	}
	defer os.Remove(zipPath)
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return "", err
	}
	defer r.Close()
	var dll *zip.File
	for _, f := range r.File {
		base := strings.ToLower(path.Base(strings.ReplaceAll(f.Name, `\`, "/")))
		if strings.HasPrefix(base, "php_"+ext.Name) && strings.HasSuffix(base, ".dll") {
			dll = f
		}
	}
	if dll == nil {
		return "", fmt.Errorf("%s no trae php_%s*.dll", path.Base(ext.URL), ext.Name)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	dest := filepath.Join(dir, "php_"+ext.Name+".dll")
	in, err := dll.Open()
	if err != nil {
		return "", err
	}
	defer in.Close()
	tmp := dest + ".tmp"
	out, err := os.Create(tmp)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return "", err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return "", err
	}
	// A running php-cgi may hold the previous DLL; then the new one waits for the next start.
	if err := os.Rename(tmp, dest); err != nil {
		os.Remove(tmp)
		return "", fmt.Errorf("no se pudo reemplazar %s (¿PHP la tiene abierta?): %w", dest, err)
	}
	return dest, nil
}
