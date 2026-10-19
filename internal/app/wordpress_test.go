package app

import (
	"strings"
	"testing"
)

func TestWordPressLocales(t *testing.T) {
	for _, ok := range []string{"es_ES", "es_MX", "en_US", "pt_BR", "de_DE_formal", "eo"} {
		if !localeRe.MatchString(ok) {
			t.Errorf("%s es un idioma de WordPress", ok)
		}
	}
	for _, bad := range []string{"", "es-ES", "ES_es", "es_ES; rm", "../es"} {
		if localeRe.MatchString(bad) {
			t.Errorf("%q no es un idioma válido", bad)
		}
	}
}

func TestGeneratedPasswordsAreLongAndDifferent(t *testing.T) {
	a, err := generatePassword(16)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := generatePassword(16)
	if len(a) != 16 || a == b {
		t.Fatalf("got %q and %q", a, b)
	}
	if strings.ContainsAny(a, "0O1lI ") {
		t.Fatalf("the password avoids characters that look alike: %q", a)
	}
}
