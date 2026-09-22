package longpoll

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/moverq1337/govka/api"
)

// ErrBadVersion is returned when the server rejects the protocol version
// (failed: 4).
var ErrBadVersion = errors.New("longpoll: unsupported protocol version")

// Config configures a Poller. Zero fields take the defaults above.
type Config struct {
	Version int
	Mode    int
	Wait    int
	// GroupID makes the poll return community messages for a user token.
	GroupID int64
	// HTTPClient is used for the poll requests. It must not have a Timeout
	// shorter than Wait+10 s; the poller applies its own per-request
	// deadline.
	HTTPClient *http.Client
	// Logger receives debug/warn messages. Defaults to slog.Default().
	Logger *slog.Logger
	// MaxBackoff caps the delay between retries after network errors.
	MaxBackoff time.Duration
	// Sleep is used for backoff; tests replace it.
	Sleep func(ctx context.Context, d time.Duration) error
}

func (c Config) withDefaults() Config {
	if c.Version == 0 {
		c.Version = DefaultVersion
	}
	if c.Mode == 0 {
		c.Mode = DefaultMode
	}
	if c.Wait <= 0 {
		c.Wait = DefaultWait
	}
	if c.Wait > MaxWait {
		c.Wait = MaxWait
	}
	if c.HTTPClient == nil {
		c.HTTPClient = &http.Client{}
	}
	if c.Logger == nil {
		c.Logger = slog.Default()
	}
	if c.MaxBackoff <= 0 {
		c.MaxBackoff = 30 * time.Second
	}
	if c.Sleep == nil {
		c.Sleep = sleep
	}
	return c
}

// Handler receives decoded events. It runs on the poll goroutine.
type Handler func(evt any)

// Poller runs the long poll loop.
type Poller struct {
	api *api.Client
	cfg Config

	mu     sync.Mutex
	server *Server
	ts     string
	pts    string
}

// New returns a Poller that uses c for API calls.
func New(c *api.Client, cfg Config) *Poller {
	return &Poller{api: c, cfg: cfg.withDefaults()}
}

// TS returns the last acknowledged ts.
func (p *Poller) TS() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.ts
}

// PTS returns the last pts (only with ModePts).
func (p *Poller) PTS() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.pts
}

// Run polls until ctx is done or a fatal error occurs. Fatal errors are
// ErrBadVersion and API errors that cannot be retried (for example
// authorization failure); everything else is retried with backoff. Each
// decoded event is passed to h. Run returns ctx.Err() on cancellation.
func (p *Poller) Run(ctx context.Context, h Handler) error {
	failures := 0
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		srv, err := p.ensureServer(ctx, false)
		if err != nil {
			if fatal(err) {
				return err
			}
			failures++
			if err := p.backoff(ctx, failures, err); err != nil {
				return err
			}
			continue
		}
		resp, err := p.poll(ctx, srv)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			failures++
			if err := p.backoff(ctx, failures, err); err != nil {
				return err
			}
			continue
		}
		failures = 0
		if resp.Failed != 0 {
			if err := p.handleFailed(ctx, resp); err != nil {
				return err
			}
			continue
		}
		p.mu.Lock()
		p.ts = resp.TS.String()
		if resp.PTS != "" {
			p.pts = resp.PTS.String()
		}
		p.mu.Unlock()
		for _, raw := range resp.Updates {
			h(Decode(raw))
		}
	}
}

func (p *Poller) ensureServer(ctx context.Context, force bool) (*Server, error) {
	p.mu.Lock()
	if p.server != nil && !force {
		s := *p.server
		s.TS = json.Number(p.ts)
		p.mu.Unlock()
		return &s, nil
	}
	p.mu.Unlock()
	srv, err := GetServer(ctx, p.api, p.cfg.Version, p.cfg.GroupID, p.cfg.Mode&ModePts != 0)
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	p.server = srv
	p.ts = srv.TS.String()
	if srv.PTS != "" {
		p.pts = srv.PTS.String()
	}
	s := *srv
	p.mu.Unlock()
	return &s, nil
}

// refreshServer re-fetches key/server; when keepTS is true the current ts
// is preserved (failed: 2), otherwise the server's ts is taken (failed: 3).
func (p *Poller) refreshServer(ctx context.Context, keepTS bool) error {
	p.mu.Lock()
	oldTS := p.ts
	p.mu.Unlock()
	if _, err := p.ensureServer(ctx, true); err != nil {
		return err
	}
	if keepTS && oldTS != "" {
		p.mu.Lock()
		p.ts = oldTS
		p.mu.Unlock()
	}
	return nil
}

type response struct {
	TS         json.Number       `json:"ts"`
	PTS        json.Number       `json:"pts"`
	Updates    []json.RawMessage `json:"updates"`
	Failed     int               `json:"failed"`
	MinVersion int               `json:"min_version"`
	MaxVersion int               `json:"max_version"`
}

func (p *Poller) poll(ctx context.Context, srv *Server) (*response, error) {
	q := url.Values{}
	q.Set("act", "a_check")
	q.Set("key", srv.Key)
	q.Set("ts", srv.TS.String())
	q.Set("wait", strconv.Itoa(p.cfg.Wait))
	q.Set("mode", strconv.Itoa(p.cfg.Mode))
	q.Set("version", strconv.Itoa(p.cfg.Version))
	u := srv.URL() + "?" + q.Encode()

	ctx, cancel := context.WithTimeout(ctx, time.Duration(p.cfg.Wait+10)*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("longpoll: build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	res, err := p.cfg.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("longpoll: %w", err)
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, 16<<20))
	if err != nil {
		return nil, fmt.Errorf("longpoll: read body: %w", err)
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("longpoll: http %d", res.StatusCode)
	}
	var r response
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("longpoll: decode: %w", err)
	}
	return &r, nil
}

func (p *Poller) handleFailed(ctx context.Context, r *response) error {
	switch r.Failed {
	case 1:
		p.mu.Lock()
		if r.TS != "" {
			p.ts = r.TS.String()
		}
		p.mu.Unlock()
		p.cfg.Logger.Debug("longpoll: history outdated, ts updated", "ts", r.TS.String())
		return nil
	case 2:
		p.cfg.Logger.Debug("longpoll: key expired, refreshing server")
		return p.retryRefresh(ctx, true)
	case 3:
		p.cfg.Logger.Debug("longpoll: user info lost, refreshing server and ts")
		return p.retryRefresh(ctx, false)
	case 4:
		return fmt.Errorf("%w: server accepts %d..%d, requested %d", ErrBadVersion, r.MinVersion, r.MaxVersion, p.cfg.Version)
	default:
		p.cfg.Logger.Warn("longpoll: unknown failed code, refreshing server", "failed", r.Failed)
		return p.retryRefresh(ctx, false)
	}
}

func (p *Poller) retryRefresh(ctx context.Context, keepTS bool) error {
	for attempt := 1; ; attempt++ {
		err := p.refreshServer(ctx, keepTS)
		if err == nil {
			return nil
		}
		if fatal(err) {
			return err
		}
		if err := p.backoff(ctx, attempt, err); err != nil {
			return err
		}
	}
}

func (p *Poller) backoff(ctx context.Context, n int, cause error) error {
	d := time.Second << uint(min(n-1, 6))
	if d > p.cfg.MaxBackoff {
		d = p.cfg.MaxBackoff
	}
	p.cfg.Logger.Warn("longpoll: retrying", "after", d, "error", cause)
	return p.cfg.Sleep(ctx, d)
}

// fatal reports API errors that will not go away by retrying the poll:
// the token is invalid or lacks the messages right.
func fatal(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	e, ok := api.AsError(err)
	if !ok {
		return false
	}
	switch e.Code {
	case api.CodeAuthFailed, api.CodePermissionDenied, api.CodeAccessDenied, api.CodeAppDisabled, api.CodeUnknownMethod, api.CodeValidationRequired, api.CodeCaptchaNeeded:
		return true
	}
	return false
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
