package install

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/nicotc/laraclon/internal/download"
	"github.com/nicotc/laraclon/internal/paths"
	"github.com/nicotc/laraclon/internal/versions"
)

const phpReleases = "https://windows.php.net/downloads/releases/"

type phpZip struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Size   string `json:"size"`
}

type phpBuild struct {
	Zip *phpZip `json:"zip"`
}

// PHPBranch is one maintained branch ("8.4") from releases.json.
type PHPBranch struct {
	Branch  string
	Version string
	Builds  map[string]phpBuild // "nts-vs17-x64" → zip
}

// PHPBranches lists the branches currently published by windows.php.net, newest first.
func PHPBranches(ctx context.Context) ([]PHPBranch, error) {
	var raw map[string]map[string]any
	if err := download.JSON(ctx, phpReleases+"releases.json", &raw); err != nil {
		return nil, fmt.Errorf("no se pudo leer la lista de versiones de PHP: %w", err)
	}
	var out []PHPBranch
	for branch, entry := range raw {
		b := PHPBranch{Branch: branch, Builds: map[string]phpBuild{}}
		if v, ok := entry["version"].(string); ok {
			b.Version = v
		}
		for key, val := range entry {
			m, ok := val.(map[string]any)
			if !ok || !(strings.HasPrefix(key, "nts-") || strings.HasPrefix(key, "ts-")) {
				continue
			}
			z, ok := m["zip"].(map[string]any)
			if !ok {
				continue
			}
			pz := &phpZip{}
			pz.Path, _ = z["path"].(string)
			pz.SHA256, _ = z["sha256"].(string)
			pz.Size, _ = z["size"].(string)
			b.Builds[key] = phpBuild{Zip: pz}
		}
		out = append(out, b)
	}
	sort.Slice(out, func(i, j int) bool { return versions.Compare(out[i].Branch, out[j].Branch) > 0 })
	return out, nil
}

// pickBuild returns the 64-bit NTS build (FastCGI does not need thread safety).
func (b PHPBranch) pickBuild() (string, *phpZip, error) {
	for key, build := range b.Builds {
		if strings.HasPrefix(key, "nts-") && strings.HasSuffix(key, "-x64") && build.Zip != nil {
			return key, build.Zip, nil
		}
	}
	return "", nil, fmt.Errorf("PHP %s no tiene build NTS de 64 bits", b.Branch)
}

// InstallPHP downloads the newest release of a branch ("8.4") into bin\php\<version>-nts-x64.
func InstallPHP(ctx context.Context, home paths.Home, want string, progress download.Progress) (versions.Version, error) {
	branches, err := PHPBranches(ctx)
	if err != nil {
		return versions.Version{}, err
	}
	want = strings.TrimSpace(want)
	var branch *PHPBranch
	for i := range branches {
		b := branches[i]
		if want == "" || want == "latest" || b.Branch == want || versions.MajorMinor(want) == b.Branch {
			branch = &b
			break
		}
	}
	if branch == nil {
		names := []string{}
		for _, b := range branches {
			names = append(names, b.Branch)
		}
		return versions.Version{}, fmt.Errorf("windows.php.net no publica PHP %s; disponibles: %s (las más viejas se importan con 'clon import')", want, strings.Join(names, ", "))
	}
	_, zip, err := branch.pickBuild()
	if err != nil {
		return versions.Version{}, err
	}
	zipPath := home.Cache("downloads", zip.Path)
	if _, err := download.File(ctx, phpReleases+zip.Path, zipPath, zip.SHA256, progress); err != nil {
		return versions.Version{}, err
	}
	dest := home.Bin("php", branch.Version+"-nts-x64")
	if err := unpackFresh(zipPath, dest); err != nil {
		return versions.Version{}, err
	}
	v, err := versions.DetectPHP(dest)
	if err != nil {
		return versions.Version{}, err
	}
	v.Source = "download"
	return v, nil
}
