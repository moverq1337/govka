package types

import (
	"encoding/json"
	"time"
)

// Message is the message object of VK API v5.199.
type Message struct {
	ID                    int64        `json:"id"`
	Date                  int64        `json:"date"`
	PeerID                PeerID       `json:"peer_id"`
	FromID                int64        `json:"from_id"`
	Text                  string       `json:"text"`
	RandomID              int64        `json:"random_id,omitempty"`
	Ref                   string       `json:"ref,omitempty"`
	RefSource             string       `json:"ref_source,omitempty"`
	Attachments           []Attachment `json:"attachments,omitempty"`
	Important             bool         `json:"important,omitempty"`
	Geo                   *Geo         `json:"geo,omitempty"`
	Payload               string       `json:"payload,omitempty"`
	FwdMessages           []Message    `json:"fwd_messages,omitempty"`
	ReplyMessage          *Message     `json:"reply_message,omitempty"`
	Action                *Action      `json:"action,omitempty"`
	AdminAuthorID         int64        `json:"admin_author_id,omitempty"`
	ConversationMessageID int64        `json:"conversation_message_id,omitempty"`
	IsCropped             bool         `json:"is_cropped,omitempty"`
	MembersCount          int          `json:"members_count,omitempty"`
	UpdateTime            int64        `json:"update_time,omitempty"`
	WasListened           bool         `json:"was_listened,omitempty"`
	PinnedAt              int64        `json:"pinned_at,omitempty"`
	Out                   int          `json:"out,omitempty"`
}

// Time returns the message date as time.Time.
func (m *Message) Time() time.Time { return time.Unix(m.Date, 0) }

// Attachment keeps the attachment type and the raw object so callers can
// decode the specific media kind they care about.
type Attachment struct {
	Type string          `json:"type"`
	Raw  json.RawMessage `json:"-"`
}

// UnmarshalJSON stores the whole object in Raw and extracts Type.
func (a *Attachment) UnmarshalJSON(b []byte) error {
	var head struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(b, &head); err != nil {
		return err
	}
	a.Type = head.Type
	a.Raw = append(a.Raw[:0], b...)
	return nil
}

// MarshalJSON writes the raw object back unchanged.
func (a Attachment) MarshalJSON() ([]byte, error) {
	if len(a.Raw) == 0 {
		return json.Marshal(struct {
			Type string `json:"type"`
		}{a.Type})
	}
	return a.Raw, nil
}

// Object decodes the attachment payload (the object under the key named by
// Type, e.g. "photo") into v.
func (a Attachment) Object(v any) error {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(a.Raw, &m); err != nil {
		return err
	}
	raw, ok := m[a.Type]
	if !ok {
		return nil
	}
	return json.Unmarshal(raw, v)
}

// Geo is a geo attachment of a message.
type Geo struct {
	Type        string `json:"type"`
	Coordinates struct {
		Latitude  float64 `json:"latitude"`
		Longitude float64 `json:"longitude"`
	} `json:"coordinates"`
	Place *struct {
		ID      int64   `json:"id"`
		Title   string  `json:"title"`
		Lat     float64 `json:"latitude"`
		Long    float64 `json:"longitude"`
		Country string  `json:"country"`
		City    string  `json:"city"`
	} `json:"place,omitempty"`
}

// Action is a service action inside a chat (user joined, title changed ...).
type Action struct {
	Type     string `json:"type"`
	MemberID int64  `json:"member_id,omitempty"`
	Text     string `json:"text,omitempty"`
	Email    string `json:"email,omitempty"`
	Photo    *struct {
		Photo50  string `json:"photo_50"`
		Photo100 string `json:"photo_100"`
		Photo200 string `json:"photo_200"`
	} `json:"photo,omitempty"`
}

// User is the user object of VK API.
type User struct {
	ID              int64  `json:"id"`
	FirstName       string `json:"first_name"`
	LastName        string `json:"last_name"`
	Deactivated     string `json:"deactivated,omitempty"`
	IsClosed        bool   `json:"is_closed,omitempty"`
	CanAccessClosed bool   `json:"can_access_closed,omitempty"`
	ScreenName      string `json:"screen_name,omitempty"`
	Photo50         string `json:"photo_50,omitempty"`
	Photo100        string `json:"photo_100,omitempty"`
	Photo200        string `json:"photo_200,omitempty"`
	Online          int    `json:"online,omitempty"`
	Sex             int    `json:"sex,omitempty"`
	Verified        int    `json:"verified,omitempty"`
}

// Group is the community object of VK API.
type Group struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	ScreenName string `json:"screen_name,omitempty"`
	IsClosed   int    `json:"is_closed,omitempty"`
	Type       string `json:"type,omitempty"`
	Photo50    string `json:"photo_50,omitempty"`
	Photo100   string `json:"photo_100,omitempty"`
	Photo200   string `json:"photo_200,omitempty"`
}

// Conversation is the conversation object of VK API.
type Conversation struct {
	Peer struct {
		ID      PeerID `json:"id"`
		Type    string `json:"type"`
		LocalID int64  `json:"local_id"`
	} `json:"peer"`
	LastMessageID int64 `json:"last_message_id"`
	InRead        int64 `json:"in_read"`
	OutRead       int64 `json:"out_read"`
	UnreadCount   int   `json:"unread_count,omitempty"`
	Important     bool  `json:"important,omitempty"`
	Unanswered    bool  `json:"unanswered,omitempty"`
	CanWrite      *struct {
		Allowed bool `json:"allowed"`
		Reason  int  `json:"reason,omitempty"`
	} `json:"can_write,omitempty"`
	ChatSettings *struct {
		Title        string `json:"title"`
		MembersCount int    `json:"members_count"`
		OwnerID      int64  `json:"owner_id"`
		State        string `json:"state"`
	} `json:"chat_settings,omitempty"`
}

// ConversationItem is one element of messages.getConversations.
type ConversationItem struct {
	Conversation Conversation `json:"conversation"`
	LastMessage  *Message     `json:"last_message,omitempty"`
}

// Conversations is the response of messages.getConversations.
type Conversations struct {
	Count       int                `json:"count"`
	Items       []ConversationItem `json:"items"`
	UnreadCount int                `json:"unread_count,omitempty"`
	Profiles    []User             `json:"profiles,omitempty"`
	Groups      []Group            `json:"groups,omitempty"`
}

// History is the response of messages.getHistory.
type History struct {
	Count    int       `json:"count"`
	Items    []Message `json:"items"`
	Skipped  int       `json:"skipped,omitempty"`
	Profiles []User    `json:"profiles,omitempty"`
	Groups   []Group   `json:"groups,omitempty"`
}
