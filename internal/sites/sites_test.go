package sites

import "testing"

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
