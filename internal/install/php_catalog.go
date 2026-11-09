package install

import (
	"context"
	"sort"

	"github.com/nicotc/laraclon/internal/download"
	"github.com/nicotc/laraclon/internal/versions"
)

// PHPRelease is a PHP branch that can be installed: the ones windows.php.net maintains and the older
// ones it keeps in its archives.
type PHPRelease struct {
	Branch  string `json:"branch"`
	Version string `json:"version"`
	Note    string `json:"note"` // "mantenida" or "sin soporte"
}

// PHPCatalog lists every branch Nido can install, newest first. The archives are optional: without
// them (offline, or the listing changed) the maintained branches are still offered.
func PHPCatalog(ctx context.Context) ([]PHPRelease, error) {
	branches, err := PHPBranches(ctx)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []PHPRelease
	for _, b := range branches {
		if _, zip, err := b.pickBuild(); err != nil || zip == nil {
			continue
		}
		seen[b.Branch] = true
		out = append(out, PHPRelease{Branch: b.Branch, Version: b.Version, Note: "mantenida"})
	}
	if listing, err := download.Text(ctx, phpArchives); err == nil {
		for _, branch := range archivedBranches(listing) {
			if seen[branch] {
				continue
			}
			if _, version := newestArchived(listing, branch); version != "" {
				seen[branch] = true
				out = append(out, PHPRelease{Branch: branch, Version: version, Note: "sin soporte"})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return versions.Compare(out[i].Branch, out[j].Branch) > 0 })
	return out, nil
}

// archivedBranches are the branches with a 64-bit NTS build in the archives' listing.
func archivedBranches(listing string) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range archivedPHPRe.FindAllStringSubmatch(listing, -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			out = append(out, m[1])
		}
	}
	return out
}
