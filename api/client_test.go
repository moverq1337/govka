package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func newTestClient(t *testing.T, h http.HandlerFunc, mod func(*Config)) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	cfg := Config{
		Token:    StaticToken("secret"),
		Endpoint: srv.URL + "/method",
		RPS:      -1,
		Sleep:    func(context.Context, time.Duration) error { return nil },
	}
	if mod != nil {
		mod(&cfg)
	}
	c, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return c, srv
}

func TestCallEncodesRequest(t *testing.T) {
	var got struct {
		path, auth, ctype, v, peer, lang string
		method                           string
	}
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		got.path = r.URL.Path
		got.method = r.Method
		got.auth = r.Header.Get("Authorization")
		got.ctype = r.Header.Get("Content-Type")
		got.v = r.PostForm.Get("v")
		got.peer = r.PostForm.Get("peer_id")
		got.lang = r.PostForm.Get("lang")
		_, _ = w.Write([]byte(`{"response": 42}`))
	}, func(c *Config) { c.Lang = "ru" })
	var id int64
	if err := c.Call(context.Background(), "messages.send", Params{}.Set("peer_id", int64(7)), &id); err != nil {
		t.Fatal(err)
	}
	if id != 42 {
		t.Fatalf("id=%d", id)
	}
	if got.path != "/method/messages.send" || got.method != http.MethodPost {
		t.Errorf("path/method: %+v", got)
	}
	if got.auth != "Bearer secret" || got.ctype != "application/x-www-form-urlencoded" {
		t.Errorf("headers: %+v", got)
	}
	if got.v != DefaultVersion || got.peer != "7" || got.lang != "ru" {
		t.Errorf("form: %+v", got)
	}
}

func TestCallReturnsTypedError(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"error":{"error_code":5,"error_msg":"User authorization failed: invalid access_token (4).","request_params":[{"key":"v","value":"5.199"}]}}`))
	}, nil)
	err := c.Call(context.Background(), "users.get", nil, nil)
	if !errors.Is(err, ErrAuthFailed) {
		t.Fatalf("errors.Is: %v", err)
	}
	e, ok := AsError(err)
	if !ok || e.Code != 5 || e.Method != "users.get" || len(e.RequestParams) != 1 {
		t.Fatalf("error: %+v", e)
	}
	if errors.Is(err, ErrCaptchaNeeded) {
		t.Fatal("must not match other codes")
	}
}

func TestCaptchaAndValidationFields(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"error":{"error_code":17,"error_msg":"Validation required","redirect_uri":"https://id.vk.ru/x"}}`))
	}, nil)
	err := c.Call(context.Background(), "messages.send", nil, nil)
	e, _ := AsError(err)
	if !errors.Is(err, ErrValidationRequired) || e.RedirectURI != "https://id.vk.ru/x" {
		t.Fatalf("%+v", e)
	}
}

func TestRetryOnTransient(t *testing.T) {
	var n int32
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&n, 1) < 3 {
			_, _ = w.Write([]byte(`{"error":{"error_code":6,"error_msg":"Too many requests per second"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"response":{"ok":true}}`))
	}, nil)
	var out struct {
		OK bool `json:"ok"`
	}
	if err := c.Call(context.Background(), "users.get", nil, &out); err != nil || !out.OK {
		t.Fatalf("err=%v out=%+v", err, out)
	}
	if n != 3 {
		t.Fatalf("attempts=%d", n)
	}
}

func TestRetryGivesUp(t *testing.T) {
	var n int32
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&n, 1)
		_, _ = w.Write([]byte(`{"error":{"error_code":10,"error_msg":"Internal server error"}}`))
	}, func(c *Config) { c.MaxRetries = 2 })
	err := c.Call(context.Background(), "users.get", nil, nil)
	if !errors.Is(err, ErrInternal) || n != 3 {
		t.Fatalf("err=%v attempts=%d", err, n)
	}
}

func TestNoRetryOnPermanent(t *testing.T) {
	var n int32
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&n, 1)
		_, _ = w.Write([]byte(`{"error":{"error_code":15,"error_msg":"Access denied"}}`))
	}, nil)
	_ = c.Call(context.Background(), "users.get", nil, nil)
	if n != 1 {
		t.Fatalf("attempts=%d", n)
	}
}

func TestHTTPErrorWithoutEnvelope(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("<html>bad gateway</html>"))
	}, nil)
	err := c.Call(context.Background(), "users.get", nil, nil)
	if err == nil || !contains(err.Error(), "http 502") {
		t.Fatalf("err=%v", err)
	}
}

func TestExecuteErrors(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.URL.Path != "/method/execute" || r.PostForm.Get("code") != "return 1;" {
			t.Errorf("bad execute request: %s %v", r.URL.Path, r.PostForm)
		}
		_, _ = w.Write([]byte(`{"response":[1,false],"execute_errors":[{"method":"messages.send","error_code":7,"error_msg":"Permission denied"}]}`))
	}, nil)
	var out []any
	err := c.Execute(context.Background(), "return 1;", &out)
	var ee ExecuteErrors
	if !errors.As(err, &ee) || len(ee) != 1 || ee[0].Code != 7 {
		t.Fatalf("err=%v", err)
	}
	if len(out) != 2 {
		t.Fatalf("out=%v", out)
	}
}

func TestParamsSet(t *testing.T) {
	p := Params{}.
		Set("s", "x").
		Set("b", true).
		Set("i", 3).
		Set("i64", int64(-4)).
		Set("f", 1.5).
		Set("ss", []string{"a", "b"}).
		Set("is", []int64{1, 2}).
		Set("nil", nil)
	want := map[string]string{"s": "x", "b": "1", "i": "3", "i64": "-4", "f": "1.5", "ss": "a,b", "is": "1,2"}
	if len(p) != len(want) {
		t.Fatalf("got %v", p)
	}
	for k, v := range want {
		if p[k] != v {
			t.Errorf("%s=%q want %q", k, p[k], v)
		}
	}
}

func TestLimiterSpacesRequests(t *testing.T) {
	now := time.Unix(0, 0)
	var slept time.Duration
	l := newLimiter(2) // 500ms interval
	l.now = func() time.Time { return now }
	l.sleep = func(_ context.Context, d time.Duration) error { slept += d; return nil }
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if err := l.wait(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if slept != 1500*time.Millisecond {
		t.Fatalf("slept=%v", slept)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
