package types

import (
	"encoding/json"
	"testing"
)

func TestPeerID(t *testing.T) {
	cases := []struct {
		p                 PeerID
		user, group, chat bool
		chatID, groupID   int64
	}{
		{UserPeer(123), true, false, false, 0, 0},
		{GroupPeer(1), false, true, false, 0, 1},
		{GroupPeer(-7), false, true, false, 0, 7},
		{ChatPeer(5), false, false, true, 5, 0},
	}
	for _, c := range cases {
		if c.p.IsUser() != c.user || c.p.IsGroup() != c.group || c.p.IsChat() != c.chat {
			t.Errorf("%v: kind mismatch", c.p)
		}
		if c.p.ChatID() != c.chatID || c.p.GroupID() != c.groupID {
			t.Errorf("%v: ids mismatch", c.p)
		}
	}
	if ChatPeer(5).String() != "2000000005" {
		t.Errorf("String: %s", ChatPeer(5).String())
	}
}

func TestFlags(t *testing.T) {
	f := MessageFlags(561) // 512+32+16+1
	if !f.Has(FlagUnread) || !f.Has(FlagChat) || !f.Has(FlagFriends) || !f.Has(FlagMedia) {
		t.Fatal("expected bits missing")
	}
	if f.Outgoing() {
		t.Fatal("561 must not be outgoing")
	}
	if !MessageFlags(3).Outgoing() {
		t.Fatal("3 must be outgoing")
	}
}

func TestAttachmentRoundTrip(t *testing.T) {
	src := `{"id":1,"date":2,"peer_id":3,"from_id":4,"text":"hi","attachments":[{"type":"photo","photo":{"id":42,"owner_id":4}}]}`
	var m Message
	if err := json.Unmarshal([]byte(src), &m); err != nil {
		t.Fatal(err)
	}
	if len(m.Attachments) != 1 || m.Attachments[0].Type != "photo" {
		t.Fatalf("attachments: %+v", m.Attachments)
	}
	var photo struct {
		ID      int64 `json:"id"`
		OwnerID int64 `json:"owner_id"`
	}
	if err := m.Attachments[0].Object(&photo); err != nil {
		t.Fatal(err)
	}
	if photo.ID != 42 || photo.OwnerID != 4 {
		t.Fatalf("photo: %+v", photo)
	}
	out, err := json.Marshal(m.Attachments[0])
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != `{"type":"photo","photo":{"id":42,"owner_id":4}}` {
		t.Fatalf("marshal: %s", out)
	}
	if m.Time().Unix() != 2 {
		t.Fatal("Time")
	}
}
