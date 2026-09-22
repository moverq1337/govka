package vkid

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"
)

// LoginOptions tune Login.
type LoginOptions struct {
	// OnURL receives the authorize URL once the callback listener is ready.
	// The program should open it in a browser or show it to the user.
	// Required.
	OnURL func(url string)
	// Timeout bounds the wait for the callback. 0 means 10 minutes, the
	// lifetime of a VK ID authorization code.
	Timeout time.Duration
	// SuccessHTML is served to the browser after a successful callback.
	SuccessHTML string
	// Listener overrides the loopback listener (tests).
	Listener net.Listener
}

const defaultSuccessHTML = `<!doctype html><meta charset="utf-8"><title>govka</title>` +
	`<body style="font-family:sans-serif;padding:2em"><h1>Signed in</h1>` +
	`<p>You can close this tab and return to the application.</p></body>`

// Login runs the whole QR authorization for a program that controls the
// redirect URI: it listens on the loopback address named by
// cfg.RedirectURI (for example http://127.0.0.1:8765/callback), hands the
// authorize URL to opts.OnURL, waits for VK ID to redirect the browser back,
// validates state and exchanges the code for tokens.
//
// The redirect URI must be registered in the VK ID cabinet exactly as
// configured here, including the port.
func Login(ctx context.Context, cfg Config, opts LoginOptions) (*Token, error) {
	if opts.OnURL == nil {
		return nil, errors.New("vkid: LoginOptions.OnURL is required")
	}
	flow, err := NewFlow(cfg)
	if err != nil {
		return nil, err
	}
	ru, err := url.Parse(cfg.RedirectURI)
	if err != nil {
		return nil, fmt.Errorf("vkid: redirect uri: %w", err)
	}
	if ru.Scheme != "http" || !isLoopbackHost(ru.Hostname()) {
		return nil, errors.New("vkid: Login needs an http://127.0.0.1 or http://localhost redirect uri; use Flow for other setups")
	}
	ln := opts.Listener
	if ln == nil {
		addr := net.JoinHostPort(ru.Hostname(), ru.Port())
		if ru.Port() == "" {
			addr = net.JoinHostPort(ru.Hostname(), "80")
		}
		ln, err = net.Listen("tcp", addr)
		if err != nil {
			return nil, fmt.Errorf("vkid: listen %s: %w", addr, err)
		}
	}
	timeout := opts.Timeout
	if timeout == 0 {
		timeout = 10 * time.Minute
	}
	successHTML := opts.SuccessHTML
	if successHTML == "" {
		successHTML = defaultSuccessHTML
	}
	path := ru.Path
	if path == "" {
		path = "/"
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	results := make(chan Callback, 1)
	mux := http.NewServeMux()
	mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
		cb := ParseCallback(r.URL.Query())
		if cb.Error == "" && cb.Code == "" {
			http.Error(w, "missing code", http.StatusBadRequest)
			return
		}
		if cb.State != flow.State {
			http.Error(w, "state mismatch", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if cb.Error != "" {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintf(w, "<!doctype html><meta charset=utf-8><p>Authorization failed: %s</p>", htmlEscape(cb.Error))
		} else {
			_, _ = w.Write([]byte(successHTML))
		}
		select {
		case results <- cb:
		default:
		}
	})
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ln) }()
	defer func() {
		shutdownCtx, c := context.WithTimeout(context.Background(), 2*time.Second)
		defer c()
		_ = srv.Shutdown(shutdownCtx)
	}()

	opts.OnURL(flow.AuthorizeURL())

	select {
	case cb := <-results:
		return flow.Exchange(ctx, cb)
	case err := <-serveErr:
		if errors.Is(err, http.ErrServerClosed) {
			return nil, ErrAuthorizationTimeout
		}
		return nil, fmt.Errorf("vkid: callback server: %w", err)
	case <-ctx.Done():
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, ErrAuthorizationTimeout
		}
		return nil, ctx.Err()
	}
}

func isLoopbackHost(h string) bool {
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

func htmlEscape(s string) string {
	r := []rune(s)
	out := make([]rune, 0, len(r))
	for _, c := range r {
		switch c {
		case '<':
			out = append(out, []rune("&lt;")...)
		case '>':
			out = append(out, []rune("&gt;")...)
		case '&':
			out = append(out, []rune("&amp;")...)
		case '"':
			out = append(out, []rune("&quot;")...)
		default:
			out = append(out, c)
		}
	}
	return string(out)
}
