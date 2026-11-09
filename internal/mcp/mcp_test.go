package mcp

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func run(t *testing.T, s *Server, lines ...string) []map[string]any {
	t.Helper()
	var out bytes.Buffer
	if err := s.Serve(strings.NewReader(strings.Join(lines, "\n")+"\n"), &out); err != nil {
		t.Fatal(err)
	}
	var msgs []map[string]any
	for _, l := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if l == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("bad line %q: %v", l, err)
		}
		msgs = append(msgs, m)
	}
	return msgs
}

func TestHandshakeListAndCall(t *testing.T) {
	s := &Server{Name: "nido", Version: "1", Tools: []Tool{
		{Name: "eco", Description: "repite", InputSchema: Schema([]string{"texto"}, map[string]string{"texto": "string:lo que repite"}),
			Call: func(a map[string]any) (string, error) { return "eco: " + Str(a, "texto"), nil }},
		{Name: "falla", Description: "falla", InputSchema: Schema(nil, nil),
			Call: func(map[string]any) (string, error) { return "", errors.New("no se pudo") }},
	}}
	msgs := run(t, s,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"x","version":"1"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"eco","arguments":{"texto":"hola"}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"falla"}}`,
		`{"jsonrpc":"2.0","id":5,"method":"nada"}`,
		`no es json`,
	)
	if len(msgs) != 6 {
		t.Fatalf("%d answers (the notification gets none): %v", len(msgs), msgs)
	}
	if v := msgs[0]["result"].(map[string]any)["protocolVersion"]; v != "2025-03-26" {
		t.Errorf("protocol version %v", v)
	}
	if tools := msgs[1]["result"].(map[string]any)["tools"].([]any); len(tools) != 2 {
		t.Errorf("tools %v", tools)
	}
	text := msgs[2]["result"].(map[string]any)["content"].([]any)[0].(map[string]any)["text"]
	if text != "eco: hola" {
		t.Errorf("call: %v", text)
	}
	if msgs[3]["result"].(map[string]any)["isError"] != true {
		t.Errorf("a failing tool must answer isError: %v", msgs[3])
	}
	if msgs[4]["error"].(map[string]any)["code"].(float64) != -32601 {
		t.Errorf("unknown method: %v", msgs[4])
	}
	if msgs[5]["error"].(map[string]any)["code"].(float64) != -32700 {
		t.Errorf("parse error: %v", msgs[5])
	}
}
