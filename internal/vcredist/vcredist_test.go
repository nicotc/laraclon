package vcredist

import (
	"os"
	"testing"
)

func TestNeedsNothingForProgramsWithoutTheRuntime(t *testing.T) {
	// The test binary is a Go program: it does not load the Visual C++ runtime.
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if got := Needs(exe); got != "" {
		t.Fatalf("Needs(%s) = %q, want nothing", exe, got)
	}
	if got := Needs("no-such-program.exe"); got != "" {
		t.Fatalf("a missing program needs nothing, got %q", got)
	}
}

func TestOlderComparesWithTheInstalledRuntime(t *testing.T) {
	have := Version()
	if have == "" {
		t.Skip("this Windows has no Visual C++ runtime")
	}
	if Older("14.0") {
		t.Errorf("runtime %s must not be older than 14.0", have)
	}
	if !Older("99.0") {
		t.Errorf("runtime %s must be older than 99.0", have)
	}
}
