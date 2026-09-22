// Package govka provides a live VK user session: it verifies the account,
// runs the user Long Poll in the background, delivers decoded events to
// registered handlers and exposes the messaging methods a program needs to
// act as that user. Tokens obtained through VK ID are refreshed
// automatically and persisted with the configured session storage.
//
// The public surface follows the shape of go.mau.fi/whatsmeow and
// github.com/gotd/td: one Client per account, AddEventHandler for events,
// Connect/Disconnect for the lifecycle, and a session.Storage for state.
package govka

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/moverq1337/govka/api"
	"github.com/moverq1337/govka/events"
	"github.com/moverq1337/govka/longpoll"
	"github.com/moverq1337/govka/session"
	"github.com/moverq1337/govka/types"
	"github.com/moverq1337/govka/vkid"
)

// Errors returned by Client.
var (
	ErrAlreadyConnected = errors.New("govka: already connected")
	ErrNotConnected     = errors.New("govka: not connected")
	ErrLoggedOut        = errors.New("govka: session is logged out")
)

// refreshLeeway is how long before expiry a VK ID token is refreshed.
const refreshLeeway = 60 * time.Second

// Config configures a Client.
type Config struct {
	// Session is the account to act as. Required.
	Session *session.Session
	// Storage, when set, receives the session after every token refresh.
	Storage session.Storage
	// VKID enables automatic token refresh for sessions created through
	// VK ID. ClientID is taken from the session when empty.
	VKID *vkid.Config
	// HTTPClient is shared by the API and long poll transports.
	HTTPClient *http.Client
	// APIEndpoint defaults to api.DefaultEndpoint (https://api.vk.ru/method/).
	APIEndpoint string
	// APIVersion defaults to api.DefaultVersion.
	APIVersion string
	// Lang is passed to API calls (affects names in responses).
	Lang string
	// RPS caps API calls per second; 0 means api.DefaultRPS.
	RPS int
	// LongPoll tunes the update loop.
	LongPoll longpoll.Config
	// HydrateMessages fetches full message objects (messages.getById) for
	// new messages and puts them in events.Message.Full. Costs one API call
	// per poll cycle that contained messages.
	HydrateMessages bool
	// Logger defaults to slog.Default().
	Logger *slog.Logger
	// Now is the clock; tests replace it.
	Now func() time.Time
}

// EventHandler receives every event. Handlers run synchronously on the
// long poll goroutine in registration order.
type EventHandler func(evt any)

// Client is a live VK user session.
type Client struct {
	cfg    Config
	log    *slog.Logger
	api    *api.Client
	poller *longpoll.Poller

	sessMu  sync.RWMutex
	sess    *session.Session
	refresh singleflight

	handlersMu sync.RWMutex
	handlers   map[uint32]EventHandler
	nextID     uint32

	connMu    sync.Mutex
	connected bool
	cancel    context.CancelFunc
	done      chan struct{}
	loggedOut atomic.Bool
}

// NewClient validates cfg and builds a Client. It performs no network I/O.
func NewClient(cfg Config) (*Client, error) {
	if err := cfg.Session.Validate(); err != nil {
		return nil, err
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 60 * time.Second}
	}
	c := &Client{
		cfg:      cfg,
		log:      cfg.Logger.With("component", "govka"),
		sess:     cfg.Session.Clone(),
		handlers: make(map[uint32]EventHandler),
	}
	if cfg.VKID != nil && cfg.VKID.ClientID == "" {
		v := *cfg.VKID
		v.ClientID = c.sess.ClientID
		c.cfg.VKID = &v
	}
	apiClient, err := api.New(api.Config{
		Token:      c,
		HTTPClient: cfg.HTTPClient,
		Endpoint:   cfg.APIEndpoint,
		Version:    cfg.APIVersion,
		Lang:       cfg.Lang,
		RPS:        cfg.RPS,
	})
	if err != nil {
		return nil, err
	}
	c.api = apiClient
	lp := cfg.LongPoll
	if lp.HTTPClient == nil {
		// Long poll requests last up to wait+10 s; do not inherit a short timeout.
		lp.HTTPClient = &http.Client{Transport: cfg.HTTPClient.Transport}
	}
	if lp.Logger == nil {
		lp.Logger = c.log
	}
	c.poller = longpoll.New(apiClient, lp)
	return c, nil
}

// API returns the low-level API client for methods govka does not wrap.
func (c *Client) API() *api.Client { return c.api }

// Session returns a copy of the current session (including refreshed tokens).
func (c *Client) Session() *session.Session {
	c.sessMu.RLock()
	defer c.sessMu.RUnlock()
	return c.sess.Clone()
}

// AddEventHandler registers fn and returns an id for RemoveEventHandler.
func (c *Client) AddEventHandler(fn EventHandler) uint32 {
	c.handlersMu.Lock()
	defer c.handlersMu.Unlock()
	c.nextID++
	c.handlers[c.nextID] = fn
	return c.nextID
}

// RemoveEventHandler unregisters a handler.
func (c *Client) RemoveEventHandler(id uint32) bool {
	c.handlersMu.Lock()
	defer c.handlersMu.Unlock()
	_, ok := c.handlers[id]
	delete(c.handlers, id)
	return ok
}

// RemoveEventHandlers unregisters every handler.
func (c *Client) RemoveEventHandlers() {
	c.handlersMu.Lock()
	defer c.handlersMu.Unlock()
	c.handlers = make(map[uint32]EventHandler)
}

func (c *Client) dispatch(evt any) {
	c.handlersMu.RLock()
	ids := make([]uint32, 0, len(c.handlers))
	for id := range c.handlers {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	fns := make([]EventHandler, len(ids))
	for i, id := range ids {
		fns[i] = c.handlers[id]
	}
	c.handlersMu.RUnlock()
	for _, fn := range fns {
		c.safeCall(fn, evt)
	}
}

func (c *Client) safeCall(fn EventHandler, evt any) {
	defer func() {
		if r := recover(); r != nil {
			c.log.Error("event handler panicked", "event", fmt.Sprintf("%T", evt), "panic", r)
		}
	}()
	fn(evt)
}

// IsConnected reports whether the long poll loop is running.
func (c *Client) IsConnected() bool {
	c.connMu.Lock()
	defer c.connMu.Unlock()
	return c.connected
}

// IsLoggedOut reports whether the session was found invalid.
func (c *Client) IsLoggedOut() bool { return c.loggedOut.Load() }

// Connect verifies the session with users.get, emits events.Connected and
// starts the long poll loop in a goroutine. It returns once the loop is
// running; events flow until Disconnect is called or the session dies.
func (c *Client) Connect(ctx context.Context) error {
	c.connMu.Lock()
	if c.connected {
		c.connMu.Unlock()
		return ErrAlreadyConnected
	}
	if c.loggedOut.Load() {
		c.connMu.Unlock()
		return ErrLoggedOut
	}
	c.connMu.Unlock()

	me, err := c.Me(ctx)
	if err != nil {
		return fmt.Errorf("govka: verify session: %w", err)
	}
	c.sessMu.Lock()
	if c.sess.UserID == 0 {
		c.sess.UserID = me.ID
	}
	userID := c.sess.UserID
	c.sessMu.Unlock()

	c.connMu.Lock()
	if c.connected {
		c.connMu.Unlock()
		return ErrAlreadyConnected
	}
	runCtx, cancel := context.WithCancel(context.Background())
	c.cancel = cancel
	c.done = make(chan struct{})
	c.connected = true
	done := c.done
	c.connMu.Unlock()

	c.dispatch(events.Connected{UserID: userID})
	go c.run(runCtx, done)
	return nil
}

func (c *Client) run(ctx context.Context, done chan struct{}) {
	defer close(done)
	err := c.poller.Run(ctx, c.handleUpdate)
	c.connMu.Lock()
	c.connected = false
	c.connMu.Unlock()
	if errors.Is(err, context.Canceled) {
		c.dispatch(events.Disconnected{})
		return
	}
	if errors.Is(err, api.ErrAuthFailed) {
		// Try once to refresh; the poller already gave up.
		rerr := c.tryRefresh(ctx)
		if ctx.Err() != nil {
			// Disconnect was called meanwhile: report a clean stop.
			c.dispatch(events.Disconnected{})
			return
		}
		if rerr == nil {
			c.log.Info("token refreshed after long poll auth failure, reconnecting")
			c.connMu.Lock()
			if c.cancel != nil || c.connected {
				// A concurrent Connect already started a loop.
				c.connMu.Unlock()
				return
			}
			runCtx, cancel := context.WithCancel(context.Background())
			c.cancel = cancel
			c.done = make(chan struct{})
			c.connected = true
			nd := c.done
			c.connMu.Unlock()
			go c.run(runCtx, nd)
			return
		}
		c.loggedOut.Store(true)
		c.dispatch(events.LoggedOut{Err: err})
		return
	}
	c.dispatch(events.Disconnected{Err: err})
}

// Disconnect stops the long poll loop and waits for it to finish. It is
// safe to call when not connected.
func (c *Client) Disconnect() {
	c.connMu.Lock()
	cancel, done := c.cancel, c.done
	c.cancel, c.done = nil, nil
	c.connMu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	<-done
}

// Logout invalidates the VK ID tokens on the server (when the session came
// from VK ID), disconnects and marks the client logged out.
func (c *Client) Logout(ctx context.Context) error {
	c.Disconnect()
	c.loggedOut.Store(true)
	s := c.Session()
	if c.cfg.VKID != nil && s.Refreshable() {
		if err := vkid.Logout(ctx, *c.cfg.VKID, s.AccessToken); err != nil {
			return err
		}
	}
	c.dispatch(events.LoggedOut{})
	return nil
}

// Token implements api.TokenSource: it refreshes VK ID tokens shortly
// before they expire.
func (c *Client) Token(ctx context.Context) (string, error) {
	c.sessMu.RLock()
	s := c.sess
	needRefresh := c.cfg.VKID != nil && s.Refreshable() && !s.ExpiresAt.IsZero() &&
		c.cfg.Now().Add(refreshLeeway).After(s.ExpiresAt)
	token := s.AccessToken
	c.sessMu.RUnlock()
	if !needRefresh {
		return token, nil
	}
	if err := c.tryRefresh(ctx); err != nil {
		c.log.Warn("token refresh failed, using current token", "error", err)
		return token, nil
	}
	c.sessMu.RLock()
	defer c.sessMu.RUnlock()
	return c.sess.AccessToken, nil
}

// tryRefresh refreshes the VK ID token once, coalescing concurrent calls.
func (c *Client) tryRefresh(ctx context.Context) error {
	if c.cfg.VKID == nil {
		return errors.New("govka: VK ID refresh not configured")
	}
	return c.refresh.do(func() error {
		c.sessMu.RLock()
		s := c.sess.Clone()
		c.sessMu.RUnlock()
		if !s.Refreshable() {
			return errors.New("govka: session is not refreshable")
		}
		tok, err := vkid.RefreshSession(ctx, *c.cfg.VKID, s)
		if err != nil {
			return err
		}
		c.sessMu.Lock()
		c.sess = s
		c.sessMu.Unlock()
		if c.cfg.Storage != nil {
			if err := session.Save(ctx, c.cfg.Storage, s); err != nil {
				c.log.Error("persist refreshed session", "error", err)
			}
		}
		c.log.Info("VK ID token refreshed", "expires_at", tok.ExpiresAt)
		c.dispatch(events.TokenRefreshed{Session: s.Clone()})
		return nil
	})
}

// handleUpdate receives every decoded long poll event; it hydrates
// messages on request and forwards to handlers.
func (c *Client) handleUpdate(evt any) {
	if m, ok := evt.(events.Message); ok {
		if m.Outgoing && m.From == 0 {
			c.sessMu.RLock()
			m.From = c.sess.UserID
			c.sessMu.RUnlock()
		}
		if c.cfg.HydrateMessages {
			c.hydrate(&m)
		}
		evt = m
	}
	c.dispatch(evt)
}

func (c *Client) hydrate(m *events.Message) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	full, err := c.GetMessages(ctx, m.ID)
	if err != nil {
		c.log.Warn("hydrate message", "id", m.ID, "error", err)
		return
	}
	if len(full) == 1 {
		f := full[0]
		m.Full = &f
		if m.From == 0 {
			m.From = f.FromID
		}
	}
}

// Me returns the current user.
func (c *Client) Me(ctx context.Context) (*types.User, error) {
	var users []types.User
	if err := c.api.Call(ctx, "users.get", api.Params{}.Set("fields", "screen_name,photo_100"), &users); err != nil {
		return nil, err
	}
	if len(users) == 0 {
		return nil, errors.New("govka: users.get returned no user")
	}
	return &users[0], nil
}

// singleflight coalesces concurrent calls of the same function.
type singleflight struct {
	mu   sync.Mutex
	wait *sync.WaitGroup
	err  error
}

func (s *singleflight) do(fn func() error) error {
	s.mu.Lock()
	if s.wait != nil {
		wg := s.wait
		s.mu.Unlock()
		wg.Wait()
		s.mu.Lock()
		err := s.err
		s.mu.Unlock()
		return err
	}
	wg := &sync.WaitGroup{}
	wg.Add(1)
	s.wait = wg
	s.mu.Unlock()
	err := fn()
	s.mu.Lock()
	s.err = err
	s.wait = nil
	s.mu.Unlock()
	wg.Done()
	return err
}
