package statusstream

import "testing"

func received(ch <-chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

func TestPublishSignalsOnlyThatTransaction(t *testing.T) {
	h := NewHub()
	a, unsubA := h.Subscribe("a")
	defer unsubA()
	b, unsubB := h.Subscribe("b")
	defer unsubB()

	h.Publish("a")

	if !received(a) {
		t.Fatal("subscriber of a was not signalled")
	}
	if received(b) {
		t.Fatal("subscriber of b was signalled for a")
	}
}

func TestSignalsCoalesceAndNeverBlock(t *testing.T) {
	h := NewHub()
	ch, unsub := h.Subscribe("a")
	defer unsub()

	h.Publish("a")
	h.Publish("a") // must not block on the full buffer

	if !received(ch) {
		t.Fatal("no signal")
	}
	if received(ch) {
		t.Fatal("two publishes left two signals; want one")
	}
}

func TestUnsubscribeStopsSignalsAndCleansUp(t *testing.T) {
	h := NewHub()
	ch, unsub := h.Subscribe("a")
	unsub()

	h.Publish("a")

	if received(ch) {
		t.Fatal("signalled after unsubscribe")
	}
	if len(h.subs) != 0 {
		t.Fatalf("subs not cleaned up: %v", h.subs)
	}
}

func TestPublishAllSignalsEveryone(t *testing.T) {
	h := NewHub()
	a, unsubA := h.Subscribe("a")
	defer unsubA()
	b, unsubB := h.Subscribe("b")
	defer unsubB()

	h.publishAll()

	if !received(a) || !received(b) {
		t.Fatal("publishAll missed a subscriber")
	}
}
