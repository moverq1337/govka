// Package events defines the values delivered to govka event handlers.
// Every event is a plain struct; handlers switch on the concrete type.
package events

import (
	"encoding/json"
	"time"

	"github.com/moverq1337/govka/session"
	"github.com/moverq1337/govka/types"
)

// Connected is emitted once the session is verified and the long poll
// loop is running.
type Connected struct {
	UserID int64
}

// Disconnected is emitted when the long poll loop stops. Err is nil for a
// clean shutdown (context cancelled or Disconnect called).
type Disconnected struct {
	Err error
}

// LoggedOut is emitted when the access token stopped working and could not
// be refreshed. The client stops after emitting it.
type LoggedOut struct {
	Err error
}

// TokenRefreshed is emitted after a successful VK ID token refresh. The
// session has already been persisted when a Storage is configured.
type TokenRefreshed struct {
	Session *session.Session
}

// FlagsOp says how MessageFlags / DialogFlags should be applied.
type FlagsOp int

// Flag operations.
const (
	FlagsReplace FlagsOp = iota + 1
	FlagsSet
	FlagsReset
)

// String implements fmt.Stringer.
func (o FlagsOp) String() string {
	switch o {
	case FlagsReplace:
		return "replace"
	case FlagsSet:
		return "set"
	case FlagsReset:
		return "reset"
	}
	return "unknown"
}

// Message is a new message (long poll event 4). Text has HTML entities
// decoded and <br> turned into newlines.
type Message struct {
	ID       int64
	Flags    types.MessageFlags
	Peer     types.PeerID
	Date     time.Time
	Text     string
	RandomID int64
	// Extra holds the additional fields object (title, from, emoji,
	// source_act ...). Values are strings as VK sends them.
	Extra map[string]string
	// Attachments holds the attachments object (attach1_type, attach1,
	// fwd, reply, geo ...).
	Attachments map[string]string
	// From is the sender: the peer for private chats, extra["from"] for
	// group chats, and the current user for outgoing messages when known.
	From int64
	// Outgoing is true for messages sent by the current user.
	Outgoing bool
	// Full is filled when the client hydrates messages via messages.getById.
	Full *types.Message
}

// IsChat reports whether the message belongs to a group chat.
func (m *Message) IsChat() bool { return m.Peer.IsChat() }

// HasAttachments reports whether the attachments object is non-empty.
func (m *Message) HasAttachments() bool { return len(m.Attachments) > 0 }

// MessageEdit is an edited message (event 5).
type MessageEdit struct {
	ID          int64
	Flags       types.MessageFlags
	Peer        types.PeerID
	Date        time.Time
	Text        string
	Attachments map[string]string
}

// MessageFlags reports a change of message flags (events 1, 2, 3).
type MessageFlags struct {
	Op    FlagsOp
	ID    int64
	Flags types.MessageFlags
	Peer  types.PeerID
	// The remaining fields are present only when VK includes the extra
	// fields; check Peer != 0 before relying on them.
	Date        time.Time
	Text        string
	Extra       map[string]string
	Attachments map[string]string
}

// ReadInbox: incoming messages up to LocalID were read (event 6).
type ReadInbox struct {
	Peer    types.PeerID
	LocalID int64
}

// ReadOutbox: outgoing messages up to LocalID were read by the peer (event 7).
type ReadOutbox struct {
	Peer    types.PeerID
	LocalID int64
}

// UserOnline (event 8). Platform is the low byte of the extra field.
type UserOnline struct {
	UserID   int64
	Platform int
	Time     time.Time
}

// UserOffline (event 9). Timeout is true when the user simply went idle.
type UserOffline struct {
	UserID  int64
	Timeout bool
	Time    time.Time
}

// DialogFlags reports a change of conversation flags (events 10, 11, 12).
type DialogFlags struct {
	Op    FlagsOp
	Peer  types.PeerID
	Flags types.DialogFlags
}

// MessagesDeleted: all messages in Peer up to LocalID were deleted (event 13).
type MessagesDeleted struct {
	Peer    types.PeerID
	LocalID int64
}

// MessagesRestored: all messages in Peer up to LocalID were restored (event 14).
type MessagesRestored struct {
	Peer    types.PeerID
	LocalID int64
}

// ConversationOrder reports a change of major/minor sort ids (events 20, 21).
type ConversationOrder struct {
	Peer    types.PeerID
	MajorID int64 // event 20
	MinorID int64 // event 21
}

// ChatChanged: chat parameters changed (event 51). Self is set when the
// change was caused by the current user.
type ChatChanged struct {
	ChatID int64
	Self   bool
}

// ChatInfo is a specific chat change (event 52).
type ChatInfo struct {
	TypeID int
	Peer   types.PeerID
	Info   int64
}

// Typing reports users typing or recording a voice message (events 61-64).
type Typing struct {
	Peer    types.PeerID
	UserIDs []int64
	// Voice is true for event 64 (recording audio message).
	Voice bool
	// TotalCount is set for chat events 63/64.
	TotalCount int
	Time       time.Time
}

// Call (event 70).
type Call struct {
	UserID int64
	CallID int64
}

// Counter is the unread counter shown in the left menu (event 80).
type Counter struct {
	Count int
}

// NotificationSettings (event 114). DisabledUntil: 0 enabled, -1 forever,
// otherwise a unix timestamp.
type NotificationSettings struct {
	Peer          types.PeerID
	Sound         bool
	DisabledUntil int64
}

// RawUpdate is delivered for long poll events govka does not decode, and
// for events whose layout did not match expectations.
type RawUpdate struct {
	Code int
	Raw  []json.RawMessage
}
