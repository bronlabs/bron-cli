package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
	"github.com/spf13/cobra"

	sdkauth "github.com/bronlabs/bron-sdk-go/sdk/auth"

	"github.com/bronlabs/bron-cli/internal/config"
	"github.com/bronlabs/bron-cli/internal/output"
	"github.com/bronlabs/bron-cli/internal/util"
)

// newTxSubscribeCmd builds `bron tx subscribe` — a thin WebSocket prototype.
//
// Wire protocol (see libs/sbus/akka-http-tools WebsocketHandlerDirective):
//   - WS endpoint: <wss://api-host>/ws
//   - To subscribe: send a JSON envelope { method:"SUBSCRIBE", uri:"/...", headers:{...}, body:{...} }
//   - The server pushes JSON envelopes back: { status, headers, body } (body is a JSON string)
//   - To unsubscribe: same envelope with method:"UNSUBSCRIBE" and the same Correlation-Id
//
// Auth: the JWT signs (method + uri + body + iat + kid) — same scheme as HTTP,
// just on the SUBSCRIBE envelope rather than a request line.
func newTxSubscribeCmd(gf *globalFlags) *cobra.Command {
	var (
		accountID     string
		statuses      string
		txTypes       string
		correlationID string
	)
	cmd := &cobra.Command{
		Use:   "subscribe",
		Short: "Stream transaction updates over WebSocket (prototype)",
		Long: `Stream transaction updates from the Bron public API over a WebSocket.

The CLI connects to wss://<api-host>/ws, sends a SUBSCRIBE envelope with the
configured filters, and prints each pushed transaction as one JSON line on
stdout (newline-delimited, easy to pipe to jq / awk / fzf).

This is a prototype — kept intentionally small. Existing list-endpoint filters
work the same way: --account, --statuses, --transactionTypes.`,
		Example: `  bron tx subscribe
  bron tx subscribe --statuses signing-required,waiting-approval
  bron tx subscribe --account <accountId> --transactionTypes withdrawal,bridge
  bron tx subscribe | jq 'select(.status=="signed") | .transactionId'`,
		RunE: func(c *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			profile, err := cfg.Resolve(gf.profile)
			if err != nil {
				return err
			}
			workspace := firstNonEmpty(gf.workspace, profile.Workspace)
			if workspace == "" {
				return fmt.Errorf("workspace not set")
			}

			keyPath, err := util.Expand(profile.KeyFile)
			if err != nil {
				return err
			}
			keyBytes, err := os.ReadFile(keyPath)
			if err != nil {
				return fmt.Errorf("read key file %s: %w", keyPath, err)
			}
			keyJSON := strings.TrimSpace(string(keyBytes))

			var jwk map[string]interface{}
			if err := json.Unmarshal([]byte(keyJSON), &jwk); err != nil {
				return fmt.Errorf("parse JWK: %w", err)
			}
			kid, _ := jwk["kid"].(string)
			if kid == "" {
				return fmt.Errorf("JWK file %s missing 'kid' field — server rejects unsigned-with-empty-kid tokens", keyPath)
			}

			subscribeURI := fmt.Sprintf("/workspaces/%s/transactions", url.PathEscape(workspace))
			body := map[string]interface{}{}
			if accountID != "" {
				body["accountId"] = accountID
			}
			if statuses != "" {
				body["transactionStatuses"] = splitCSV(statuses)
			}
			if txTypes != "" {
				body["transactionTypes"] = splitCSV(txTypes)
			}
			bodyBytes, _ := json.Marshal(body)

			iat := time.Now().Unix()
			token, err := sdkauth.GenerateBronJwt(sdkauth.BronJwtOptions{
				Method:     "SUBSCRIBE",
				Path:       subscribeURI,
				Body:       string(bodyBytes),
				Kid:        kid,
				PrivateKey: keyJSON,
				Iat:        &iat,
			})
			if err != nil {
				return fmt.Errorf("sign JWT: %w", err)
			}

			wsURL := httpToWs(profile.BaseURL) + "/ws"
			fmt.Fprintf(os.Stderr, "connecting to %s ...\n", wsURL)
			dialer := *websocket.DefaultDialer
			dialer.HandshakeTimeout = 10 * time.Second
			// Mirror the HTTP client's proxy chain: explicit profile.Proxy wins,
			// otherwise fall back to HTTP_PROXY / HTTPS_PROXY env vars. Required
			// by the corporate proxy contract documented in ops/proxy.md.
			if profile.Proxy != "" {
				u, err := url.Parse(profile.Proxy)
				if err != nil {
					return fmt.Errorf("parse proxy URL %q: %w", profile.Proxy, err)
				}
				dialer.Proxy = http.ProxyURL(u)
			} else {
				dialer.Proxy = http.ProxyFromEnvironment
			}
			conn, _, err := dialer.DialContext(c.Context(), wsURL, nil)
			if err != nil {
				return fmt.Errorf("dial websocket: %w", err)
			}

			if correlationID == "" {
				correlationID = newCorrelationID()
			}

			// gorilla/websocket explicitly forbids concurrent writes; serialize
			// every WriteJSON through writeMu, plus a fresh WriteDeadline so a
			// stalled server can't hang the CLI forever.
			var writeMu sync.Mutex
			writeJSON := func(v interface{}) error {
				writeMu.Lock()
				defer writeMu.Unlock()
				_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
				return conn.WriteJSON(v)
			}

			subEnvelope := map[string]interface{}{
				"method": "SUBSCRIBE",
				"uri":    subscribeURI,
				"headers": map[string]string{
					"Correlation-Id": correlationID,
					"Authorization":  "ApiKey " + token,
					"Content-Type":   "application/json",
				},
				"body": body,
			}
			if err := writeJSON(subEnvelope); err != nil {
				_ = conn.Close()
				return fmt.Errorf("send subscribe: %w", err)
			}
			fmt.Fprintf(os.Stderr, "subscribed (correlation=%s); Ctrl-C to exit\n", correlationID)

			ctx, cancel := signal.NotifyContext(c.Context(), os.Interrupt, syscall.SIGTERM)

			// Shutdown coordinator: when ctx.Done fires, send UNSUBSCRIBE through
			// the writer mutex, then close the conn. conn.Close is concurrent-safe
			// with ReadMessage (gorilla/websocket allows it from another goroutine),
			// which unblocks the read loop. SetReadDeadline would NOT be safe — it's
			// a read-side method and racing the active reader.
			//
			// Defer ordering matters: we want cancel → wait-for-goroutine → ensure-close,
			// in that order, so a non-signal exit path (read error, server hangup) still
			// signals the goroutine, waits for it to finish writing UNSUBSCRIBE, and only
			// then unwinds. LIFO means the single combined defer below runs FIRST, the
			// safety-net Close runs SECOND (no-op if already closed).
			done := make(chan struct{})
			go func() {
				defer close(done)
				<-ctx.Done()
				_ = writeJSON(map[string]interface{}{
					"method":  "UNSUBSCRIBE",
					"uri":     subscribeURI,
					"headers": map[string]string{"Correlation-Id": correlationID},
				})
				_ = conn.Close()
			}()
			defer conn.Close()
			defer func() {
				cancel()
				<-done
			}()

			enc := json.NewEncoder(os.Stdout)
			for {
				if ctx.Err() != nil {
					return nil
				}
				_, raw, err := conn.ReadMessage()
				if err != nil {
					if ctx.Err() != nil {
						return nil
					}
					if isExpectedClose(err) {
						return nil
					}
					return fmt.Errorf("read: %w", err)
				}
				// Tolerant decoding: status may be int or string ("200"); body may be a
				// JSON string (double-encoded), a JSON object, or a top-level field.
				var env map[string]json.RawMessage
				if err := json.Unmarshal(raw, &env); err != nil {
					// Don't dump arbitrary server bytes verbatim — encoder
					// neutralises ANSI/control chars in case TLS were ever bypassed.
					_ = enc.Encode(json.RawMessage(raw))
					continue
				}
				if statusRaw, ok := env["status"]; ok {
					var statusCode int
					_ = json.Unmarshal(statusRaw, &statusCode)
					if statusCode == 0 {
						var s string
						_ = json.Unmarshal(statusRaw, &s)
						_, _ = fmt.Sscanf(s, "%d", &statusCode)
					}
					if statusCode >= 400 {
						return fmt.Errorf("subscribe failed (status=%d): %s", statusCode, string(env["body"]))
					}
				}
				body := env["body"]
				if body == nil {
					_ = enc.Encode(json.RawMessage(raw))
					continue
				}
				if len(body) > 0 && body[0] == '"' {
					var bodyStr string
					if err := json.Unmarshal(body, &bodyStr); err == nil {
						body = json.RawMessage(bodyStr)
					}
				}
				var payload struct {
					Transactions []json.RawMessage `json:"transactions"`
				}
				if err := json.Unmarshal(body, &payload); err == nil && len(payload.Transactions) > 0 {
					for _, tx := range payload.Transactions {
						var txObj interface{}
						if err := json.Unmarshal(tx, &txObj); err == nil {
							_ = enc.Encode(output.HumanizeDates(txObj))
						} else {
							_ = enc.Encode(json.RawMessage(tx))
						}
					}
					continue
				}
				var bodyObj interface{}
				if err := json.Unmarshal(body, &bodyObj); err == nil {
					_ = enc.Encode(output.HumanizeDates(bodyObj))
				} else {
					_ = enc.Encode(json.RawMessage(body))
				}
			}
		},
	}
	cmd.Flags().StringVar(&accountID, "accountId", "", "filter by account ID")
	cmd.Flags().StringVar(&statuses, "transactionStatuses", "", "comma-separated status filter (e.g. signing-required,waiting-approval)")
	cmd.Flags().StringVar(&txTypes, "transactionTypes", "", "comma-separated transactionType filter (e.g. withdrawal,bridge)")
	cmd.Flags().StringVar(&correlationID, "correlation-id", "", "correlation id for this subscription (auto-generated when empty)")
	return cmd
}

// newCorrelationID returns a 32-char hex token (128 bits of entropy) prefixed
// with `cli-`. Hex is preferable to UnixNano() because log pipelines that
// expect UUID-shaped trace ids parse hex tokens cleanly. Falls back to
// UnixNano() only if crypto/rand is unavailable (effectively never).
func newCorrelationID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("cli-%d", time.Now().UnixNano())
	}
	return "cli-" + hex.EncodeToString(b[:])
}

func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func httpToWs(base string) string {
	switch {
	case strings.HasPrefix(base, "https://"):
		return "wss://" + strings.TrimPrefix(base, "https://")
	case strings.HasPrefix(base, "http://"):
		return "ws://" + strings.TrimPrefix(base, "http://")
	}
	return base
}

func isExpectedClose(err error) bool {
	if err == nil {
		return true
	}
	if ne, ok := err.(net.Error); ok && ne.Timeout() {
		return true
	}
	if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
		return true
	}
	return false
}
