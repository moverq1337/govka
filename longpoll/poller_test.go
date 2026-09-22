package longpoll

import (
	"context"
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
)

// fakeVK serves both the API (getLongPollServer) and the long poll endpoint.
type fakeVK struct {
	t   *testing.T
	srv *httptest.Server

	mu        sync.Mutex
	serverGet int
	polls     []string // ts values received
	script    []string // successive poll responses
	keyN      int
}

func newFakeVK(t *testing.T, script ...string) *fakeVK {
	f := &fakeVK{t: t, script: script}
	mux := http.NewServeMux()
	mux.HandleFunc("/method/messages.getLongPollServer", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.serverGet++
		f.keyN++
		n := f.keyN
		f.mu.Unlock()
		_ = r.ParseForm()
		if r.PostForm.Get("lp_version") != "3" || r.PostForm.Get("need_pts") != "1" {
			t.Errorf("bad getLongPollServer params: %v", r.PostForm)
		}
		fmt.Fprintf(w, `{"response":{"key":"k%d","server":"http://%s/lp","ts":%d,"pts":5}}`, n, r.Host, 100*n)
	})
	mux.HandleFunc("/lp", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("act") != "a_check" || q.Get("version") != "3" || q.Get("mode") != "234" || q.Get("wait") != "25" || q.Get("key") == "" {
			t.Errorf("bad poll query: %v", q)
		}
		f.mu.Lock()
		f.polls = append(f.polls, q.Get("ts"))
		var body string
		if len(f.script) > 0 {
			body, f.script = f.script[0], f.script[1:]
		}
		f.mu.Unlock()
		if body == "" {
			// End of script: block until the client goes away.
			<-r.Context().Done()
			return
		}
		if body == "HTTP500" {
			w.WriteHeader(500)
			return
		}
		_, _ = w.Write([]byte(body))
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeVK) poller(t *testing.T) *Poller {
	c, err := api.New(api.Config{Token: api.StaticToken("t"), Endpoint: f.srv.URL + "/method", RPS: -1, MaxRetries: -1})
	if err != nil {
		t.Fatal(err)
	}
	return New(c, Config{Sleep: func(context.Context, time.Duration) error { return nil }, Logger: slog.New(slog.DiscardHandler)})
}

func runUntil(t *testing.T, p *Poller, n int) ([]any, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var got []any
	err := p.Run(ctx, func(evt any) {
		got = append(got, evt)
		if len(got) >= n {
			cancel()
		}
	})
	return got, err
}

func TestRunDeliversEventsAndTracksTS(t *testing.T) {
	f := newFakeVK(t,
		`{"ts":101,"updates":[[80,1,0]]}`,
		`{"ts":102,"pts":6,"updates":[[6,42,1],[80,2,0]]}`,
	)
	p := f.poller(t)
	got, err := runUntil(t, p, 3)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %v", got)
	}
	if _, ok := got[1].(events.ReadInbox); !ok {
		t.Fatalf("got %T", got[1])
	}
	if p.TS() != "102" || p.PTS() != "6" {
		t.Fatalf("ts %s pts %s", p.TS(), p.PTS())
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.serverGet != 1 || len(f.polls) != 2 || f.polls[0] != "100" || f.polls[1] != "101" {
		t.Fatalf("serverGet %d polls %v", f.serverGet, f.polls)
	}
}

func TestFailed1KeepsGoingWithNewTS(t *testing.T) {
	f := newFakeVK(t, `{"failed":1,"ts":"555"}`, `{"ts":556,"updates":[[80,1,0]]}`)
	p := f.poller(t)
	if _, err := runUntil(t, p, 1); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.serverGet != 1 || f.polls[1] != "555" {
		t.Fatalf("serverGet %d polls %v", f.serverGet, f.polls)
	}
}

func TestFailed2RefreshesKeyKeepsTS(t *testing.T) {
	f := newFakeVK(t, `{"ts":150,"updates":[]}`, `{"failed":2}`, `{"ts":151,"updates":[[80,1,0]]}`)
	p := f.poller(t)
	if _, err := runUntil(t, p, 1); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.serverGet != 2 {
		t.Fatalf("serverGet %d", f.serverGet)
	}
	// After failed:2 the poll must continue from our own ts (150), not the new server's (200).
	if f.polls[2] != "150" {
		t.Fatalf("polls %v", f.polls)
	}
}

func TestFailed3TakesServerTS(t *testing.T) {
	f := newFakeVK(t, `{"failed":3}`, `{"ts":201,"updates":[[80,1,0]]}`)
	p := f.poller(t)
	if _, err := runUntil(t, p, 1); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.serverGet != 2 || f.polls[1] != "200" {
		t.Fatalf("serverGet %d polls %v", f.serverGet, f.polls)
	}
}

func TestFailed4IsFatal(t *testing.T) {
	f := newFakeVK(t, `{"failed":4,"min_version":0,"max_version":21}`)
	p := f.poller(t)
	_, err := runUntil(t, p, 1)
	if !errors.Is(err, ErrBadVersion) {
		t.Fatalf("err %v", err)
	}
}

func TestNetworkErrorRetries(t *testing.T) {
	f := newFakeVK(t, "HTTP500", `not json`, `{"ts":101,"updates":[[80,1,0]]}`)
	p := f.poller(t)
	got, err := runUntil(t, p, 1)
	if !errors.Is(err, context.Canceled) || len(got) != 1 {
		t.Fatalf("err %v got %v", err, got)
	}
}

func TestAuthErrorIsFatal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"error":{"error_code":5,"error_msg":"User authorization failed"}}`))
	}))
	t.Cleanup(srv.Close)
	c, _ := api.New(api.Config{Token: api.StaticToken("t"), Endpoint: srv.URL, RPS: -1, MaxRetries: -1})
	p := New(c, Config{Logger: slog.New(slog.DiscardHandler)})
	err := p.Run(context.Background(), func(any) {})
	if !errors.Is(err, api.ErrAuthFailed) {
		t.Fatalf("err %v", err)
	}
}

func TestServerURL(t *testing.T) {
	if (Server{Server: "im.vk.com/im123"}).URL() != "https://im.vk.com/im123" {
		t.Fatal("scheme not added")
	}
	if (Server{Server: "http://x/y"}).URL() != "http://x/y" {
		t.Fatal("scheme replaced")
	}
}
