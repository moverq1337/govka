# govka

Native VK user session for Go: sign in as a real account (not a community
bot), receive events through the user Long Poll, send messages. The API is
shaped after [whatsmeow](https://github.com/tulir/whatsmeow) and
[gotd/td](https://github.com/gotd/td): one `Client`, `AddEventHandler`,
`Connect`/`Disconnect`, a pluggable session store, explicit login.

```
go get github.com/moverq1337/govka
```

No third-party dependencies. Go 1.24+.

## What "native" means here

VK has no separate client protocol to speak: the official API is HTTP.
The difference between a bot and a live account is where the token comes
from. govka supports two sources:

1. **VK ID QR login** (`vkid` package) — the official OAuth 2.1 + PKCE
   flow requested in `qr_auth` mode. The hosted VK ID page shows a QR code,
   the user scans it with the VK app, VK redirects back to your program
   with a one-time code, govka exchanges it for tokens and refreshes them
   automatically (access tokens live 1 h, refresh tokens 180 days).
2. **An existing user token** (`session.FromToken`) — a perpetual token
   issued before VK ID, or one the user obtained through an official
   client. govka never ships credentials of official VK apps.

### Read this before relying on messages

VK ID does not grant the `messages` right to self-registered applications.
The documented VK ID scopes are `vkid.personal_info`, `email` and `phone`;
extended rights require business verification and are granted "in
exclusive cases". Implicit Flow, which used to issue `messages` tokens, was
switched off on 2024-06-25. In practice:

- `messages.*` methods and `messages.getLongPollServer` work with tokens
  that already carry `messages` (option 2 above).
- A token from VK ID with default scopes will pass `Connect` (it calls
  `users.get`) and then fail the long poll with API error 5 or 15. govka
  reports this as `events.LoggedOut` / `events.Disconnected`.

The research behind this is in `docs/research/`.

## Quick start

```go
store := session.NewFile("session.json")
cfg := &vkid.Config{ClientID: "12345678", RedirectURI: "http://127.0.0.1:8765/callback", QROnly: true}

sess, err := session.Load(ctx, store)
if errors.Is(err, session.ErrNotFound) {
    tok, err := vkid.Login(ctx, *cfg, vkid.LoginOptions{
        OnURL: func(u string) { fmt.Println("open in a browser and scan the QR:", u) },
    })
    // handle err
    sess = tok.Session()
    _ = session.Save(ctx, store, sess)
}

client, _ := govka.NewClient(govka.Config{Session: sess, Storage: store, VKID: cfg})
client.AddEventHandler(func(evt any) {
    switch e := evt.(type) {
    case events.Message:
        if !e.Outgoing {
            _, _ = client.SendMessage(ctx, e.Peer, "echo: "+e.Text, govka.WithReplyTo(e.ID))
        }
    case events.LoggedOut:
        // the token died and could not be refreshed
    }
})
_ = client.Connect(ctx)
defer client.Disconnect()
```

With an existing token instead:

```go
client, _ := govka.NewClient(govka.Config{Session: session.FromToken(os.Getenv("VK_TOKEN"))})
```

A runnable version is in `examples/echo`.

## Packages

| package | purpose |
|---|---|
| `govka` | `Client`: lifecycle, event dispatch, token refresh, messaging helpers |
| `vkid` | VK ID authorize URL (`action=qr_auth`), loopback `Login`, `Exchange`, `Refresh`, `Logout`, `Revoke`, `UserInfo` |
| `session` | `Session` data, `Storage` interface, `Memory` and `File` stores |
| `api` | low-level `Call(ctx, method, params, &out)`: Bearer auth, `v`, typed `*api.Error`, 3 rps limiter, retries, `Execute` |
| `longpoll` | user Long Poll v3: server discovery, `failed` 1/2/3/4 recovery, backoff, positional update decoding |
| `events` | event structs: `Connected`, `Message`, `MessageEdit`, `MessageFlags`, `ReadInbox/Outbox`, `Typing`, `TokenRefreshed`, `LoggedOut`, `RawUpdate`, ... |
| `types` | `PeerID`, message flags, API objects (`Message`, `User`, `Conversation`, ...) |

## Events

Handlers run synchronously on the poll goroutine, in registration order; a
panicking handler is recovered and logged. `events.Message` carries the Long
Poll fields (id, flags, peer, date, text with HTML entities decoded, extra
fields, attachments, random_id). Set `Config.HydrateMessages` to also get
the full `types.Message` from `messages.getById` in `Message.Full`.

Long poll events govka does not decode arrive as `events.RawUpdate`.

## Errors

VK API failures are `*api.Error` and match sentinels with `errors.Is`:
`api.ErrAuthFailed` (5), `api.ErrTooManyRequests` (6),
`api.ErrCaptchaNeeded` (14, `RedirectURI` set), `api.ErrAccessDenied` (15),
`api.ErrValidationRequired` (17, `RedirectURI` set), `api.ErrRateLimit` (29).
Captcha and validation are never solved automatically: show `RedirectURI`
to a human. VK ID failures are `*vkid.Error` (`vkid.ErrAccessDenied`, ...).

## Redirect URI for QR login

`vkid.Login` needs an `http://127.0.0.1:PORT/path` or
`http://localhost:PORT/path` redirect URI registered in the VK ID cabinet for
your app. For any other setup (a public web callback, a mobile deep link)
use `vkid.NewFlow` → `AuthorizeURL` → `ParseCallback` → `Exchange` directly.

## Status

v0.1: the library is complete against the documented API and fully covered
by offline tests (`go test -race ./...`). It has not been exercised against a
live account with the `messages` right yet; see the note above.

## License

MIT
