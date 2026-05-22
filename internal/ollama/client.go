// Package ollama is a minimal HTTP client for the Ollama REST API.
//
// It targets only the endpoints localrouter needs: /api/tags, /api/show,
// /api/pull (non-streaming form), and a TCP-level reachability probe.
package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client talks to a remote Ollama instance.
type Client struct {
	BaseURL *url.URL
	HTTP    *http.Client
}

// New parses rawURL and returns a Client backed by an HTTP client with a
// long timeout suitable for large model pulls.
func New(rawURL string) (*Client, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("parse remote url: %w", err)
	}
	if u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("remote url must include scheme and host: %q", rawURL)
	}
	return &Client{
		BaseURL: u,
		HTTP: &http.Client{
			// No global timeout; per-request contexts handle deadlines so
			// that long-running pulls aren't cut off mid-download.
			Timeout: 0,
		},
	}, nil
}

// Tag describes one row from /api/tags.
type Tag struct {
	Name       string    `json:"name"`
	Model      string    `json:"model"`
	ModifiedAt time.Time `json:"modified_at"`
	Size       int64     `json:"size"`
	Digest     string    `json:"digest"`
	Details    struct {
		Format            string   `json:"format"`
		Family            string   `json:"family"`
		Families          []string `json:"families"`
		ParameterSize     string   `json:"parameter_size"`
		QuantizationLevel string   `json:"quantization_level"`
	} `json:"details"`
}

type tagsResponse struct {
	Models []Tag `json:"models"`
}

// Tags calls GET /api/tags and returns the list of installed models on the
// remote. The caller must supply a context for cancellation.
func (c *Client) Tags(ctx context.Context) ([]Tag, error) {
	req, err := c.newRequest(ctx, http.MethodGet, "/api/tags", nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET /api/tags: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, unexpectedStatus(resp)
	}
	var out tagsResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode /api/tags: %w", err)
	}
	return out.Models, nil
}

// HasModel reports whether the remote currently has the named model
// installed. The match is exact against the "name" field returned by
// /api/tags (Ollama tags are stored as "family:tag", e.g. "llama3:latest").
//
// If the caller passes a tag without an explicit ":tag" suffix, this also
// matches "<name>:latest" so the common shorthand keeps working.
func (c *Client) HasModel(ctx context.Context, name string) (bool, error) {
	tags, err := c.Tags(ctx)
	if err != nil {
		return false, err
	}
	want := name
	wantLatest := ""
	if !strings.Contains(name, ":") {
		wantLatest = name + ":latest"
	}
	for _, t := range tags {
		if t.Name == want || (wantLatest != "" && t.Name == wantLatest) {
			return true, nil
		}
	}
	return false, nil
}

// ShowResponse is a subset of /api/show fields we care about.
type ShowResponse struct {
	Modelfile  string            `json:"modelfile"`
	Parameters string            `json:"parameters"`
	Template   string            `json:"template"`
	Details    map[string]any    `json:"details"`
	ModelInfo  map[string]any    `json:"model_info"`
	License    string            `json:"license"`
	System     string            `json:"system"`
	Messages   []json.RawMessage `json:"messages"`
}

// Show calls POST /api/show for the given model.
func (c *Client) Show(ctx context.Context, name string) (*ShowResponse, error) {
	body, _ := json.Marshal(map[string]string{"name": name})
	req, err := c.newRequest(ctx, http.MethodPost, "/api/show", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("POST /api/show: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return nil, unexpectedStatus(resp)
	}
	var out ShowResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode /api/show: %w", err)
	}
	return &out, nil
}

// ErrNotFound is returned when the remote reports the model does not exist.
var ErrNotFound = errors.New("ollama: model not found")

// Pull asks the remote to download a model.
//
// We always request the non-streaming form (stream:false) so the response is
// a single JSON object. The remote keeps the HTTP connection open for the
// duration of the download — which can be many minutes — so the caller is
// expected to pass a context with an appropriately generous deadline.
//
// onStatus, if non-nil, is invoked once when the request is dispatched so
// the caller can log progress; the underlying API does not stream useful
// progress events in non-stream mode.
func (c *Client) Pull(ctx context.Context, name string, onStatus func(string)) error {
	body, _ := json.Marshal(map[string]any{
		"name":   name,
		"stream": false,
	})
	req, err := c.newRequest(ctx, http.MethodPost, "/api/pull", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if onStatus != nil {
		onStatus(fmt.Sprintf("pull started: model=%q remote=%s", name, c.BaseURL))
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("POST /api/pull: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return unexpectedStatus(resp)
	}
	// Drain the single JSON object. Errors during a pull come back as a
	// 200 with {"error": "..."} so we still need to read and inspect it.
	var summary struct {
		Status string `json:"status"`
		Error  string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&summary); err != nil {
		// Non-JSON 200 is unexpected but not fatal — treat as success
		// after the body is drained.
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	if summary.Error != "" {
		return fmt.Errorf("ollama pull failed: %s", summary.Error)
	}
	if onStatus != nil && summary.Status != "" {
		onStatus(fmt.Sprintf("pull finished: %s", summary.Status))
	}
	return nil
}

// Reachable performs a fast TCP-level health probe against the remote host.
// Used for the `status` subcommand and pre-flight checks. It does NOT call
// the Ollama API; an Ollama instance with a broken /api/tags would still
// report reachable here.
func (c *Client) Reachable(timeout time.Duration) bool {
	host := c.BaseURL.Host
	if !strings.Contains(host, ":") {
		// http://x with no port
		if c.BaseURL.Scheme == "https" {
			host += ":443"
		} else {
			host += ":80"
		}
	}
	conn, err := net.DialTimeout("tcp", host, timeout)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func (c *Client) newRequest(ctx context.Context, method, path string, body io.Reader) (*http.Request, error) {
	u := *c.BaseURL
	u.Path = strings.TrimRight(u.Path, "/") + path
	req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return nil, fmt.Errorf("new request: %w", err)
	}
	return req, nil
}

func unexpectedStatus(resp *http.Response) error {
	snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	msg := strings.TrimSpace(string(snippet))
	if msg == "" {
		return fmt.Errorf("%s %s: %s", resp.Request.Method, resp.Request.URL.Path, resp.Status)
	}
	return fmt.Errorf("%s %s: %s: %s", resp.Request.Method, resp.Request.URL.Path, resp.Status, msg)
}
