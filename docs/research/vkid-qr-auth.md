# VK ID `qr_auth` — research notes (2026-09-22)

Legend: [OFFICIAL] confirmed by id.vk.ru / dev.vk.ru docs or the official SDK
source; [BUNDLE] read from VK ID's production front-end bundles
(`static.vk.ru/vkid/1.1.1414/{authorize,auth}.js`); [3P] third-party;
[UNKNOWN] not established.

## What qr_auth is

- Not an OAuth 2.1 device-authorization grant. No `device_code` / `user_code`
  endpoint exists in VK ID. [OFFICIAL + BUNDLE]
- It is a UI mode of the hosted VK ID login page. The browser is sent to the
  normal Authorization Code + PKCE `authorize` URL; the hosted page renders a
  QR instead of the phone-number form. After the user confirms on the phone
  the page redirects to `redirect_uri` with `code`, `device_id`, `state`,
  `type=code_v2`, `expires_in`. [OFFICIAL]
- QR login is enabled by default for every app and can be disabled per app in
  the cabinet ("Авторизация → Авторизация по QR-коду"). [OFFICIAL]
- Docs: pass `action: { name: "qr_auth" }` to make QR the primary scenario;
  `params: { flow_name: "qr_only" }` hides the switch to phone/SMS. The
  front-end bundle checks `action.params.flow_type === "qr_only"` (note
  `flow_type` vs `flow_name`). Exact accepted `params` schema: [UNKNOWN].
- On the wire `action` is a query parameter on `/authorize` holding
  base64(JSON). The published Web SDK (v2.6.x) has no `qr_auth` code yet;
  its only action helper is `btoa(JSON.stringify({name:'sdk_oauth',...}))`.
  [BUNDLE / OFFICIAL SDK]
- The user needs an active session in the VK app or VK Messenger on
  iOS/Android. "Available only in the web version of your application."
  [OFFICIAL]

## Hosted page internals (not to be reproduced)

1. `auth.getAnonymToken {client_id, device_id}`.
2. `auth.getAuthCode {device_name:"web", auth_code_flow:0}` → `{auth_hash,
   auth_url, auth_id}`; `auth_url` is rendered as the QR.
3. `auth.checkAuthCode {auth_hash, web_auth:1}` every 3 s; `status` 0 waiting,
   1 scanned, 2 approved, 3 declined, 4 expired.
4. On 2: `auth.getOauthCode {...}` and redirect to `redirect_uri`.
All [BUNDLE], undocumented; govka does not implement this.

## Endpoints (form-encoded) [OFFICIAL]

| Step | Request | Params |
|---|---|---|
| Authorize | `GET https://id.vk.ru/authorize` | `response_type=code`, `client_id`, `redirect_uri`, `state` (≥32 chars `[A-Za-z0-9_-]`), `code_challenge`, `code_challenge_method=S256`; optional `scope`, `prompt`, `provider`, `lang_id`, `scheme`, `action` |
| Callback | `GET redirect_uri?code&device_id&state&type=code_v2&expires_in` | code lives 10 min |
| Exchange | `POST https://id.vk.ru/oauth2/auth` | `grant_type=authorization_code`, `code`, `code_verifier`, `client_id`, `device_id`, `redirect_uri`, `state` |
| Refresh | `POST https://id.vk.ru/oauth2/auth` | `grant_type=refresh_token`, `refresh_token`, `client_id`, `device_id`, `state` |
| Revoke | `POST https://id.vk.ru/oauth2/revoke` | `access_token`, `client_id` → `{"response":1}` |
| Logout | `POST https://id.vk.ru/oauth2/logout` | `access_token`, `client_id` → `{"response":1}` |
| User info | `POST https://id.vk.ru/oauth2/user_info` | `access_token`, `client_id` |
| Public info | `POST https://id.vk.ru/oauth2/public_info` | `id_token`, `client_id` |

Token response:
`{"refresh_token","access_token","id_token","token_type":"Bearer","expires_in":3600,"user_id","state","scope"}`.
Error response: `{"error","error_description","state"}` with codes
`access_denied | invalid_token | server_error | slow_down |
temporarily_unavailable | invalid_client | invalid_request | invalid_scope |
login_required | interaction_required`.

Observed today: bogus exchange → `{"error":"invalid_request","error_description":"device_id is invalid","state":"s"}`;
bogus user_info → `{"error":"invalid_token","error_description":"access_token is missing or invalid"}`.

## Lifetimes [OFFICIAL]

- Access token 1 h; refresh token 180 d; authorization code 10 min.
- Refresh rotates the pair; reusing an old refresh token invalidates the whole
  session. `device_id` is required for refresh.
- `id_token` is an RS256 JWT (`iss, sub, app, exp, iat, jti`).
- Rate limits for VK ID endpoints: public apps 15 000 req/day per IP.

## Scopes [OFFICIAL]

- VK ID scope reference lists only `vkid.personal_info`, `email`, `phone`.
- Extended scopes (`friends, video, photos, groups, docs, notes, stats,
  market, stories, notifications`) require business verification and a
  request to devsupport@corp.vk.com. `messages`, `wall`, `offline`, `audio`
  are not listed at all.
- dev.vk.ru privacy page: `messages` "is not issued to new applications".
- Implicit Flow and legacy Authorization Code Flow were disabled 2024-06-25;
  earlier perpetual tokens keep working.

## Third-party observations [3P]

- New Standalone apps cannot be created; `messages` cannot be obtained for a
  self-registered VK ID app.
- Password grant with official Android credentials answers "This grant_type
  available only for approved applications". Using another app's id/secret
  is prohibited by VK Terms §6.7.
- No dedicated Go library for VK ID PKCE exists; `SevereCloud/vksdk` only
  covers legacy `oauth.vk.ru`.

## Consequence for govka

There is no supported headless "print QR, poll" flow. The library must (a)
build the authorize URL with `action=qr_auth`, (b) receive the callback on a
`redirect_uri` the app controls (loopback listener), (c) exchange/refresh via
`/oauth2/auth`, and (d) accept externally obtained user tokens as a
first-class alternative, because `messages` is not grantable through VK ID.
