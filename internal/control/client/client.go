// Package client controls an existing daemon; it never opens controller state.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"
)

type Client struct {
	http      *http.Client
	transport *http.Transport
}

func New(socket string) *Client {
	t := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "unix", socket)
	}, DisableCompression: true}
	return &Client{http: &http.Client{Transport: t, Timeout: 30 * time.Second}, transport: t}
}
func (c *Client) Close() { c.transport.CloseIdleConnections() }
func (c *Client) Do(ctx context.Context, method, path string, body any) (json.RawMessage, error) {
	var data []byte
	var e error
	if body != nil {
		data, e = json.Marshal(body)
		if e != nil {
			return nil, e
		}
	}
	req, e := http.NewRequestWithContext(ctx, method, "http://daemon"+path, bytes.NewReader(data))
	if e != nil {
		return nil, e
	}
	req.Header.Set("Content-Type", "application/json")
	r, e := c.http.Do(req)
	if e != nil {
		return nil, fmt.Errorf("connect to controller (is kee-route-managerd running?): %w", e)
	}
	defer r.Body.Close()
	data, e = io.ReadAll(io.LimitReader(r.Body, 8<<20))
	if e != nil {
		return nil, e
	}
	if r.StatusCode >= 400 {
		return nil, fmt.Errorf("controller returned HTTP %d: %s", r.StatusCode, data)
	}
	return json.RawMessage(data), nil
}
