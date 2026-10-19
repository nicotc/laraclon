package install

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/nicotc/laraclon/internal/download"
	"github.com/nicotc/laraclon/internal/paths"
	"github.com/nicotc/laraclon/internal/versions"
)

const (
	composerURL    = "https://getcomposer.org/download/latest-stable/composer.phar"
	composerLTSURL = "https://getcomposer.org/download/latest-2.2.x/composer.phar"
	caBundleURL    = "https://curl.se/ca/cacert.pem"
)

// ComposerPhar is where the managed composer.phar lives.
func ComposerPhar(home paths.Home) string { return home.Bin("composer", "composer.phar") }

// ComposerLTSPhar is Composer 2.2 LTS, the last one that runs on PHP older than 7.2.5.
func ComposerLTSPhar(home paths.Home) string { return home.Bin("composer", "composer-2.2.phar") }

// ComposerFor is the composer.phar a PHP version can run: 2.2 LTS below PHP 7.2.5.
func ComposerFor(home paths.Home, phpVersion string) string {
	if NeedsComposerLTS(phpVersion) {
		return ComposerLTSPhar(home)
	}
	return ComposerPhar(home)
}

// NeedsComposerLTS reports whether a PHP version is too old for the current Composer.
func NeedsComposerLTS(phpVersion string) bool { return versions.Compare(phpVersion, "7.2.5") < 0 }

// InstallComposer downloads the latest stable composer.phar, verified with its published SHA-256.
func InstallComposer(ctx context.Context, home paths.Home) (string, error) {
	return fetchPhar(ctx, composerURL, ComposerPhar(home))
}

// InstallComposerLTS downloads the latest Composer 2.2 LTS for projects pinned to an older PHP.
func InstallComposerLTS(ctx context.Context, home paths.Home) (string, error) {
	return fetchPhar(ctx, composerLTSURL, ComposerLTSPhar(home))
}

func fetchPhar(ctx context.Context, url, dest string) (string, error) {
	sum, err := download.Text(ctx, url+".sha256sum")
	if err != nil {
		return "", err
	}
	fields := strings.Fields(sum)
	if len(fields) == 0 {
		return "", fmt.Errorf("respuesta vacía de getcomposer.org")
	}
	if _, err := download.File(ctx, url, dest, fields[0], nil); err != nil {
		return "", err
	}
	return dest, nil
}

// MozillaBundle is the public CA list PHP and Composer need for external HTTPS (curl.se mirror).
func MozillaBundle(home paths.Home) string { return home.SSL("mozilla-cacert.pem") }

// InstallCABundle downloads curl.se's copy of the Mozilla CA list, verified with its SHA-256.
func InstallCABundle(ctx context.Context, home paths.Home) error {
	sum, err := download.Text(ctx, caBundleURL+".sha256")
	if err != nil {
		return err
	}
	fields := strings.Fields(sum)
	if len(fields) == 0 {
		return fmt.Errorf("respuesta vacía de curl.se")
	}
	_, err = download.File(ctx, caBundleURL, MozillaBundle(home), fields[0], nil)
	return err
}

// HaveCABundle reports whether the Mozilla bundle is present.
func HaveCABundle(home paths.Home) bool {
	st, err := os.Stat(MozillaBundle(home))
	return err == nil && st.Size() > 0
}
