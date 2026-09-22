// Package types holds the VK data types shared by the govka packages:
// peer identifiers, message flags and the JSON objects returned by the API.
package types

import "strconv"

// ChatPeerOffset is added to a chat id to form its peer id.
const ChatPeerOffset int64 = 2_000_000_000

// PeerID identifies a conversation: a user id, a negative community id,
// or ChatPeerOffset+chat_id for a group chat.
type PeerID int64

// UserPeer returns the peer id of a private conversation with a user.
func UserPeer(userID int64) PeerID { return PeerID(userID) }

// GroupPeer returns the peer id of a conversation with a community.
func GroupPeer(groupID int64) PeerID {
	if groupID < 0 {
		return PeerID(groupID)
	}
	return PeerID(-groupID)
}

// ChatPeer returns the peer id of a group chat.
func ChatPeer(chatID int64) PeerID { return PeerID(ChatPeerOffset + chatID) }

// IsUser reports whether the peer is a private conversation with a user.
func (p PeerID) IsUser() bool { return p > 0 && int64(p) < ChatPeerOffset }

// IsGroup reports whether the peer is a conversation with a community.
func (p PeerID) IsGroup() bool { return p < 0 }

// IsChat reports whether the peer is a group chat.
func (p PeerID) IsChat() bool { return int64(p) >= ChatPeerOffset }

// ChatID returns the chat id for a group chat peer and 0 otherwise.
func (p PeerID) ChatID() int64 {
	if !p.IsChat() {
		return 0
	}
	return int64(p) - ChatPeerOffset
}

// GroupID returns the positive community id for a community peer and 0 otherwise.
func (p PeerID) GroupID() int64 {
	if !p.IsGroup() {
		return 0
	}
	return -int64(p)
}

// String formats the peer id as VK expects it in request parameters.
func (p PeerID) String() string { return strconv.FormatInt(int64(p), 10) }
