package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Client talks to a running daemon through its control port.
type Client struct {
	base  string
	token string
	http  *http.Client
}

// Connect finds the running daemon of an installation, or returns an error when none answers.
func Connect(home string) (*Client, error) {
	c, err := ReadControl(home)
	if err != nil {
		return nil, fmt.Errorf("Nido no está corriendo")
	}
	cl := &Client{base: fmt.Sprintf("http://127.0.0.1:%d", c.Port), token: c.Token, http: &http.Client{Timeout: 10 * time.Minute}}
	var st Status
	if err := cl.Call("GET", "/api/status", nil, &st); err != nil {
		return nil, fmt.Errorf("Nido no está corriendo")
	}
	return cl, nil
}

// Call sends a request and decodes the JSON answer into out (when not nil).
func (c *Client) Call(method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, c.base+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("X-Nido-Token", c.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		var e struct{ Error string }
		if json.Unmarshal(data, &e) == nil && e.Error != "" {
			return fmt.Errorf("%s", e.Error)
		}
		return fmt.Errorf("%s: %s", resp.Status, bytes.TrimSpace(data))
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}
