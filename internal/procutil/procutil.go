// Package procutil runs short-lived helper commands (version checks, initialisation, admin tools)
// without flashing a console window.
package procutil

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
)

// ErrMissingDLL means a program could not start because a DLL it needs is missing; for PHP,
// Apache and MySQL that is almost always the Visual C++ 2015-2022 runtime.
var ErrMissingDLL = errors.New("falta el runtime de Visual C++ 2015-2022 x64 (instálalo con 'clon vcredist install')")

const (
	statusDLLNotFound        = 0xC0000135
	statusInvalidImageFormat = 0xC000007B
)

// ExitHint explains Windows loader failures hidden behind odd exit codes, or returns "".
func ExitHint(code int) string {
	switch uint32(code) {
	case statusDLLNotFound:
		return ErrMissingDLL.Error()
	case statusInvalidImageFormat:
		return "el programa o una de sus DLL es de otra arquitectura (32/64 bits) o está dañado"
	}
	return ""
}

// Output runs exe with args, waits up to timeout and returns combined stdout and stderr.
func Output(timeout time.Duration, dir, exe string, args ...string) (string, error) {
	return OutputEnv(timeout, dir, nil, exe, args...)
}

// OutputEnv is Output with extra KEY=VALUE environment variables.
func OutputEnv(timeout time.Duration, dir string, env []string, exe string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, args...)
	cmd.Dir = dir
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	text := strings.TrimSpace(out.String())
	if ctx.Err() == context.DeadlineExceeded {
		return text, fmt.Errorf("%s tardó más de %s", exe, timeout)
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && uint32(exitErr.ExitCode()) == statusDLLNotFound {
		return text, fmt.Errorf("%s no puede arrancar: %w", exe, ErrMissingDLL)
	}
	if err != nil {
		if hint := exitHintOf(err); hint != "" {
			return text, fmt.Errorf("%s no puede arrancar: %s", exe, hint)
		}
		return text, fmt.Errorf("%s: %w: %s", exe, err, LastLines(text, 5))
	}
	return text, nil
}

func exitHintOf(err error) string {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return ExitHint(exitErr.ExitCode())
	}
	return ""
}

// LastLines returns at most n trailing lines of text.
func LastLines(text string, n int) string {
	lines := strings.Split(strings.TrimRight(text, "\r\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}
