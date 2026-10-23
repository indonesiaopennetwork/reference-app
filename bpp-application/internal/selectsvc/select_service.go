package selectsvc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/remiges-tech/logharbour/logharbour"

	dbsqlc "github.com/ion/winroom/bpp/db/sqlc"
	"github.com/ion/winroom/bpp/internal/config"
	"github.com/ion/winroom/bpp/internal/httpclient"
	"github.com/ion/winroom/bpp/internal/offerterms"
	"github.com/ion/winroom/bpp/internal/settlement"
)

// SelectService handles the full /beckn/select flow:
//  1. Validate that every requested offer/resource exists in our inventory.
//  2. Enrich the contract with full product details and a calculated price breakdown.
//  3. POST the on_select callback to the BAP.
//  4. Write INBOUND and OUTBOUND audit rows to beckn_message_log.
type SelectService struct {
	pool       *pgxpool.Pool
	cfg        *config.Config
	lh         *logharbour.Logger
	httpClient *http.Client
}

func NewSelectService(pool *pgxpool.Pool, cfg *config.Config, lh *logharbour.Logger) *SelectService {
	return &SelectService{
		pool:       pool,
		cfg:        cfg,
		lh:         lh,
		httpClient: httpclient.New(30 * time.Second),
	}
}

// ProcessSelect is called asynchronously after the BPP returns ACK to the BAP.
// It enriches the contract, calls on_select on the BAP, and logs both messages.
func (s *SelectService) ProcessSelect(ctx context.Context, req *SelectRequest) {
	start := time.Now()

	txID, _ := uuid.Parse(req.Context.TransactionID)
	inboundMsgID, _ := uuid.Parse(req.Context.MessageID)
	reqJSON, _ := json.Marshal(req)

	prettyLog(fmt.Sprintf("← IN   SELECT    | txn=%.8s… | bap=%s", req.Context.TransactionID, req.Context.BapID), req)

	// Enrich the contract with DB data.
	enrichedContract := s.enrichContract(ctx, req)

	// Build the on_select payload.
	outboundMsgID := uuid.New()
	onSelectReq := OnSelectRequest{
		Context: BecknContext{
			Version:       req.Context.Version,
			Action:        "on_select",
			Timestamp:     time.Now().UTC().Format(time.RFC3339),
			MessageID:     outboundMsgID.String(),
			TransactionID: req.Context.TransactionID,
			BapID:         req.Context.BapID,
			BapURI:        req.Context.BapURI,
			BppID:         req.Context.BppID,
			BppURI:        req.Context.BppURI,
			TTL:           req.Context.TTL,
			NetworkID:     req.Context.NetworkID,
		},
		Message: OnSelectMessage{Contract: enrichedContract},
	}

	// Log INBOUND.
	s.writeMessageLog(ctx, dbsqlc.InsertMessageLogParams{
		TransactionID:        uuidToPgtype(txID),
		MessageID:            inboundMsgID,
		Action:               dbsqlc.BecknActionSelect,
		Direction:            dbsqlc.MessageDirectionINBOUND,
		Url:                  strPtr("/webhook/select"),
		BapID:                strPtr(req.Context.BapID),
		BapUri:               strPtr(req.Context.BapURI),
		BppID:                strPtr(req.Context.BppID),
		BppUri:               strPtr(req.Context.BppURI),
		NetworkID:            strPtr(req.Context.NetworkID),
		AckStatus:            dbsqlc.NullAckStatus{AckStatus: dbsqlc.AckStatusACK, Valid: true},
		RequestPayload:       reqJSON,
		ProcessingDurationMs: durationMs(start),
	})

	// POST on_select through ONIX BPP caller (so the request is signed) and log OUTBOUND.
	s.callOnSelect(ctx, txID, outboundMsgID, onSelectReq)
}

// enrichContract builds a copy of the inbound contract with each commitment
// populated with the full resource/offer details and pricing from the DB.
func (s *SelectService) enrichContract(ctx context.Context, req *SelectRequest) Contract {
	q := dbsqlc.New(s.pool)
	bppID := s.cfg.BppID

	contract := Contract{
		Status:       req.Message.Contract.Status,
		Participants: req.Message.Contract.Participants,
		Performance:  req.Message.Contract.Performance,
	}

	var allBreakup []breakupItem
	currency := "IDR"

	for _, c := range req.Message.Contract.Commitments {
		enriched, unitLines, curr, found := s.enrichCommitment(ctx, q, bppID, c)
		if curr != "" {
			currency = curr
		}
		contract.Commitments = append(contract.Commitments, enriched)

		if found {
			qty := extractQty(c.CommitmentAttributes)
			for _, line := range unitLines {
				if line.Type == "BASE_PRICE" {
					line.Title = enriched.Offer.Descriptor.Name
				}
				allBreakup = append(allBreakup, line.scaled(qty))
			}
		}
	}

	// The seller's delivery fee: one DELIVERY_CHARGE line per order.
	if line, ok := deliveryLine(contract.Commitments, contract.Performance); ok && len(allBreakup) > 0 {
		allBreakup = append(allBreakup, line)
	}

	// Build aggregate contract-level consideration from all found line items.
	if len(allBreakup) > 0 {
		var total float64
		for _, b := range allBreakup {
			total += b.Amount
		}
		attrsJSON, _ := json.Marshal(dbConsiderationAttrs{
			Context:     "https://schema.ion.id/release1/schema/extension/trade/TradeConsideration/v1/context.jsonld",
			Type:        "ion:IONTradeConsideration",
			Currency:    currency,
			TotalAmount: total,
			Breakup:     allBreakup,
		})
		contract.Consideration = []Consideration{
			{
				ID:                      "consideration-" + uuid.New().String()[:8],
				Status:                  &ConsiderationStatus{Code: "QUOTED"},
				ConsiderationAttributes: attrsJSON,
			},
		}
	}

	// The seller's terms on each offer (with this BPP's defaults where an
	// offer was published without them), and the commission the quote comes
	// to on the price.
	if err := offerterms.ApplyTo(ctx, q, bppID, offerterms.FromConfig(s.cfg), &contract, func(m map[string]any) {
		settlement.Refresh(m)
	}); err != nil {
		s.lh.WithModule("selectsvc").Warn().Error(err).Log("settlement terms not applied to the quote")
	}

	return contract
}

// toOfferConsiderations maps the offer's stored catalog prices to
// Offer.considerations[].
func toOfferConsiderations(rows []dbsqlc.OfferConsideration) []OfferConsideration {
	out := make([]OfferConsideration, 0, len(rows))
	for _, r := range rows {
		oc := OfferConsideration{ID: r.ConsiderationID, ConsiderationAttributes: r.ConsiderationAttributes}
		if r.StatusCode != "" || r.StatusName != nil {
			oc.Status = &OfferConsiderationStatus{Code: r.StatusCode, Name: strVal(r.StatusName)}
		}
		out = append(out, oc)
	}
	return out
}

// enrichCommitment looks up the offer and each resource in the DB.
// Returns the enriched Commitment, the per-unit price breakup lines (BASE_PRICE
// plus any TAX lines), currency, and whether the offer was found.
func (s *SelectService) enrichCommitment(
	ctx context.Context, q *dbsqlc.Queries, bppID string, c Commitment,
) (enriched Commitment, unitLines []breakupItem, currency string, found bool) {
	enriched = c

	if c.Offer == nil {
		enriched.Status = unavailableStatus()
		return
	}

	// Look up offer.
	dbOffer, err := q.GetOffer(ctx, dbsqlc.GetOfferParams{ID: c.Offer.ID, BppID: bppID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			s.lh.WithModule("selectsvc").
				Warn().Log(fmt.Sprintf("offer %q not found in inventory", c.Offer.ID))
		} else {
			s.lh.WithModule("selectsvc").Err().Error(err).Log("DB error looking up offer")
		}
		enriched.Status = unavailableStatus()
		return
	}
	found = true

	// Enrich offer with full attributes from DB.
	providerID := ""
	if dbOffer.ProviderID != nil {
		providerID = *dbOffer.ProviderID
	}
	provider := &Provider{ID: providerID}
	if c.Offer != nil && c.Offer.Provider != nil {
		if c.Offer.Provider.ID != "" {
			provider.ID = c.Offer.Provider.ID
		}
		if c.Offer.Provider.Descriptor != nil {
			provider.Descriptor = &Descriptor{
				Name:      c.Offer.Provider.Descriptor.Name,
				ShortDesc: c.Offer.Provider.Descriptor.ShortDesc,
				LongDesc:  c.Offer.Provider.Descriptor.LongDesc,
			}
		}
	}
	enriched.Offer = &Offer{
		ID:          dbOffer.ID,
		ResourceIDs: dbOffer.ResourceIds,
		Descriptor: &Descriptor{
			Code:           strVal(dbOffer.DescriptorCode),
			Name:           strVal(dbOffer.DescriptorName),
			ShortDesc:      strVal(dbOffer.DescriptorShortDesc),
			LongDesc:       strVal(dbOffer.DescriptorLongDesc),
			ThumbnailImage: strVal(dbOffer.DescriptorThumbnailImage),
			Docs:           nonEmptyArray(dbOffer.DescriptorDocs),
			MediaFile:      nonEmptyArray(dbOffer.DescriptorMediaFiles),
		},
		Provider:        provider,
		OfferAttributes: dbOffer.OfferAttributes,
	}

	// Enrich each resource with full descriptor + attributes.
	qty := extractQty(c.CommitmentAttributes)
	var enrichedResources []Resource
	for _, r := range c.Resources {
		dbRes, err := q.GetResource(ctx, dbsqlc.GetResourceParams{ID: r.ID, BppID: bppID})
		if err != nil {
			s.lh.WithModule("selectsvc").Warn().Error(err).Log(fmt.Sprintf("resource %q lookup failed; passing it through as sent", r.ID))
			// Pass-through as-is, but the schema requires every resource to
			// carry its own quantity — the inbound resource from BAP only
			// has one nested in commitmentAttributes, not here.
			if r.Quantity == nil {
				r.Quantity = &quantity{UnitCode: "EA", UnitQuantity: qty}
			}
			enrichedResources = append(enrichedResources, r)
			continue
		}
		enrichedResources = append(enrichedResources, Resource{
			ID: r.ID,
			Descriptor: &Descriptor{
				Code:           strVal(dbRes.DescriptorCode),
				Name:           strVal(dbRes.DescriptorName),
				ShortDesc:      strVal(dbRes.DescriptorShortDesc),
				LongDesc:       strVal(dbRes.DescriptorLongDesc),
				ThumbnailImage: strVal(dbRes.DescriptorThumbnailImage),
				Docs:           nonEmptyArray(dbRes.DescriptorDocs),
				MediaFile:      nonEmptyArray(dbRes.DescriptorMediaFiles),
			},
			ResourceAttributes: dbRes.ResourceAttributes,
			Quantity:           &quantity{UnitCode: "EA", UnitQuantity: qty},
		})
	}
	enriched.Resources = enrichedResources

	// Look up pricing from offer considerations.
	considerations, err := q.GetOfferConsiderations(ctx, dbsqlc.GetOfferConsiderationsParams{
		OfferID: c.Offer.ID,
		BppID:   bppID,
	})
	if err != nil || len(considerations) == 0 {
		return
	}
	enriched.Offer.Considerations = toOfferConsiderations(considerations)

	// The first consideration's breakup is the per-unit price; the commitment
	// carries the pre-tax BASE_PRICE as its unit price.
	var attrs dbConsiderationAttrs
	var unitPrice float64
	if err := json.Unmarshal(considerations[0].ConsiderationAttributes, &attrs); err == nil {
		currency = attrs.Currency
		unitLines = unitBreakup(attrs)
		unitPrice = attrs.TotalAmount
		for _, line := range unitLines {
			if line.Type == "BASE_PRICE" {
				unitPrice = line.Amount
			}
		}
	}

	// Merge unit price into commitmentAttributes.
	enriched.CommitmentAttributes = mergePrice(c.CommitmentAttributes, unitPrice, currency, qty)

	return
}

// nonEmptyArray returns raw unless it is empty, null or "[]", so empty docs /
// media lists are omitted rather than sent as [].
func nonEmptyArray(raw json.RawMessage) json.RawMessage {
	var items []json.RawMessage
	if len(raw) == 0 || json.Unmarshal(raw, &items) != nil || len(items) == 0 {
		return nil
	}
	return raw
}

// unitBreakup returns an offer consideration's per-unit breakup: its BASE_PRICE
// and TAX lines, or -- for offers priced before breakups carried tax -- a
// single BASE_PRICE line of totalAmount.
func unitBreakup(attrs dbConsiderationAttrs) []breakupItem {
	var lines []breakupItem
	for _, b := range attrs.Breakup {
		if b.Type == "BASE_PRICE" || b.Type == "TAX" {
			lines = append(lines, b)
		}
	}
	if len(lines) == 0 {
		lines = []breakupItem{{Amount: attrs.TotalAmount, Type: "BASE_PRICE"}}
	}
	return lines
}

// deliveryLine is the order's delivery charge (IONDeliveryFee): the highest
// deliveryFee.amount stated on the stored considerations of the offers
// ordered, charged once as a DELIVERY_CHARGE line. None when no offer states
// one, or when the order is picked up (performanceMode SELF_PICKUP).
func deliveryLine(commitments []Commitment, performance []Performance) (breakupItem, bool) {
	for _, p := range performance {
		var attrs struct {
			PerformanceMode string `json:"performanceMode"`
		}
		if json.Unmarshal(p.PerformanceAttributes, &attrs) == nil && attrs.PerformanceMode == "SELF_PICKUP" {
			return breakupItem{}, false
		}
	}
	fee, found := 0.0, false
	for _, c := range commitments {
		if c.Offer == nil || len(c.Offer.Considerations) == 0 {
			continue
		}
		var attrs struct {
			DeliveryFee *struct {
				Amount float64 `json:"amount"`
			} `json:"deliveryFee"`
		}
		if json.Unmarshal(c.Offer.Considerations[0].ConsiderationAttributes, &attrs) != nil || attrs.DeliveryFee == nil {
			continue
		}
		if !found || attrs.DeliveryFee.Amount > fee {
			fee = attrs.DeliveryFee.Amount
		}
		found = true
	}
	if !found {
		return breakupItem{}, false
	}
	return breakupItem{Title: "Delivery fee", Amount: fee, Type: "DELIVERY_CHARGE"}, true
}

// callOnSelect POSTs the on_select payload via ONIX BPP caller and logs the OUTBOUND entry.
func (s *SelectService) callOnSelect(
	ctx context.Context,
	txID uuid.UUID,
	msgID uuid.UUID,
	req OnSelectRequest,
) {
	start := time.Now()
	onSelectURL, err := s.resolveOnSelectURL(req.Context.BapURI)
	reqJSON, _ := json.Marshal(req)

	if err != nil {
		s.lh.WithModule("selectsvc").Err().Error(err).Log("on_select target resolution failed")
		s.writeMessageLog(ctx, dbsqlc.InsertMessageLogParams{
			TransactionID:        uuidToPgtype(txID),
			MessageID:            msgID,
			Action:               dbsqlc.BecknActionOnSelect,
			Direction:            dbsqlc.MessageDirectionOUTBOUND,
			BapID:                strPtr(req.Context.BapID),
			BapUri:               strPtr(req.Context.BapURI),
			BppID:                strPtr(req.Context.BppID),
			BppUri:               strPtr(req.Context.BppURI),
			NetworkID:            strPtr(req.Context.NetworkID),
			AckStatus:            dbsqlc.NullAckStatus{AckStatus: dbsqlc.AckStatusNACK, Valid: true},
			RequestPayload:       reqJSON,
			ErrorMessage:         errStrPtr(err),
			ProcessingDurationMs: durationMs(start),
		})
		return
	}

	var (
		respBody []byte
		callErr  error
	)

	defer func() {
		ackStatus := dbsqlc.NullAckStatus{AckStatus: dbsqlc.AckStatusACK, Valid: true}
		if callErr != nil {
			ackStatus = dbsqlc.NullAckStatus{AckStatus: dbsqlc.AckStatusNACK, Valid: true}
		}
		s.writeMessageLog(ctx, dbsqlc.InsertMessageLogParams{
			TransactionID:        uuidToPgtype(txID),
			MessageID:            msgID,
			Action:               dbsqlc.BecknActionOnSelect,
			Direction:            dbsqlc.MessageDirectionOUTBOUND,
			Url:                  strPtr(onSelectURL),
			BapID:                strPtr(req.Context.BapID),
			BapUri:               strPtr(req.Context.BapURI),
			BppID:                strPtr(req.Context.BppID),
			BppUri:               strPtr(req.Context.BppURI),
			NetworkID:            strPtr(req.Context.NetworkID),
			AckStatus:            ackStatus,
			RequestPayload:       reqJSON,
			ResponsePayload:      rawMessageOrNull(respBody),
			ErrorMessage:         errStrPtr(callErr),
			ProcessingDurationMs: durationMs(start),
		})
	}()

	prettyLog(fmt.Sprintf("→ OUT  ON_SELECT | txn=%.8s… | POST %s", txID, onSelectURL), req)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, onSelectURL, bytes.NewReader(reqJSON))
	if err != nil {
		callErr = fmt.Errorf("build on_select request: %w", err)
		prettyLog(fmt.Sprintf("← ERR  ON_SELECT | txn=%.8s… | dur=%dms | %s", txID, time.Since(start).Milliseconds(), callErr), map[string]string{"error": callErr.Error()})
		s.lh.WithModule("selectsvc").Err().Error(callErr).Log("on_select HTTP request build failed")
		return
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(httpReq)
	if err != nil {
		callErr = fmt.Errorf("on_select HTTP call failed: %w", err)
		prettyLog(fmt.Sprintf("← ERR  ON_SELECT | txn=%.8s… | dur=%dms | %s", txID, time.Since(start).Milliseconds(), callErr), map[string]string{"error": callErr.Error()})
		s.lh.WithModule("selectsvc").Err().Error(callErr).Log("on_select call failed")
		return
	}
	defer resp.Body.Close() //nolint:errcheck

	respBody, _ = io.ReadAll(resp.Body)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		callErr = fmt.Errorf("on_select returned non-2xx status %d: %s", resp.StatusCode, string(respBody))
		prettyLog(fmt.Sprintf("← ERR  ON_SELECT | txn=%.8s… | http=%d | dur=%dms | %s", txID, resp.StatusCode, time.Since(start).Milliseconds(), callErr), map[string]interface{}{"http_code": resp.StatusCode, "error": callErr.Error(), "response": json.RawMessage(respBody)})
		s.lh.WithModule("selectsvc").Warn().Error(callErr).Log("on_select response error")
		return
	}

	prettyLog(fmt.Sprintf("← ACK  ON_SELECT | txn=%.8s… | http=%d | dur=%dms", txID, resp.StatusCode, time.Since(start).Milliseconds()), map[string]interface{}{"http_code": resp.StatusCode, "response": json.RawMessage(respBody)})
	s.lh.WithModule("selectsvc").Log("on_select sent successfully")
}

// writeMessageLog inserts one audit row into beckn_message_log.
// Errors are logged as warnings and never propagated.
func (s *SelectService) writeMessageLog(ctx context.Context, params dbsqlc.InsertMessageLogParams) {
	q := dbsqlc.New(s.pool)
	if err := q.InsertMessageLog(ctx, params); err != nil {
		s.lh.WithModule("selectsvc").Warn().Error(err).Log("failed to write beckn_message_log")
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func unavailableStatus() *CommitmentStatus {
	return &CommitmentStatus{Descriptor: &StatusDescriptor{Code: "CLOSED"}}
}

// resolveOnSelectURL returns the ONIX BPP caller on_select endpoint.
// Priority:
//  1. BPP_CALLER_URL env/config (e.g. https://bpptest.remiges.tech/bpp/caller)
//  2. Derived from BPP_URI by replacing /bpp/receiver with /bpp/caller
func (s *SelectService) resolveOnSelectURL(_ string) (string, error) {
	base := strings.TrimSpace(s.cfg.BppCallerURL)
	if base == "" {
		base = strings.TrimSpace(s.cfg.BppURI)
		if base == "" {
			return "", fmt.Errorf("cannot resolve on_select URL: both BPP_CALLER_URL and BPP_URI are empty")
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
	return base + "/on_select", nil
}

// extractQty returns the quantity from the BAP's commitmentAttributes JSON.
// Defaults to 1 if the field is absent or unreadable.
func extractQty(raw json.RawMessage) float64 {
	if len(raw) == 0 {
		return 1
	}
	var attrs inboundCommitmentAttrs
	if err := json.Unmarshal(raw, &attrs); err != nil || attrs.Quantity == nil {
		return 1
	}
	if attrs.Quantity.UnitQuantity <= 0 {
		return 1
	}
	return attrs.Quantity.UnitQuantity
}

// mergePrice adds price information to the existing commitmentAttributes JSON.
func mergePrice(raw json.RawMessage, unitPrice float64, currency string, qty float64) json.RawMessage {
	m := make(map[string]json.RawMessage)
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &m)
	}
	priceJSON, _ := json.Marshal(map[string]any{
		"value":    unitPrice,
		"currency": currency,
	})
	m["price"] = priceJSON
	// totalPrice is intentionally not added here because RetailCommitment v2.1
	// does not allow that field (additionalProperties=false).
	// Aggregate totals are carried at contract.consideration level.
	merged, _ := json.Marshal(m)
	return merged
}

// rawMessageOrNull returns nil (SQL NULL) when b is empty so that
// pgx does not send an empty byte slice to a JSONB column, which
// Postgres rejects as invalid JSON.
func rawMessageOrNull(b []byte) json.RawMessage {
	if len(b) == 0 {
		return nil
	}
	return json.RawMessage(b)
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func strVal(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func errStrPtr(err error) *string {
	if err == nil {
		return nil
	}
	s := err.Error()
	return &s
}

func uuidToPgtype(id uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: id, Valid: true}
}

func durationMs(start time.Time) *int32 {
	ms := int32(time.Since(start).Milliseconds())
	return &ms
}

// prettyLog prints a labelled pretty-JSON block to stdout.
func prettyLog(label string, v interface{}) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		log.Printf("[beckn] %s\n(marshal error: %v)", label, err)
		return
	}
	log.Printf("[beckn] %s\n%s", label, string(b))
}
