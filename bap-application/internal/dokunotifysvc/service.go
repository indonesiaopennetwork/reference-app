package dokunotifysvc

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/remiges-tech/logharbour/logharbour"

	dbsqlc "github.com/ion/winroom/bap/db/sqlc"
	"github.com/ion/winroom/bap/internal/clientsvc"
	"github.com/ion/winroom/bap/internal/config"
	"github.com/ion/winroom/bap/internal/dokusvc"
)

// prettyLog prints a labelled pretty-JSON block to stdout, mirroring
// internal/dokusvc's own helper for the outbound checkout call -- plain
// log.Printf, not the structured *logharbour.Logger (s.lh, used below for
// error/warning cases), so the raw notification is easy to read in
// `docker compose logs`.
func prettyLog(label string, v interface{}) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		log.Printf("[doku] %s\n(marshal error: %v)", label, err)
		return
	}
	log.Printf("[doku] %s\n%s", label, string(b))
}

type Service struct {
	pool      *pgxpool.Pool
	cfg       *config.Config
	lh        *logharbour.Logger
	client    *dokusvc.Client
	confirmer *clientsvc.ClientService
}

func NewService(pool *pgxpool.Pool, cfg *config.Config, lh *logharbour.Logger, confirmer *clientsvc.ClientService) *Service {
	return &Service{
		pool:      pool,
		cfg:       cfg,
		lh:        lh,
		client:    dokusvc.NewClient(cfg.DokuBaseURL, cfg.DokuClientID, cfg.DokuSecretKey),
		confirmer: confirmer,
	}
}

// Process applies a verified DOKU notification: updates the matching
// payment_sessions row (by order.invoice_number) and, only when the
// notification reports a successful payment, calls the real
// ClientService.Confirm -- the one point in this app where a transaction
// actually moves from AWAITING_PAYMENT to CONFIRM_SENT.
func (s *Service) Process(ctx context.Context, body []byte) {
	payload, err := dokusvc.ParseNotification(body)
	if err != nil {
		s.lh.WithModule("doku_notify_svc").Err().Error(err).Log("could not parse doku notification")
		return
	}

	var bodyForLog interface{}
	if jsonErr := json.Unmarshal(body, &bodyForLog); jsonErr != nil {
		bodyForLog = string(body)
	}
	prettyLog("← IN   NOTIFY   | invoice="+payload.Order.InvoiceNumber, bodyForLog)

	q := dbsqlc.New(s.pool)
	session, err := q.GetPaymentSessionByInvoiceNumber(ctx, payload.Order.InvoiceNumber)
	if err != nil {
		s.lh.WithModule("doku_notify_svc").Err().Error(err).Log("no payment session for doku invoice_number " + payload.Order.InvoiceNumber)
		return
	}

	status := dbsqlc.PaymentSessionStatusFAILED
	if payload.Transaction.Status == "SUCCESS" {
		status = dbsqlc.PaymentSessionStatusPAID
	}

	var paidAt pgtype.Timestamptz
	method := dokusvc.MethodFor(payload.Service.ID)
	if status == dbsqlc.PaymentSessionStatusPAID {
		t := time.Now().UTC()
		if parsed, err := time.Parse(time.RFC3339, payload.Transaction.Date); err == nil {
			t = parsed
		}
		paidAt = pgtype.Timestamptz{Time: t, Valid: true}
	}

	if err := q.UpdatePaymentSessionStatus(ctx, dbsqlc.UpdatePaymentSessionStatusParams{
		InvoiceNumber:   payload.Order.InvoiceNumber,
		Status:          status,
		GatewayRef:      strPtr(payload.Transaction.OriginalRequestID),
		Method:          strPtr(method),
		PaidAt:          paidAt,
		RawNotification: json.RawMessage(body),
	}); err != nil {
		s.lh.WithModule("doku_notify_svc").Err().Error(err).Log("failed to update payment session " + payload.Order.InvoiceNumber)
		return
	}

	if status != dbsqlc.PaymentSessionStatusPAID {
		s.lh.WithModule("doku_notify_svc").Warn().Log("doku reported a non-success payment for invoice " + payload.Order.InvoiceNumber + ": " + payload.Transaction.Status)
		return
	}

	if err := s.confirmer.Confirm(ctx, &clientsvc.ClientConfirmRequest{TransactionID: session.TransactionID.String()}); err != nil {
		s.lh.WithModule("doku_notify_svc").Err().Error(err).Log("confirm after doku payment failed for txn " + session.TransactionID.String())
	}
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
