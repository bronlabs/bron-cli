package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	sdkhttp "github.com/bronlabs/bron-sdk-go/sdk/http"

	"github.com/bronlabs/bron-cli/internal/config"
	"github.com/bronlabs/bron-cli/internal/util"
)

type Client struct {
	http        *sdkhttp.Client
	WorkspaceID string
}

// New builds an HTTP client backed by bron-sdk-go using the resolved profile.
// The private key (JWK) is read from KeyFile. Workspace ID is interpolated
// into request paths via Do().
func New(p *config.Profile) (*Client, error) {
	if p.Workspace == "" {
		return nil, fmt.Errorf("workspace not set (configure ~/.config/bron/config.yaml or pass --workspace)")
	}
	if p.KeyFile == "" {
		return nil, fmt.Errorf("api key file not set (configure ~/.config/bron/config.yaml or pass --key-file)")
	}
	keyPath, err := util.Expand(p.KeyFile)
	if err != nil {
		return nil, err
	}

	if info, err := os.Stat(keyPath); err == nil {
		// Block (don't warn) on group/world-readable key files. SSH does the
		// same; for a CLI that signs withdrawals it's the only safe default.
		// Permission gives an attacker on a shared host enough to impersonate
		// the workspace and move funds. Fix is one chmod away.
		if info.Mode().Perm()&0o077 != 0 {
			return nil, fmt.Errorf("key file %s has overly permissive mode %#o (group/world readable); run `chmod 600 %s` and retry",
				keyPath, info.Mode().Perm(), keyPath)
		}
	}

	keyBytes, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, fmt.Errorf("read key file %s: %w", keyPath, err)
	}

	httpClient, err := buildHTTPClient(p.Proxy)
	if err != nil {
		return nil, err
	}
	hc := sdkhttp.NewClientWithHTTP(p.BaseURL, strings.TrimSpace(string(keyBytes)), httpClient)
	return &Client{http: hc, WorkspaceID: p.Workspace}, nil
}

// buildHTTPClient returns an *http.Client whose Transport is a clone of
// http.DefaultTransport with proxy resolution wired in:
//   - if proxyURL is set, all traffic goes through it (supports user:pass@host:port)
//   - otherwise, falls back to HTTP_PROXY / HTTPS_PROXY / NO_PROXY env vars
func buildHTTPClient(proxyURL string) (*http.Client, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = http.ProxyFromEnvironment
	if proxyURL != "" {
		u, err := url.Parse(proxyURL)
		if err != nil {
			return nil, fmt.Errorf("parse proxy URL %q: %w", proxyURL, err)
		}
		// url.Parse accepts "host:8080" without complaining and produces an
		// empty Host — which silently drops the proxy and falls back to env.
		if u.Scheme == "" || u.Host == "" {
			return nil, fmt.Errorf("proxy URL %q must include scheme and host (e.g. http://user:pass@host:8080)", proxyURL)
		}
		transport.Proxy = http.ProxyURL(u)
	}
	return &http.Client{Timeout: 30 * time.Second, Transport: transport}, nil
}

// Do executes a request, substituting `{workspaceId}` and any other path
// placeholders (`{accountId}`, `{transactionId}`, ...) before sending.
// All values are URL-escaped via `url.PathEscape`.
//
// Decoding goes through json.Decoder.UseNumber so decimal payloads keep their
// exact wire representation. The default decoder converts every JSON number to
// float64, which truncates large integers and rounds long decimals — fatal for
// balances/amounts where a single trailing digit can be billions of wei. We
// capture the raw response into json.RawMessage and re-decode locally so that
// numeric leaves arrive as json.Number; output formatters then emit them
// verbatim.
func (c *Client) Do(ctx context.Context, method, path string, pathParams map[string]string, body, query, result interface{}) error {
	resolved := strings.ReplaceAll(path, "{workspaceId}", url.PathEscape(c.WorkspaceID))
	for name, val := range pathParams {
		resolved = strings.ReplaceAll(resolved, "{"+name+"}", url.PathEscape(val))
	}
	opts := sdkhttp.RequestOptions{Method: method, Path: resolved, Body: body, Query: query}
	if result == nil {
		return c.http.RequestWithContext(ctx, nil, opts)
	}
	var raw json.RawMessage
	if err := c.http.RequestWithContext(ctx, &raw, opts); err != nil {
		return err
	}
	if len(raw) == 0 {
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	return dec.Decode(result)
}
