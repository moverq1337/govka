# VK API for a user session — research notes (2026-09-22)

Legend: [OFFICIAL] dev.vk.com / dev.vk.ru; [3P] library source or community
docs (danyadev/longpoll-doc, python273/vk_api, SevereCloud/vksdk, vkbottle);
[INFERRED]; [UNKNOWN].

## messages.getLongPollServer [OFFICIAL]

Params: `need_pts` (0/1), `group_id`, `lp_version` (docs: current 3).
Response: `{key, server, ts[, pts]}`. `server` has no scheme; libraries
prepend `https://` [3P]. `key` lives ~1 h and is bound to the client IP [3P].
Server currently answers `failed:4` with `min_version:0, max_version:21`; the
modern protocol (v21) renumbers message events to `1000x` codes [3P]. No
official changelog beyond v3 exists. Default to 3.

## Poll request [OFFICIAL]

`https://{server}?act=a_check&key={key}&ts={ts}&wait=25&mode={mode}&version=3`

- `wait` max 90; 25 recommended (proxies cut at 30 s).
- `mode` bits: 2 attachments, 8 extended events, 32 pts, 64 extra online
  data, 128 random_id. Libraries default to 234 [3P].
- Response `{"ts", "updates":[[code,...],...][, "pts"]}`.

| body | meaning | action |
|---|---|---|
| `{"failed":1,"ts":N}` | history outdated | set ts=N, continue (optionally getLongPollHistory) |
| `{"failed":2}` | key expired | getLongPollServer, take key/server, keep ts |
| `{"failed":3}` | user info lost | getLongPollServer, take key/server/ts |
| `{"failed":4,"min_version","max_version"}` | bad version | fatal |

## Event layout, version 3 [OFFICIAL]

`extra*` = `peer_id, timestamp, text, {extra}, {attachments}, random_id`.

| code | fields |
|---|---|
| 1 | message_id, flags, extra* (replace flags) |
| 2 | message_id, mask, extra* (set flags) |
| 3 | message_id, mask, extra* (reset flags) |
| 4 | message_id, flags, peer_id, timestamp, text, {extra}, {attachments}, random_id |
| 5 | message_id, mask, peer_id, timestamp, new_text, attachments, 0 |
| 6 / 7 | peer_id, local_id (incoming / outgoing read) |
| 8 / 9 | -user_id, extra, timestamp (online / offline; no longer sent [3P]) |
| 10 / 11 / 12 | peer_id, mask (dialog flags reset / replace / set) |
| 13 / 14 | peer_id, local_id (delete all / restore) |
| 20 / 21 | peer_id, major_id / minor_id |
| 51 | chat_id, self |
| 52 | type_id, peer_id, info |
| 61 | user_id, flags (typing DM) |
| 62 | user_id, chat_id (typing chat) |
| 63 / 64 | user_ids[], peer_id, total_count, ts (typing / voice in chat) |
| 70 | user_id, call_id |
| 80 | count, 0 |
| 114 | {peer_id, sound, disabled_until} |

Official event 4 example:
`[4,2105994,561,123456,1496404246,"hello",{"title":" ... "},{"attach1_type":"photo","attach1":"123456_417336473"}]`.
Short tuples occur for events 1/2/3 (often only peer_id). Text is HTML
escaped, `<br>` for newlines [3P]. `{extra}` fields: `title`, `from`
(string user id in chats), `emoji`, `from_admin`, `source_act`,
`source_mid`, `source_text`, `source_old_text`, `source_message`,
`source_chat_local_id`, `expire_ttl`, `is_expired`, `marked_users`,
`pinned_at`, `keyboard`, `payload`. `{attachments}`: `attach{i}_type`,
`attach{i}`, `fwd`, `reply`, `geo`, `geo_provider`, `attachments_count`,
`attachments` (JSON string). All values are strings.

Message flags: UNREAD=1, OUTBOX=2, REPLIED=4, IMPORTANT=8, CHAT=16,
FRIENDS=32, SPAM=64, DELETED=128, FIXED=256, MEDIA=512, HIDDEN=65536,
DELETE_FOR_ALL=131072, NOT_DELIVERED=262144.

peer_id: user → id; community → -id; chat → 2000000000 + chat_id.

## Methods [OFFICIAL]

- `messages.send`: `random_id` (int32, required), `peer_id`, `message`
  (≤9000), `attachment`, `reply_to`, `forward_messages`, `forward` (JSON),
  `sticker_id`, `dont_parse_links`, `disable_mentions`, `lat`/`long`,
  `group_id`. Returns message id. Error 912 for bot-only params.
- `messages.getById`: `message_ids` (≤100) or `peer_id`+`cmids`; `extended`,
  `fields`. Returns `{count, items}`.
- `messages.markAsRead`: `peer_id`, `start_message_id`, `up_to_cmid`,
  `mark_conversation_as_read`.
- `messages.setActivity`: `peer_id`, `type` in typing | audiomessage |
  photo | video | file | videomessage.
- `messages.getConversations`: `offset`, `count` (≤200), `filter`,
  `extended`, `start_message_id`, `fields`. Returns `{count, items:[{conversation,last_message}], unread_count, profiles, groups}`.
- `messages.getHistory`: `peer_id`, `offset`, `count` (≤200),
  `start_message_id` (-1 = first unread), `rev`, `extended`, `fields`.
- `messages.getLongPollHistory`: `ts`, `pts`, ...; errors 907/908 → refetch.
- `users.get`: `user_ids`, `fields`, `name_case`; no ids = current user.

Message object (v ≥ 5.80): `id, date, peer_id, from_id, text, random_id,
ref, ref_source, attachments[], important, geo, payload, fwd_messages[],
reply_message, action{type, member_id, text}, admin_author_id,
conversation_message_id, is_cropped, members_count, update_time,
was_listened, pinned_at`.

## Request conventions [OFFICIAL]

- `https://api.vk.ru/method/{method}` (api.vk.com still resolves).
- GET or POST, `application/x-www-form-urlencoded`; JSON bodies unsupported.
- `Authorization: Bearer <token>`; `access_token` param still accepted.
- `v` required; latest listed 5.199.
- Envelope `{"response":…}` / `{"error":{"error_code","error_msg","request_params":[{key,value}]}}`.
- User tokens: 3 rps; exceeding → error 6.
- Errors: 1 unknown, 5 auth failed, 6 too many rps, 7 permission denied,
  9 flood, 10 internal, 14 captcha (`redirect_uri` for VK ID captcha;
  legacy `captcha_sid`/`captcha_img`), 15 access denied, 17 validation
  required (`redirect_uri`, new token in fragment on success), 29 rate limit.
- `execute`: `code` (VKScript), ≤25 calls, counts as one call.

Observed today: no token → error 15 "Access denied: token required";
bad bearer → error 5 "User authorization failed: invalid access_token (4)".

## Token availability for messages.* [OFFICIAL + 3P]

Every messages.* page: user token must come from a Standalone app via Implicit
Flow with `messages`, "issued in exceptional cases via devsupport". The right
is not issued to new applications. VK ID scopes do not include `messages`.
Libraries cope by accepting pre-issued tokens or by embedding official client
ids, which VK Terms forbid. govka accepts externally supplied tokens and does
not ship official credentials.
