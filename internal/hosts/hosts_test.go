package hosts

import (
	"strings"
	"testing"
)

const sample = "# Copyright (c) Microsoft\r\n127.0.0.1      app.test             #otra herramienta   \r\n127.0.0.1 localhost\r\n"

func TestRenderKeepsForeignLinesAndReplacesBlock(t *testing.T) {
	first := Render(sample, []string{"b.test", "a.test", "A.test", "bad name"})
	if !strings.Contains(first, "#otra herramienta") || !strings.Contains(first, "127.0.0.1 localhost") {
		t.Fatal("lines outside Nido's block must be kept")
	}
	if strings.Count(first, "127.0.0.1  a.test") != 1 || strings.Contains(first, "bad name") {
		t.Fatalf("names must be cleaned and de-duplicated:\n%s", first)
	}
	if !strings.Contains(first, "\r\n") {
		t.Fatal("CRLF line endings must be preserved")
	}
	second := Render(first, []string{"c.test"})
	if strings.Contains(second, "a.test") || !strings.Contains(second, "c.test") {
		t.Fatalf("the block must be replaced, not appended:\n%s", second)
	}
	if strings.Count(second, beginMarker) != 1 {
		t.Fatal("exactly one managed block expected")
	}
	if !InSync(second, []string{"c.test"}) || InSync(second, []string{"d.test"}) {
		t.Fatal("InSync must compare the managed block")
	}
	cleared := Render(second, nil)
	if strings.Contains(cleared, beginMarker) {
		t.Fatal("an empty list removes the block")
	}
}

func TestMissing(t *testing.T) {
	got := Missing(sample, []string{"app.test", "nuevo.test"})
	if len(got) != 1 || got[0] != "nuevo.test" {
		t.Fatalf("Missing = %v", got)
	}
}
