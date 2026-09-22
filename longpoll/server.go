// Package longpoll implements the VK user Long Poll: it obtains a server
// with messages.getLongPollServer, polls it, recovers from the documented
// failure codes and decodes the positional update arrays into events.
package longpoll

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/moverq1337/govka/api"
)

// Mode bits for the long poll request.
const (
	ModeAttachments = 2
	ModeExtended    = 8
	ModePts         = 32
	ModeExtraOnline = 64
	ModeRandomID    = 128
)

// Defaults.
const (
	DefaultVersion = 3
	DefaultMode    = ModeAttachments | ModeExtended | ModePts | ModeExtraOnline | ModeRandomID
	DefaultWait    = 25
	MaxWait        = 90
)

// Server is the response of messages.getLongPollServer.
type Server struct {
	Key    string      `json:"key"`
	Server string      `json:"server"`
	TS     json.Number `json:"ts"`
	PTS    json.Number `json:"pts"`
}

// URL returns the base poll URL with scheme.
func (s Server) URL() string {
	if strings.HasPrefix(s.Server, "http://") || strings.HasPrefix(s.Server, "https://") {
		return s.Server
	}
	return "https://" + s.Server
}

// GetServer calls messages.getLongPollServer.
func GetServer(ctx context.Context, c *api.Client, version int, groupID int64, needPts bool) (*Server, error) {
	p := api.Params{}.Set("lp_version", version).Set("need_pts", needPts)
	if groupID != 0 {
		p.Set("group_id", groupID)
	}
	var s Server
	if err := c.Call(ctx, "messages.getLongPollServer", p, &s); err != nil {
		return nil, err
	}
	if s.Key == "" || s.Server == "" {
		return nil, fmt.Errorf("longpoll: incomplete getLongPollServer response")
	}
	return &s, nil
}
