package dns

import (
	"encoding/binary"
	"net"
	"strings"
	"testing"
	"time"
)

// query builds a DNS query for name and record type.
func query(id uint16, name string, qtype uint16) []byte {
	q := make([]byte, 12)
	binary.BigEndian.PutUint16(q, id)
	q[2] = 0x01 // recursion desired
	binary.BigEndian.PutUint16(q[4:], 1)
	for _, l := range strings.Split(name, ".") {
		q = append(q, byte(len(l)))
		q = append(q, l...)
	}
	q = append(q, 0)
	q = binary.BigEndian.AppendUint16(q, qtype)
	return binary.BigEndian.AppendUint16(q, classIN)
}

func TestAnswer(t *testing.T) {
	cases := []struct {
		name    string
		qtype   uint16
		rcode   byte
		answers uint16
		ip      string
	}{
		{"tienda.test", typeA, rcodeOK, 1, "127.0.0.1"},
		{"API.Tienda.TEST", typeA, rcodeOK, 1, "127.0.0.1"},
		{"feature-x.tienda.test", typeAAAA, rcodeOK, 1, "::1"},
		{"db.localhost", typeA, rcodeOK, 1, "127.0.0.1"},
		{"tienda.test", 65, rcodeOK, 0, ""}, // HTTPS record: none, but the name exists
		{"example.com", typeA, rcodeRefused, 0, ""},
		{"testing.com", typeA, rcodeRefused, 0, ""},
	}
	for _, c := range cases {
		out, ok := Answer(query(0xbeef, c.name, c.qtype), "test")
		if !ok {
			t.Fatalf("%s: no answer", c.name)
		}
		if binary.BigEndian.Uint16(out) != 0xbeef || out[2]&0x80 == 0 || out[2]&0x01 == 0 {
			t.Errorf("%s: header %x", c.name, out[:4])
		}
		if got := out[3] & 0x0f; got != c.rcode {
			t.Errorf("%s: rcode %d, want %d", c.name, got, c.rcode)
		}
		if got := binary.BigEndian.Uint16(out[6:]); got != c.answers {
			t.Errorf("%s: %d answers, want %d", c.name, got, c.answers)
		}
		if c.ip != "" {
			rdlen := int(binary.BigEndian.Uint16(out[len(out)-len(net.ParseIP(c.ip).To16())-2:]))
			ip := net.IP(out[len(out)-rdlen:])
			if !ip.Equal(net.ParseIP(c.ip)) {
				t.Errorf("%s: %s, want %s", c.name, ip, c.ip)
			}
		}
	}
}

func TestAnswerIgnoresGarbage(t *testing.T) {
	if _, ok := Answer([]byte{1, 2, 3}, "test"); ok {
		t.Error("a short packet was answered")
	}
	resp := query(1, "tienda.test", typeA)
	resp[2] |= 0x80
	if _, ok := Answer(resp, "test"); ok {
		t.Error("a response was answered")
	}
	bad := query(2, "tienda.test", typeA)
	bad[12] = 0xc0 // a pointer where the name should be
	out, ok := Answer(bad, "test")
	if !ok || out[3]&0x0f != rcodeFormErr {
		t.Errorf("a broken name got %v", out)
	}
}

func TestServerResolves(t *testing.T) {
	s := &Server{TLD: "test", Addr: "127.0.0.1:0"}
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	defer s.Stop()
	addr := s.udp.LocalAddr().String()
	c, err := net.Dial("udp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(3 * time.Second))
	c.Write(query(7, "blog.tienda.test", typeA))
	buf := make([]byte, 512)
	n, err := c.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	if !net.IP(buf[n-4 : n]).Equal(net.IPv4(127, 0, 0, 1)) {
		t.Fatalf("answer %x", buf[:n])
	}
}

func TestScript(t *testing.T) {
	on := script("test", true)
	if !strings.Contains(on, "Add-DnsClientNrptRule -Namespace '.test' -NameServers '127.0.0.1'") || !strings.Contains(on, "Clear-DnsClientCache") {
		t.Errorf("on: %s", on)
	}
	if strings.Contains(script("test", false), "Add-DnsClientNrptRule") {
		t.Error("off adds a rule")
	}
	if validTLD("te'st") == nil || validTLD("") == nil || validTLD("test") != nil {
		t.Error("validTLD")
	}
}
