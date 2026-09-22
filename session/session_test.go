package session

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestMemoryRoundTrip(t *testing.T) {
	ctx := context.Background()
	st := &Memory{}
	if _, err := Load(ctx, st); !errors.Is(err, ErrNotFound) {
		t.Fatalf("empty load: %v", err)
	}
	want := &Session{UserID: 1, AccessToken: "tok", RefreshToken: "r", DeviceID: "d", ClientID: "c", ExpiresAt: time.Unix(100, 0).UTC()}
	if err := Save(ctx, st, want); err != nil {
		t.Fatal(err)
	}
	got, err := Load(ctx, st)
	if err != nil {
		t.Fatal(err)
	}
	if *got != *want {
		t.Fatalf("got %+v want %+v", got, want)
	}
	if !got.Refreshable() {
		t.Fatal("must be refreshable")
	}
	if !got.Expired(time.Unix(100, 0)) || got.Expired(time.Unix(99, 0)) {
		t.Fatal("Expired")
	}
}

func TestSaveRejectsEmptyToken(t *testing.T) {
	if err := Save(context.Background(), &Memory{}, &Session{}); err == nil {
		t.Fatal("expected error")
	}
}

func TestFileStorage(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "nested", "s.json")
	st := NewFile(path)
	if _, err := st.LoadSession(ctx); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing file: %v", err)
	}
	s := FromToken("abc")
	if err := Save(ctx, st, s); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("perm %o", info.Mode().Perm())
		}
	}
	got, err := Load(ctx, st)
	if err != nil {
		t.Fatal(err)
	}
	if got.AccessToken != "abc" || got.Refreshable() || got.Expired(time.Now()) {
		t.Fatalf("got %+v", got)
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Fatalf("temp file left behind: %v", entries)
	}
}
