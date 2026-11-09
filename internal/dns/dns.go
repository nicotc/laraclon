// Package dns answers the local top-level domain: every name under .test (subdomains too, such as
// api.tienda.test or reverb.tienda.test) and under .localhost resolves to this computer. Windows sends
// it only those names, through a name resolution policy (NRPT) rule for the domain, so the hosts file
// no longer has to list every site.
package dns

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
)

const (
	typeA    = 1
	typeAAAA = 28
	classIN  = 1

	rcodeOK       = 0
	rcodeFormErr  = 1
	rcodeNXDomain = 3
	rcodeRefused  = 5

	ttl = 60
)

// Server is the DNS server of the local domain on 127.0.0.1:53 (UDP and TCP).
type Server struct {
	TLD  string // "test"
	Addr string // "127.0.0.1:53"

	mu  sync.Mutex
	udp net.PacketConn
	tcp net.Listener
}

// Start opens the port and answers in the background.
func (s *Server) Start() error {
	udp, err := net.ListenPacket("udp", s.Addr)
	if err != nil {
		return fmt.Errorf("el DNS de Nido no puede abrir %s (UDP): %w", s.Addr, err)
	}
	tcp, err := net.Listen("tcp", s.Addr)
	if err != nil {
		udp.Close()
		return fmt.Errorf("el DNS de Nido no puede abrir %s (TCP): %w", s.Addr, err)
	}
	s.mu.Lock()
	s.udp, s.tcp = udp, tcp
	s.mu.Unlock()
	go s.serveUDP(udp)
	go s.serveTCP(tcp)
	return nil
}

// Stop closes the port.
func (s *Server) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.udp != nil {
		s.udp.Close()
		s.udp = nil
	}
	if s.tcp != nil {
		s.tcp.Close()
		s.tcp = nil
	}
}

// Running reports whether the server has its port open.
func (s *Server) Running() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.udp != nil
}

func (s *Server) serveUDP(conn net.PacketConn) {
	buf := make([]byte, 1500)
	for {
		n, addr, err := conn.ReadFrom(buf)
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			continue
		}
		if out, ok := Answer(buf[:n], s.TLD); ok {
			conn.WriteTo(out, addr)
		}
	}
}

func (s *Server) serveTCP(ln net.Listener) {
	for {
		c, err := ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			continue
		}
		go func() {
			defer c.Close()
			var size [2]byte
			for {
				if _, err := readFull(c, size[:]); err != nil {
					return
				}
				msg := make([]byte, binary.BigEndian.Uint16(size[:]))
				if _, err := readFull(c, msg); err != nil {
					return
				}
				out, ok := Answer(msg, s.TLD)
				if !ok {
					return
				}
				binary.BigEndian.PutUint16(size[:], uint16(len(out)))
				if _, err := c.Write(append(size[:], out...)); err != nil {
					return
				}
			}
		}()
	}
}

func readFull(c net.Conn, b []byte) (int, error) {
	n := 0
	for n < len(b) {
		m, err := c.Read(b[n:])
		n += m
		if err != nil {
			return n, err
		}
	}
	return n, nil
}

// Local reports whether a name belongs to this computer: the local domain and its subdomains, and
// localhost with its subdomains (db.localhost, salud.localhost).
func Local(name, tld string) bool {
	name = strings.ToLower(strings.TrimSuffix(name, "."))
	for _, d := range []string{tld, "localhost"} {
		if d != "" && (name == d || strings.HasSuffix(name, "."+d)) {
			return true
		}
	}
	return false
}

// Answer builds the reply to one query: 127.0.0.1 and ::1 for the local names, an empty answer for
// their other record types, and a refusal for anything else (Windows only asks for the local
// domain). ok is false for packets that are not worth answering at all.
func Answer(req []byte, tld string) (out []byte, ok bool) {
	if len(req) < 12 || req[2]&0x80 != 0 { // too short, or a response
		return nil, false
	}
	id := req[0:2]
	opcode := (req[2] >> 3) & 0x0f
	rd := req[2] & 0x01
	if binary.BigEndian.Uint16(req[4:6]) != 1 || opcode != 0 {
		return header(id, rd, rcodeFormErr, 0, 0), true
	}
	name, end, err := readName(req, 12)
	if err != nil || end+4 > len(req) {
		return header(id, rd, rcodeFormErr, 0, 0), true
	}
	qtype := binary.BigEndian.Uint16(req[end : end+2])
	qclass := binary.BigEndian.Uint16(req[end+2 : end+4])
	question := req[12 : end+4]
	if !Local(name, tld) {
		return append(header(id, rd, rcodeRefused, 1, 0), question...), true
	}
	if qclass != classIN {
		return append(header(id, rd, rcodeNXDomain, 1, 0), question...), true
	}
	var rdata []byte
	switch qtype {
	case typeA:
		rdata = net.IPv4(127, 0, 0, 1).To4()
	case typeAAAA:
		rdata = net.IPv6loopback
	}
	if rdata == nil {
		return append(header(id, rd, rcodeOK, 1, 0), question...), true // the name exists, without that record
	}
	out = append(header(id, rd, rcodeOK, 1, 1), question...)
	out = append(out, 0xc0, 0x0c) // the name, as a pointer to the question
	out = binary.BigEndian.AppendUint16(out, qtype)
	out = binary.BigEndian.AppendUint16(out, classIN)
	out = binary.BigEndian.AppendUint32(out, ttl)
	out = binary.BigEndian.AppendUint16(out, uint16(len(rdata)))
	return append(out, rdata...), true
}

// header is a response header: authoritative, the query's id and recursion flag.
func header(id []byte, rd byte, rcode byte, questions, answers uint16) []byte {
	h := make([]byte, 12)
	copy(h, id)
	h[2] = 0x80 | 0x04 | rd // QR, AA, RD as asked
	h[3] = rcode & 0x0f
	binary.BigEndian.PutUint16(h[4:], questions)
	binary.BigEndian.PutUint16(h[6:], answers)
	return h
}

// readName reads the question's name (queries carry it without compression).
func readName(msg []byte, off int) (string, int, error) {
	var labels []string
	for {
		if off >= len(msg) {
			return "", 0, errors.New("nombre cortado")
		}
		n := int(msg[off])
		off++
		if n == 0 {
			break
		}
		if n&0xc0 != 0 || off+n > len(msg) || len(labels) > 127 {
			return "", 0, errors.New("nombre no válido")
		}
		labels = append(labels, string(msg[off:off+n]))
		off += n
	}
	return strings.Join(labels, "."), off, nil
}
