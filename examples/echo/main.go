// Command echo signs in to VK through VK ID (QR mode) or an existing token,
// prints every event and replies to incoming private messages.
//
//	go run ./examples/echo -client-id 12345678 -redirect http://127.0.0.1:8765/callback
//	go run ./examples/echo -token "$VK_TOKEN"
//
// The session is saved to -session (default ./session.json) and reused on
// the next run.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/moverq1337/govka"
	"github.com/moverq1337/govka/events"
	"github.com/moverq1337/govka/session"
	"github.com/moverq1337/govka/vkid"
)

func main() {
	clientID := flag.String("client-id", os.Getenv("VKID_CLIENT_ID"), "VK ID application id")
	redirect := flag.String("redirect", "http://127.0.0.1:8765/callback", "registered loopback redirect URI")
	token := flag.String("token", os.Getenv("VK_TOKEN"), "existing user access token (skips VK ID)")
	sessionPath := flag.String("session", "session.json", "session file")
	echo := flag.Bool("echo", true, "reply to incoming private messages")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	store := session.NewFile(*sessionPath)
	var vkidCfg *vkid.Config
	if *clientID != "" {
		vkidCfg = &vkid.Config{ClientID: *clientID, RedirectURI: *redirect, QROnly: true}
	}

	sess, err := loadOrLogin(ctx, store, *token, vkidCfg)
	if err != nil {
		log.Error("login", "error", err)
		os.Exit(1)
	}

	client, err := govka.NewClient(govka.Config{
		Session: sess,
		Storage: store,
		VKID:    vkidCfg,
		Logger:  log,
	})
	if err != nil {
		log.Error("client", "error", err)
		os.Exit(1)
	}

	client.AddEventHandler(func(evt any) {
		switch e := evt.(type) {
		case events.Connected:
			log.Info("connected", "user_id", e.UserID)
		case events.Message:
			dir := "in "
			if e.Outgoing {
				dir = "out"
			}
			fmt.Printf("%s %s peer=%d from=%d id=%d %q\n", e.Date.Format("15:04:05"), dir, e.Peer, e.From, e.ID, e.Text)
			if *echo && !e.Outgoing && e.Peer.IsUser() && e.Text != "" {
				if _, err := client.SendMessage(ctx, e.Peer, "echo: "+e.Text, govka.WithReplyTo(e.ID)); err != nil {
					log.Error("send", "error", err)
				}
			}
		case events.Typing:
			fmt.Printf("typing peer=%d users=%v voice=%v\n", e.Peer, e.UserIDs, e.Voice)
		case events.ReadOutbox:
			fmt.Printf("read by peer=%d up to %d\n", e.Peer, e.LocalID)
		case events.TokenRefreshed:
			log.Info("token refreshed", "expires_at", e.Session.ExpiresAt)
		case events.LoggedOut:
			log.Error("logged out", "error", e.Err)
			stop()
		case events.Disconnected:
			if e.Err != nil {
				log.Error("disconnected", "error", e.Err)
			}
		case events.RawUpdate:
			// Uncomment to see everything govka does not decode.
			// fmt.Printf("raw %d %s\n", e.Code, e.Raw)
		}
	})

	if err := client.Connect(ctx); err != nil {
		log.Error("connect", "error", err)
		os.Exit(1)
	}
	<-ctx.Done()
	client.Disconnect()
}

// loadOrLogin returns a stored session, or creates one from -token, or runs
// the VK ID QR login.
func loadOrLogin(ctx context.Context, store session.Storage, token string, vkidCfg *vkid.Config) (*session.Session, error) {
	if s, err := session.Load(ctx, store); err == nil {
		return s, nil
	} else if !errors.Is(err, session.ErrNotFound) {
		return nil, err
	}
	var s *session.Session
	switch {
	case token != "":
		s = session.FromToken(token)
	case vkidCfg != nil:
		tok, err := vkid.Login(ctx, *vkidCfg, vkid.LoginOptions{
			OnURL: func(u string) {
				fmt.Fprintln(os.Stderr, strings.Repeat("-", 72))
				fmt.Fprintln(os.Stderr, "Open this URL in a browser, then scan the QR code with the VK app:")
				fmt.Fprintln(os.Stderr, u)
				fmt.Fprintln(os.Stderr, strings.Repeat("-", 72))
			},
		})
		if err != nil {
			return nil, err
		}
		s = tok.Session()
	default:
		return nil, errors.New("pass -token or -client-id")
	}
	if err := session.Save(ctx, store, s); err != nil {
		return nil, err
	}
	return s, nil
}
