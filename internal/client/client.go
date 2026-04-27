package client

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"

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
		// Warn (do not block) if the key file is group/world readable.
		// Industry practice is mixed (SSH blocks, AWS doesn't); we surface
		// the risk but trust the user's filesystem setup.
		if info.Mode().Perm()&0o077 != 0 {
			fmt.Fprintf(os.Stderr, "warning: key file %s has overly permissive mode %#o; recommended: chmod 600\n",
				keyPath, info.Mode().Perm())
		}
	}

	keyBytes, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, fmt.Errorf("read key file %s: %w", keyPath, err)
	}

	hc := sdkhttp.NewClient(p.BaseURL, strings.TrimSpace(string(keyBytes)))
	return &Client{http: hc, WorkspaceID: p.Workspace}, nil
}

// Do executes a request, substituting `{workspaceId}` and any other path
// placeholders (`{accountId}`, `{transactionId}`, ...) before sending.
// All values are URL-escaped via `url.PathEscape`.
// body / query / result are passed straight through to bron-sdk-go.
func (c *Client) Do(ctx context.Context, method, path string, pathParams map[string]string, body, query, result interface{}) error {
	resolved := strings.ReplaceAll(path, "{workspaceId}", url.PathEscape(c.WorkspaceID))
	for name, val := range pathParams {
		resolved = strings.ReplaceAll(resolved, "{"+name+"}", url.PathEscape(val))
	}
	return c.http.RequestWithContext(ctx, result, sdkhttp.RequestOptions{
		Method: method,
		Path:   resolved,
		Body:   body,
		Query:  query,
	})
}
