package main

import "testing"

func TestPickDirChoosesTheDeepestProjectThatContainsTheFolder(t *testing.T) {
	dirs := []dirTarget{
		{Dir: `C:\www\viejo`, Exe: "php72.exe"},
		{Dir: `C:\www\viejo\api`, Exe: "php56.exe"},
		{Dir: `C:\www\otro\`, Exe: "php83.exe"},
	}
	cases := map[string]string{
		`C:\www\viejo`:            "php72.exe",
		`c:\WWW\Viejo\app\Models`: "php72.exe", // Windows paths ignore case
		`C:\www\viejo\api\src`:    "php56.exe",
		`C:\www\otro`:             "php83.exe",
		`C:\www\viejo-nuevo`:      "", // a sibling whose name only starts the same
		`C:\www`:                  "",
	}
	for wd, want := range cases {
		got, ok := pickDir(dirs, wd)
		if (want == "") == ok || got.Exe != want {
			t.Errorf("%s: got %q (%v), want %q", wd, got.Exe, ok, want)
		}
	}
}
