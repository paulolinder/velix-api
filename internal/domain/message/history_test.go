package message

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"velix/internal/engine"
)

// fakeRepo implements only what the history code paths touch; any other
// Repository method panics via the nil embedded interface.
type fakeRepo struct {
	Repository
	oldest  *Message
	saved   []*Message
	seenIDs map[string]bool
}

func (f *fakeRepo) OldestByChat(_ context.Context, _, chatJID string) (*Message, error) {
	if f.oldest == nil || f.oldest.ChatJID != chatJID {
		return nil, ErrNotFound
	}
	return f.oldest, nil
}

func (f *fakeRepo) CreateIfAbsent(_ context.Context, msg *Message) (bool, error) {
	if f.seenIDs == nil {
		f.seenIDs = map[string]bool{}
	}
	if f.seenIDs[msg.WhatsAppMessageID] {
		return false, nil
	}
	f.seenIDs[msg.WhatsAppMessageID] = true
	f.saved = append(f.saved, msg)
	return true, nil
}

type fakeEngine struct {
	engine.Engine
	calls  int
	anchor engine.HistoryAnchor
	count  int
}

func (f *fakeEngine) RequestHistory(_ context.Context, _ string, anchor engine.HistoryAnchor, count int) error {
	f.calls++
	f.anchor = anchor
	f.count = count
	return nil
}

func newTestService(repo *fakeRepo, eng *fakeEngine) *Service {
	return &Service{repo: repo, engine: eng, log: zerolog.Nop()}
}

const groupJID = "120363012345678901@g.us"

func TestRequestHistory_NoAnchor(t *testing.T) {
	eng := &fakeEngine{}
	svc := newTestService(&fakeRepo{}, eng)

	_, err := svc.RequestHistory(context.Background(), "inst", groupJID, 10)

	if !errors.Is(err, ErrNoHistoryAnchor) {
		t.Fatalf("err = %v, want ErrNoHistoryAnchor", err)
	}
	if eng.calls != 0 {
		t.Errorf("engine called %d times, want 0", eng.calls)
	}
}

func TestRequestHistory_UsesOldestMessageAsAnchor(t *testing.T) {
	sent := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	repo := &fakeRepo{oldest: &Message{
		ChatJID:           groupJID,
		WhatsAppMessageID: "3EB0OLDEST",
		Direction:         DirectionOutbound,
		SentAt:            &sent,
	}}
	eng := &fakeEngine{}
	svc := newTestService(repo, eng)

	hr, err := svc.RequestHistory(context.Background(), "inst", "  "+groupJID+" ", 30)
	if err != nil {
		t.Fatalf("RequestHistory: %v", err)
	}

	want := engine.HistoryAnchor{ChatJID: groupJID, MessageID: "3EB0OLDEST", FromMe: true, Timestamp: sent}
	if eng.anchor != want {
		t.Errorf("anchor = %+v, want %+v", eng.anchor, want)
	}
	if eng.count != 30 || hr.Count != 30 {
		t.Errorf("count = %d (resp %d), want 30", eng.count, hr.Count)
	}
	if hr.AnchorMessageID != "3EB0OLDEST" || hr.ChatJID != groupJID {
		t.Errorf("response = %+v", hr)
	}
}

func TestRequestHistory_CountBounds(t *testing.T) {
	sent := time.Now()
	for _, tc := range []struct{ in, want int }{
		{0, DefaultHistoryCount},
		{-5, DefaultHistoryCount},
		{500, MaxHistoryCount},
	} {
		repo := &fakeRepo{oldest: &Message{ChatJID: groupJID, WhatsAppMessageID: "X", SentAt: &sent}}
		eng := &fakeEngine{}
		if _, err := newTestService(repo, eng).RequestHistory(context.Background(), "inst", groupJID, tc.in); err != nil {
			t.Fatalf("count %d: %v", tc.in, err)
		}
		if eng.count != tc.want {
			t.Errorf("count %d -> %d, want %d", tc.in, eng.count, tc.want)
		}
	}
}

func TestPersistHistory_GroupMessagesAndDedup(t *testing.T) {
	repo := &fakeRepo{seenIDs: map[string]bool{"LIVE1": true}} // already stored by live sync
	svc := newTestService(repo, &fakeEngine{})
	ts := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)

	svc.persistHistory(context.Background(), "inst", &engine.HistorySyncPayload{
		OwnJID: "5511999990000@s.whatsapp.net",
		Messages: []engine.HistorySyncMessage{
			{ChatJID: groupJID, SenderJID: "5511999990001@s.whatsapp.net", IsGroup: true, MessageID: "A", Text: "oi", Type: "text", Timestamp: ts, PushName: "Ana"},
			{ChatJID: groupJID, SenderJID: "me", FromMe: true, IsGroup: true, MessageID: "B", Text: "olá", Type: "text", Timestamp: ts},
			{ChatJID: groupJID, SenderJID: "5511999990002@s.whatsapp.net", IsGroup: true, MessageID: "LIVE1", Text: "dup", Type: "text", Timestamp: ts},
			{ChatJID: groupJID, MessageID: "", Text: "no id"},
		},
	})

	if len(repo.saved) != 2 {
		t.Fatalf("saved %d messages, want 2", len(repo.saved))
	}

	in := repo.saved[0]
	if in.Direction != DirectionInbound || in.FromJID != "5511999990001@s.whatsapp.net" || !in.IsGroup ||
		in.ChatJID != groupJID || in.SentAt == nil || !in.SentAt.Equal(ts) {
		t.Errorf("inbound row = %+v", in)
	}
	if in.Content["text"] != "oi" || in.Content["push_name"] != "Ana" || in.Content["history"] != true {
		t.Errorf("inbound content = %v", in.Content)
	}

	out := repo.saved[1]
	if out.Direction != DirectionOutbound || out.Status != StatusSent || out.FromJID != "5511999990000@s.whatsapp.net" {
		t.Errorf("outbound row = %+v", out)
	}
}
