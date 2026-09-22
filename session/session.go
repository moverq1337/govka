// Package session describes the persistent state of a VK user session and
// the storage interface used to keep it between runs.
package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// ErrNotFound is returned by Storage.LoadSession when nothing is stored yet.
var ErrNotFound = errors.New("session: not found")

// Session is everything needed to act as a VK user. AccessToken is the only
// mandatory field. The VK ID fields (RefreshToken, DeviceID, ClientID) are
// present when the session was created through VK ID and allow refreshing.
type Session struct {
	UserID       int64     `json:"user_id,omitempty"`
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	IDToken      string    `json:"id_token,omitempty"`
	DeviceID     string    `json:"device_id,omitempty"`
	ClientID     string    `json:"client_id,omitempty"`
	Scope        string    `json:"scope,omitempty"`
	ExpiresAt    time.Time `json:"expires_at,omitempty"`
}

// FromToken builds a session around an externally obtained access token,
// for example a perpetual token issued before VK ID or one the user copied
// from an official client.
func FromToken(accessToken string) *Session {
	return &Session{AccessToken: accessToken}
}

// Refreshable reports whether the session carries what VK ID needs to
// refresh the access token.
func (s *Session) Refreshable() bool {
	return s != nil && s.RefreshToken != "" && s.DeviceID != "" && s.ClientID != ""
}

// Expired reports whether the access token is known to be expired at t.
// A zero ExpiresAt means the lifetime is unknown and Expired returns false.
func (s *Session) Expired(t time.Time) bool {
	return s != nil && !s.ExpiresAt.IsZero() && !t.Before(s.ExpiresAt)
}

// Clone returns a copy of the session.
func (s *Session) Clone() *Session {
	if s == nil {
		return nil
	}
	c := *s
	return &c
}

// Validate checks that the session can be used at all.
func (s *Session) Validate() error {
	if s == nil {
		return errors.New("session: nil")
	}
	if s.AccessToken == "" {
		return errors.New("session: access token is empty")
	}
	return nil
}

// Storage persists an opaque encoded session, like gotd's session.Storage.
type Storage interface {
	// LoadSession returns the stored bytes or ErrNotFound.
	LoadSession(ctx context.Context) ([]byte, error)
	// StoreSession replaces the stored bytes.
	StoreSession(ctx context.Context, data []byte) error
}

// Load decodes the session kept in st. It returns ErrNotFound when the
// storage is empty.
func Load(ctx context.Context, st Storage) (*Session, error) {
	data, err := st.LoadSession(ctx)
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, ErrNotFound
	}
	var s Session
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("session: decode: %w", err)
	}
	return &s, nil
}

// Save encodes s and stores it in st.
func Save(ctx context.Context, st Storage, s *Session) error {
	if err := s.Validate(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("session: encode: %w", err)
	}
	return st.StoreSession(ctx, data)
}
