// Package procutil runs short-lived helper commands (version checks, initialisation, admin tools)
// without flashing a console window.
package procutil

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
)

// ErrMissingDLL means a program could not start because a DLL it needs is missing; for PHP,
// Apache and MySQL that is almost always the Visual C++ runtime.
var ErrMissingDLL = errors.New("falta el runtime de Visual C++ x64 (instálalo con 'nido vcredist install')")

const (
	statusDLLNotFound        = 0xC0000135
	statusEntryPointNotFound = 0xC0000139
	statusInvalidImageFormat = 0xC000007B
)

// ExitHint explains Windows loader failures hidden behind odd exit codes, or returns "".
func ExitHint(code int) string {
	switch uint32(code) {
	case statusDLLNotFound:
		return ErrMissingDLL.Error()
	case statusEntryPointNotFound:
		return "una de sus DLL es más vieja de lo que necesita; si es el runtime de Visual C++, actualízalo con 'nido vcredist install'"
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

// RunTree runs a long command (Composer, npm) with its output going to out. When ctx ends, the
// whole process tree is ended: Composer and npm start children of their own that would otherwise
// keep running.
func RunTree(ctx context.Context, dir string, env []string, out io.Writer, exe string, args ...string) error {
	cmd := exec.Command(exe, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	return runTree(ctx, cmd, dir, env, out, exe)
}

// RunShell is RunTree for a command line run by cmd.exe exactly as written, quotes included (Go's
// own quoting of an argument escapes inner quotes in a way cmd.exe does not understand).
func RunShell(ctx context.Context, dir string, env []string, out io.Writer, command string) error {
	comspec := filepath.Join(os.Getenv("SystemRoot"), "System32", "cmd.exe")
	cmd := exec.Command(comspec)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW,
		CmdLine: `"` + comspec + `" /d /s /c "` + command + `"`}
	return runTree(ctx, cmd, dir, env, out, "cmd.exe")
}

func runTree(ctx context.Context, cmd *exec.Cmd, dir string, env []string, out io.Writer, exe string) error {
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdout, cmd.Stderr = out, out
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if hint := exitHintOf(err); hint != "" {
			return fmt.Errorf("%s no puede arrancar: %s", exe, hint)
		}
		return err
	case <-ctx.Done():
		kill := exec.Command("taskkill", "/T", "/F", "/PID", fmt.Sprint(cmd.Process.Pid))
		kill.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
		kill.Run()
		<-done
		return ctx.Err()
	}
}

// LastLines returns at most n trailing lines of text.
func LastLines(text string, n int) string {
	lines := strings.Split(strings.TrimRight(text, "\r\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}
