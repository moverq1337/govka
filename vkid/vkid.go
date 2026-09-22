// Package vkid implements the official VK ID authorization for a native
// application: OAuth 2.1 Authorization Code flow with PKCE, requested in the
// "qr_auth" mode so the hosted VK ID page shows a QR code that the user
// scans with the VK app. It also refreshes, revokes and inspects tokens.
//
// The QR itself is rendered by VK's page, not by this package: the program
// opens Flow.AuthorizeURL in a browser, VK redirects back to RedirectURI
// with a one-time code, and Flow.Exchange turns it into tokens. Login wires
// those steps together with a loopback HTTP listener.
package vkid

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/moverq1337/govka/session"
)

// DefaultEndpoint is the VK ID service base URL.
const DefaultEndpoint = "https://id.vk.ru"

// DefaultScope is granted to every app without extra verification.
const DefaultScope = "vkid.personal_info"

// Config identifies the VK ID application.
type Config struct {
	// ClientID is the app id from the VK ID cabinet. Required.
	ClientID string
	// RedirectURI must match one registered for the app. Required for
	// authorization; not needed for refresh/logout.
	RedirectURI string
	// Scope lists the requested rights. Defaults to [DefaultScope].
	Scope []string
	// QROnly hides the "log in with phone" switch on the VK ID page
	// (action.params.flow_type=qr_only). Best effort.
	QROnly bool
	// Prompt is the OAuth prompt value: none, login, consent, select_account.
	Prompt string
	// Lang is the lang_id parameter of the authorize page.
	Lang string
	// Scheme is the page theme: light or dark.
	Scheme string
	// ServiceToken is sent by confidential apps on exchange/refresh.
	ServiceToken string
	// Endpoint overrides DefaultEndpoint.
	Endpoint string
	// HTTPClient defaults to a client with a 30 s timeout.
	HTTPClient *http.Client
}

func (c Config) endpoint() string {
	if c.Endpoint == "" {
		return DefaultEndpoint
	}
	return strings.TrimSuffix(c.Endpoint, "/")
}

func (c Config) httpClient() *http.Client {
	if c.HTTPClient == nil {
		return &http.Client{Timeout: 30 * time.Second}
	}
	return c.HTTPClient
}

func (c Config) validate(needRedirect bool) error {
	if c.ClientID == "" {
		return errors.New("vkid: ClientID is required")
	}
	if needRedirect && c.RedirectURI == "" {
		return errors.New("vkid: RedirectURI is required")
	}
	return nil
}

// Error is the error object VK ID returns: {"error","error_description"}.
type Error struct {
	Code        string `json:"error"`
	Description string `json:"error_description"`
	State       string `json:"state,omitempty"`
	HTTPStatus  int    `json:"-"`
}

// Error implements error.
func (e *Error) Error() string {
	if e.Description != "" {
		return fmt.Sprintf("vkid: %s: %s", e.Code, e.Description)
	}
	return "vkid: " + e.Code
}

// Is matches by Code so callers can compare with the sentinels below.
func (e *Error) Is(target error) bool {
	var t *Error
	if !errors.As(target, &t) {
		return false
	}
	return t.Code == e.Code
}

// Sentinel VK ID errors.
var (
	ErrAccessDenied         = &Error{Code: "access_denied"}
	ErrInvalidToken         = &Error{Code: "invalid_token"}
	ErrInvalidRequest       = &Error{Code: "invalid_request"}
	ErrInvalidClient        = &Error{Code: "invalid_client"}
	ErrInvalidScope         = &Error{Code: "invalid_scope"}
	ErrServerError          = &Error{Code: "server_error"}
	ErrSlowDown             = &Error{Code: "slow_down"}
	ErrTemporarilyUnavail   = &Error{Code: "temporarily_unavailable"}
	ErrLoginRequired        = &Error{Code: "login_required"}
	ErrInteractionRequired  = &Error{Code: "interaction_required"}
	ErrStateMismatch        = errors.New("vkid: state mismatch")
	ErrCallbackWithoutCode  = errors.New("vkid: callback has no code")
	ErrDeviceIDMissing      = errors.New("vkid: device_id missing in callback")
	ErrAuthorizationTimeout = errors.New("vkid: authorization timed out")
)

// Token is the response of POST /oauth2/auth.
type Token struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int64  `json:"expires_in"`
	UserID       int64  `json:"user_id"`
	State        string `json:"state"`
	Scope        string `json:"scope"`

	// ExpiresAt is derived from ExpiresIn at the time of the response.
	ExpiresAt time.Time `json:"-"`
	// DeviceID is the value the flow used; it is needed for refresh.
	DeviceID string `json:"-"`
	// ClientID is the app the token belongs to.
	ClientID string `json:"-"`
}

// Session converts the token into a govka session.
func (t *Token) Session() *session.Session {
	return &session.Session{
		UserID:       t.UserID,
		AccessToken:  t.AccessToken,
		RefreshToken: t.RefreshToken,
		IDToken:      t.IDToken,
		DeviceID:     t.DeviceID,
		ClientID:     t.ClientID,
		Scope:        t.Scope,
		ExpiresAt:    t.ExpiresAt,
	}
}

// Action is the `action` parameter of the authorize URL, base64(JSON).
type Action struct {
	Name   string         `json:"name"`
	Params map[string]any `json:"params,omitempty"`
}

// Encode returns the base64 form VK expects.
func (a Action) Encode() string {
	b, _ := json.Marshal(a)
	return base64.StdEncoding.EncodeToString(b)
}

// QRAction is the action that makes the VK ID page open in QR mode.
func QRAction(qrOnly bool) Action {
	a := Action{Name: "qr_auth"}
	if qrOnly {
		a.Params = map[string]any{"flow_type": "qr_only"}
	}
	return a
}

// Flow is one authorization attempt: it owns the state and PKCE verifier.
type Flow struct {
	cfg   Config
	State string
	PKCE  PKCE
}

// NewFlow validates cfg and generates fresh state and PKCE values.
func NewFlow(cfg Config) (*Flow, error) {
	if err := cfg.validate(true); err != nil {
		return nil, err
	}
	state, err := NewState()
	if err != nil {
		return nil, fmt.Errorf("vkid: state: %w", err)
	}
	pkce, err := NewPKCE()
	if err != nil {
		return nil, fmt.Errorf("vkid: pkce: %w", err)
	}
	return &Flow{cfg: cfg, State: state, PKCE: pkce}, nil
}

// AuthorizeURL is the URL to open in the user's browser. The hosted VK ID
// page renders the QR code and, after the phone confirms, redirects to
// Config.RedirectURI with code, device_id and state.
func (f *Flow) AuthorizeURL() string {
	scope := f.cfg.Scope
	if len(scope) == 0 {
		scope = []string{DefaultScope}
	}
	q := url.Values{}
	q.Set("response_type", "code")
	q.Set("client_id", f.cfg.ClientID)
	q.Set("redirect_uri", f.cfg.RedirectURI)
	q.Set("state", f.State)
	q.Set("code_challenge", f.PKCE.Challenge)
	q.Set("code_challenge_method", "S256")
	q.Set("scope", strings.Join(scope, " "))
	q.Set("action", QRAction(f.cfg.QROnly).Encode())
	if f.cfg.Prompt != "" {
		q.Set("prompt", f.cfg.Prompt)
	}
	if f.cfg.Lang != "" {
		q.Set("lang_id", f.cfg.Lang)
	}
	if f.cfg.Scheme != "" {
		q.Set("scheme", f.cfg.Scheme)
	}
	return f.cfg.endpoint() + "/authorize?" + q.Encode()
}

// Callback is what VK ID appends to the redirect URI.
type Callback struct {
	Code      string
	DeviceID  string
	State     string
	Type      string
	ExpiresIn int64
	ExtID     string
	// Error fields are set when the user declined or something failed.
	Error            string
	ErrorDescription string
}

// ParseCallback extracts the callback parameters from a redirect URL query.
func ParseCallback(q url.Values) Callback {
	var exp int64
	if v := q.Get("expires_in"); v != "" {
		fmt.Sscan(v, &exp) //nolint:errcheck
	}
	return Callback{
		Code:             q.Get("code"),
		DeviceID:         q.Get("device_id"),
		State:            q.Get("state"),
		Type:             q.Get("type"),
		ExpiresIn:        exp,
		ExtID:            q.Get("ext_id"),
		Error:            q.Get("error"),
		ErrorDescription: q.Get("error_description"),
	}
}

// Exchange validates the callback against the flow and exchanges the code
// for tokens.
func (f *Flow) Exchange(ctx context.Context, cb Callback) (*Token, error) {
	if cb.Error != "" {
		return nil, &Error{Code: cb.Error, Description: cb.ErrorDescription, State: cb.State}
	}
	if cb.State != f.State {
		return nil, ErrStateMismatch
	}
	if cb.Code == "" {
		return nil, ErrCallbackWithoutCode
	}
	if cb.DeviceID == "" {
		return nil, ErrDeviceIDMissing
	}
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", cb.Code)
	form.Set("code_verifier", f.PKCE.Verifier)
	form.Set("client_id", f.cfg.ClientID)
	form.Set("device_id", cb.DeviceID)
	form.Set("redirect_uri", f.cfg.RedirectURI)
	form.Set("state", f.State)
	if f.cfg.ServiceToken != "" {
		form.Set("service_token", f.cfg.ServiceToken)
	}
	tok, err := postToken(ctx, f.cfg, "/oauth2/auth", form)
	if err != nil {
		return nil, err
	}
	tok.DeviceID = cb.DeviceID
	return tok, nil
}

// Refresh exchanges a refresh token for a new access/refresh pair. VK ID
// rotates the pair: store the returned token and never reuse the old one.
func Refresh(ctx context.Context, cfg Config, refreshToken, deviceID string) (*Token, error) {
	if err := cfg.validate(false); err != nil {
		return nil, err
	}
	if refreshToken == "" || deviceID == "" {
		return nil, errors.New("vkid: refresh token and device id are required")
	}
	state, err := NewState()
	if err != nil {
		return nil, err
	}
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", refreshToken)
	form.Set("client_id", cfg.ClientID)
	form.Set("device_id", deviceID)
	form.Set("state", state)
	if len(cfg.Scope) > 0 {
		form.Set("scope", strings.Join(cfg.Scope, " "))
	}
	if cfg.ServiceToken != "" {
		form.Set("service_token", cfg.ServiceToken)
	}
	tok, err := postToken(ctx, cfg, "/oauth2/auth", form)
	if err != nil {
		return nil, err
	}
	tok.DeviceID = deviceID
	return tok, nil
}

// RefreshSession refreshes s in place and returns the new token.
func RefreshSession(ctx context.Context, cfg Config, s *session.Session) (*Token, error) {
	if !s.Refreshable() {
		return nil, errors.New("vkid: session is not refreshable")
	}
	if cfg.ClientID == "" {
		cfg.ClientID = s.ClientID
	}
	tok, err := Refresh(ctx, cfg, s.RefreshToken, s.DeviceID)
	if err != nil {
		return nil, err
	}
	ns := tok.Session()
	if ns.UserID == 0 {
		ns.UserID = s.UserID
	}
	*s = *ns
	return tok, nil
}

// Logout invalidates the access token and its refresh token.
func Logout(ctx context.Context, cfg Config, accessToken string) error {
	return postSimple(ctx, cfg, "/oauth2/logout", accessToken)
}

// Revoke withdraws the permissions the user granted to the app.
func Revoke(ctx context.Context, cfg Config, accessToken string) error {
	return postSimple(ctx, cfg, "/oauth2/revoke", accessToken)
}

// User is the response of POST /oauth2/user_info.
type User struct {
	UserID    int64  `json:"user_id"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Phone     string `json:"phone"`
	Avatar    string `json:"avatar"`
	Email     string `json:"email"`
	Sex       int    `json:"sex"`
	Verified  bool   `json:"verified"`
	Birthday  string `json:"birthday"`
}

// UserInfo returns the profile data available for the token's scopes.
func UserInfo(ctx context.Context, cfg Config, accessToken string) (*User, error) {
	if err := cfg.validate(false); err != nil {
		return nil, err
	}
	form := url.Values{}
	form.Set("access_token", accessToken)
	form.Set("client_id", cfg.ClientID)
	var out struct {
		User *User `json:"user"`
	}
	if err := post(ctx, cfg, "/oauth2/user_info", form, &out); err != nil {
		return nil, err
	}
	if out.User == nil {
		return nil, errors.New("vkid: user_info: empty response")
	}
	return out.User, nil
}

func postSimple(ctx context.Context, cfg Config, path, accessToken string) error {
	if err := cfg.validate(false); err != nil {
		return err
	}
	form := url.Values{}
	form.Set("access_token", accessToken)
	form.Set("client_id", cfg.ClientID)
	var out struct {
		Response int `json:"response"`
	}
	return post(ctx, cfg, path, form, &out)
}

func postToken(ctx context.Context, cfg Config, path string, form url.Values) (*Token, error) {
	var tok Token
	if err := post(ctx, cfg, path, form, &tok); err != nil {
		return nil, err
	}
	if tok.AccessToken == "" {
		return nil, errors.New("vkid: token response without access_token")
	}
	if tok.ExpiresIn > 0 {
		tok.ExpiresAt = time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second)
	}
	tok.ClientID = cfg.ClientID
	return &tok, nil
}

func post(ctx context.Context, cfg Config, path string, form url.Values, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.endpoint()+path, strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("vkid: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := cfg.httpClient().Do(req)
	if err != nil {
		return fmt.Errorf("vkid: %s: %w", path, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("vkid: %s: read body: %w", path, err)
	}
	var probe struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(data, &probe) == nil && probe.Error != "" {
		var e Error
		_ = json.Unmarshal(data, &e)
		e.HTTPStatus = resp.StatusCode
		return &e
	}
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("vkid: %s: http %d: %s", path, resp.StatusCode, truncate(data))
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("vkid: %s: decode: %w", path, err)
	}
	return nil
}

func truncate(b []byte) string {
	const n = 200
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "..."
}
