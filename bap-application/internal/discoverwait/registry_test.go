package discoverwait

import (
	"encoding/json"
	"testing"
	"time"
)

func TestRegistry_DeliverWakesUpTheRegisteredWaiter(t *testing.T) {
	r := NewRegistry()
	ch := r.Register("txn-1")

	payload := json.RawMessage(`{"context":{},"message":{"catalogs":[]}}`)
	if ok := r.Deliver("txn-1", payload); !ok {
		t.Fatal("Deliver() = false, want true (a waiter was registered)")
	}

	select {
	case got := <-ch:
		if string(got) != string(payload) {
			t.Errorf("received payload = %s, want %s", got, payload)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for delivered payload")
	}
}

func TestRegistry_DeliverToUnregisteredTransactionReturnsFalse(t *testing.T) {
	r := NewRegistry()
	if ok := r.Deliver("no-such-txn", json.RawMessage(`{}`)); ok {
		t.Error("Deliver() = true, want false for a transaction nobody registered")
	}
}

func TestRegistry_CancelPreventsALateDeliveryFromBlocking(t *testing.T) {
	r := NewRegistry()
	r.Register("txn-1")
	r.Cancel("txn-1")

	if ok := r.Deliver("txn-1", json.RawMessage(`{}`)); ok {
		t.Error("Deliver() = true, want false after Cancel removed the waiter")
	}
}

func TestRegistry_DeliverOnlyDeliversOnce(t *testing.T) {
	r := NewRegistry()
	r.Register("txn-1")

	if ok := r.Deliver("txn-1", json.RawMessage(`{"first":true}`)); !ok {
		t.Fatal("first Deliver() = false, want true")
	}
	if ok := r.Deliver("txn-1", json.RawMessage(`{"second":true}`)); ok {
		t.Error("second Deliver() = true, want false (waiter already consumed and removed)")
	}
}

func TestRegistry_IndependentTransactionsDoNotInterfere(t *testing.T) {
	r := NewRegistry()
	chA := r.Register("txn-a")
	chB := r.Register("txn-b")

	r.Deliver("txn-b", json.RawMessage(`{"who":"b"}`))

	select {
	case got := <-chB:
		if string(got) != `{"who":"b"}` {
			t.Errorf("chB received %s, want txn-b's payload", got)
		}
	default:
		t.Fatal("chB should have received its payload immediately (buffered channel)")
	}

	select {
	case got := <-chA:
		t.Fatalf("chA received %s, want nothing — its transaction was never delivered", got)
	default:
	}
}
