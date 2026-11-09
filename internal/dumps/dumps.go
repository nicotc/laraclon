// Package dumps collects what PHP's dump() and dd() show, grouped by request, for the panel's
// «Dumps» tab. A small file PHP loads before every script (auto_prepend_file) defines both functions
// in the projects that use Symfony's VarDumper (Laravel, Symfony): each dump goes as one JSON line to
// 127.0.0.1:9912, and the page still shows it as before unless that is turned off.
package dumps

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Port is where PHP sends the dumps.
const Port = 9912

// keep bounds the dumps held in memory (the oldest go first).
const keep = 2000

// maxText bounds one dump's text (a huge collection is cut, not dropped).
const maxText = 256 << 10

// Dump is one call to dump() or dd().
type Dump struct {
	Seq     int64     `json:"seq"`
	Request string    `json:"request"` // id of the request or command that made it
	Kind    string    `json:"kind"`    // "dump" or "dd"
	Text    string    `json:"text"`    // the value, as VarDumper prints it in a terminal
	Type    string    `json:"type"`    // "array", "App\\Models\\User"…
	File    string    `json:"file"`
	Line    int       `json:"line"`
	Host    string    `json:"host"`   // tienda.test ("" in a terminal)
	Method  string    `json:"method"` // GET, POST… or CLI
	URI     string    `json:"uri"`    // /productos?page=2, or the command line
	Cwd     string    `json:"cwd"`
	Time    time.Time `json:"time"`
}

// Group is the dumps of one request, in the order they were made.
type Group struct {
	Request string    `json:"request"`
	Host    string    `json:"host"`
	Method  string    `json:"method"`
	URI     string    `json:"uri"`
	Cwd     string    `json:"cwd"`
	Started time.Time `json:"started"`
	Dumps   []Dump    `json:"dumps"`
}

// Store keeps the last dumps.
type Store struct {
	mu   sync.Mutex
	seq  int64
	list []Dump
	// OnAdd is called after a dump arrives (without locks held).
	OnAdd func()
}

// Add stores a dump and returns its sequence number.
func (s *Store) Add(d Dump) int64 {
	if len(d.Text) > maxText {
		d.Text = d.Text[:maxText] + "\n… (cortado)"
	}
	if d.Time.IsZero() {
		d.Time = time.Now()
	}
	s.mu.Lock()
	s.seq++
	d.Seq = s.seq
	s.list = append(s.list, d)
	if len(s.list) > keep {
		s.list = append([]Dump(nil), s.list[len(s.list)-keep:]...)
	}
	s.mu.Unlock()
	if s.OnAdd != nil {
		s.OnAdd()
	}
	return d.Seq
}

// Last is the sequence number of the newest dump (0 when there is none).
func (s *Store) Last() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.seq
}

// All returns every dump held, oldest first.
func (s *Store) All() []Dump {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Dump(nil), s.list...)
}

// Clear forgets every dump.
func (s *Store) Clear() {
	s.mu.Lock()
	s.list = nil
	s.mu.Unlock()
}

// Groups puts dumps together by request, the newest request first.
func Groups(list []Dump) []Group {
	byID := map[string]*Group{}
	var order []string
	for _, d := range list {
		g, ok := byID[d.Request]
		if !ok {
			g = &Group{Request: d.Request, Host: d.Host, Method: d.Method, URI: d.URI, Cwd: d.Cwd, Started: d.Time}
			byID[d.Request] = g
			order = append(order, d.Request)
		}
		g.Dumps = append(g.Dumps, d)
	}
	out := make([]Group, 0, len(order))
	for _, id := range order {
		out = append(out, *byID[id])
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Dumps[len(out[i].Dumps)-1].Seq > out[j].Dumps[len(out[j].Dumps)-1].Seq
	})
	return out
}

// Server receives dumps on 127.0.0.1:Port.
type Server struct {
	Store *Store
	Addr  string // "127.0.0.1:9912" when empty

	mu sync.Mutex
	ln net.Listener
}

// Start opens the port and receives in the background.
func (s *Server) Start() error {
	addr := s.Addr
	if addr == "" {
		addr = net.JoinHostPort("127.0.0.1", strconv.Itoa(Port))
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("el receptor de dumps no puede abrir %s: %w", addr, err)
	}
	s.mu.Lock()
	s.ln = ln
	s.mu.Unlock()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				if errors.Is(err, net.ErrClosed) {
					return
				}
				continue
			}
			go s.receive(c)
		}
	}()
	return nil
}

// Address is where the server listens ("" when stopped).
func (s *Server) Address() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ln == nil {
		return ""
	}
	return s.ln.Addr().String()
}

// Stop closes the port.
func (s *Server) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ln != nil {
		s.ln.Close()
		s.ln = nil
	}
}

func (s *Server) receive(c net.Conn) {
	defer c.Close()
	c.SetDeadline(time.Now().Add(10 * time.Second))
	sc := bufio.NewScanner(c)
	sc.Buffer(make([]byte, 64<<10), 4*maxText)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var d Dump
		if err := json.Unmarshal([]byte(line), &d); err != nil || d.Request == "" {
			continue
		}
		d.Seq, d.Time = 0, time.Time{}
		s.Store.Add(d)
	}
}
