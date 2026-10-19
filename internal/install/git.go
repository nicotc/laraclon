package install

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/nicotc/laraclon/internal/download"
	"github.com/nicotc/laraclon/internal/paths"
)

// Git for Windows publishes MinGit, a portable Git without shell or GUI made for programs that need
// git, in the releases of its GitHub repository, whose API also gives each file's SHA-256.
const minGitReleaseURL = "https://api.github.com/repos/git-for-windows/git/releases/latest"

// MinGit-2.51.2-64-bit.zip (not the busybox flavour nor ARM).
var minGitZipRe = regexp.MustCompile(`^MinGit-\d+(?:\.\d+)+-64-bit\.zip$`)

// GitExe is the Git Nido downloads when this computer has none.
func GitExe(home paths.Home) string { return home.Bin("git", "cmd", "git.exe") }

// InstallGit downloads the latest MinGit into bin\git, verified with its SHA-256.
func InstallGit(ctx context.Context, home paths.Home, progress download.Progress) (string, error) {
	var rel githubRelease
	if err := download.JSON(ctx, minGitReleaseURL, &rel); err != nil {
		return "", fmt.Errorf("no se pudo consultar la última versión de Git para Windows en GitHub: %w", err)
	}
	for _, a := range rel.Assets {
		sum, ok := strings.CutPrefix(a.Digest, "sha256:")
		if !ok || len(sum) != 64 || !minGitZipRe.MatchString(a.Name) {
			continue
		}
		zip := home.Cache("downloads", a.Name)
		if _, err := download.File(ctx, a.URL, zip, sum, progress); err != nil {
			return "", err
		}
		defer os.Remove(zip)
		if err := unpackFresh(zip, home.Bin("git")); err != nil {
			return "", err
		}
		return GitExe(home), nil
	}
	return "", fmt.Errorf("Git para Windows %s no publica MinGit de 64 bits con SHA-256 en GitHub", rel.Tag)
}
