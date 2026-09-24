package ratesvc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/remiges-tech/logharbour/logharbour"

	dbsqlc "github.com/ion/winroom/bpp/db/sqlc"
	"github.com/ion/winroom/bpp/internal/config"
	"github.com/ion/winroom/bpp/internal/httpclient"
)

type Service struct {
	pool       *pgxpool.Pool
	cfg        *config.Config
	lh         *logharbour.Logger
	httpClient *http.Client
}

func NewService(pool *pgxpool.Pool, cfg *config.Config, lh *logharbour.Logger) *Service {
	return &Service{pool: pool, cfg: cfg, lh: lh, httpClient: httpclient.New(30 * time.Second)}
}

// Process persists all rating inputs and fires on_rate to the BAP.
func (s *Service) Process(ctx context.Context, req *RateRequest) {
	prettyLog(fmt.Sprintf("← IN   RATE     | txn=%.8s… | bap=%s | inputs=%d", req.Context.TransactionID, req.Context.BapID, len(req.Message.RatingInputs)), req)

	txID, err := uuid.Parse(req.Context.TransactionID)
	if err != nil {
		s.lh.WithModule("ratesvc").Err().Error(err).Log("invalid transactionId")
		return
	}

	q := dbsqlc.New(s.pool)
	row, err := q.GetActiveContractByTxnID(ctx, dbsqlc.GetActiveContractByTxnIDParams{
		TransactionID: txID,
		BppID:         s.cfg.BppID,
	})
	if err != nil {
		s.lh.WithModule("ratesvc").Err().Error(err).Log("contract not found for rate")
		return
	}

	var summaries []RatingSummary
	for _, input := range req.Message.RatingInputs {
		rangeJSON := input.Range
		if rangeJSON == nil {
			rangeJSON = json.RawMessage(`{}`)
		}
		descriptorJSON := input.Target.Descriptor
		if descriptorJSON == nil {
			descriptorJSON = json.RawMessage(`{}`)
		}
		feedbackJSON := input.FeedbackFormSubmission
		if feedbackJSON == nil {
			feedbackJSON = json.RawMessage(`null`)
		}

		if err := q.InsertRating(ctx, dbsqlc.InsertRatingParams{
			ContractID:             row.ID,
			BapID:                  req.Context.BapID,
			BapUri:                 req.Context.BapURI,
			TargetID:               row.ID.String(),
			TargetDescriptor:       descriptorJSON,
			Range:                  rangeJSON,
			FeedbackFormSubmission: feedbackJSON,
			IsPreview:              false,
		}); err != nil {
			s.lh.WithModule("ratesvc").Warn().Error(err).Log("insert rating failed for contract " + row.ID.String())
		}
		summaries = append(summaries, RatingSummary{Target: input.Target, Range: rangeJSON})
	}

	outMsgID := uuid.New()
	onRateReq := OnRateRequest{
		Context: BecknContext{
			Version:       req.Context.Version,
			Action:        "on_rate",
			Timestamp:     time.Now().UTC().Format(time.RFC3339),
			MessageID:     outMsgID.String(),
			TransactionID: req.Context.TransactionID,
			BapID:         req.Context.BapID,
			BapURI:        req.Context.BapURI,
			BppID:         req.Context.BppID,
			BppURI:        req.Context.BppURI,
			TTL:           req.Context.TTL,
			NetworkID:     req.Context.NetworkID,
		},
		Message: OnRateMessage{Ratings: summaries},
	}
	s.callOnRate(ctx, row.BapUri, onRateReq)
}

func (s *Service) callOnRate(ctx context.Context, bapURI string, req OnRateRequest) {
	url, err := s.resolveOnRateURL(bapURI)
	if err != nil {
		s.lh.WithModule("ratesvc").Err().Error(err).Log("on_rate target resolution failed")
		return
	}
	body, _ := json.Marshal(req)
	prettyLog(fmt.Sprintf("→ OUT  ON_RATE   | txn=%.8s… | POST %s", req.Context.TransactionID, url), req)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		s.lh.WithModule("ratesvc").Err().Error(err).Log("build on_rate request failed")
		return
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := s.httpClient.Do(httpReq)
	if err != nil {
		s.lh.WithModule("ratesvc").Warn().Error(err).Log("on_rate callback failed")
		return
	}
	defer resp.Body.Close() //nolint:errcheck
	respBody, _ := io.ReadAll(resp.Body)
	prettyLog(fmt.Sprintf("← ACK  ON_RATE   | http=%d", resp.StatusCode), map[string]interface{}{"response": json.RawMessage(respBody)})
}

// resolveOnRateURL returns the ONIX BPP caller on_rate endpoint.
// Priority:
//  1. BPP_CALLER_URL env/config (e.g. https://bpptest.remiges.tech/bpp/caller)
//  2. Derived from BPP_URI by replacing /bpp/receiver with /bpp/caller
func (s *Service) resolveOnRateURL(_ string) (string, error) {
	base := strings.TrimSpace(s.cfg.BppCallerURL)
	if base == "" {
		base = strings.TrimSpace(s.cfg.BppURI)
		if base == "" {
			return "", fmt.Errorf("cannot resolve on_rate URL: both BPP_CALLER_URL and BPP_URI are empty")
		}
		if strings.HasSuffix(base, "/") {
			base = strings.TrimSuffix(base, "/")
		}
		if strings.HasSuffix(base, "/bpp/receiver") {
			base = strings.TrimSuffix(base, "/bpp/receiver") + "/bpp/caller"
		} else {
			return "", fmt.Errorf("cannot derive caller URL from BPP_URI %q; set BPP_CALLER_URL", s.cfg.BppURI)
		}
	}
	base = strings.TrimRight(base, "/")
	return base + "/on_rate", nil
}

func prettyLog(label string, v interface{}) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		log.Printf("[beckn] %s\n(marshal error: %v)", label, err)
		return
	}
	log.Printf("[beckn] %s\n%s", label, string(b))
}
