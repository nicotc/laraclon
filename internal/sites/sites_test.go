package sites

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHasViteNeedsPackageJSONAndAViteConfig(t *testing.T) {
	dir := t.TempDir()
	write := func(name string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("package.json")
	if hasVite(dir) {
		t.Fatal("package.json alone is not a Vite project")
	}
	write("vite.config.ts")
	if !hasVite(dir) {
		t.Fatal("package.json and vite.config.ts make a Vite project")
	}
}

func TestNormalize(t *testing.T) {
	cases := map[string]string{
		"app":            "app",
		"tienda_nueva":   "tienda-nueva",
		"mi proyecto  2": "mi-proyecto-2",
		"proyecto final": "proyecto-final",
		"_borrador":      "borrador",
		"2026_campaña":   "2026-campana",
		"MiBlog":         "miblog",
		"araña":          "arana",
		"Diseño Él":      "diseno-el",
		"blog - copia":   "blog-copia",
		"api.v2":         "api-v2",
	}
	for in, want := range cases {
		got, ok := Normalize(in)
		if !ok || got != want {
			t.Errorf("Normalize(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
	if _, ok := Normalize("___"); ok {
		t.Error("a name without letters or digits must be rejected")
	}
}
