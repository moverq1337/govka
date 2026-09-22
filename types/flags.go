package types

// MessageFlags is the bitmask attached to messages in the user Long Poll.
type MessageFlags int64

// Message flag bits documented for Long Poll version 3.
const (
	FlagUnread       MessageFlags = 1
	FlagOutbox       MessageFlags = 2
	FlagReplied      MessageFlags = 4
	FlagImportant    MessageFlags = 8
	FlagChat         MessageFlags = 16
	FlagFriends      MessageFlags = 32
	FlagSpam         MessageFlags = 64
	FlagDeleted      MessageFlags = 128
	FlagFixed        MessageFlags = 256
	FlagMedia        MessageFlags = 512
	FlagHidden       MessageFlags = 65536
	FlagDeleteForAll MessageFlags = 131072
	FlagNotDelivered MessageFlags = 262144
)

// Has reports whether all bits of f are set.
func (m MessageFlags) Has(f MessageFlags) bool { return m&f == f }

// Outgoing reports whether the message was sent by the current user.
func (m MessageFlags) Outgoing() bool { return m.Has(FlagOutbox) }

// DialogFlags is the bitmask used by Long Poll events 10, 11 and 12.
type DialogFlags int64

// Dialog flag bits.
const (
	DialogImportant  DialogFlags = 1
	DialogUnanswered DialogFlags = 2
)

// Activity is a value accepted by messages.setActivity.
type Activity string

// Activities documented for messages.setActivity.
const (
	ActivityTyping       Activity = "typing"
	ActivityAudioMessage Activity = "audiomessage"
	ActivityPhoto        Activity = "photo"
	ActivityVideo        Activity = "video"
	ActivityFile         Activity = "file"
	ActivityVideoMessage Activity = "videomessage"
)
