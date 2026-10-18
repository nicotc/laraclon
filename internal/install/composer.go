package install

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/nicotc/laraclon/internal/download"
	"github.com/nicotc/laraclon/internal/paths"
)

const (
	composerURL = "https://getcomposer.org/download/latest-stable/composer.phar"
	caBundleURL = "https://curl.se/ca/cacert.pem"
)

// ComposerPhar is where the managed composer.phar lives.
func ComposerPhar(home paths.Home) string { return home.Bin("composer", "composer.phar") }

// InstallComposer downloads the latest stable composer.phar, verified with its published SHA-256.
func InstallComposer(ctx context.Context, home paths.Home) (string, error) {
	sum, err := download.Text(ctx, composerURL+".sha256sum")
	if err != nil {
		return "", err
	}
	fields := strings.Fields(sum)
	if len(fields) == 0 {
		return "", fmt.Errorf("respuesta vacía de getcomposer.org")
	}
	dest := ComposerPhar(home)
	if _, err := download.File(ctx, composerURL, dest, fields[0], nil); err != nil {
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
