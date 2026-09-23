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
)

type Service struct {
	pool *pgxpool.Pool
	cfg  *config.Config
	lh   *logharbour.Logger
}

func NewService(pool *pgxpool.Pool, cfg *config.Config, lh *logharbour.Logger) *Service {
	return &Service{pool: pool, cfg: cfg, lh: lh}
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

type becknInActivity struct {
	Action  string          `json:"action"`
	TxnID   string          `json:"txn_id"`
	MsgID   string          `json:"msg_id"`
	BapID   string          `json:"bap_id"`
	BppID   string          `json:"bpp_id"`
	Payload json.RawMessage `json:"payload"`
}

func (s *Service) Process(ctx context.Context, req *OnDiscoverRequest) {
	start := time.Now()
	reqJSON, _ := json.Marshal(req)
	txID, _ := uuid.Parse(req.Context.TransactionID)
	msgID, _ := uuid.Parse(req.Context.MessageID)
	q := dbsqlc.New(s.pool)

	if err := q.UpsertTransaction(ctx, dbsqlc.UpsertTransactionParams{
		TransactionID: txID,
		BapID:         s.cfg.BapID,
		NetworkID:     strPtr(req.Context.NetworkID),
		BppID:         strPtr(req.Context.BppID),
		BppUri:        strPtr(req.Context.BppURI),
		Status:        dbsqlc.TransactionStatusDISCOVERRECEIVED,
	}); err != nil {
		s.lh.WithModule("on_discover_svc").Warn().Error(err).Log("upsert transaction failed")
	}

	ms := int32(time.Since(start).Milliseconds())
	if err := q.InsertMessageLog(ctx, dbsqlc.InsertMessageLogParams{
		TransactionID:        pgtype.UUID{Bytes: txID, Valid: true},
		MessageID:            msgID,
		Action:               dbsqlc.BecknActionOnDiscover,
		Direction:            dbsqlc.MessageDirectionINBOUND,
		Url:                  strPtr("/api/webhook/on_discover"),
		BapID:                strPtr(req.Context.BapID),
		BapUri:               strPtr(req.Context.BapURI),
		BppID:                strPtr(req.Context.BppID),
		BppUri:               strPtr(req.Context.BppURI),
		NetworkID:            strPtr(req.Context.NetworkID),
		AckStatus:            dbsqlc.NullAckStatus{AckStatus: dbsqlc.AckStatusACK, Valid: true},
		RequestPayload:       reqJSON,
		ProcessingDurationMs: &ms,
	}); err != nil {
		s.lh.WithModule("on_discover_svc").Warn().Error(err).Log(fmt.Sprintf("message log insert failed: %v", err))
	}

	s.lh.WithModule("on_discover_svc").LogActivity(
		fmt.Sprintf("← IN   ON_DISCOVER | txn=%.8s… | bpp=%s | dur=%dms", req.Context.TransactionID, req.Context.BppID, time.Since(start).Milliseconds()),
		becknInActivity{
			Action:  "on_discover",
			TxnID:   req.Context.TransactionID,
			MsgID:   req.Context.MessageID,
			BapID:   req.Context.BapID,
			BppID:   req.Context.BppID,
			Payload: reqJSON,
		},
	)
}
