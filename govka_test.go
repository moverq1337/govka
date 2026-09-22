package govka

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/moverq1337/govka/api"
	"github.com/moverq1337/govka/events"
	"github.com/moverq1337/govka/longpoll"
	"github.com/moverq1337/govka/session"
	"github.com/moverq1337/govka/types"
	"github.com/moverq1337/govka/vkid"
)

// fake is a minimal VK + VK ID + long poll server.
type fake struct {
	t   *testing.T
	srv *httptest.Server

	mu       sync.Mutex
	calls    []call
	updates  chan string
	refreshN int
	tokens   map[string]bool // valid access tokens
}

type call struct {
	method string
	token  string
	form   map[string]string
}

func newFake(t *testing.T) *fake {
	f := &fake{t: t, updates: make(chan string, 16), tokens: map[string]bool{"tok": true, "tok2": true}}
	mux := http.NewServeMux()
	mux.HandleFunc("/method/", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		method := r.URL.Path[len("/method/"):]
		token := r.Header.Get("Authorization")
		if len(token) > 7 {
			token = token[7:]
		}
		form := map[string]string{}
		for k := range r.PostForm {
			form[k] = r.PostForm.Get(k)
		}
		f.mu.Lock()
		f.calls = append(f.calls, call{method, token, form})
		valid := f.tokens[token]
		f.mu.Unlock()
		if !valid {
			_, _ = w.Write([]byte(`{"error":{"error_code":5,"error_msg":"User authorization failed: invalid access_token (4)."}}`))
			return
		}
		switch method {
		case "users.get":
			_, _ = w.Write([]byte(`{"response":[{"id":42,"first_name":"Test","last_name":"User"}]}`))
		case "messages.getLongPollServer":
			fmt.Fprintf(w, `{"response":{"key":"key","server":"http://%s/lp","ts":1,"pts":1}}`, r.Host)
		case "messages.send":
			_, _ = w.Write([]byte(`{"response":777}`))
		case "messages.getById":
			_, _ = w.Write([]byte(`{"response":{"count":1,"items":[{"id":5,"date":1,"peer_id":100,"from_id":100,"text":"full text"}]}}`))
		case "messages.markAsRead", "messages.setActivity":
			_, _ = w.Write([]byte(`{"response":1}`))
		case "messages.getHistory":
			_, _ = w.Write([]byte(`{"response":{"count":1,"items":[{"id":1,"date":1,"peer_id":100,"from_id":100,"text":"h"}]}}`))
		case "messages.getConversations":
			_, _ = w.Write([]byte(`{"response":{"count":1,"items":[{"conversation":{"peer":{"id":100,"type":"user"}},"last_message":{"id":1,"peer_id":100,"text":"h"}}]}}`))
		default:
			_, _ = w.Write([]byte(`{"error":{"error_code":3,"error_msg":"Unknown method"}}`))
		}
	})
	mux.HandleFunc("/lp", func(w http.ResponseWriter, r *http.Request) {
		select {
		case u := <-f.updates:
			_, _ = w.Write([]byte(u))
		case <-r.Context().Done():
		}
	})
	mux.HandleFunc("/oauth2/auth", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		f.mu.Lock()
		f.refreshN++
		f.mu.Unlock()
		if r.PostForm.Get("grant_type") != "refresh_token" || r.PostForm.Get("refresh_token") != "rt" || r.PostForm.Get("device_id") != "dev" {
			w.WriteHeader(400)
			_, _ = w.Write([]byte(`{"error":"invalid_request","error_description":"bad refresh"}`))
			return
		}
		_, _ = w.Write([]byte(`{"access_token":"tok2","refresh_token":"rt2","token_type":"Bearer","expires_in":3600,"user_id":42}`))
	})
	mux.HandleFunc("/oauth2/logout", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"response":1}`))
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fake) methods() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.calls))
	for i, c := range f.calls {
		out[i] = c.method
	}
	return out
}

func (f *fake) lastCall(method string) (call, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.calls) - 1; i >= 0; i-- {
		if f.calls[i].method == method {
			return f.calls[i], true
		}
	}
	return call{}, false
}

func (f *fake) client(t *testing.T, sess *session.Session, mod func(*Config)) *Client {
	t.Helper()
	cfg := Config{
		Session:     sess,
		APIEndpoint: f.srv.URL + "/method",
		APIVersion:  "5.199",
		RPS:         -1,
		Logger:      slog.New(slog.DiscardHandler),
		LongPoll:    longpoll.Config{Sleep: func(context.Context, time.Duration) error { return nil }},
	}
	if mod != nil {
		mod(&cfg)
	}
	c, err := NewClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

type recorder struct {
	mu   sync.Mutex
	evts []any
	ch   chan any
}

func newRecorder() *recorder { return &recorder{ch: make(chan any, 64)} }

func (r *recorder) handle(evt any) {
	r.mu.Lock()
	r.evts = append(r.evts, evt)
	r.mu.Unlock()
	r.ch <- evt
}

func (r *recorder) wait(t *testing.T, want any) any {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case e := <-r.ch:
			if fmt.Sprintf("%T", e) == fmt.Sprintf("%T", want) {
				return e
			}
		case <-deadline:
			t.Fatalf("timeout waiting for %T; got %v", want, r.evts)
		}
	}
}

func TestConnectReceiveSendDisconnect(t *testing.T) {
	f := newFake(t)
	c := f.client(t, session.FromToken("tok"), nil)
	rec := newRecorder()
	c.AddEventHandler(rec.handle)
	ctx := context.Background()
	if err := c.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	if err := c.Connect(ctx); !errors.Is(err, ErrAlreadyConnected) {
		t.Fatalf("second connect: %v", err)
	}
	conn := rec.wait(t, events.Connected{}).(events.Connected)
	if conn.UserID != 42 || c.Session().UserID != 42 || !c.IsConnected() {
		t.Fatalf("connected %+v", conn)
	}

	f.updates <- `{"ts":2,"updates":[[4,5,1,100,1700000000,"hi &amp; there",{"title":" ... "},{}]]}`
	msg := rec.wait(t, events.Message{}).(events.Message)
	if msg.ID != 5 || msg.Peer != 100 || msg.Text != "hi & there" || msg.From != 100 || msg.Outgoing {
		t.Fatalf("message %+v", msg)
	}

	id, err := c.SendMessage(ctx, msg.Peer, "reply", WithReplyTo(msg.ID), WithoutLinkPreview())
	if err != nil || id != 777 {
		t.Fatalf("send: id=%d err=%v", id, err)
	}
	sent, _ := f.lastCall("messages.send")
	if sent.form["peer_id"] != "100" || sent.form["message"] != "reply" || sent.form["reply_to"] != "5" || sent.form["dont_parse_links"] != "1" || sent.form["random_id"] == "" || sent.form["random_id"] == "0" {
		t.Fatalf("send form %v", sent.form)
	}
	if sent.token != "tok" || sent.form["v"] != "5.199" {
		t.Fatalf("send call %+v", sent)
	}

	// Outgoing echo without "from": From is filled with our user id.
	f.updates <- `{"ts":3,"updates":[[4,6,3,100,1700000001,"reply",{},{},123]]}`
	echo := rec.wait(t, events.Message{}).(events.Message)
	if !echo.Outgoing || echo.From != 42 || echo.RandomID != 123 {
		t.Fatalf("echo %+v", echo)
	}

	if err := c.MarkAsRead(ctx, msg.Peer, 0); err != nil {
		t.Fatal(err)
	}
	mr, _ := f.lastCall("messages.markAsRead")
	if mr.form["mark_conversation_as_read"] != "1" {
		t.Fatalf("markAsRead %v", mr.form)
	}
	if err := c.SetActivity(ctx, msg.Peer, ""); err != nil {
		t.Fatal(err)
	}
	sa, _ := f.lastCall("messages.setActivity")
	if sa.form["type"] != "typing" {
		t.Fatalf("setActivity %v", sa.form)
	}
	h, err := c.GetHistory(ctx, msg.Peer, HistoryCount(1), HistoryChronological())
	if err != nil || h.Count != 1 || h.Items[0].Text != "h" {
		t.Fatalf("history %+v err %v", h, err)
	}
	convs, err := c.GetConversations(ctx, ConversationsExtended("photo_100"))
	if err != nil || len(convs.Items) != 1 || convs.Items[0].LastMessage.Text != "h" {
		t.Fatalf("convs %+v err %v", convs, err)
	}
	gc, _ := f.lastCall("messages.getConversations")
	if gc.form["extended"] != "1" || gc.form["fields"] != "photo_100" {
		t.Fatalf("getConversations %v", gc.form)
	}

	c.Disconnect()
	d := rec.wait(t, events.Disconnected{}).(events.Disconnected)
	if d.Err != nil || c.IsConnected() {
		t.Fatalf("disconnected %+v", d)
	}
	c.Disconnect() // idempotent
	if err := c.Connect(ctx); err != nil {
		t.Fatalf("reconnect: %v", err)
	}
	rec.wait(t, events.Connected{})
	c.Disconnect()
}

func TestHydrateMessages(t *testing.T) {
	f := newFake(t)
	c := f.client(t, session.FromToken("tok"), func(cfg *Config) { cfg.HydrateMessages = true })
	rec := newRecorder()
	c.AddEventHandler(rec.handle)
	if err := c.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer c.Disconnect()
	f.updates <- `{"ts":2,"updates":[[4,5,1,100,1700000000,"hi",{},{}]]}`
	msg := rec.wait(t, events.Message{}).(events.Message)
	if msg.Full == nil || msg.Full.Text != "full text" {
		t.Fatalf("not hydrated: %+v", msg)
	}
	gb, ok := f.lastCall("messages.getById")
	if !ok || gb.form["message_ids"] != "5" {
		t.Fatalf("getById %v", gb.form)
	}
}

func TestTokenRefreshBeforeExpiry(t *testing.T) {
	f := newFake(t)
	now := time.Unix(1_000_000, 0)
	sess := &session.Session{AccessToken: "tok", RefreshToken: "rt", DeviceID: "dev", ClientID: "app", ExpiresAt: now.Add(30 * time.Second)}
	store := &session.Memory{}
	c := f.client(t, sess, func(cfg *Config) {
		cfg.Now = func() time.Time { return now }
		cfg.VKID = &vkid.Config{Endpoint: f.srv.URL}
		cfg.Storage = store
	})
	rec := newRecorder()
	c.AddEventHandler(rec.handle)
	me, err := c.Me(context.Background())
	if err != nil || me.ID != 42 {
		t.Fatalf("me %+v err %v", me, err)
	}
	if f.refreshN != 1 {
		t.Fatalf("refreshN %d", f.refreshN)
	}
	last, _ := f.lastCall("users.get")
	if last.token != "tok2" {
		t.Fatalf("call used token %q", last.token)
	}
	s := c.Session()
	if s.AccessToken != "tok2" || s.RefreshToken != "rt2" || s.ClientID != "app" || s.DeviceID != "dev" || s.UserID != 42 {
		t.Fatalf("session %+v", s)
	}
	stored, err := session.Load(context.Background(), store)
	if err != nil || stored.AccessToken != "tok2" {
		t.Fatalf("stored %+v err %v", stored, err)
	}
	tr := rec.wait(t, events.TokenRefreshed{}).(events.TokenRefreshed)
	if tr.Session.AccessToken != "tok2" {
		t.Fatalf("event %+v", tr)
	}
	// Token no longer near expiry: no second refresh.
	if _, err := c.Me(context.Background()); err != nil || f.refreshN != 1 {
		t.Fatalf("second call: err %v refreshN %d", err, f.refreshN)
	}
}

func TestLoggedOutWhenTokenDies(t *testing.T) {
	f := newFake(t)
	c := f.client(t, session.FromToken("tok"), nil)
	rec := newRecorder()
	c.AddEventHandler(rec.handle)
	if err := c.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	rec.wait(t, events.Connected{})
	// Invalidate the token and force the poller to re-request the server.
	f.mu.Lock()
	f.tokens["tok"] = false
	f.mu.Unlock()
	f.updates <- `{"failed":3}`
	lo := rec.wait(t, events.LoggedOut{}).(events.LoggedOut)
	if !errors.Is(lo.Err, api.ErrAuthFailed) || !c.IsLoggedOut() || c.IsConnected() {
		t.Fatalf("logged out %+v", lo)
	}
	if err := c.Connect(context.Background()); !errors.Is(err, ErrLoggedOut) {
		t.Fatalf("connect after logout: %v", err)
	}
}

func TestReconnectAfterRefreshOnAuthFailure(t *testing.T) {
	f := newFake(t)
	sess := &session.Session{AccessToken: "tok", RefreshToken: "rt", DeviceID: "dev", ClientID: "app"}
	c := f.client(t, sess, func(cfg *Config) { cfg.VKID = &vkid.Config{Endpoint: f.srv.URL} })
	rec := newRecorder()
	c.AddEventHandler(rec.handle)
	if err := c.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	rec.wait(t, events.Connected{})
	f.mu.Lock()
	f.tokens["tok"] = false
	f.mu.Unlock()
	f.updates <- `{"failed":3}`
	rec.wait(t, events.TokenRefreshed{})
	f.updates <- `{"ts":9,"updates":[[80,1,0]]}`
	rec.wait(t, events.Counter{})
	if !c.IsConnected() || c.Session().AccessToken != "tok2" {
		t.Fatal("not reconnected with the new token")
	}
	c.Disconnect()
}

func TestHandlerPanicIsRecovered(t *testing.T) {
	f := newFake(t)
	c := f.client(t, session.FromToken("tok"), nil)
	c.AddEventHandler(func(any) { panic("boom") })
	rec := newRecorder()
	id := c.AddEventHandler(rec.handle)
	if err := c.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	rec.wait(t, events.Connected{})
	if !c.RemoveEventHandler(id) || c.RemoveEventHandler(id) {
		t.Fatal("RemoveEventHandler")
	}
	c.Disconnect()
}

func TestLogout(t *testing.T) {
	f := newFake(t)
	sess := &session.Session{AccessToken: "tok", RefreshToken: "rt", DeviceID: "dev", ClientID: "app"}
	c := f.client(t, sess, func(cfg *Config) { cfg.VKID = &vkid.Config{Endpoint: f.srv.URL} })
	rec := newRecorder()
	c.AddEventHandler(rec.handle)
	if err := c.Logout(context.Background()); err != nil {
		t.Fatal(err)
	}
	rec.wait(t, events.LoggedOut{})
	if !c.IsLoggedOut() {
		t.Fatal("not logged out")
	}
}

func TestNewClientValidation(t *testing.T) {
	if _, err := NewClient(Config{}); err == nil {
		t.Fatal("nil session accepted")
	}
	if _, err := NewClient(Config{Session: &session.Session{}}); err == nil {
		t.Fatal("empty token accepted")
	}
}

func TestRandomIDNonZero(t *testing.T) {
	for i := 0; i < 100; i++ {
		if RandomID() == 0 {
			t.Fatal("zero")
		}
	}
}

func TestDecodeSessionJSONShape(t *testing.T) {
	// Guard the on-disk format: field names are part of the public contract.
	b, _ := json.Marshal(session.Session{AccessToken: "a", RefreshToken: "r", DeviceID: "d", ClientID: "c"})
	want := `{"access_token":"a","refresh_token":"r","device_id":"d","client_id":"c"}`
	if string(b) != want {
		t.Fatalf("%s", b)
	}
	_ = types.UserPeer(1)
}
