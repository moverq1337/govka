package longpoll

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/moverq1337/govka/events"
	"github.com/moverq1337/govka/types"
)

func TestDecodeMessageOfficialExample(t *testing.T) {
	raw := `[4,2105994,561,123456,1496404246,"hello &amp; bye<br>line2",{"title":" ... "},{"attach1_type":"photo","attach1":"123456_417336473","attach2_type":"audio","attach2":"123456_456239018"}]`
	evt := Decode(json.RawMessage(raw))
	m, ok := evt.(events.Message)
	if !ok {
		t.Fatalf("type %T", evt)
	}
	if m.ID != 2105994 || m.Flags != 561 || m.Peer != 123456 || m.Date.Unix() != 1496404246 {
		t.Fatalf("%+v", m)
	}
	if m.Text != "hello & bye\nline2" {
		t.Fatalf("text %q", m.Text)
	}
	if m.Extra["title"] != " ... " || m.Attachments["attach1_type"] != "photo" || m.Attachments["attach2"] != "123456_456239018" {
		t.Fatalf("%+v", m)
	}
	if m.Outgoing || m.From != 123456 || m.IsChat() || !m.HasAttachments() {
		t.Fatalf("%+v", m)
	}
}

func TestDecodeChatMessageWithRandomID(t *testing.T) {
	raw := `[4,10,3,2000000005,1700000000,"x",{"from":"777","title":"chat"},{},12345]`
	m := Decode(json.RawMessage(raw)).(events.Message)
	if !m.Outgoing || m.From != 777 || m.Peer.ChatID() != 5 || m.RandomID != 12345 {
		t.Fatalf("%+v", m)
	}
	if m.Attachments != nil {
		t.Fatalf("empty object must decode to nil, got %v", m.Attachments)
	}
	// Outgoing private message without "from": sender unknown, From stays 0.
	m = Decode(json.RawMessage(`[4,11,3,42,1,"",{},{}]`)).(events.Message)
	if m.From != 0 || !m.Outgoing {
		t.Fatalf("%+v", m)
	}
}

func TestDecodeShortAndMalformed(t *testing.T) {
	if _, ok := Decode(json.RawMessage(`[4,1]`)).(events.RawUpdate); !ok {
		t.Fatal("short event 4 must be raw")
	}
	if r, ok := Decode(json.RawMessage(`[999,1,2]`)).(events.RawUpdate); !ok || r.Code != 999 {
		t.Fatal("unknown code must be raw")
	}
	if _, ok := Decode(json.RawMessage(`{"not":"array"}`)).(events.RawUpdate); !ok {
		t.Fatal("object must be raw")
	}
	if _, ok := Decode(json.RawMessage(`[]`)).(events.RawUpdate); !ok {
		t.Fatal("empty must be raw")
	}
}

func TestDecodeOthers(t *testing.T) {
	cases := []struct {
		raw  string
		want any
	}{
		{`[2,5,128,42]`, events.MessageFlags{Op: events.FlagsSet, ID: 5, Flags: 128, Peer: 42}},
		{`[3,5,1]`, events.MessageFlags{Op: events.FlagsReset, ID: 5, Flags: 1}},
		{`[6,42,100]`, events.ReadInbox{Peer: 42, LocalID: 100}},
		{`[7,42,101]`, events.ReadOutbox{Peer: 42, LocalID: 101}},
		{`[10,42,2]`, events.DialogFlags{Op: events.FlagsReset, Peer: 42, Flags: 2}},
		{`[12,42,1]`, events.DialogFlags{Op: events.FlagsSet, Peer: 42, Flags: 1}},
		{`[13,42,7]`, events.MessagesDeleted{Peer: 42, LocalID: 7}},
		{`[14,42,7]`, events.MessagesRestored{Peer: 42, LocalID: 7}},
		{`[20,42,9]`, events.ConversationOrder{Peer: 42, MajorID: 9}},
		{`[51,5,1]`, events.ChatChanged{ChatID: 5, Self: true}},
		{`[52,1,2000000005,777]`, events.ChatInfo{TypeID: 1, Peer: 2000000005, Info: 777}},
		{`[70,1,2]`, events.Call{UserID: 1, CallID: 2}},
		{`[80,3,0]`, events.Counter{Count: 3}},
		{`[114,{"peer_id":42,"sound":1,"disabled_until":-1}]`, events.NotificationSettings{Peer: 42, Sound: true, DisabledUntil: -1}},
	}
	for _, c := range cases {
		got := Decode(json.RawMessage(c.raw))
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %#v want %#v", c.raw, got, c.want)
		}
	}
}

func TestDecodeOnlineAndTyping(t *testing.T) {
	on := Decode(json.RawMessage(`[8,-123,7,1700000000]`)).(events.UserOnline)
	if on.UserID != 123 || on.Platform != 7 || on.Time.Unix() != 1700000000 {
		t.Fatalf("%+v", on)
	}
	off := Decode(json.RawMessage(`[9,-123,1,1700000000]`)).(events.UserOffline)
	if off.UserID != 123 || !off.Timeout {
		t.Fatalf("%+v", off)
	}
	ty := Decode(json.RawMessage(`[61,5,1]`)).(events.Typing)
	if ty.Peer != types.UserPeer(5) || len(ty.UserIDs) != 1 || ty.UserIDs[0] != 5 || ty.Voice {
		t.Fatalf("%+v", ty)
	}
	ty = Decode(json.RawMessage(`[62,5,9]`)).(events.Typing)
	if ty.Peer != types.ChatPeer(9) || ty.UserIDs[0] != 5 {
		t.Fatalf("%+v", ty)
	}
	ty = Decode(json.RawMessage(`[64,[1,2],2000000009,2,1700000000]`)).(events.Typing)
	if !ty.Voice || len(ty.UserIDs) != 2 || ty.TotalCount != 2 || ty.Time.Unix() != 1700000000 {
		t.Fatalf("%+v", ty)
	}
	e := Decode(json.RawMessage(`[5,7,0,42,1700000000,"edited",{"attach1_type":"doc"},0]`)).(events.MessageEdit)
	if e.ID != 7 || e.Peer != 42 || e.Text != "edited" || e.Attachments["attach1_type"] != "doc" {
		t.Fatalf("%+v", e)
	}
}

func TestFlagsWithExtraFields(t *testing.T) {
	e := Decode(json.RawMessage(`[1,5,3,42,1700000000,"t",{"title":"x"},{}]`)).(events.MessageFlags)
	if e.Op != events.FlagsReplace || e.Text != "t" || e.Extra["title"] != "x" || e.Date.Unix() != 1700000000 {
		t.Fatalf("%+v", e)
	}
}
