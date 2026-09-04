package p2p

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Client talks to a remote coordinator's /v1/net endpoints.
type Client struct {
	HTTP *http.Client
}

func NewClient() *Client {
	return &Client{
		HTTP: &http.Client{Timeout: 15 * time.Second},
	}
}

// NewClientTLS returns a client that can talk to HTTPS peers.
// If insecure is true, certificate verification is skipped (self-signed / --dev).
func NewClientTLS(insecure bool) *Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: insecure} //nolint:gosec // intentional for --dev self-signed
	return &Client{
		HTTP: &http.Client{Timeout: 15 * time.Second, Transport: tr},
	}
}

func (c *Client) Hello(ctx context.Context, base string, req Hello) (Hello, error) {
	var out Hello
	if err := c.post(ctx, base, "/v1/net/hello", req, &out); err != nil {
		return Hello{}, err
	}
	return out, nil
}

func (c *Client) Pair(ctx context.Context, base string, req PairRequest) (Hello, error) {
	var out Hello
	if err := c.post(ctx, base, "/v1/net/pair", req, &out); err != nil {
		return Hello{}, err
	}
	return out, nil
}

func (c *Client) Sync(ctx context.Context, base string, req SyncMessage) (SyncMessage, error) {
	var out SyncMessage
	if err := c.post(ctx, base, "/v1/net/sync", req, &out); err != nil {
		return SyncMessage{}, err
	}
	return out, nil
}

// PostJSON POSTs JSON to a peer path. Consensus uses this for propose/tx/commit.
func (c *Client) PostJSON(ctx context.Context, base, path string, in, out any) error {
	return c.post(ctx, base, path, in, out)
}

func (c *Client) post(ctx context.Context, base, path string, in, out any) error {
	base = NormalizeURL(base)
	body, err := json.Marshal(in)
	if err != nil {
		return err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, base+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("%s%s: HTTP %d: %s", base, path, resp.StatusCode, string(data))
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("decode %s%s: %w", base, path, err)
	}
	return nil
}
