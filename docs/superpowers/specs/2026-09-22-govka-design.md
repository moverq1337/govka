# govka — design

Status: accepted as the working design for v0.1. Written from the research
notes in `docs/research/` (VK ID `qr_auth`, user Long Poll, messages.*).

## 1. Goal and non-goals

govka is a Go module that gives a program a *live VK user session*: the same
account the user has on their phone, not a community bot. The public API
should feel like `go.mau.fi/whatsmeow` and `github.com/gotd/td`: one session
object, events delivered through a handler, an explicit login flow, a
pluggable session store, and no leaked internals.

Non-goals for v0.1:

- No reverse engineering. Only documented VK ID and VK API endpoints are
  used. The hosted VK ID page internally calls `auth.getAuthCode` /
  `auth.checkAuthCode`; govka does not reproduce that chain.
- No embedded credentials of official VK clients. VK Terms §6.7 forbid it.
- No Bots Long Poll, no community-token features (keyboards, templates).
- No media upload helpers yet (attachments are passed as strings).

## 2. What the research established (constraints the design must respect)

1. **`qr_auth` is a UI mode of the hosted VK ID page, not a device flow.**
   The app opens `https://id.vk.ru/authorize` (OAuth 2.1 + PKCE) with
   `action=base64({"name":"qr_auth"})`; the page renders the QR, polls the
   phone confirmation itself, and finally redirects to the app's
   `redirect_uri` with `code`, `device_id`, `state`, `type=code_v2`. The
   app then exchanges the code at `POST https://id.vk.ru/oauth2/auth`.
   Access tokens live 1 h, refresh tokens 180 d and rotate; `device_id`
   is required for refresh; reusing a stale refresh token kills the
   session.
2. **VK ID does not grant `messages` to self-registered apps.** The
   official scope list is `vkid.personal_info`, `email`, `phone`; the
   `messages` right "is not issued to new applications". Implicit Flow was
   switched off on 2024-06-25. Tokens that still can call `messages.*` are
   (a) perpetual tokens issued before that date, (b) tokens the user obtains
   themself through an official client. govka must therefore accept an
   externally supplied access token as a first-class login method.
3. **User Long Poll** (`messages.getLongPollServer`, `lp_version=3`,
   `mode=2|8|32|64|128 = 234`, `wait=25`) delivers positional arrays; the
   documented event 4 layout is
   `[4, message_id, flags, peer_id, timestamp, text, {extra}, {attachments}, random_id]`.
   `failed` 1/2/3/4 have prescribed recovery actions. Rate limit for user
   tokens is 3 rps. Errors 14 (captcha) and 17 (validation) carry a
   `redirect_uri` the human has to open.
4. **VK API conventions**: `https://api.vk.ru/method/{name}`, form-encoded
   POST, `Authorization: Bearer`, mandatory `v` (5.199), envelope
   `{"response":…}` or `{"error":{"error_code","error_msg","request_params"}}`.
   VK ID errors are `{"error","error_description","state"}`.

## 3. Package layout

```
github.com/moverq1337/govka
├── govka.go          Client: session lifecycle, event dispatch, high-level messaging
├── api/              low-level VK API transport: Call, typed Error, rate limit, retries
├── events/           event structs delivered to handlers
├── longpoll/         user Long Poll: server discovery, poll loop, update decoding
├── session/          Session data + Storage interface (Memory, File)
├── types/            PeerID, message flags, Message/User/Attachment objects
├── vkid/             VK ID OAuth 2.1 + PKCE login (qr_auth), refresh, logout, user_info
├── examples/echo/    runnable example: login, print events, echo messages
└── docs/             research notes and this design
```

Dependency direction: `vkid`, `longpoll`, `api`, `session`, `types`,
`events` never import the root package. The root package composes them.
No third-party dependencies in the module.

## 4. Components

### 4.1 `session`

```go
type Session struct {
    UserID       int64
    AccessToken  string
    RefreshToken string    // empty for legacy / external tokens
    DeviceID     string    // VK ID device_id, required for refresh
    ClientID     string    // VK ID app id the tokens belong to
    Scope        string
    ExpiresAt    time.Time // zero = unknown / never
}
type Storage interface {
    LoadSession(ctx) ([]byte, error)   // returns ErrNotFound when nothing is stored
    StoreSession(ctx, []byte) error
}
```
`Memory` and `File` (0600 perms, atomic rename) implementations, plus
`Load(ctx, Storage) (*Session, error)` / `Save(ctx, Storage, *Session)`
helpers with JSON encoding. Mirrors gotd's `session.Storage`.

### 4.2 `api`

`Client` with `Call(ctx, method string, params Params, out any) error`.
Responsibilities: form-encoding, `Authorization: Bearer`, `v`, JSON
envelope, 3 rps token bucket (configurable), retry with backoff on
error codes 1, 6, 10 (bounded), transparent typed errors:

```go
type Error struct { Code int; Msg string; RequestParams []KV; CaptchaSID, CaptchaImg, RedirectURI string }
func (e *Error) Error() string
func (e *Error) Is(target error) bool   // errors.Is(err, api.ErrAuthFailed) etc.
```
Sentinels: `ErrAuthFailed` (5), `ErrTooManyRequests` (6), `ErrPermissionDenied` (7),
`ErrFlood` (9), `ErrCaptchaNeeded` (14), `ErrAccessDenied` (15),
`ErrValidationRequired` (17), `ErrRateLimit` (29). The token is supplied
by a `TokenSource` interface (`Token(ctx) (string, error)`) so refresh
lives outside the transport.

### 4.3 `vkid`

```go
type Config struct {
    ClientID    string
    RedirectURI string           // must be registered in the VK ID cabinet
    Scope       []string         // default ["vkid.personal_info"]
    QROnly      bool             // action.params.flow_type = qr_only (best effort)
    HTTPClient  *http.Client
    Endpoint    string           // default https://id.vk.ru
}
type Flow struct { ... }   // one authorization attempt
func NewFlow(cfg Config) (*Flow, error)              // generates state, code_verifier
func (f *Flow) AuthorizeURL() string                 // with action=qr_auth
func (f *Flow) Exchange(ctx, code, deviceID, state string) (*Token, error)
func Refresh(ctx, cfg, refreshToken, deviceID string) (*Token, error)
func Logout(ctx, cfg, accessToken string) error
func Revoke(ctx, cfg, accessToken string) error
func UserInfo(ctx, cfg, accessToken string) (*User, error)
type Token struct { AccessToken, RefreshToken, IDToken, TokenType, UserID, State, Scope string; ExpiresIn int; ExpiresAt time.Time }
func (t *Token) Session(clientID, deviceID string) *session.Session
```
Convenience for the common headless case: `Login(ctx, cfg, opts)` starts
a loopback HTTP listener on the `RedirectURI` host:port (must be
`http://127.0.0.1:PORT/...` or `http://localhost:PORT/...`), calls
`opts.OnURL(url)` so the caller can print/open it, waits for the callback,
validates `state`, exchanges the code and returns `*Token`. Errors from
VK ID are `*vkid.Error{Code, Description}`.

### 4.4 `types`

`PeerID int64` with `IsUser/IsChat/IsGroup`, `ChatPeer(chatID)`,
`GroupPeer(groupID)`, `UserPeer(userID)`. `MessageFlags` bitmask with the
documented constants. `Message`, `Attachment` (raw JSON kept), `User`,
`Conversation` structs matching VK JSON (API v5.199).

### 4.5 `longpoll`

```go
type Config struct { Version int (3); Mode int (234); Wait int (25); GroupID int64; HTTPClient *http.Client }
type Poller struct { ... }
func New(api *api.Client, cfg Config) *Poller
func (p *Poller) Run(ctx context.Context, fn func(Update)) error   // blocks until ctx done or fatal error
type Update struct { Code int; Raw []json.RawMessage }             // plus typed decoders
func (u Update) Message() (*MessageUpdate, bool)                    // code 4
func (u Update) Edit() ...; Flags(); Read(); Typing(); ...
```
Recovery: `failed:1` → keep ts from body; `failed:2` → re-get key;
`failed:3` → re-get key+ts; `failed:4` → fatal `ErrBadVersion`. Network
errors → exponential backoff (1 s → 30 s) and retry. HTTP timeout =
wait+10 s. Unknown codes are surfaced as `events.RawUpdate`.

### 4.6 `events`

Plain structs, one per event: `Connected`, `Disconnected{Err}`,
`LoggedOut`, `TokenRefreshed{Session}`, `Message{...}`, `MessageEdit`,
`MessageFlags{Set/Reset/Replace}`, `ReadInbox`, `ReadOutbox`,
`Typing`, `ChatUpdate`, `Counter`, `RawUpdate`. `Message` carries the
Long Poll fields (ID, Flags, Peer, From, Date, Text, Extra, Attachments,
RandomID) and derived `Outgoing bool`. When `Config.HydrateMessages` is
on, the client batches `messages.getById` per poll cycle and fills
`Message.Full *types.Message`.

### 4.7 root `govka`

```go
type Config struct {
    Session      *session.Session   // required
    Storage      session.Storage    // optional: persists refreshed tokens
    VKID         *vkid.Config       // optional: enables auto-refresh
    HTTPClient   *http.Client
    APIVersion   string             // default 5.199
    LongPoll     longpoll.Config
    HydrateMessages bool
    Logger       *slog.Logger
}
type Client struct { ... }
func NewClient(cfg Config) (*Client, error)
func (c *Client) AddEventHandler(fn func(evt any)) uint32
func (c *Client) RemoveEventHandler(id uint32) bool
func (c *Client) Connect(ctx) error       // validates token (users.get), starts long poll goroutine
func (c *Client) Disconnect()
func (c *Client) IsConnected() bool
func (c *Client) Session() *session.Session
func (c *Client) API() *api.Client
// messaging
func (c *Client) SendMessage(ctx, peer types.PeerID, text string, opts ...SendOption) (int64, error)
func (c *Client) MarkAsRead(ctx, peer types.PeerID, upToMessageID int64) error
func (c *Client) SetActivity(ctx, peer types.PeerID, kind types.Activity) error
func (c *Client) GetHistory(ctx, peer types.PeerID, opts ...HistoryOption) (*types.History, error)
func (c *Client) GetMessages(ctx, ids ...int64) ([]types.Message, error)
func (c *Client) GetConversations(ctx, opts ...ConversationsOption) (*types.Conversations, error)
func (c *Client) GetUsers(ctx, ids ...int64) ([]types.User, error)
func (c *Client) Me(ctx) (*types.User, error)
```
Token refresh: the client is the `api.TokenSource`. Before each call it
checks `ExpiresAt`; if within 60 s and `VKID`+`RefreshToken`+`DeviceID`
are present it refreshes (single-flight), persists via `Storage`, and
emits `TokenRefreshed`. On API error 5 it refreshes once and retries; if
that fails it emits `LoggedOut` and stops.

Handlers run synchronously in the poll goroutine, in registration order,
like whatsmeow. A panicking handler is recovered and logged.

## 5. Data flow

```
vkid.Login ──► *vkid.Token ──► session.Session ──► session.Save(Storage)
                                        │
                        govka.NewClient(Config{Session})
                                        │
        Connect ──► users.get (sanity) ──► longpoll.Run goroutine
                                        │
              updates ──► events.* ──► handlers (sync)
SendMessage ──► api.Call("messages.send") ──► echo arrives as OUTBOX event 4
```

## 6. Error handling

- All VK API failures are `*api.Error`; wrap with method name.
- Captcha/validation errors are never auto-handled; they bubble up with
  `RedirectURI` so the host program can show it to a human.
- Long Poll fatal errors (`failed:4`, auth failure after refresh) end
  `Run` and are delivered as `Disconnected{Err}`; transient ones are
  retried with backoff and never surface as events except through the
  logger.
- Context cancellation is the only clean shutdown path; `Disconnect`
  cancels the internal context and waits for the goroutine.

## 7. Testing

- Unit tests with `httptest.Server` for `api` (envelope, errors, retry,
  bearer header), `vkid` (PKCE derivation vectors, authorize URL,
  exchange/refresh/logout bodies, loopback callback with wrong state),
  `longpoll` (failed 1/2/3/4 recovery, event 4 decoding with short and
  full tuples, HTML unescape), `session` (round trip, file perms),
  root client (connect → event → send).
- No network in tests. `go vet` and `go test -race ./...` must pass.
- A manual smoke test against real VK requires a token with `messages`
  and is documented in README, not automated.

## 8. Assumptions taken without confirmation

1. Go 1.24 minimum, module path `github.com/moverq1337/govka`, MIT license.
2. The `action` payload for QR is `{"name":"qr_auth"}`; `flow_type: "qr_only"`
   is sent when `QROnly` is set, based on the front-end bundle, and may be
   ignored by the server.
3. Long Poll version 3 by default (documented); newer versions are
   selectable but their event codes are passed through as `RawUpdate`.
4. The client hydrates messages only when asked, to stay under 3 rps.
