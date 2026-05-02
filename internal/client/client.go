package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	sdkhttp "github.com/bronlabs/bron-sdk-go/sdk/http"

	"github.com/bronlabs/bron-cli/internal/config"
)

type Client struct {
	http        *sdkhttp.Client
	WorkspaceID string
}

// New builds an HTTP client backed by bron-sdk-go using the resolved profile.
// The private key (JWK) comes from `config.Profile.LoadKey` — either inline
// from $BRON_API_KEY or read from KeyFile on disk. Workspace ID is interpolated
// into request paths via Do().
func New(p *config.Profile) (*Client, error) {
	if p.Workspace == "" {
		return nil, fmt.Errorf("workspace not set (configure ~/.config/bron/config.yaml or pass --workspace)")
	}
	return newClient(p)
}

// NewForBootstrap is the workspace-less variant of New, intended for the
// `bron config init` flow before we know which workspace this key is bound
// to. Right for paths that don't carry `{workspaceId}` (e.g. GET /workspaces);
// any path that needs interpolation will produce an unresolved literal,
// which is the desired hard-fail signal for misuse.
func NewForBootstrap(p *config.Profile) (*Client, error) { return newClient(p) }

func newClient(p *config.Profile) (*Client, error) {
	keyBytes, err := p.LoadKey()
	if err != nil {
		return nil, err
	}

	httpClient, err := BuildHTTPClient(p.Proxy)
	if err != nil {
		return nil, err
	}
	hc := sdkhttp.NewClientWithHTTP(p.BaseURL, strings.TrimSpace(string(keyBytes)), httpClient)
	return &Client{http: hc, WorkspaceID: p.Workspace}, nil
}

// BuildHTTPClient returns an *http.Client whose Transport is a clone of
// http.DefaultTransport with proxy resolution wired in:
//   - if proxyURL is set, all traffic goes through it (supports user:pass@host:port)
//   - otherwise, falls back to HTTP_PROXY / HTTPS_PROXY / NO_PROXY env vars
//
// Exported so the MCP path can reuse the same transport (auth-validated proxy,
// scheme/host check) when building the bron-sdk-go realtime client. Without
// this the SDK's own proxy-string handling skips the validation pass.
func BuildHTTPClient(proxyURL string) (*http.Client, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = http.ProxyFromEnvironment
	if proxyURL != "" {
		u, err := url.Parse(proxyURL)
		if err != nil {
			return nil, fmt.Errorf("parse proxy URL %q: %w", redactProxy(proxyURL), err)
		}
		// url.Parse accepts "host:8080" without complaining and produces an
		// empty Host — which silently drops the proxy and falls back to env.
		if u.Scheme == "" || u.Host == "" {
			return nil, fmt.Errorf("proxy URL %q must include scheme and host (e.g. http://user:pass@host:8080)", redactProxy(proxyURL))
		}
		transport.Proxy = http.ProxyURL(u)
	}
	return &http.Client{Timeout: 30 * time.Second, Transport: transport}, nil
}

// redactProxy strips userinfo (user:password@) from a proxy URL so the
// password never lands in error messages, logs, or panic traces. Two paths:
//
//  1. url.Parse recognises a userinfo block (`u.User != nil`) — let
//     url.Redacted handle it; this gets the canonical "scheme://user:xxxxx@host"
//     output for well-formed URLs.
//  2. The string contains an `@` but url.Parse didn't recover userinfo
//     (malformed URL — no scheme, ambiguous shape, etc.). Manual scrub:
//     replace everything before the last `@` with `***`. Without this fall-
//     through `url.Parse("user:pass@host:8080")` returns `Scheme=user` and
//     `User=nil`, so Redacted would echo the password verbatim.
func redactProxy(raw string) string {
	if raw == "" {
		return raw
	}
	if u, err := url.Parse(raw); err == nil && u.User != nil {
		return u.Redacted()
	}
	if at := strings.LastIndexByte(raw, '@'); at >= 0 {
		scheme := ""
		if i := strings.Index(raw, "://"); i >= 0 {
			scheme = raw[:i+3]
		}
		return scheme + "***@" + raw[at+1:]
	}
	return raw
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
