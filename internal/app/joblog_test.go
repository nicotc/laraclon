package app

import (
	"reflect"
	"testing"
)

func TestJobOutputBecomesCleanLines(t *testing.T) {
	text := "\x1b[32mInstalling\x1b[0m laravel/framework\r\n" + // colors out
		"  - Downloading (30%)\r  - Downloading (100%)\r\n" + // a progress line rewritten
		"\r\n" + // empty lines skipped
		"Generating autoload files\n" +
		"building ass" // not finished yet
	rest, lines := splitOutput(text)
	want := []string{"Installing laravel/framework", "- Downloading (30%)", "- Downloading (100%)", "Generating autoload files"}
	if !reflect.DeepEqual(lines, want) || rest != "building ass" {
		t.Fatalf("lines %q, rest %q", lines, rest)
	}
	if got := lastOf([]string{"a", "b", "c"}, 2); !reflect.DeepEqual(got, []string{"b", "c"}) {
		t.Fatalf("got %q", got)
	}
	bars := []string{"Installing dependencies", "85/139 [=================>----------]  61%",
		"139/139 [============================] 100%", "Generating optimized autoload files"}
	if got := collapseBars(bars); !reflect.DeepEqual(got, []string{bars[0], bars[2], bars[3]}) {
		t.Fatalf("got %q", got)
	}
}
