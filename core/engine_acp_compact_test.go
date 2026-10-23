package core

import (
	"errors"
	"strings"
	"testing"
	"time"
)

type saturatedACPForwardSession struct {
	stubAgentSession
	events    chan Event
	sendError error
}

func (s *saturatedACPForwardSession) Events() <-chan Event { return s.events }
func (s *saturatedACPForwardSession) Send(_ string, _ string, _ []ImageAttachment, _ []FileAttachment) error {
	if s.sendError != nil {
		return s.sendError
	}
	// Mirrors a synchronous ACP session/prompt that emits notifications before
	// replying to JSON-RPC. Without concurrent draining, 128 events block Send.
	for i := 0; i < 200; i++ {
		s.events <- Event{Type: EventText}
	}
	s.events <- Event{Type: EventResult, Content: "compact complete", Done: true}
	return nil
}
func TestACPForwardedCompactDrainsEventsWhileSendWaits(t *testing.T) {
	platform := &stubPlatformEngine{n: "test"}
	agent := &stubCompressorAgent{cmd: "/compact"}
	e := NewEngine("test", agent, []Platform{platform}, "", LangEnglish)
	defer e.Stop()
	e.eventIdleTimeout = 500 * time.Millisecond
	key := "test:compact-backpressure"
	live := &saturatedACPForwardSession{events: make(chan Event, 128)}
	state := &interactiveState{agentSession: live, platform: platform, replyCtx: "ctx"}
	e.interactiveMu.Lock()
	e.interactiveStates[key] = state
	e.interactiveMu.Unlock()
	e.cmdCompress(platform, &Message{SessionKey: key, Content: "/compress", ReplyCtx: "ctx"})
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(strings.Join(platform.getSent(), "\n"), "compact complete") {
			if e.sessions.GetOrCreateActive(key).Busy() {
				t.Fatal("session remains busy after compact")
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("200 ACP notifications blocked compression: %v", platform.getSent())
}
func TestACPForwardedCompactSendErrorDoesNotHang(t *testing.T) {
	platform := &stubPlatformEngine{n: "test"}
	agent := &stubCompressorAgent{cmd: "/compact"}
	e := NewEngine("test", agent, []Platform{platform}, "", LangEnglish)
	defer e.Stop()
	e.eventIdleTimeout = 3 * time.Second
	key := "test:compact-error"
	live := &saturatedACPForwardSession{events: make(chan Event, 128), sendError: errors.New("fake ACP failure")}
	state := &interactiveState{agentSession: live, platform: platform, replyCtx: "ctx"}
	e.interactiveMu.Lock()
	e.interactiveStates[key] = state
	e.interactiveMu.Unlock()
	e.cmdCompress(platform, &Message{SessionKey: key, Content: "/compress", ReplyCtx: "ctx"})
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(strings.Join(platform.getSent(), "\n"), "fake ACP failure") {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("send error not surfaced: %v", platform.getSent())
}

func TestForwardedCommandTimeoutIsCompressionOnly(t *testing.T) {
	if got := forwardedCommandTimeout(true); got != 5*time.Minute {
		t.Fatalf("compression operation timeout=%v", got)
	}
	if got := forwardedCommandTimeout(false); got != 0 {
		t.Fatalf("other forwarded operations must retain old timeout=%v", got)
	}
}

// The ACP adapter may emit EventError while its synchronous Send is still
// waiting for session/prompt. The engine must not unlock the chat until Send
// itself finishes, even when the error notification arrives first.
type delayedErrorACPForwardSession struct {
	stubAgentSession
	events  chan Event
	notify  chan struct{}
	release chan struct{}
}

func (s *delayedErrorACPForwardSession) Events() <-chan Event { return s.events }
func (s *delayedErrorACPForwardSession) Send(_ string, _ string, _ []ImageAttachment, _ []FileAttachment) error {
	s.events <- Event{Type: EventError, Error: errors.New("early ACP error")}
	close(s.notify)
	<-s.release
	return nil
}
func TestACPForwardedCompactHoldsSessionLockUntilSendReturns(t *testing.T) {
	p := &stubPlatformEngine{n: "test"}
	a := &stubCompressorAgent{cmd: "/compact"}
	e := NewEngine("test", a, []Platform{p}, "", LangEnglish)
	defer e.Stop()
	e.eventIdleTimeout = 2 * time.Second
	key := "test:compact-early-error"
	live := &delayedErrorACPForwardSession{events: make(chan Event, 8), notify: make(chan struct{}), release: make(chan struct{})}
	state := &interactiveState{agentSession: live, platform: p, replyCtx: "ctx"}
	e.interactiveMu.Lock()
	e.interactiveStates[key] = state
	e.interactiveMu.Unlock()
	e.cmdCompress(p, &Message{SessionKey: key, Content: "/compress", ReplyCtx: "ctx"})
	select {
	case <-live.notify:
	case <-time.After(time.Second):
		t.Fatal("ACP did not report early error")
	}
	// Allow the foreground event consumer to process the error while Send
	// remains blocked. This must not release the user-visible session lock.
	time.Sleep(80 * time.Millisecond)
	if !e.sessions.GetOrCreateActive(key).Busy() {
		close(live.release)
		t.Fatal("session unlocked before ACP Send returned")
	}
	close(live.release)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(strings.Join(p.getSent(), "\n"), "early ACP error") {
			if e.sessions.GetOrCreateActive(key).Busy() {
				t.Fatal("session stays busy after Send and error handling")
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("early ACP error was not reported: %v", p.getSent())
}
