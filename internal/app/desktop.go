package app

import (
	"path/filepath"
	"strings"

	"github.com/nicotc/laraclon/internal/config"
	"github.com/nicotc/laraclon/internal/logo"
	"github.com/nicotc/laraclon/internal/winx"
)

// How a person finds Nido on Windows: a «Nido» shortcut in the Start menu and on the desktop, with
// its logo, and Nido starting when they sign in.

// autostartName is Nido's entry among the programs Windows starts at sign-in.
const autostartName = "Nido"

// AutostartFlag tells nidow it was started at sign-in, and QuietFlag that a command started it
// ('nido run'): either way it starts without opening its panel.
const (
	AutostartFlag = "--inicio"
	QuietFlag     = "--sin-panel"
)

// IconPath is Nido's logo as an icon file, for its shortcuts.
func (e *Env) IconPath() string { return e.Home.Path("nido.ico") }

// TrayExe is nidow.exe, Nido with its tray icon.
func (e *Env) TrayExe() string { return e.Home.Path("nidow.exe") }

// InstallShortcuts writes the «Nido» shortcuts of the Start menu and the desktop. They open Nido and
// its panel, or just the panel when Nido already runs.
func (e *Env) InstallShortcuts() ([]string, error) {
	if err := config.WriteFileAtomic(e.IconPath(), logo.ICO(logo.Amber, logo.AmberDark, false)); err != nil {
		return nil, err
	}
	var made []string
	for _, folder := range []func() (string, error){winx.StartMenuPrograms, winx.Desktop} {
		dir, err := folder()
		if err != nil {
			return made, err
		}
		lnk := filepath.Join(dir, "Nido.lnk")
		err = winx.CreateShortcut(winx.Shortcut{Path: lnk, Target: e.TrayExe(), Dir: e.Home.Root, Icon: e.IconPath(),
			Description: "Nido: donde viven tus proyectos"})
		if err != nil {
			return made, err
		}
		made = append(made, lnk)
	}
	return made, nil
}

func (e *Env) autostartCommand() string { return `"` + e.TrayExe() + `" ` + AutostartFlag }

// SetAutostart makes Nido start, or not, when the user signs in to Windows.
func (e *Env) SetAutostart(on bool) error {
	command := ""
	if on {
		command = e.autostartCommand()
	}
	return winx.SetRunAtLogon(autostartName, command)
}

// Autostart reports whether this installation starts when the user signs in.
func (e *Env) Autostart() bool {
	return strings.EqualFold(winx.RunAtLogon(autostartName), e.autostartCommand())
}
