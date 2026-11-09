// Package mcp is a minimal Model Context Protocol server over stdio: newline-delimited JSON-RPC 2.0,
// with tools only. 'nido mcp' uses it so AI editors (Claude Code, Cursor, VS Code, Windsurf, Codex)
// can read and drive the running Nido.
package mcp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"sync"
)

// ProtocolVersion is the newest MCP revision this server speaks; an older one asked by the client is
// answered with that one.
const ProtocolVersion = "2025-06-18"

var supported = map[string]bool{"2024-11-05": true, "2025-03-26": true, "2025-06-18": true}

// Tool is one thing the model can call.
type Tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
	// Call runs the tool with its arguments and returns text for the model; an error becomes a tool
	// result with isError, which the model reads, not a protocol error.
	Call func(args map[string]any) (string, error) `json:"-"`
}

// Server answers one client.
type Server struct {
	Name, Version, Instructions string
	Tools                       []Tool

	mu  sync.Mutex
	out io.Writer
}

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Serve reads requests from in until it ends and writes the answers to out.
func (s *Server) Serve(in io.Reader, out io.Writer) error {
	s.out = out
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 64<<10), 16<<20)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var req request
		if err := json.Unmarshal(line, &req); err != nil {
			s.send(json.RawMessage("null"), nil, &rpcError{Code: -32700, Message: "JSON no válido"})
			continue
		}
		s.handle(req)
	}
	return sc.Err()
}

func (s *Server) handle(req request) {
	isNotification := len(req.ID) == 0
	result, err := s.dispatch(req)
	if isNotification {
		return
	}
	s.send(req.ID, result, err)
}

func (s *Server) dispatch(req request) (any, *rpcError) {
	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		json.Unmarshal(req.Params, &p)
		version := ProtocolVersion
		if supported[p.ProtocolVersion] {
			version = p.ProtocolVersion
		}
		return map[string]any{
			"protocolVersion": version,
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]any{"name": s.Name, "version": s.Version},
			"instructions":    s.Instructions,
		}, nil
	case "notifications/initialized", "notifications/cancelled":
		return nil, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		return map[string]any{"tools": s.Tools}, nil
	case "tools/call":
		var p struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, &rpcError{Code: -32602, Message: "parámetros no válidos"}
		}
		for _, t := range s.Tools {
			if t.Name != p.Name {
				continue
			}
			if p.Arguments == nil {
				p.Arguments = map[string]any{}
			}
			text, err := safeCall(t, p.Arguments)
			if err != nil {
				return map[string]any{"content": []any{map[string]any{"type": "text", "text": err.Error()}}, "isError": true}, nil
			}
			return map[string]any{"content": []any{map[string]any{"type": "text", "text": text}}}, nil
		}
		return nil, &rpcError{Code: -32602, Message: "herramienta desconocida: " + p.Name}
	}
	return nil, &rpcError{Code: -32601, Message: "método desconocido: " + req.Method}
}

// safeCall turns a panic in a tool into an error the model can read.
func safeCall(t Tool, args map[string]any) (text string, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%s falló: %v", t.Name, r)
		}
	}()
	return t.Call(args)
}

func (s *Server) send(id json.RawMessage, result any, e *rpcError) {
	msg := map[string]any{"jsonrpc": "2.0", "id": id}
	if e != nil {
		msg["error"] = e
	} else {
		if result == nil {
			result = map[string]any{}
		}
		msg["result"] = result
	}
	data, _ := json.Marshal(msg)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.out.Write(append(data, '\n'))
}

// Schema builds a JSON schema for an object with string, integer or boolean properties; required
// lists the names that must be given. props maps a name to "string:description" (or integer:,
// boolean:).
func Schema(required []string, props map[string]string) map[string]any {
	p := map[string]any{}
	for name, spec := range props {
		typ, desc := "string", spec
		for _, t := range []string{"string", "integer", "boolean"} {
			if len(spec) > len(t) && spec[:len(t)+1] == t+":" {
				typ, desc = t, spec[len(t)+1:]
			}
		}
		p[name] = map[string]any{"type": typ, "description": desc}
	}
	s := map[string]any{"type": "object", "properties": p}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

// Str reads a string argument ("" when missing).
func Str(args map[string]any, name string) string {
	if v, ok := args[name].(string); ok {
		return v
	}
	if v, ok := args[name].(float64); ok {
		return fmt.Sprint(v)
	}
	return ""
}

// Int reads an integer argument (def when missing).
func Int(args map[string]any, name string, def int) int {
	switch v := args[name].(type) {
	case float64:
		return int(v)
	case string:
		var n int
		if _, err := fmt.Sscan(v, &n); err == nil {
			return n
		}
	}
	return def
}

// Bool reads a boolean argument (def when missing).
func Bool(args map[string]any, name string, def bool) bool {
	if v, ok := args[name].(bool); ok {
		return v
	}
	return def
}
