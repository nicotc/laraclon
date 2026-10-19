package procutil

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestRunShellPassesTheCommandLineAsWritten(t *testing.T) {
	var out bytes.Buffer
	err := RunShell(context.Background(), t.TempDir(), nil, &out, `echo --name="Mi App" & echo fin`)
	if err != nil {
		t.Fatal(err)
	}
	if got := out.String(); !strings.Contains(got, `--name="Mi App"`) || !strings.Contains(got, "fin") {
		t.Fatalf("cmd.exe got the quotes changed: %q", got)
	}
}
