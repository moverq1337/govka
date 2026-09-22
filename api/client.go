// Package api is the low-level transport for VK API methods: it encodes
// parameters, sends the request with a Bearer token, decodes the response
// envelope and converts VK errors into *Error.
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Defaults used when Config fields are zero.
const (
	DefaultEndpoint = "https://api.vk.ru/method/"
	DefaultVersion  = "5.199"
	// DefaultRPS is the documented limit for user tokens.
	DefaultRPS = 3
	// DefaultMaxRetries bounds retries of transient errors (codes 1, 6, 10).
	DefaultMaxRetries = 3
)

// TokenSource supplies the access token for each call. It lets the owner
// of the session refresh tokens without the transport knowing about VK ID.
type TokenSource interface {
	Token(ctx context.Context) (string, error)
}

// StaticToken is a TokenSource that always returns the same token.
type StaticToken string

// Token implements TokenSource.
func (s StaticToken) Token(context.Context) (string, error) { return string(s), nil }

// Config configures a Client.
type Config struct {
	// Token supplies the access token. Required.
	Token TokenSource
	// HTTPClient defaults to a client with a 30 s timeout.
	HTTPClient *http.Client
	// Endpoint defaults to DefaultEndpoint.
	Endpoint string
	// Version is the API version sent as v. Defaults to DefaultVersion.
	Version string
	// Lang is sent as the lang parameter when set.
	Lang string
	// RPS caps outgoing requests per second. 0 means DefaultRPS, a negative
	// value disables the limiter.
	RPS int
	// MaxRetries bounds retries of transient errors. 0 means
	// DefaultMaxRetries, a negative value disables retries.
	MaxRetries int
	// UserAgent overrides the User-Agent header.
	UserAgent string
	// Sleep is used between retries; tests replace it.
	Sleep func(ctx context.Context, d time.Duration) error
}

// Client calls VK API methods.
type Client struct {
	cfg     Config
	limiter *limiter
}

// New returns a Client for cfg.
func New(cfg Config) (*Client, error) {
	if cfg.Token == nil {
		return nil, errors.New("api: Config.Token is required")
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 30 * time.Second}
	}
	if cfg.Endpoint == "" {
		cfg.Endpoint = DefaultEndpoint
	}
	if !strings.HasSuffix(cfg.Endpoint, "/") {
		cfg.Endpoint += "/"
	}
	if cfg.Version == "" {
		cfg.Version = DefaultVersion
	}
	if cfg.RPS == 0 {
		cfg.RPS = DefaultRPS
	}
	if cfg.MaxRetries == 0 {
		cfg.MaxRetries = DefaultMaxRetries
	}
	if cfg.UserAgent == "" {
		cfg.UserAgent = "govka (+https://github.com/moverq1337/govka)"
	}
	if cfg.Sleep == nil {
		cfg.Sleep = sleep
	}
	c := &Client{cfg: cfg}
	if cfg.RPS > 0 {
		c.limiter = newLimiter(cfg.RPS)
	}
	return c, nil
}

// Version returns the API version the client sends.
func (c *Client) Version() string { return c.cfg.Version }

// HTTPClient returns the underlying HTTP client.
func (c *Client) HTTPClient() *http.Client { return c.cfg.HTTPClient }

// Call invokes method with params and decodes the "response" field into
// out (which may be nil). VK errors are returned as *Error. Transient
// errors are retried with backoff up to Config.MaxRetries times.
func (c *Client) Call(ctx context.Context, method string, params Params, out any) error {
	raw, err := c.CallRaw(ctx, method, params)
	if err != nil {
		return err
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("vk api: %s: decode response: %w", method, err)
	}
	return nil
}

// CallRaw is Call without decoding: it returns the raw "response" JSON.
func (c *Client) CallRaw(ctx context.Context, method string, params Params) (json.RawMessage, error) {
	env, err := c.callEnvelope(ctx, method, params)
	if err != nil {
		return nil, err
	}
	return env.Response, nil
}

// callEnvelope performs the request with retries and returns the decoded
// envelope of a successful response.
func (c *Client) callEnvelope(ctx context.Context, method string, params Params) (*envelope, error) {
	if method == "" {
		return nil, errors.New("vk api: empty method name")
	}
	for attempt := 0; ; attempt++ {
		env, err := c.do(ctx, method, params)
		if err == nil {
			return env, nil
		}
		if c.cfg.MaxRetries < 0 || attempt >= c.cfg.MaxRetries || !retryable(err) {
			return nil, err
		}
		if err := c.cfg.Sleep(ctx, backoff(attempt)); err != nil {
			return nil, err
		}
	}
}

func retryable(err error) bool {
	if e, ok := AsError(err); ok {
		return e.Retryable()
	}
	var te interface{ Timeout() bool }
	if errors.As(err, &te) && te.Timeout() {
		return true
	}
	return false
}

func backoff(attempt int) time.Duration {
	d := time.Second << uint(attempt)
	if d > 10*time.Second {
		d = 10 * time.Second
	}
	return d
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func (c *Client) do(ctx context.Context, method string, params Params) (*envelope, error) {
	token, err := c.cfg.Token.Token(ctx)
	if err != nil {
		return nil, fmt.Errorf("vk api: %s: token: %w", method, err)
	}
	if c.limiter != nil {
		if err := c.limiter.wait(ctx); err != nil {
			return nil, err
		}
	}
	form := params.Values()
	form.Set("v", c.cfg.Version)
	if c.cfg.Lang != "" && form.Get("lang") == "" {
		form.Set("lang", c.cfg.Lang)
	}
	body := strings.NewReader(form.Encode())
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.Endpoint+method, body)
	if err != nil {
		return nil, fmt.Errorf("vk api: %s: build request: %w", method, err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.cfg.UserAgent)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := c.cfg.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("vk api: %s: %w", method, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, fmt.Errorf("vk api: %s: read body: %w", method, err)
	}
	return decodeEnvelope(method, resp.StatusCode, data)
}

type envelope struct {
	Response      json.RawMessage `json:"response"`
	Error         *Error          `json:"error"`
	ExecuteErrors ExecuteErrors   `json:"execute_errors,omitempty"`
}

func decodeEnvelope(method string, status int, data []byte) (*envelope, error) {
	var env envelope
	if err := json.Unmarshal(bytes.TrimSpace(data), &env); err != nil {
		if status != http.StatusOK {
			return nil, fmt.Errorf("vk api: %s: http %d: %s", method, status, truncate(data))
		}
		return nil, fmt.Errorf("vk api: %s: decode envelope: %w", method, err)
	}
	if env.Error != nil {
		env.Error.Method = method
		return nil, env.Error
	}
	if env.Response == nil && len(env.ExecuteErrors) == 0 {
		if status != http.StatusOK {
			return nil, fmt.Errorf("vk api: %s: http %d: %s", method, status, truncate(data))
		}
		return nil, fmt.Errorf("vk api: %s: response field missing", method)
	}
	return &env, nil
}

func truncate(b []byte) string {
	const n = 200
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "..."
}
