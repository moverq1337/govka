package vkid

import (
	"context"
	"encoding/base64"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestPKCEVector(t *testing.T) {
	// RFC 7636 appendix B.
	p := PKCEFromVerifier("dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk")
	if p.Challenge != "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM" {
		t.Fatalf("challenge %s", p.Challenge)
	}
	g, err := NewPKCE()
	if err != nil || len(g.Verifier) < 43 || len(g.Verifier) > 128 {
		t.Fatalf("verifier %q err %v", g.Verifier, err)
	}
	s, _ := NewState()
	if len(s) < 32 {
		t.Fatalf("state %q", s)
	}
}

func TestAuthorizeURL(t *testing.T) {
	f, err := NewFlow(Config{ClientID: "123", RedirectURI: "http://127.0.0.1:9/cb", QROnly: true, Scope: []string{"vkid.personal_info", "email"}})
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(f.AuthorizeURL())
	if err != nil {
		t.Fatal(err)
	}
	if u.Host != "id.vk.ru" || u.Path != "/authorize" {
		t.Fatalf("url %s", u)
	}
	q := u.Query()
	if q.Get("response_type") != "code" || q.Get("client_id") != "123" || q.Get("code_challenge_method") != "S256" {
		t.Fatalf("query %v", q)
	}
	if q.Get("state") != f.State || q.Get("code_challenge") != f.PKCE.Challenge || q.Get("scope") != "vkid.personal_info email" {
		t.Fatalf("query %v", q)
	}
	raw, err := base64.StdEncoding.DecodeString(q.Get("action"))
	if err != nil || string(raw) != `{"name":"qr_auth","params":{"flow_type":"qr_only"}}` {
		t.Fatalf("action %s err %v", raw, err)
	}
	if QRAction(false).Encode() != base64.StdEncoding.EncodeToString([]byte(`{"name":"qr_auth"}`)) {
		t.Fatal("plain action")
	}
}

func tokenServer(t *testing.T, check func(path string, form url.Values)) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if check != nil {
			check(r.URL.Path, r.PostForm)
		}
		switch r.URL.Path {
		case "/oauth2/auth":
			if r.PostForm.Get("code") == "bad" {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":"invalid_request","error_description":"device_id is invalid","state":"s"}`))
				return
			}
			_, _ = w.Write([]byte(`{"access_token":"at","refresh_token":"rt","id_token":"it","token_type":"Bearer","expires_in":3600,"user_id":42,"state":"x","scope":"vkid.personal_info"}`))
		case "/oauth2/logout", "/oauth2/revoke":
			_, _ = w.Write([]byte(`{"response":1}`))
		case "/oauth2/user_info":
			_, _ = w.Write([]byte(`{"user":{"user_id":42,"first_name":"A","last_name":"B"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestExchange(t *testing.T) {
	var gotForm url.Values
	srv := tokenServer(t, func(_ string, f url.Values) { gotForm = f })
	cfg := Config{ClientID: "1", RedirectURI: "http://127.0.0.1:1/cb", Endpoint: srv.URL}
	f, _ := NewFlow(cfg)
	cb := ParseCallback(url.Values{"code": {"c"}, "device_id": {"d"}, "state": {f.State}, "type": {"code_v2"}, "expires_in": {"600"}})
	if cb.ExpiresIn != 600 || cb.Type != "code_v2" {
		t.Fatalf("callback %+v", cb)
	}
	tok, err := f.Exchange(context.Background(), cb)
	if err != nil {
		t.Fatal(err)
	}
	if gotForm.Get("grant_type") != "authorization_code" || gotForm.Get("code_verifier") != f.PKCE.Verifier ||
		gotForm.Get("device_id") != "d" || gotForm.Get("redirect_uri") != cfg.RedirectURI || gotForm.Get("state") != f.State {
		t.Fatalf("form %v", gotForm)
	}
	if tok.AccessToken != "at" || tok.UserID != 42 || tok.DeviceID != "d" || tok.ClientID != "1" || tok.ExpiresAt.IsZero() {
		t.Fatalf("token %+v", tok)
	}
	s := tok.Session()
	if !s.Refreshable() || s.UserID != 42 {
		t.Fatalf("session %+v", s)
	}

	if _, err := f.Exchange(context.Background(), Callback{Code: "c", DeviceID: "d", State: "other"}); !errors.Is(err, ErrStateMismatch) {
		t.Fatalf("state: %v", err)
	}
	if _, err := f.Exchange(context.Background(), Callback{Code: "c", State: f.State}); !errors.Is(err, ErrDeviceIDMissing) {
		t.Fatalf("device: %v", err)
	}
	_, err = f.Exchange(context.Background(), Callback{Error: "access_denied", State: f.State})
	if !errors.Is(err, ErrAccessDenied) {
		t.Fatalf("denied: %v", err)
	}
	_, err = f.Exchange(context.Background(), Callback{Code: "bad", DeviceID: "d", State: f.State})
	var e *Error
	if !errors.As(err, &e) || !errors.Is(err, ErrInvalidRequest) || e.HTTPStatus != 400 || e.Description != "device_id is invalid" {
		t.Fatalf("server error: %v", err)
	}
}

func TestRefreshLogoutUserInfo(t *testing.T) {
	var paths []string
	var forms []url.Values
	srv := tokenServer(t, func(p string, f url.Values) { paths = append(paths, p); forms = append(forms, f) })
	cfg := Config{ClientID: "1", Endpoint: srv.URL}
	ctx := context.Background()
	tok, err := Refresh(ctx, cfg, "old", "dev")
	if err != nil {
		t.Fatal(err)
	}
	if forms[0].Get("grant_type") != "refresh_token" || forms[0].Get("refresh_token") != "old" || forms[0].Get("device_id") != "dev" || len(forms[0].Get("state")) < 32 {
		t.Fatalf("refresh form %v", forms[0])
	}
	if tok.DeviceID != "dev" {
		t.Fatalf("token %+v", tok)
	}
	if _, err := Refresh(ctx, cfg, "", "dev"); err == nil {
		t.Fatal("empty refresh token accepted")
	}
	if err := Logout(ctx, cfg, "at"); err != nil {
		t.Fatal(err)
	}
	if err := Revoke(ctx, cfg, "at"); err != nil {
		t.Fatal(err)
	}
	u, err := UserInfo(ctx, cfg, "at")
	if err != nil || u.UserID != 42 || u.FirstName != "A" {
		t.Fatalf("user %+v err %v", u, err)
	}
	if strings.Join(paths, ",") != "/oauth2/auth,/oauth2/logout,/oauth2/revoke,/oauth2/user_info" {
		t.Fatalf("paths %v", paths)
	}
}

func TestRefreshSession(t *testing.T) {
	srv := tokenServer(t, nil)
	s := (&Token{AccessToken: "a", RefreshToken: "r", DeviceID: "d", ClientID: "1", UserID: 7}).Session()
	tok, err := RefreshSession(context.Background(), Config{Endpoint: srv.URL}, s)
	if err != nil {
		t.Fatal(err)
	}
	if s.AccessToken != "at" || s.RefreshToken != "rt" || s.DeviceID != "d" || s.ClientID != "1" || tok.UserID != 42 || s.UserID != 42 {
		t.Fatalf("session %+v", s)
	}
}

func TestLoginLoopback(t *testing.T) {
	srv := tokenServer(t, nil)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	cfg := Config{ClientID: "1", RedirectURI: "http://127.0.0.1:" + itoa(port) + "/cb", Endpoint: srv.URL}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	tok, err := Login(ctx, cfg, LoginOptions{
		Listener: ln,
		OnURL: func(u string) {
			pu, _ := url.Parse(u)
			state := pu.Query().Get("state")
			go func() {
				// Simulate VK ID redirecting the browser back. Wrong state first.
				resp, _ := http.Get(cfg.RedirectURI + "?code=c&device_id=d&state=wrong")
				if resp != nil {
					resp.Body.Close()
					if resp.StatusCode != http.StatusBadRequest {
						t.Errorf("wrong state accepted: %d", resp.StatusCode)
					}
				}
				resp, err := http.Get(cfg.RedirectURI + "?code=c&device_id=d&state=" + state + "&type=code_v2")
				if err != nil {
					t.Error(err)
					return
				}
				resp.Body.Close()
				if resp.StatusCode != http.StatusOK {
					t.Errorf("status %d", resp.StatusCode)
				}
			}()
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if tok.AccessToken != "at" || tok.DeviceID != "d" {
		t.Fatalf("token %+v", tok)
	}
}

func TestLoginTimeout(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	port := ln.Addr().(*net.TCPAddr).Port
	cfg := Config{ClientID: "1", RedirectURI: "http://localhost:" + itoa(port) + "/"}
	_, err := Login(context.Background(), cfg, LoginOptions{Listener: ln, Timeout: 50 * time.Millisecond, OnURL: func(string) {}})
	if !errors.Is(err, ErrAuthorizationTimeout) {
		t.Fatalf("err %v", err)
	}
}

func TestLoginRejectsNonLoopback(t *testing.T) {
	_, err := Login(context.Background(), Config{ClientID: "1", RedirectURI: "https://example.com/cb"}, LoginOptions{OnURL: func(string) {}})
	if err == nil {
		t.Fatal("expected error")
	}
}

func itoa(i int) string { return strconv.Itoa(i) }
