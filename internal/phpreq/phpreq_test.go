package phpreq

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSatisfies(t *testing.T) {
	cases := []struct {
		v, c string
		want bool
	}{
		{"8.5.12", ">=7.4.0 <8.5.0", false},
		{"8.4.27", ">=7.4.0 <8.5.0", true},
		{"8.4.27", "^8.2", true},
		{"9.0.0", "^8.2", false},
		{"8.1.30", "^8.2", false},
		{"8.3.1", "^7.4 || ^8.0", true},
		{"7.3.0", "^7.4|^8.0", false},
		{"8.2.5", "~8.2.0", true},
		{"8.3.0", "~8.2.0", false},
		{"8.9.0", "~8.2", true},
		{"8.1.4", "8.1.*", true},
		{"8.2.0", "8.1.*", false},
		{"8.4.0", ">=8.1, <8.5", true},
		{"8.4.0", ">= 8.1 < 8.4", false},
		{"8.3.9", "7.4 - 8.3", true},
		{"8.4.0", "7.4 - 8.3", false},
		{"8.4.0", "*", true},
		{"8.4.0", "algo raro", true}, // unreadable: Composer decides
	}
	for _, c := range cases {
		if got := Satisfies(c.v, c.c); got != c.want {
			t.Errorf("Satisfies(%s, %q) = %v, want %v", c.v, c.c, got, c.want)
		}
	}
}

func TestReadAndUnmet(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "composer.json"), []byte(`{"require":{"php":"^8.1","laravel/framework":"^10"}}`), 0o644)
	os.WriteFile(filepath.Join(dir, "composer.lock"), []byte(`{"packages":[{"name":"phpoffice/phpspreadsheet","version":"1.30.4","require":{"php":">=7.4.0 <8.5.0"}}],
"packages-dev":[{"name":"x/y","version":"1.0","require":{"php":">=8.0"}}]}`), 0o644)
	reqs, pinned, err := Read(dir)
	if err != nil || pinned || len(reqs) != 3 {
		t.Fatalf("Read = %v %v %v", reqs, pinned, err)
	}
	if u := Unmet("8.5.12", reqs); u == nil || u.From != "phpoffice/phpspreadsheet 1.30.4" {
		t.Errorf("8.5 should fail on phpspreadsheet: %v", u)
	}
	if u := Unmet("8.4.27", reqs); u != nil {
		t.Errorf("8.4 should pass: %v", u)
	}
	os.WriteFile(filepath.Join(dir, "composer.json"), []byte(`{"config":{"platform":{"php":"8.2.0"}}}`), 0o644)
	if _, pinned, _ := Read(dir); !pinned {
		t.Error("config.platform.php should pin the platform")
	}
}
