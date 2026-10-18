// clon-shim is copied into shims\ under names such as php.exe or npm.exe. It looks up its own name in
// targets.json and runs that program with the caller's arguments, keeping their exact quoting.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"
)

type target struct {
	Exe         string            `json:"exe"`
	Args        []string          `json:"args"`
	Env         map[string]string `json:"env"`
	ForceEnv    map[string]string `json:"force_env"`
	PathPrepend []string          `json:"path_prepend"`
}

func main() {
	exe, err := os.Executable()
	if err != nil {
		fail(err)
	}
	name := strings.ToLower(strings.TrimSuffix(filepath.Base(exe), filepath.Ext(exe)))
	data, err := os.ReadFile(filepath.Join(filepath.Dir(exe), "targets.json"))
	if err != nil {
		fail(fmt.Errorf("no se pudo leer targets.json: %w", err))
	}
	var targets map[string]target
	if err := json.Unmarshal(data, &targets); err != nil {
		fail(fmt.Errorf("targets.json no es válido: %w", err))
	}
	t, ok := targets[name]
	if !ok {
		fail(fmt.Errorf("%s no está configurado; ejecuta 'clon shims' para regenerar los atajos", name))
	}

	cmdline := syscall.EscapeArg(t.Exe)
	for _, a := range t.Args {
		cmdline += " " + syscall.EscapeArg(a)
	}
	if rest := argsTail(windows.UTF16PtrToString(windows.GetCommandLine())); rest != "" {
		cmdline += " " + rest
	}

	cmd := exec.Command(t.Exe)
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: cmdline}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	cmd.Env = os.Environ()
	for k, v := range t.Env {
		if _, set := os.LookupEnv(k); !set {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
	}
	for k, v := range t.ForceEnv {
		cmd.Env = append(cmd.Env, k+"="+v) // later entries win in exec on Windows
	}
	if len(t.PathPrepend) > 0 {
		// Scripts started by the child (npm run, composer scripts, artisan) then find the same
		// node, php and composer even when the user's PATH does not include clon.
		cmd.Env = append(cmd.Env, "PATH="+prependPath(t.PathPrepend, os.Getenv("PATH")))
	}

	// Ctrl+C reaches the child through the shared console; the shim just waits for it to finish.
	signal.Ignore(os.Interrupt)
	err = cmd.Run()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		os.Exit(exitErr.ExitCode())
	}
	if err != nil {
		fail(err)
	}
}

// prependPath puts dirs first in a PATH value, dropping earlier copies so nested shims don't grow it.
func prependPath(dirs []string, current string) string {
	out := append([]string{}, dirs...)
	for _, entry := range strings.Split(current, ";") {
		if entry == "" {
			continue
		}
		dup := false
		for _, d := range dirs {
			if strings.EqualFold(strings.TrimRight(entry, `\`), strings.TrimRight(d, `\`)) {
				dup = true
				break
			}
		}
		if !dup {
			out = append(out, entry)
		}
	}
	return strings.Join(out, ";")
}

// argsTail returns the raw command line after the program name, following the
// CommandLineToArgvW rules for argv[0] (quoted, or up to the first space or tab).
func argsTail(cl string) string {
	i := 0
	if strings.HasPrefix(cl, `"`) {
		end := strings.IndexByte(cl[1:], '"')
		if end < 0 {
			return ""
		}
		i = end + 2
	} else {
		for i < len(cl) && cl[i] != ' ' && cl[i] != '\t' {
			i++
		}
	}
	return strings.TrimLeft(cl[i:], " \t")
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "clon:", err)
	os.Exit(127)
}
