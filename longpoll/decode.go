package longpoll

import (
	"encoding/json"
	"html"
	"strconv"
	"strings"
	"time"

	"github.com/moverq1337/govka/events"
	"github.com/moverq1337/govka/types"
)

// Decode converts one positional update array into an events value. Unknown
// or malformed updates come back as events.RawUpdate.
func Decode(raw json.RawMessage) any {
	var arr []json.RawMessage
	if err := json.Unmarshal(raw, &arr); err != nil || len(arr) == 0 {
		return events.RawUpdate{Raw: arr}
	}
	u := update(arr)
	code, ok := u.int(0)
	if !ok {
		return events.RawUpdate{Raw: arr}
	}
	if evt := decode(int(code), u); evt != nil {
		return evt
	}
	return events.RawUpdate{Code: int(code), Raw: arr}
}

type update []json.RawMessage

func (u update) int(i int) (int64, bool) {
	if i >= len(u) {
		return 0, false
	}
	var n json.Number
	if err := json.Unmarshal(u[i], &n); err == nil {
		if v, err := n.Int64(); err == nil {
			return v, true
		}
		if f, err := n.Float64(); err == nil {
			return int64(f), true
		}
	}
	var s string
	if err := json.Unmarshal(u[i], &s); err == nil {
		if v, err := strconv.ParseInt(s, 10, 64); err == nil {
			return v, true
		}
	}
	return 0, false
}

func (u update) str(i int) string {
	if i >= len(u) {
		return ""
	}
	var s string
	if err := json.Unmarshal(u[i], &s); err == nil {
		return s
	}
	return strings.Trim(string(u[i]), `"`)
}

func (u update) ints(i int) []int64 {
	if i >= len(u) {
		return nil
	}
	var raw []json.RawMessage
	if err := json.Unmarshal(u[i], &raw); err != nil {
		return nil
	}
	out := make([]int64, 0, len(raw))
	for _, r := range raw {
		one := update{r}
		if v, ok := one.int(0); ok {
			out = append(out, v)
		}
	}
	return out
}

// object decodes an object whose values are strings or scalars into a
// string map. Nested values are kept as their JSON text.
func (u update) object(i int) map[string]string {
	if i >= len(u) {
		return nil
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(u[i], &m); err != nil || len(m) == 0 {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		var s string
		if err := json.Unmarshal(v, &s); err == nil {
			out[k] = s
			continue
		}
		out[k] = string(v)
	}
	return out
}

// Text normalises long poll message text: VK HTML-escapes it and uses
// <br> for newlines.
func Text(s string) string {
	s = strings.ReplaceAll(s, "<br>", "\n")
	return html.UnescapeString(s)
}

func decode(code int, u update) any {
	switch code {
	case 1, 2, 3:
		id, ok1 := u.int(1)
		flags, ok2 := u.int(2)
		if !ok1 || !ok2 {
			return nil
		}
		op := events.FlagsReplace
		if code == 2 {
			op = events.FlagsSet
		} else if code == 3 {
			op = events.FlagsReset
		}
		e := events.MessageFlags{Op: op, ID: id, Flags: types.MessageFlags(flags)}
		if peer, ok := u.int(3); ok {
			e.Peer = types.PeerID(peer)
		}
		if ts, ok := u.int(4); ok {
			e.Date = time.Unix(ts, 0)
		}
		if len(u) > 5 {
			e.Text = Text(u.str(5))
			e.Extra = u.object(6)
			e.Attachments = u.object(7)
		}
		return e
	case 4:
		return decodeMessage(u)
	case 5:
		id, ok1 := u.int(1)
		flags, _ := u.int(2)
		peer, ok2 := u.int(3)
		if !ok1 || !ok2 {
			return nil
		}
		ts, _ := u.int(4)
		return events.MessageEdit{
			ID: id, Flags: types.MessageFlags(flags), Peer: types.PeerID(peer),
			Date: time.Unix(ts, 0), Text: Text(u.str(5)), Attachments: u.object(6),
		}
	case 6, 7:
		peer, ok1 := u.int(1)
		local, ok2 := u.int(2)
		if !ok1 || !ok2 {
			return nil
		}
		if code == 6 {
			return events.ReadInbox{Peer: types.PeerID(peer), LocalID: local}
		}
		return events.ReadOutbox{Peer: types.PeerID(peer), LocalID: local}
	case 8:
		uid, ok := u.int(1)
		if !ok {
			return nil
		}
		extra, _ := u.int(2)
		ts, _ := u.int(3)
		return events.UserOnline{UserID: -uid, Platform: int(extra & 0xFF), Time: time.Unix(ts, 0)}
	case 9:
		uid, ok := u.int(1)
		if !ok {
			return nil
		}
		flags, _ := u.int(2)
		ts, _ := u.int(3)
		return events.UserOffline{UserID: -uid, Timeout: flags == 1, Time: time.Unix(ts, 0)}
	case 10, 11, 12:
		peer, ok1 := u.int(1)
		flags, ok2 := u.int(2)
		if !ok1 || !ok2 {
			return nil
		}
		op := events.FlagsReset
		if code == 11 {
			op = events.FlagsReplace
		} else if code == 12 {
			op = events.FlagsSet
		}
		return events.DialogFlags{Op: op, Peer: types.PeerID(peer), Flags: types.DialogFlags(flags)}
	case 13, 14:
		peer, ok1 := u.int(1)
		local, ok2 := u.int(2)
		if !ok1 || !ok2 {
			return nil
		}
		if code == 13 {
			return events.MessagesDeleted{Peer: types.PeerID(peer), LocalID: local}
		}
		return events.MessagesRestored{Peer: types.PeerID(peer), LocalID: local}
	case 20, 21:
		peer, ok1 := u.int(1)
		v, ok2 := u.int(2)
		if !ok1 || !ok2 {
			return nil
		}
		e := events.ConversationOrder{Peer: types.PeerID(peer)}
		if code == 20 {
			e.MajorID = v
		} else {
			e.MinorID = v
		}
		return e
	case 51:
		chat, ok := u.int(1)
		if !ok {
			return nil
		}
		self, _ := u.int(2)
		return events.ChatChanged{ChatID: chat, Self: self == 1}
	case 52:
		typ, ok1 := u.int(1)
		peer, ok2 := u.int(2)
		if !ok1 || !ok2 {
			return nil
		}
		info, _ := u.int(3)
		return events.ChatInfo{TypeID: int(typ), Peer: types.PeerID(peer), Info: info}
	case 61:
		uid, ok := u.int(1)
		if !ok {
			return nil
		}
		return events.Typing{Peer: types.UserPeer(uid), UserIDs: []int64{uid}, Time: time.Now()}
	case 62:
		uid, ok1 := u.int(1)
		chat, ok2 := u.int(2)
		if !ok1 || !ok2 {
			return nil
		}
		return events.Typing{Peer: types.ChatPeer(chat), UserIDs: []int64{uid}, Time: time.Now()}
	case 63, 64:
		ids := u.ints(1)
		peer, ok := u.int(2)
		if !ok {
			return nil
		}
		total, _ := u.int(3)
		ts, _ := u.int(4)
		t := time.Now()
		if ts > 0 {
			t = time.Unix(ts, 0)
		}
		return events.Typing{Peer: types.PeerID(peer), UserIDs: ids, Voice: code == 64, TotalCount: int(total), Time: t}
	case 70:
		uid, ok := u.int(1)
		if !ok {
			return nil
		}
		call, _ := u.int(2)
		return events.Call{UserID: uid, CallID: call}
	case 80:
		n, ok := u.int(1)
		if !ok {
			return nil
		}
		return events.Counter{Count: int(n)}
	case 114:
		obj := u.object(1)
		if obj == nil {
			return nil
		}
		peer, _ := strconv.ParseInt(obj["peer_id"], 10, 64)
		sound, _ := strconv.ParseInt(obj["sound"], 10, 64)
		until, _ := strconv.ParseInt(obj["disabled_until"], 10, 64)
		return events.NotificationSettings{Peer: types.PeerID(peer), Sound: sound == 1, DisabledUntil: until}
	}
	return nil
}

func decodeMessage(u update) any {
	id, ok1 := u.int(1)
	flags, ok2 := u.int(2)
	peer, ok3 := u.int(3)
	if !ok1 || !ok2 || !ok3 {
		return nil
	}
	ts, _ := u.int(4)
	m := events.Message{
		ID:          id,
		Flags:       types.MessageFlags(flags),
		Peer:        types.PeerID(peer),
		Date:        time.Unix(ts, 0),
		Text:        Text(u.str(5)),
		Extra:       u.object(6),
		Attachments: u.object(7),
	}
	if rid, ok := u.int(8); ok {
		m.RandomID = rid
	}
	m.Outgoing = m.Flags.Outgoing()
	if from, err := strconv.ParseInt(m.Extra["from"], 10, 64); err == nil && from != 0 {
		m.From = from
	} else if !m.Outgoing && !m.Peer.IsChat() {
		m.From = int64(m.Peer)
	}
	return m
}
