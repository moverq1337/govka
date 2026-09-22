package govka

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"strings"

	"github.com/moverq1337/govka/api"
	"github.com/moverq1337/govka/types"
)

// SendOption customises SendMessage.
type SendOption func(api.Params)

// WithAttachments adds attachments in the "{type}{owner_id}_{media_id}" form.
func WithAttachments(attachments ...string) SendOption {
	return func(p api.Params) { p.Set("attachment", strings.Join(attachments, ",")) }
}

// WithReplyTo replies to a message id.
func WithReplyTo(messageID int64) SendOption {
	return func(p api.Params) { p.Set("reply_to", messageID) }
}

// WithForward forwards message ids.
func WithForward(messageIDs ...int64) SendOption {
	return func(p api.Params) { p.Set("forward_messages", messageIDs) }
}

// WithSticker sends a sticker.
func WithSticker(stickerID int64) SendOption {
	return func(p api.Params) { p.Set("sticker_id", stickerID) }
}

// WithRandomID overrides the generated random_id (deduplication key).
func WithRandomID(id int32) SendOption {
	return func(p api.Params) { p.Set("random_id", id) }
}

// WithoutLinkPreview disables link snippets.
func WithoutLinkPreview() SendOption {
	return func(p api.Params) { p.Set("dont_parse_links", true) }
}

// WithoutMentions disables mention notifications.
func WithoutMentions() SendOption {
	return func(p api.Params) { p.Set("disable_mentions", true) }
}

// WithParam sets an arbitrary messages.send parameter.
func WithParam(key string, value any) SendOption {
	return func(p api.Params) { p.Set(key, value) }
}

// RandomID returns a random non-zero int32 for messages.send.
func RandomID() int32 {
	var b [4]byte
	for {
		_, _ = rand.Read(b[:])
		v := int32(binary.LittleEndian.Uint32(b[:]) & 0x7fffffff)
		if v != 0 {
			return v
		}
	}
}

// SendMessage sends text (and/or attachments) to peer and returns the new
// message id. The echo arrives later as an outgoing events.Message with the
// same RandomID.
func (c *Client) SendMessage(ctx context.Context, peer types.PeerID, text string, opts ...SendOption) (int64, error) {
	p := api.Params{}.Set("peer_id", peer).Set("random_id", RandomID())
	if text != "" {
		p.Set("message", text)
	}
	for _, o := range opts {
		o(p)
	}
	var id int64
	if err := c.api.Call(ctx, "messages.send", p, &id); err != nil {
		return 0, err
	}
	return id, nil
}

// MarkAsRead marks messages in peer as read up to messageID (0 = all).
func (c *Client) MarkAsRead(ctx context.Context, peer types.PeerID, upToMessageID int64) error {
	p := api.Params{}.Set("peer_id", peer)
	if upToMessageID > 0 {
		p.Set("start_message_id", upToMessageID)
	} else {
		p.Set("mark_conversation_as_read", true)
	}
	return c.api.Call(ctx, "messages.markAsRead", p, nil)
}

// SetActivity shows a typing/recording indicator in peer for a few seconds.
func (c *Client) SetActivity(ctx context.Context, peer types.PeerID, kind types.Activity) error {
	if kind == "" {
		kind = types.ActivityTyping
	}
	return c.api.Call(ctx, "messages.setActivity", api.Params{}.Set("peer_id", peer).Set("type", string(kind)), nil)
}

// GetMessages fetches full message objects by id (up to 100).
func (c *Client) GetMessages(ctx context.Context, ids ...int64) ([]types.Message, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var out struct {
		Items []types.Message `json:"items"`
	}
	if err := c.api.Call(ctx, "messages.getById", api.Params{}.Set("message_ids", ids), &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

// HistoryOption customises GetHistory.
type HistoryOption func(api.Params)

// HistoryCount limits the number of messages (max 200).
func HistoryCount(n int) HistoryOption { return func(p api.Params) { p.Set("count", n) } }

// HistoryOffset skips n messages.
func HistoryOffset(n int) HistoryOption { return func(p api.Params) { p.Set("offset", n) } }

// HistoryStartAt starts at a message id; -1 means the first unread.
func HistoryStartAt(messageID int64) HistoryOption {
	return func(p api.Params) { p.Set("start_message_id", messageID) }
}

// HistoryChronological returns messages oldest first.
func HistoryChronological() HistoryOption { return func(p api.Params) { p.Set("rev", true) } }

// HistoryExtended includes profiles and groups.
func HistoryExtended(fields ...string) HistoryOption {
	return func(p api.Params) {
		p.Set("extended", true)
		if len(fields) > 0 {
			p.Set("fields", fields)
		}
	}
}

// GetHistory returns messages of a conversation.
func (c *Client) GetHistory(ctx context.Context, peer types.PeerID, opts ...HistoryOption) (*types.History, error) {
	p := api.Params{}.Set("peer_id", peer)
	for _, o := range opts {
		o(p)
	}
	var out types.History
	if err := c.api.Call(ctx, "messages.getHistory", p, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ConversationsOption customises GetConversations.
type ConversationsOption func(api.Params)

// ConversationsCount limits the number of conversations (max 200).
func ConversationsCount(n int) ConversationsOption { return func(p api.Params) { p.Set("count", n) } }

// ConversationsOffset skips n conversations.
func ConversationsOffset(n int) ConversationsOption {
	return func(p api.Params) { p.Set("offset", n) }
}

// ConversationsFilter is all, important, unanswered, unread or archive.
func ConversationsFilter(filter string) ConversationsOption {
	return func(p api.Params) { p.Set("filter", filter) }
}

// ConversationsExtended includes profiles and groups.
func ConversationsExtended(fields ...string) ConversationsOption {
	return func(p api.Params) {
		p.Set("extended", true)
		if len(fields) > 0 {
			p.Set("fields", fields)
		}
	}
}

// GetConversations lists the user's conversations.
func (c *Client) GetConversations(ctx context.Context, opts ...ConversationsOption) (*types.Conversations, error) {
	p := api.Params{}
	for _, o := range opts {
		o(p)
	}
	var out types.Conversations
	if err := c.api.Call(ctx, "messages.getConversations", p, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetUsers fetches user profiles (up to 1000 ids).
func (c *Client) GetUsers(ctx context.Context, ids ...int64) ([]types.User, error) {
	p := api.Params{}.Set("fields", "screen_name,photo_100,online")
	if len(ids) > 0 {
		p.Set("user_ids", ids)
	}
	var users []types.User
	if err := c.api.Call(ctx, "users.get", p, &users); err != nil {
		return nil, err
	}
	return users, nil
}
