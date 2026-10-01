package onupdatesvc

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
	"github.com/ion/winroom/bap/internal/contractx"
)

type Service struct {
	pool *pgxpool.Pool
	cfg  *config.Config
	lh   *logharbour.Logger
}

func NewService(pool *pgxpool.Pool, cfg *config.Config, lh *logharbour.Logger) *Service {
	return &Service{pool: pool, cfg: cfg, lh: lh}
}

// Process stores the updated contract snapshot received in on_update.
//
// Every on_update is logged to beckn_message_log unconditionally (full
// audit trail, including malformed/unsolicited/duplicate ones), but the
// contract snapshot -- the state Init/Confirm/etc. actually read -- is
// only overwritten when MarkUpdateReceivedIfPending confirms this callback
// corresponds to an update this transaction is actually still waiting on
// (UPDATE_SENT). A callback that arrives after that (a network-level
// duplicate delivery, a stale/replayed retry, or one that's simply
// unsolicited) is recorded for observability but never allowed to
// clobber the committed snapshot -- see clientsvc.Update's own comment on
// the matching CONFIRMED -> UPDATE_SENT transition this mirrors.
func (s *Service) Process(ctx context.Context, req *OnUpdateRequest) {
	start := time.Now()
	reqJSON, _ := json.Marshal(req)
	loggedJSON := redactForLog(reqJSON) // logs/audit only -- never used for the actual snapshot write below
	module := s.lh.WithModule("on_update_svc")

	txID, err := uuid.Parse(req.Context.TransactionID)
	if err != nil {
		module.Warn().Error(err).Log(fmt.Sprintf("on_update: malformed transactionId %q, dropping", req.Context.TransactionID))
		return
	}
	msgID, err := uuid.Parse(req.Context.MessageID)
	if err != nil {
		// Not fatal to processing (transactionId is what state is keyed
		// on), but every message_log row needs a MessageID -- fall back to
		// a fresh one so a malformed inbound messageId still gets audited
		// rather than silently discarding the whole callback.
		module.Warn().Error(err).Log(fmt.Sprintf("on_update: malformed messageId %q for txn %s, using a generated one for the audit row", req.Context.MessageID, txID))
		msgID = uuid.New()
	}
	q := dbsqlc.New(s.pool)

	contractJSON := req.Message.Contract
	if len(contractJSON) == 0 {
		contractJSON = json.RawMessage("null")
	}
	contractID := extractContractID(contractJSON)

	// Read the pre-update contract and the buyer's proposal BEFORE the
	// snapshot is overwritten below -- classifyOutcome needs both, and
	// UpsertContractSnapshot destroys the "prior" side of that comparison.
	// Both are best-effort: a missing one only costs the buyer the
	// decision readout, never the contract update itself.
	priorSnapshot, priorErr := q.GetLatestContractSnapshot(ctx, txID)
	pendingReq, pendingErr := q.GetPendingUpdateRequest(ctx, txID)

	accepted, err := q.MarkUpdateReceivedIfPending(ctx, txID)
	if err != nil {
		module.Warn().Error(err).Log(fmt.Sprintf("on_update: eligibility check failed for txn %.8s…", txID))
	}
	if accepted == 0 {
		module.Warn().LogActivity(
			fmt.Sprintf("on_update: txn %.8s… was not awaiting an update (duplicate, stale/replayed, or unsolicited callback) -- audited, snapshot left untouched", txID),
			json.RawMessage(loggedJSON),
		)
	} else {
		// MarkUpdateReceivedIfPending returned the transaction to CONFIRMED;
		// now follow the contract's own status on top of that. A partial
		// cancellation or return leaves it ACTIVE (CONFIRMED), cancelling
		// the last item makes it CANCELLED, returning every item makes it
		// COMPLETE. Only done for an accepted callback, so a duplicate or
		// unsolicited one can never move the order's state.
		var contractStatus struct {
			Status struct {
				Code string `json:"code"`
			} `json:"status"`
		}
		_ = json.Unmarshal(contractJSON, &contractStatus)
		txnStatus := map[string]dbsqlc.TransactionStatus{
			"CANCELLED": dbsqlc.TransactionStatusCANCELLED,
			"COMPLETE":  dbsqlc.TransactionStatusCOMPLETE,
		}[contractStatus.Status.Code]
		if txnStatus != "" {
			if err := q.UpsertTransaction(ctx, dbsqlc.UpsertTransactionParams{
				TransactionID: txID,
				BapID:         s.cfg.BapID,
				NetworkID:     strPtr(req.Context.NetworkID),
				BppID:         strPtr(req.Context.BppID),
				BppUri:        strPtr(req.Context.BppURI),
				Status:        txnStatus,
			}); err != nil {
				module.Warn().Error(err).Log("upsert transaction failed")
			}
		}

		// Record the seller's decision first, while priorSnapshot is still
		// the pre-update contract. ResolveUpdateRequest is itself
		// predicated on the row still being PENDING, so this stays correct
		// even if two callbacks race past the check above.
		if pendingErr == nil {
			// An explicitly stated decision wins over an inferred one: the
			// seller telling us what they did beats comparing contracts,
			// and it is the only path that carries a reason. Absent that,
			// fall back to inference (see decision.go).
			outcome, reasonCode, reasonText, stated := decisionFromContract(contractJSON)
			if !stated {
				var prior json.RawMessage
				if priorErr == nil {
					prior = priorSnapshot.Contract
				}
				outcome = classifyOutcome(pendingReq.ProposedContract, prior, contractJSON)
			}
			if _, rErr := q.ResolveUpdateRequestWithReason(ctx, dbsqlc.ResolveUpdateRequestWithReasonParams{
				ID:                 pendingReq.ID,
				Outcome:            outcome,
				DecisionReasonCode: reasonCode,
				DecisionReasonText: reasonText,
			}); rErr != nil {
				module.Warn().Error(rErr).Log(fmt.Sprintf("on_update: failed to record %s outcome for update request %.8s…", outcome, pendingReq.ID))
			} else {
				module.LogActivity(
					fmt.Sprintf("on_update: txn %.8s… seller decision recorded as %s", txID, outcome),
					map[string]any{
						"transaction_id":    txID.String(),
						"update_request_id": pendingReq.ID.String(),
						"outcome":           string(outcome),
						"decision_source":   map[bool]string{true: "stated-by-seller", false: "inferred"}[stated],
						// True when the comparison had no pre-update
						// contract to fall back on, which makes DECLINED
						// undetectable -- worth seeing in logs, because it
						// turns a rejection into a MODIFIED readout.
						"prior_snapshot_missing": priorErr != nil,
					},
				)
			}
		}

		// Only store the contract if it can actually serve as order state.
		//
		// An on_update may legitimately carry a minimal contract -- little
		// more than an id and the decision (our own rejection path does
		// this when the contract has no attributes). Storing that made it
		// the newest snapshot, shadowing the complete on_confirm one, and
		// every later action that builds an outbound contract from the
		// latest snapshot then failed with
		// `property "commitments" is missing`: /request-status, /cancel,
		// and the no-op detection in Update, which could no longer find a
		// delivery block to compare against.
		//
		// The callback is still fully audited above either way; what is
		// refused here is only the overwrite of good state with worse.
		if !contractx.HasOrderCore(contractJSON) {
			module.Warn().LogActivity(
				fmt.Sprintf("on_update: txn %.8s… carried a contract with no commitments -- audited, but NOT stored as the contract snapshot (it would shadow the complete one and break later outbound calls)", txID),
				map[string]any{"transaction_id": txID.String(), "contract_bytes": len(contractJSON)},
			)
		} else if err := q.UpsertContractSnapshot(ctx, dbsqlc.UpsertContractSnapshotParams{
			TransactionID: txID,
			Action:        dbsqlc.BecknActionOnUpdate,
			ContractID:    contractID,
			Contract:      contractJSON,
		}); err != nil {
			module.Warn().Error(err).Log("upsert contract snapshot failed")
		}
	}

	ms := int32(time.Since(start).Milliseconds())
	if err := q.InsertMessageLog(ctx, dbsqlc.InsertMessageLogParams{
		TransactionID:        pgtype.UUID{Bytes: txID, Valid: true},
		MessageID:            msgID,
		Action:               dbsqlc.BecknActionOnUpdate,
		Direction:            dbsqlc.MessageDirectionINBOUND,
		Url:                  strPtr("/api/webhook/on_update"),
		BapID:                strPtr(req.Context.BapID),
		BapUri:               strPtr(req.Context.BapURI),
		BppID:                strPtr(req.Context.BppID),
		BppUri:               strPtr(req.Context.BppURI),
		NetworkID:            strPtr(req.Context.NetworkID),
		AckStatus:            dbsqlc.NullAckStatus{AckStatus: dbsqlc.AckStatusACK, Valid: true},
		RequestPayload:       loggedJSON,
		ProcessingDurationMs: &ms,
	}); err != nil {
		module.Warn().Error(err).Log(fmt.Sprintf("message log insert failed: %v", err))
	}

	module.LogActivity(
		fmt.Sprintf("← IN   ON_UPDATE | txn=%.8s… | bpp=%s | accepted=%t | dur=%dms", req.Context.TransactionID, req.Context.BppID, accepted > 0, time.Since(start).Milliseconds()),
		json.RawMessage(loggedJSON),
	)
}

func extractContractID(raw json.RawMessage) *string {
	if len(raw) == 0 {
		return nil
	}
	var c struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &c); err != nil || c.ID == "" {
		return nil
	}
	return &c.ID
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
