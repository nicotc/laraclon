// Package procutil runs short-lived helper commands (version checks, initialisation, admin tools)
// without flashing a console window.
package procutil

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
)

// Output runs exe with args, waits up to timeout and returns combined stdout and stderr.
func Output(timeout time.Duration, dir, exe string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, args...)
	cmd.Dir = dir
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	text := strings.TrimSpace(out.String())
	if ctx.Err() == context.DeadlineExceeded {
		return text, fmt.Errorf("%s tardó más de %s", exe, timeout)
	}
	if err != nil {
		return text, fmt.Errorf("%s: %w: %s", exe, err, LastLines(text, 5))
	}
	return text, nil
}

// LastLines returns at most n trailing lines of text.
func LastLines(text string, n int) string {
	lines := strings.Split(strings.TrimRight(text, "\r\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}
