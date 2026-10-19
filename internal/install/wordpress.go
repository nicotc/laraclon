package install

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/nicotc/laraclon/internal/download"
	"github.com/nicotc/laraclon/internal/paths"
)

const wordpressVersionURL = "https://api.wordpress.org/core/version-check/1.7/"

// InstallWordPress puts the newest WordPress into dest. Nido downloads the official zip itself and
// checks it against the SHA-1 wordpress.org publishes: WP-CLI would fetch the .tar.gz, whose long
// file names PHP's PharData cannot extract on Windows (and its fallback, tar, starts with an empty
// environment there). It returns the version.
func InstallWordPress(ctx context.Context, home paths.Home, dest string, progress download.Progress) (string, error) {
	var check struct {
		Offers []struct {
			Response string `json:"response"`
			Download string `json:"download"`
			Locale   string `json:"locale"`
			Current  string `json:"current"`
		} `json:"offers"`
	}
	if err := download.JSON(ctx, wordpressVersionURL, &check); err != nil {
		return "", fmt.Errorf("no se pudo consultar la última versión de WordPress: %w", err)
	}
	if len(check.Offers) == 0 || !strings.HasSuffix(check.Offers[0].Download, ".zip") {
		return "", fmt.Errorf("wordpress.org no ofreció ningún zip de WordPress")
	}
	offer := check.Offers[0]
	sum, err := download.Text(ctx, offer.Download+".sha1")
	if err != nil {
		return "", err
	}
	want := strings.TrimSpace(sum)
	if len(want) != 40 {
		return "", fmt.Errorf("wordpress.org no publica el SHA-1 de %s", path.Base(offer.Download))
	}
	zipPath := home.Cache("downloads", path.Base(offer.Download))
	if _, err := download.File(ctx, offer.Download, zipPath, "", progress); err != nil {
		return "", err
	}
	defer os.Remove(zipPath)
	got, err := sha1File(zipPath)
	if err != nil {
		return "", err
	}
	if !strings.EqualFold(got, want) {
		return "", fmt.Errorf("el SHA-1 de %s no coincide (esperado %s, obtenido %s)", path.Base(offer.Download), want, got)
	}
	if err := download.ExtractZip(zipPath, dest, true); err != nil {
		return "", err
	}
	return offer.Current, nil
}

const wordpressTranslationsURL = "https://api.wordpress.org/translations/core/1.0/?version="

// InstallWordPressLanguage puts WordPress's translation to locale ("es_ES") into the site's
// wp-content\languages before it is installed, so its first content comes in that language too.
// It reports false when wordpress.org has no translation to that language.
func InstallWordPressLanguage(ctx context.Context, home paths.Home, dir, version, locale string) (bool, error) {
	var list struct {
		Translations []struct {
			Language string `json:"language"`
			Package  string `json:"package"`
		} `json:"translations"`
	}
	if err := download.JSON(ctx, wordpressTranslationsURL+version, &list); err != nil {
		return false, fmt.Errorf("no se pudo consultar las traducciones de WordPress: %w", err)
	}
	for _, t := range list.Translations {
		if t.Language != locale || !strings.HasSuffix(t.Package, ".zip") {
			continue
		}
		zipPath := home.Cache("downloads", "wordpress-"+version+"-"+locale+".zip")
		if _, err := download.File(ctx, t.Package, zipPath, "", nil); err != nil {
			return false, err
		}
		defer os.Remove(zipPath)
		return true, download.ExtractZip(zipPath, filepath.Join(dir, "wp-content", "languages"), false)
	}
	return false, nil
}

func sha1File(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha1.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
