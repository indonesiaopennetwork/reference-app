// Package statusstream tells open GET /api/v1/status/:id/stream connections
// that their transaction changed, so bap-frontend gets a status change the
// moment the on_* callback is stored instead of on its next poll.
//
// The signal comes from Postgres: migration 017 sends
// NOTIFY bap_transaction_status '<transaction_id>' on every write to
// transactions or contract_snapshots, and Hub.Listen forwards each one to
// that transaction's subscribers. A notification carries only the id --
// subscribers re-read the state themselves (clientsvc.HandleStatusStream).
// Going through Postgres rather than an in-process call means a callback
// taken by one bap instance still reaches a browser streaming from another.
package statusstream

import (
	"context"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/remiges-tech/logharbour/logharbour"
)

// Channel is the Postgres NOTIFY channel migration 017 sends on.
const Channel = "bap_transaction_status"

// Hub fans transaction-change signals out to subscribers, keyed by
// transaction id. Safe for concurrent use.
type Hub struct {
	mu   sync.Mutex
	subs map[string]map[chan struct{}]struct{}
}

// NewHub returns a Hub with no subscribers.
func NewHub() *Hub {
	return &Hub{subs: make(map[string]map[chan struct{}]struct{})}
}

// Subscribe returns a channel that receives a value whenever transactionID
// may have changed, and a function that ends the subscription (call it
// exactly once). The channel is buffered (capacity 1) and signals coalesce:
// several changes before the subscriber reads collapse into one signal,
// which is fine because the subscriber re-reads the current state anyway.
// Subscribe before the first read of the state, so a change between that
// read and the subscription can't be missed.
func (h *Hub) Subscribe(transactionID string) (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	h.mu.Lock()
	if h.subs[transactionID] == nil {
		h.subs[transactionID] = make(map[chan struct{}]struct{})
	}
	h.subs[transactionID][ch] = struct{}{}
	h.mu.Unlock()

	return ch, func() {
		h.mu.Lock()
		delete(h.subs[transactionID], ch)
		if len(h.subs[transactionID]) == 0 {
			delete(h.subs, transactionID)
		}
		h.mu.Unlock()
	}
}

// Publish signals every subscriber of transactionID. It never blocks.
func (h *Hub) Publish(transactionID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs[transactionID] {
		signal(ch)
	}
}

// publishAll signals every subscriber of every transaction -- after the
// listen connection was lost, when notifications may have been missed.
func (h *Hub) publishAll() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, chans := range h.subs {
		for ch := range chans {
			signal(ch)
		}
	}
}

func signal(ch chan struct{}) {
	select {
	case ch <- struct{}{}:
	default: // a signal is already pending
	}
}

// Listen holds one pool connection LISTENing on Channel and publishes each
// notification, until ctx is done. If the connection fails it reconnects
// after a short wait and signals every subscriber, since changes made in
// between were not notified. Run it in its own goroutine.
func (h *Hub) Listen(ctx context.Context, pool *pgxpool.Pool, lh *logharbour.Logger) {
	log := lh.WithModule("statusstream")
	for {
		err := h.listenOnce(ctx, pool)
		if ctx.Err() != nil {
			return
		}
		log.Warn().Error(err).Log("transaction status listener lost its connection; reconnecting")
		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Second):
		}
	}
}

func (h *Hub) listenOnce(ctx context.Context, pool *pgxpool.Pool) error {
	pooled, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	// Take the connection out of the pool for good: one that was LISTENing
	// must not be handed to anyone else.
	conn := pooled.Hijack()
	defer conn.Close(context.Background())

	if _, err := conn.Exec(ctx, "LISTEN "+Channel); err != nil {
		return err
	}
	// Anything that changed while we weren't listening went unnotified.
	h.publishAll()

	for {
		n, err := conn.WaitForNotification(ctx)
		if err != nil {
			return err
		}
		h.Publish(n.Payload)
	}
}
