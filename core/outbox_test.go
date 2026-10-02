package core

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testOutboxCfg() OutboxConfig {
	return OutboxConfig{
		Enabled:       true,
		MaxAge:        30 * time.Minute,
		MaxAttempts:   5,
		InitialDelay:  time.Millisecond,
		MaxDelay:      10 * time.Millisecond,
		SweepInterval: time.Hour, // tests drive sweep()/Kick() explicitly
		InitialGrace:  0,         // no grace in tests so sweep picks up immediately
	}
}

type permanentOutboxPlatform struct {
	stubPlatformEngine
	attempts      []string
	permanentBody string
	failAll       bool
}

type tableChunkOutboxPlatform struct {
	permanentOutboxPlatform
	splitCalls int
}

func (p *tableChunkOutboxPlatform) SplitMarkdownByTables(md string, _ int) []string {
	p.splitCalls++
	return strings.Split(md, "\n--CARD-SPLIT--\n")
}

func (p *permanentOutboxPlatform) Send(ctx context.Context, r any, content string) error {
	p.mu.Lock()
	p.attempts = append(p.attempts, content)
	fail := p.failAll || content == p.permanentBody
	p.mu.Unlock()
	if fail {
		return errors.New("platform rejected message payload")
	}
	return p.stubPlatformEngine.Send(ctx, r, content)
}

func (p *permanentOutboxPlatform) EncodeReplyCtx(r any) ([]byte, error) {
	s, ok := r.(string)
	if !ok {
		return nil, errors.New("reply context is not a string")
	}
	return []byte(s), nil
}

func (p *permanentOutboxPlatform) DecodeReplyCtx(b []byte) (any, error) {
	return string(b), nil
}

func (p *permanentOutboxPlatform) IsRetryableSendError(error) bool { return false }

func (p *permanentOutboxPlatform) getAttempts() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]string, len(p.attempts))
	copy(out, p.attempts)
	return out
}

func newPermanentOutboxTestEngine(t *testing.T, p *permanentOutboxPlatform) *Engine {
	t.Helper()
	return &Engine{
		ctx:           context.Background(),
		i18n:          NewI18n(LangEnglish),
		outbox:        NewOutbox(t.TempDir(), testOutboxCfg()),
		platformReady: map[Platform]bool{p: true},
	}
}

func TestSendFinalWithOutbox_PermanentFailureNotifiesAndDrops(t *testing.T) {
	const original = "original final payload"
	p := &permanentOutboxPlatform{
		stubPlatformEngine: stubPlatformEngine{n: "test"},
		permanentBody:      original,
	}
	e := newPermanentOutboxTestEngine(t, p)

	sendFn := func(platform Platform, replyCtx any, content string) error {
		return platform.Send(context.Background(), replyCtx, content)
	}
	if ok := e.sendFinalWithOutbox("test:u1", context.Background(), p, "ctx-u1", original, "", sendFn); ok {
		t.Fatal("permanent send failure must report final delivery failure")
	}
	if got := e.outbox.PendingCount(); got != 0 {
		t.Fatalf("permanent failure must be dropped from outbox, pending=%d", got)
	}
	attempts := p.getAttempts()
	if len(attempts) != 2 {
		t.Fatalf("send attempts=%d, want original + one failure notice: %v", len(attempts), attempts)
	}
	if attempts[0] != original {
		t.Fatalf("first attempt=%q, want original payload", attempts[0])
	}
	if attempts[1] != e.i18n.T(MsgFinalReplyDeliveryFailed) {
		t.Fatalf("second attempt=%q, want localized failure notice", attempts[1])
	}
	if attempts[1] == original {
		t.Fatal("failure notice must not resend the rejected payload")
	}
}

func TestSendFinalWithOutbox_PermanentFailureNoticeDoesNotRecurse(t *testing.T) {
	p := &permanentOutboxPlatform{
		stubPlatformEngine: stubPlatformEngine{n: "test"},
		failAll:            true,
	}
	e := newPermanentOutboxTestEngine(t, p)

	sendFn := func(platform Platform, replyCtx any, content string) error {
		return platform.Send(context.Background(), replyCtx, content)
	}
	if ok := e.sendFinalWithOutbox("test:u1", context.Background(), p, "ctx-u1", "original", "", sendFn); ok {
		t.Fatal("permanent send failure must report final delivery failure")
	}
	if got := len(p.getAttempts()); got != 2 {
		t.Fatalf("failure notice must be attempted exactly once without recursion; attempts=%d", got)
	}
	if got := e.outbox.PendingCount(); got != 0 {
		t.Fatalf("permanent failure must not remain queued, pending=%d", got)
	}
}

func TestReplayOutboxItem_PermanentFailureNotifiesOnce(t *testing.T) {
	const original = "replayed final payload"
	p := &permanentOutboxPlatform{
		stubPlatformEngine: stubPlatformEngine{n: "test"},
		permanentBody:      original,
	}
	e := newPermanentOutboxTestEngine(t, p)
	it := &OutboxItem{
		ID:         "outbox-item",
		UUID:       "outbox-uuid",
		Platform:   p.Name(),
		SessionKey: "test:u1",
		ReplyCtx:   []byte("ctx-u1"),
		Body:       original,
	}

	done, err := e.replayOutboxItem(context.Background(), it)
	if err != nil || !done {
		t.Fatalf("replayOutboxItem() = done=%v err=%v, want true,nil for permanent failure", done, err)
	}
	attempts := p.getAttempts()
	if len(attempts) != 2 {
		t.Fatalf("replay attempts=%d, want rejected payload + one failure notice: %v", len(attempts), attempts)
	}
	if attempts[0] != original || attempts[1] != e.i18n.T(MsgFinalReplyDeliveryFailed) {
		t.Fatalf("unexpected replay attempt sequence: %v", attempts)
	}
}

func TestReplayOutboxItem_PreservesV1AndUsesV2PlatformTableChunking(t *testing.T) {
	const body = "**part one**\n--CARD-SPLIT--\n**part two**"

	newEngine := func(p Platform) *Engine {
		return &Engine{
			ctx:           context.Background(),
			i18n:          NewI18n(LangEnglish),
			outbox:        NewOutbox(t.TempDir(), testOutboxCfg()),
			platformReady: map[Platform]bool{p: true},
		}
	}

	t.Run("v1 keeps historical length-only boundaries", func(t *testing.T) {
		p := &tableChunkOutboxPlatform{permanentOutboxPlatform: permanentOutboxPlatform{stubPlatformEngine: stubPlatformEngine{n: "test"}}}
		e := newEngine(p)
		it := &OutboxItem{
			ID:            "v1",
			UUID:          "uuid-v1",
			Platform:      p.Name(),
			ReplyCtx:      []byte("ctx"),
			Body:          body,
			SplitVersion:  1,
			SplitMaxRunes: 6000,
		}
		done, err := e.replayOutboxItem(context.Background(), it)
		if err != nil || !done {
			t.Fatalf("v1 replay = done=%v err=%v", done, err)
		}
		if p.splitCalls != 0 {
			t.Fatalf("v1 replay must not use platform table splitter; calls=%d", p.splitCalls)
		}
		if attempts := p.getAttempts(); len(attempts) != 1 || attempts[0] != body {
			t.Fatalf("v1 replay attempts=%#v, want original body as one chunk", attempts)
		}
	})

	t.Run("v2 applies platform table boundaries", func(t *testing.T) {
		p := &tableChunkOutboxPlatform{permanentOutboxPlatform: permanentOutboxPlatform{stubPlatformEngine: stubPlatformEngine{n: "test"}}}
		e := newEngine(p)
		it := &OutboxItem{
			ID:            "v2",
			UUID:          "uuid-v2",
			Platform:      p.Name(),
			ReplyCtx:      []byte("ctx"),
			Body:          body,
			SplitVersion:  2,
			SplitMaxRunes: 6000,
		}
		done, err := e.replayOutboxItem(context.Background(), it)
		if err != nil || !done {
			t.Fatalf("v2 replay = done=%v err=%v", done, err)
		}
		if p.splitCalls != 1 {
			t.Fatalf("v2 replay splitter calls=%d, want 1", p.splitCalls)
		}
		attempts := p.getAttempts()
		if len(attempts) != 2 || attempts[0] != "**part one**" || attempts[1] != "**part two**" {
			t.Fatalf("v2 replay attempts=%#v", attempts)
		}
	})
}

func TestOutbox_AddReturnsEmptyWhenInitialPersistFails(t *testing.T) {
	parent := t.TempDir()
	blocker := filepath.Join(parent, "not-a-directory")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	o := NewOutbox(filepath.Join(blocker, "outbox"), testOutboxCfg())

	if id := o.Add("feishu", "k", []byte(`{}`), "body", ""); id != "" {
		t.Fatalf("Add returned %q, want empty when initial persistence fails", id)
	}
	if got := o.PendingCount(); got != 0 {
		t.Fatalf("PendingCount = %d, want 0 after failed initial persistence", got)
	}
}

func TestOutbox_AddAndCompleteRemovesFile(t *testing.T) {
	dir := t.TempDir()
	o := NewOutbox(dir, testOutboxCfg())
	id := o.Add("feishu", "feishu:c:u", []byte(`{}`), "hello", "")
	if id == "" {
		t.Fatal("expected non-empty id")
	}
	if _, err := os.Stat(filepath.Join(dir, id+".json")); err != nil {
		t.Fatalf("expected persisted item file: %v", err)
	}
	if got := o.PendingCount(); got != 1 {
		t.Fatalf("PendingCount = %d, want 1", got)
	}

	var sent *OutboxItem
	done, err := o.deliverOnce(context.Background(), id, func(_ context.Context, it *OutboxItem) (bool, error) {
		sent = it
		return true, nil
	})
	if !done || err != nil {
		t.Fatalf("deliverOnce = %v,%v want true,nil", done, err)
	}
	if sent == nil || sent.Body != "hello" {
		t.Fatalf("sender received wrong item: %+v", sent)
	}
	if o.PendingCount() != 0 {
		t.Fatalf("PendingCount = %d after complete, want 0", o.PendingCount())
	}
	if _, err := os.Stat(filepath.Join(dir, id+".json")); !os.IsNotExist(err) {
		t.Fatalf("expected item file removed, got err=%v", err)
	}
}

// deliverOnce mirrors one sweep delivery of a single item by id.
func (o *Outbox) deliverOnce(ctx context.Context, id string, s OutboxSender) (bool, error) {
	o.mu.Lock()
	it, ok := o.items[id]
	o.mu.Unlock()
	if !ok {
		return true, nil
	}
	done, err := s(ctx, it)
	if done {
		o.Complete(id)
	} else if err != nil {
		o.Fail(id, err)
	}
	return done, err
}

func TestOutbox_RetryThenSucceed(t *testing.T) {
	dir := t.TempDir()
	o := NewOutbox(dir, testOutboxCfg())
	id := o.Add("feishu", "feishu:c:u", []byte(`{}`), "body", "")

	var calls int
	transient := errors.New("dial tcp: connection refused")
	for i := 0; i < 2; i++ {
		done, err := o.deliverOnce(context.Background(), id, func(_ context.Context, _ *OutboxItem) (bool, error) {
			calls++
			return false, transient
		})
		if done {
			t.Fatalf("attempt %d should not be done", i)
		}
		if err == nil {
			t.Fatalf("attempt %d should return error", i)
		}
		time.Sleep(3 * time.Millisecond) // let backoff window pass
	}
	done, _ := o.deliverOnce(context.Background(), id, func(_ context.Context, _ *OutboxItem) (bool, error) {
		calls++
		return true, nil
	})
	if !done {
		t.Fatal("third attempt should complete")
	}
	if calls != 3 {
		t.Fatalf("calls = %d, want 3", calls)
	}
	if o.PendingCount() != 0 {
		t.Fatalf("PendingCount = %d, want 0", o.PendingCount())
	}
}

func TestOutbox_ChunkProgressPersisted(t *testing.T) {
	dir := t.TempDir()
	cfg := testOutboxCfg()
	o := NewOutbox(dir, cfg)
	id := o.Add("feishu", "k", []byte(`{}`), "b", "")
	if err := o.SetSplitPlan(id, finalReplySplitVersion, 6000); err != nil {
		t.Fatal(err)
	}
	o.SetChunkProgress(id, 2)

	// Simulate restart: a fresh outbox over the same directory.
	o2 := NewOutbox(dir, cfg)
	if err := o2.Load(); err != nil {
		t.Fatal(err)
	}
	o2.mu.Lock()
	it := o2.items[id]
	o2.mu.Unlock()
	if it == nil {
		t.Fatal("expected item recovered after restart")
	}
	if it.SentChunks != 2 {
		t.Fatalf("SentChunks = %d, want 2", it.SentChunks)
	}
	if it.SplitVersion != finalReplySplitVersion || it.SplitMaxRunes != 6000 {
		t.Fatalf("split plan = version %d max %d, want version %d max 6000", it.SplitVersion, it.SplitMaxRunes, finalReplySplitVersion)
	}
	if it.Platform != "feishu" || it.SessionKey != "k" || it.Body != "b" {
		t.Fatalf("recovered item mismatch: %+v", it)
	}
}

func TestOutbox_SetSplitPlanFailureRevertsInMemoryPlan(t *testing.T) {
	dir := t.TempDir()
	o := NewOutbox(dir, testOutboxCfg())
	id := o.Add("feishu", "k", []byte(`{}`), "body", "")
	if id == "" {
		t.Fatal("expected initial outbox persist to succeed")
	}

	// Replace the persisted item file with a directory so AtomicWriteFile's
	// rename and direct-write fallback both fail deterministically.
	target := filepath.Join(dir, id+".json")
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := o.SetSplitPlan(id, finalReplySplitVersion, 6000); err == nil {
		t.Fatal("SetSplitPlan should report persistence failure")
	}
	o.mu.Lock()
	it := o.items[id]
	o.mu.Unlock()
	if it == nil {
		t.Fatal("item should remain in memory after split-plan persistence failure")
	}
	if it.SplitVersion != 0 || it.SplitMaxRunes != 0 {
		t.Fatalf("failed split-plan persist must revert in-memory plan; got version=%d max=%d", it.SplitVersion, it.SplitMaxRunes)
	}
}

func TestOutbox_MaxAttemptsMarksDead(t *testing.T) {
	dir := t.TempDir()
	cfg := testOutboxCfg()
	cfg.MaxAttempts = 2
	o := NewOutbox(dir, cfg)
	id := o.Add("feishu", "k", []byte(`{}`), "b", "")
	transient := errors.New("i/o timeout")
	for i := 0; i < 2; i++ {
		time.Sleep(2 * time.Millisecond)
		o.deliverOnce(context.Background(), id, func(_ context.Context, _ *OutboxItem) (bool, error) {
			return false, transient
		})
	}
	if o.PendingCount() != 0 {
		t.Fatalf("item should be dead after MaxAttempts, pending=%d", o.PendingCount())
	}
	if _, err := os.Stat(filepath.Join(dir, id+".json")); !os.IsNotExist(err) {
		t.Fatal("dead item file should be removed")
	}
}

func TestOutbox_ExpiredByAgeDroppedOnLoad(t *testing.T) {
	dir := t.TempDir()
	cfg := testOutboxCfg()
	o := NewOutbox(dir, cfg)
	id := o.Add("feishu", "k", []byte(`{}`), "b", "")
	// Age it beyond MaxAge.
	o.mu.Lock()
	o.items[id].CreatedAt = time.Now().Add(-31 * time.Minute)
	o.persistLocked(o.items[id])
	o.mu.Unlock()

	o2 := NewOutbox(dir, cfg)
	if err := o2.Load(); err != nil {
		t.Fatal(err)
	}
	if o2.PendingCount() != 0 {
		t.Fatalf("expired item should be dropped on load, pending=%d", o2.PendingCount())
	}
}

func TestOutbox_DueOrderedOldestFirst(t *testing.T) {
	dir := t.TempDir()
	o := NewOutbox(dir, testOutboxCfg())
	now := time.Now()
	o.mu.Lock()
	for i, key := range []string{"oldest", "middle", "newest"} {
		it := &OutboxItem{ID: key, SessionKey: "s", CreatedAt: now.Add(time.Duration(i) * time.Second), NextAt: now}
		o.items[key] = it
	}
	due := o.dueLocked(now.Add(time.Minute))
	o.mu.Unlock()
	if len(due) != 3 {
		t.Fatalf("due len = %d, want 3", len(due))
	}
	if due[0].ID != "oldest" || due[1].ID != "middle" || due[2].ID != "newest" {
		t.Fatalf("wrong order: %s,%s,%s", due[0].ID, due[1].ID, due[2].ID)
	}
}

func TestOutbox_DisabledIsNoop(t *testing.T) {
	cfg := testOutboxCfg()
	cfg.Enabled = false
	o := NewOutbox(t.TempDir(), cfg)
	if o.Enabled() {
		t.Fatal("should be disabled")
	}
	if id := o.Add("feishu", "k", nil, "b", ""); id != "" {
		t.Fatalf("Add on disabled outbox returned %q, want empty", id)
	}
	o.Kick() // must not block/panic
	if o.PendingCount() != 0 {
		t.Fatal("disabled outbox must stay empty")
	}
}

func TestOutbox_RunRedeliversOnKick(t *testing.T) {
	dir := t.TempDir()
	cfg := testOutboxCfg()
	cfg.SweepInterval = time.Hour
	o := NewOutbox(dir, cfg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var calls int
	go o.Run(ctx, func(_ context.Context, _ *OutboxItem) (bool, error) {
		calls++
		if calls < 2 {
			return false, errors.New("connection refused")
		}
		return true, nil
	})
	id := o.Add("feishu", "k", []byte(`{}`), "b", "")
	// Immediate send failed and yielded: Fail releases the immediate hold so
	// the replayer may take over (a still-held item stays blocked from sweep).
	if ok := o.Fail(id, errors.New("immediate send failed")); !ok {
		t.Fatal("immediate Fail should keep the item for replay")
	}
	// First attempt happens on Add->Kick; wait, then kick again after backoff.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
		if o.PendingCount() == 0 {
			break
		}
		o.Kick()
	}
	if o.PendingCount() != 0 {
		t.Fatalf("reply not redelivered, pending=%d calls=%d", o.PendingCount(), calls)
	}
	if calls < 2 {
		t.Fatalf("expected at least one retry, calls=%d", calls)
	}
	if _, err := os.Stat(filepath.Join(dir, id+".json")); !os.IsNotExist(err) {
		t.Fatal("redelivered item file should be removed")
	}
}

// Regression for duplicate final replies: while the immediate send path owns
// the item (held), the replayer sweep must never race it, no matter how overdue
// the item looks or how slowly the immediate send progresses.
func TestOutbox_ImmediateHoldBlocksSweep(t *testing.T) {
	o := NewOutbox(t.TempDir(), testOutboxCfg())
	id := o.Add("feishu", "k", []byte(`{}`), "b", "")

	// Force it overdue: a held item must still be invisible to the sweep.
	o.mu.Lock()
	o.items[id].NextAt = time.Now().Add(-time.Minute)
	due := o.dueLocked(time.Now())
	o.mu.Unlock()
	for _, it := range due {
		if it.ID == id {
			t.Fatal("held item must not be due while the immediate send is in flight")
		}
	}

	calls := 0
	o.setSender(func(context.Context, *OutboxItem) (bool, error) {
		calls++
		return true, nil
	})
	o.sweep(context.Background())
	if calls != 0 {
		t.Fatalf("sweep invoked sender %d times on a held item, want 0", calls)
	}

	// Immediate success removes the item -> nothing left to replay.
	o.Complete(id)
	if o.PendingCount() != 0 {
		t.Fatalf("PendingCount=%d after Complete, want 0", o.PendingCount())
	}
}

// Once the immediate path reports a retryable error (Fail), the hold is
// released and the replayer may take over after the backoff window.
func TestOutbox_ImmediateFailReleasesHoldForReplay(t *testing.T) {
	o := NewOutbox(t.TempDir(), testOutboxCfg())
	id := o.Add("feishu", "k", []byte(`{}`), "b", "")
	if ok := o.Fail(id, errors.New("connection reset by peer")); !ok {
		t.Fatal("Fail should keep the item scheduled for replay")
	}
	o.mu.Lock()
	held := o.items[id].immediateHeld
	o.items[id].NextAt = time.Now().Add(-time.Second) // backoff elapsed
	due := o.dueLocked(time.Now())
	o.mu.Unlock()
	if held {
		t.Fatal("Fail must release the immediate hold")
	}
	if len(due) != 1 || due[0].ID != id {
		t.Fatalf("released item should be due exactly once, got %d", len(due))
	}
}

// The immediate hold is in-process only: after a restart the item is loaded
// without the hold (so recovery can replay) while its idempotency UUID survives.
func TestOutbox_HoldReleasedAndUUIDPersistedAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	cfg := testOutboxCfg()
	o := NewOutbox(dir, cfg)
	id := o.Add("feishu", "k", []byte(`{}`), "b", "")
	wantUUID := o.ItemUUID(id)
	if wantUUID == "" {
		t.Fatal("expected a non-empty idempotency uuid")
	}

	o2 := NewOutbox(dir, cfg)
	if err := o2.Load(); err != nil {
		t.Fatal(err)
	}
	o2.mu.Lock()
	it := o2.items[id]
	o2.mu.Unlock()
	if it == nil {
		t.Fatal("item should be recovered after restart")
	}
	if it.immediateHeld {
		t.Fatal("immediate hold must NOT persist across restart")
	}
	if it.UUID != wantUUID {
		t.Fatalf("uuid not persisted: got %q want %q", it.UUID, wantUUID)
	}
}

func TestOutbox_UUIDUniquePerReply(t *testing.T) {
	o := NewOutbox(t.TempDir(), testOutboxCfg())
	a := o.Add("feishu", "k1", nil, "x", "")
	b := o.Add("feishu", "k2", nil, "y", "")
	ua, ub := o.ItemUUID(a), o.ItemUUID(b)
	if ua == "" || ub == "" || ua == ub {
		t.Fatalf("uuids must be non-empty and unique: %q %q", ua, ub)
	}
}

func TestOutboxCtxUUIDRoundTrip(t *testing.T) {
	if OutboxUUIDFromContext(nil) != "" {
		t.Fatal("nil ctx must yield empty uuid")
	}
	base := context.Background()
	if OutboxUUIDFromContext(base) != "" {
		t.Fatal("ctx without uuid must yield empty")
	}
	if WithOutboxUUID(base, "") != base {
		t.Fatal("empty uuid must return ctx unchanged")
	}
	want := "uuid-xyz"
	if got := OutboxUUIDFromContext(WithOutboxUUID(base, want)); got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

// Stop must halt the replayer and leave the data directory quiescent: later
// mutating calls short-circuit disk I/O, and Stop is idempotent / non-blocking.
func TestOutbox_StopQuiescesDisk(t *testing.T) {
	dir := t.TempDir()
	cfg := testOutboxCfg()
	cfg.SweepInterval = time.Hour
	o := NewOutbox(dir, cfg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go o.Run(ctx, func(context.Context, *OutboxItem) (bool, error) { return true, nil })

	id := o.Add("feishu", "k", []byte(`{}`), "b", "")
	if _, err := os.Stat(filepath.Join(dir, id+".json")); err != nil {
		t.Fatalf("item should be on disk before Stop: %v", err)
	}

	done := make(chan struct{})
	go func() { o.Stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop blocked; replayer did not unwind")
	}
	o.Stop() // idempotent: must not panic or block

	// After Stop, further mutations must not create/remove files on disk.
	o.SetChunkProgress(id, 1)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("disk mutated after Stop: %d entries, want 1", len(entries))
	}
}
