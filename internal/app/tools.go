package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/nicotc/laraclon/internal/install"
	"github.com/nicotc/laraclon/internal/procutil"
	"github.com/nicotc/laraclon/internal/winx"
)

// ToolStatus is Composer or Git as the panel shows it.
type ToolStatus struct {
	Installed bool   `json:"installed"`
	Version   string `json:"version,omitempty"`
	Path      string `json:"path,omitempty"`
	Source    string `json:"source,omitempty"` // "nido" (downloaded by Nido) or "sistema" (installed apart)
}

// PathEntry is one folder Nido wants in the user's PATH.
type PathEntry struct {
	Dir    string `json:"dir"`
	Label  string `json:"label"`
	InPath bool   `json:"in_path"`
}

// ToolsView is the «Composer, Git y PATH» section.
type ToolsView struct {
	Composer ToolStatus  `json:"composer"`
	Git      ToolStatus  `json:"git"`
	Path     []PathEntry `json:"path"`
}

// ComposerGlobalBin is where 'composer global require' puts its commands (laravel, phpstan…):
// COMPOSER_HOME\vendor\bin, %APPDATA%\Composer by default.
func ComposerGlobalBin() string {
	home := os.Getenv("COMPOSER_HOME")
	if home == "" {
		home = filepath.Join(os.Getenv("APPDATA"), "Composer")
	}
	return filepath.Join(home, "vendor", "bin")
}

// PathEntries are the folders that make Nido's tools work in any terminal: its shims (php, composer,
// node, npm, wp, mysql…), npm's global packages, Composer's global commands and, when Nido brought
// it, Git.
func (e *Env) PathEntries() []PathEntry {
	list := []PathEntry{
		{Dir: e.Home.Shims(), Label: "php, composer, node, npm, npx, wp, mysql… de Nido"},
		{Dir: e.NPMPrefix(), Label: "paquetes globales de npm (pnpm, yarn…)"},
		{Dir: ComposerGlobalBin(), Label: "comandos de 'composer global require' (laravel, phpstan…)"},
	}
	if git := filepath.Dir(install.GitExe(e.Home)); fileExists(git) {
		list = append(list, PathEntry{Dir: git, Label: "git (MinGit que descargó Nido)"})
	}
	dirs := make([]string, len(list))
	for i := range list {
		dirs[i] = list[i].Dir
	}
	for i, in := range winx.UserPathHas(dirs...) {
		list[i].InPath = in
	}
	return list
}

// SetUserPath adds Nido's folders to the user's PATH (on) or takes them out. New terminals see the
// change at once; the ones already open keep their PATH.
func (e *Env) SetUserPath(on bool) ([]PathEntry, error) {
	var dirs []string
	for _, p := range e.PathEntries() {
		dirs = append(dirs, p.Dir)
	}
	if on {
		os.MkdirAll(e.NPMPrefix(), 0o755)
		os.MkdirAll(ComposerGlobalBin(), 0o755)
		if err := winx.AddToUserPath(dirs...); err != nil {
			return nil, err
		}
	} else if err := winx.RemoveFromUserPath(dirs...); err != nil {
		return nil, err
	}
	return e.PathEntries(), nil
}

var (
	composerVersionRe = regexp.MustCompile(`Composer (?:version )?(\d+\.\d+\.\d+)`)
	gitVersionRe      = regexp.MustCompile(`git version (\d+\.\d+\.\d+)`)
)

// Tools reads Composer, Git and the PATH (the versions are asked to the programs and kept a minute).
func (d *Daemon) Tools() ToolsView {
	d.mu.Lock()
	cached, at := d.tools, d.toolsAt
	d.mu.Unlock()
	if time.Since(at) < time.Minute && cached != nil {
		v := *cached
		v.Path = d.env.PathEntries()
		return v
	}
	v := ToolsView{}
	if phar := install.ComposerPhar(d.env.Home); fileExists(phar) {
		v.Composer = ToolStatus{Installed: true, Path: phar, Source: "nido"}
		if global, err := d.env.GlobalPHP(); err == nil {
			out, _ := procutil.Output(30*time.Second, filepath.Dir(phar), filepath.Join(global.Path, "php.exe"), "-n", phar, "--version", "--no-ansi")
			if m := composerVersionRe.FindStringSubmatch(out); m != nil {
				v.Composer.Version = m[1]
			}
		}
	}
	if git := findGit(d.env.Home); git != "" {
		v.Git = ToolStatus{Installed: true, Path: git, Source: "sistema"}
		if strings.EqualFold(git, install.GitExe(d.env.Home)) {
			v.Git.Source = "nido"
		}
		out, _ := procutil.Output(30*time.Second, filepath.Dir(git), git, "--version")
		if m := gitVersionRe.FindStringSubmatch(out); m != nil {
			v.Git.Version = m[1]
		}
	}
	d.mu.Lock()
	d.tools, d.toolsAt = &v, time.Now()
	d.mu.Unlock()
	v.Path = d.env.PathEntries()
	return v
}

func (d *Daemon) forgetTools() {
	d.mu.Lock()
	d.tools = nil
	d.mu.Unlock()
}

// InstallTool downloads or updates Composer (with 2.2 LTS when it is there) or MinGit in the
// background, as a job.
func (d *Daemon) InstallTool(name string) (Job, error) {
	var work func(ctx context.Context, j *Job) error
	switch name {
	case "composer":
		work = func(ctx context.Context, j *Job) error {
			if _, err := install.InstallComposer(ctx, d.env.Home); err != nil {
				return err
			}
			if fileExists(install.ComposerLTSPhar(d.env.Home)) {
				if _, err := install.InstallComposerLTS(ctx, d.env.Home); err != nil {
					return err
				}
			}
			return nil
		}
	case "git":
		if git := findGit(d.env.Home); git != "" && !strings.EqualFold(git, install.GitExe(d.env.Home)) {
			return Job{}, fmt.Errorf("ya tienes Git en %s: Nido lo usa tal cual", git)
		}
		work = func(ctx context.Context, j *Job) error {
			_, err := install.InstallGit(ctx, d.env.Home, d.jobProgress(j))
			return err
		}
	default:
		return Job{}, fmt.Errorf("Nido instala composer y git, no %q", name)
	}
	j, snap, ctx, running := d.newJob(name, "latest", "última versión", 0)
	if !running {
		go func() {
			defer j.cancel()
			d.jobs.update(j, func(j *Job) { j.Phase = "descargando" })
			err := work(ctx, j)
			if err == nil {
				d.refreshShims()
			}
			d.forgetTools()
			d.endJob(j, err)
		}()
	}
	return snap, nil
}
