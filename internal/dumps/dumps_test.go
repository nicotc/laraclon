package dumps

import (
	"fmt"
	"net"
	"strings"
	"testing"
	"time"
)

func TestGroupsByRequestNewestFirst(t *testing.T) {
	var s Store
	s.Add(Dump{Request: "a", Text: "1", Method: "GET", URI: "/"})
	s.Add(Dump{Request: "b", Text: "2", Method: "CLI", URI: "artisan tinker"})
	s.Add(Dump{Request: "a", Text: "3"})
	g := Groups(s.All())
	if len(g) != 2 || g[0].Request != "a" || len(g[0].Dumps) != 2 || g[0].Dumps[1].Text != "3" || g[1].URI != "artisan tinker" {
		t.Fatalf("groups: %+v", g)
	}
	if s.Last() != 3 {
		t.Errorf("Last = %d", s.Last())
	}
	s.Clear()
	if len(s.All()) != 0 || s.Last() != 3 {
		t.Error("Clear keeps the sequence and drops the dumps")
	}
}

func TestStoreKeepsTheNewest(t *testing.T) {
	var s Store
	for i := 0; i < keep+10; i++ {
		s.Add(Dump{Request: "r", Text: fmt.Sprint(i)})
	}
	all := s.All()
	if len(all) != keep || all[0].Text != "10" {
		t.Fatalf("kept %d, first %q", len(all), all[0].Text)
	}
	s.Add(Dump{Request: "r", Text: strings.Repeat("x", maxText+5)})
	if last := s.All()[keep-1]; !strings.HasSuffix(last.Text, "(cortado)") {
		t.Error("a huge dump is not cut")
	}
}

func TestServerReceivesJSONLines(t *testing.T) {
	store := &Store{}
	got := make(chan struct{}, 4)
	store.OnAdd = func() { got <- struct{}{} }
	srv := &Server{Store: store, Addr: "127.0.0.1:0"}
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()
	c, err := net.Dial("tcp", srv.Address())
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprintln(c, `{"request":"r1","kind":"dump","text":"array:1 [\n  0 => 1\n]","file":"C:\\www\\app\\routes\\web.php","line":7,"host":"app.test","method":"GET","uri":"/"}`)
	fmt.Fprintln(c, `no es json`)
	fmt.Fprintln(c, `{"kind":"dump"}`) // without a request: dropped
	c.Close()
	select {
	case <-got:
	case <-time.After(3 * time.Second):
		t.Fatal("no dump arrived")
	}
	time.Sleep(100 * time.Millisecond)
	all := store.All()
	if len(all) != 1 || all[0].Line != 7 || all[0].Host != "app.test" || all[0].Seq != 1 {
		t.Fatalf("store: %+v", all)
	}
}

func TestPrepend(t *testing.T) {
	p := Prepend(9912, false)
	if !strings.HasPrefix(p, "<?php") || !strings.Contains(p, "define('NIDO_DUMPS_PORT', 9912);") || !strings.Contains(p, "define('NIDO_DUMPS_PAGE', false);") {
		t.Fatalf("prepend:\n%s", p[:300])
	}
	if strings.Contains(p, "__PORT__") || strings.Contains(p, "__PAGE__") {
		t.Error("placeholders left")
	}
}
