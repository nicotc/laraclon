package proxy

import (
	"net/url"
	"testing"
)

func TestLookupMatchesSubdomainsThroughTheSite(t *testing.T) {
	u, _ := url.Parse("http://127.0.0.1:20001")
	var tb Table
	tb.Set(map[string]Route{"app.test": {Site: "app.test", Backend: u}})
	for _, host := range []string{"app.test", "APP.test.", "admin.app.test", "a.b.app.test"} {
		if _, ok := tb.Lookup(host); !ok {
			t.Errorf("Lookup(%q) should match app.test", host)
		}
	}
	for _, host := range []string{"otra.test", "test", "app.test.example"} {
		if _, ok := tb.Lookup(host); ok {
			t.Errorf("Lookup(%q) should not match", host)
		}
	}
	if got := certHost("admin.app.test", &tb); got != "app.test" {
		t.Errorf("certHost = %q, want app.test", got)
	}
}
