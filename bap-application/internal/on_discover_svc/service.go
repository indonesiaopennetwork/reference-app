package ondiscoversvc

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/remiges-tech/logharbour/logharbour"

	dbsqlc "github.com/ion/winroom/bap/db/sqlc"
	"github.com/ion/winroom/bap/internal/config"
	"github.com/ion/winroom/bap/internal/discoverwait"
)

// OnDiscoverService records the catalogs received from the Discovery
// Service's on_discover callback. Unlike on_select/on_init/..., a discover
// exchange precedes any order transaction, so there is no transactions row
// to upsert here — the audit trail in beckn_message_log is the persisted
// record of what was discovered and when.
//
// It also delivers the payload to registry, which wakes up
// clientsvc.ClientService.Discover's HTTP request goroutine if one is
// still waiting on this transactionId — see internal/discoverwait's
// package doc for why this bridge exists.
type OnDiscoverService struct {
	pool     *pgxpool.Pool
	cfg      *config.Config
	lh       *logharbour.Logger
	registry *discoverwait.Registry
}

func NewOnDiscoverService(pool *pgxpool.Pool, cfg *config.Config, lh *logharbour.Logger, registry *discoverwait.Registry) *OnDiscoverService {
	return &OnDiscoverService{pool: pool, cfg: cfg, lh: lh, registry: registry}
}

// becknInActivity is the structured payload logged when receiving an inbound Beckn callback.
type becknInActivity struct {
	Action  string          `json:"action"`
	TxnID   string          `json:"txn_id"`
	MsgID   string          `json:"msg_id"`
	BapID   string          `json:"bap_id"`
	Payload json.RawMessage `json:"payload"`
}

// Process delivers the on_discover payload to any request still waiting on
// this transactionId, then writes it to beckn_message_log. Delivery runs
// first so a browser waiting on the synchronous /api/v1/discover facade
// gets its answer as soon as possible, rather than after a DB round-trip.
func (s *OnDiscoverService) Process(ctx context.Context, req *OnDiscoverRequest) {
	start := time.Now()
	reqJSON, _ := json.Marshal(req)

	if delivered := s.registry.Deliver(req.Context.TransactionID, reqJSON); !delivered {
		s.lh.WithModule("on_discover_svc").Warn().LogActivity(
			"on_discover callback arrived with no waiting request (already timed out, or an unsolicited/stale callback)",
			map[string]any{"transactionId": req.Context.TransactionID},
		)
	}

	q := dbsqlc.New(s.pool)

	var txnUUID pgtype.UUID
	if txID, err := uuid.Parse(req.Context.TransactionID); err == nil {
		txnUUID = pgtype.UUID{Bytes: txID, Valid: true}
	}
	msgID, err := uuid.Parse(req.Context.MessageID)
	if err != nil {
		msgID = uuid.New()
	}

	ms := int32(time.Since(start).Milliseconds())
	if err := q.InsertMessageLog(ctx, dbsqlc.InsertMessageLogParams{
		TransactionID:        txnUUID,
		MessageID:            msgID,
		Action:               dbsqlc.BecknActionOnDiscover,
		Direction:            dbsqlc.MessageDirectionINBOUND,
		Url:                  strPtr("/api/webhook/on_discover"),
		BapID:                strPtr(req.Context.BapID),
		BapUri:               strPtr(req.Context.BapURI),
		NetworkID:            strPtr(req.Context.NetworkID),
		AckStatus:            dbsqlc.NullAckStatus{AckStatus: dbsqlc.AckStatusACK, Valid: true},
		RequestPayload:       reqJSON,
		ProcessingDurationMs: &ms,
	}); err != nil {
		s.lh.WithModule("on_discover_svc").Warn().Error(err).Log(fmt.Sprintf("message log insert failed: %v", err))
	}

	s.lh.WithModule("on_discover_svc").LogActivity(
		fmt.Sprintf("← IN   ON_DISCOVER | txn=%.8s… | dur=%dms", req.Context.TransactionID, time.Since(start).Milliseconds()),
		becknInActivity{
			Action:  "on_discover",
			TxnID:   req.Context.TransactionID,
			MsgID:   req.Context.MessageID,
			BapID:   req.Context.BapID,
			Payload: reqJSON,
		},
	)
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
