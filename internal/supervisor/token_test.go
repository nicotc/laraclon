package supervisor

import (
	"os"
	"os/exec"
	"syscall"
	"testing"

	"golang.org/x/sys/windows"
)

func TestUnprivilegedTokenGrantsTheUserItsOwnObjects(t *testing.T) {
	token, err := unprivilegedToken()
	if err != nil {
		t.Fatal(err)
	}
	token.Close()
}

// Starts a program the way Nido starts PostgreSQL ("<exe> --version"), when NIDO_TEST_UNPRIVILEGED_EXE
// names one: a check for a computer where Nido runs as administrator. NIDO_TEST_SIN_DACL=1 leaves
// out the default DACL fix, to see the failure it prevents (0xC0000142).
func TestUnprivilegedChildStarts(t *testing.T) {
	exe := os.Getenv("NIDO_TEST_UNPRIVILEGED_EXE")
	if exe == "" {
		t.Skip("NIDO_TEST_UNPRIVILEGED_EXE no está definida")
	}
	token, err := unprivilegedToken()
	if os.Getenv("NIDO_TEST_SIN_DACL") != "" {
		token, err = restrictedToken()
	}
	if err != nil {
		t.Fatal(err)
	}
	defer token.Close()
	cmd := exec.Command(exe, "--version")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW, Token: syscall.Token(token)}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	t.Logf("%s", out)
}
