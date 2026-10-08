package clientsvc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/remiges-tech/logharbour/logharbour"

	dbsqlc "github.com/ion/winroom/bap/db/sqlc"
	"github.com/ion/winroom/bap/internal/config"
	"github.com/ion/winroom/bap/internal/discoverwait"
	"github.com/ion/winroom/bap/internal/httpclient"
)

// discoverCallbackTimeout bounds how long Discover waits for discovery
// service's on_discover callback to arrive after the initial Ack, before
// giving up and returning an error to the browser. See internal/discoverwait.
//
// Must stay comfortably above discovery-service's own
// DISPATCH_DELIVERY_TIMEOUT_SECONDS (docker-compose.yml, 45s as of this
// writing) — that bounds how long discovery-service itself will keep
// retrying delivery to onix-bap (whose bapTxnReceiver can be slow on a
// cold Extended Schema cache, see that var's own comment). If this is
// shorter, the browser sees a timeout even when delivery eventually
// succeeds a few seconds later.
const discoverCallbackTimeout = 60 * time.Second

type ClientService struct {
	pool     *pgxpool.Pool
	cfg      *config.Config
	lh       *logharbour.Logger
	client   *http.Client
	registry *discoverwait.Registry
}

func NewClientService(pool *pgxpool.Pool, cfg *config.Config, lh *logharbour.Logger, registry *discoverwait.Registry) *ClientService {
	return &ClientService{
		pool:     pool,
		cfg:      cfg,
		lh:       lh,
		// 90s (not a round-trip-sized 5-10s) for the same reason as
		// discoverCallbackTimeout above: select/init/confirm go through TWO
		// cold-Extended-Schema-cache-prone hops synchronously (onix-bap, then
		// onix-bpp) before bpp-application is even reached. A measured cold
		// round trip took 54s end-to-end; 90s leaves real margin above that.
		client:   httpclient.New(90 * time.Second),
		registry: registry,
	}
}

// Discover sends a Beckn v2 `discover` action through onix-bap's
// bapTxnCaller — the same adapter-mediated path every other action
// (select, init, confirm, ...) uses via sendToBPP, rather than a
// direct point-to-point call to discovery service. onix-bap signs the
// outbound request and, on the way back, its bapTxnReceiver verifies
// on_discover's signature against the network registry before routing it
// here — the same trust model as every other callback.
//
// Discover presents a synchronous call to its own caller (HandleDiscover,
// and so the browser) even though the real exchange underneath is
// genuinely async per the Beckn v2 spec: sendToBPP only gets back an
// immediate Ack, and the actual catalogs arrive later via a separate
// on_discover callback delivered to /api/webhook/on_discover. This method
// blocks on internal/discoverwait's Registry until that callback arrives
// (delivered by internal/on_discover_svc) or discoverCallbackTimeout
// elapses.
func (s *ClientService) Discover(ctx context.Context, req *ClientDiscoverRequest) (json.RawMessage, error) {
	txnID := uuid.New()
	msgID := uuid.New()
	txnIDStr := txnID.String()
	s.recordDiscoverer(ctx, txnID, req.BuyerSubject)

	textQuery := req.TextSearch
	if req.ProviderName != "" {
		textQuery = textQuery + " " + req.ProviderName
	}
	intent := map[string]any{
		"textSearch": strings.TrimSpace(textQuery),
	}

	msgJSON, _ := json.Marshal(map[string]any{
		"intent": intent,
	})

	becknReq := BecknRequest{
		Context: s.newContext("discover", txnIDStr, msgID.String(), "", ""),
		Message: msgJSON,
	}

	// Register before sending: a callback that arrives unusually fast must
	// never race ahead of us starting to wait for it.
	resultCh := s.registry.Register(txnIDStr)
	defer s.registry.Cancel(txnIDStr)

	if err := s.sendToBPP(ctx, s.cfg.BapCallerURL, "discover", txnID, msgID, s.cfg.NetworkID, becknReq); err != nil {
		return nil, err
	}

	select {
	case result := <-resultCh:
		return result, nil
	case <-ctx.Done():
		return nil, fmt.Errorf("discover request cancelled while waiting for on_discover: %w", ctx.Err())
	case <-time.After(discoverCallbackTimeout):
		return nil, fmt.Errorf("timed out after %s waiting for on_discover callback", discoverCallbackTimeout)
	}
}

// DiscoverSync sends a Beckn v2 `discover` action DIRECTLY to
// discovery-service — bypassing onix-bap entirely — using discovery
// service's own GET /discover sync shortcut, which returns the full
// catalog result inline in one request/response instead of the spec's
// real async on_discover callback. Kept alongside the async, onix-routed
// Discover as a fast, reliable alternative that doesn't depend on
// discovery-service's identity being resolvable through onix-bap's
// network registry (see discovery-service/CONTEXT.md D20/D21) — at the
// cost of not being the spec's real adapter-mediated topology.
// recordDiscoverer notes which buyer made a discover, so their message log
// can show it (see HandleListMessages). Anonymous searches aren't recorded.
func (s *ClientService) recordDiscoverer(ctx context.Context, txnID uuid.UUID, buyerSubject *string) {
	if buyerSubject == nil || *buyerSubject == "" {
		return
	}
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO discover_requests (transaction_id, buyer_subject) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
		txnID, *buyerSubject); err != nil {
		s.lh.WithModule("beckn_client").Warn().Error(err).Log("failed to record the buyer of a discover")
	}
}

// Logs an OUTBOUND row to beckn_message_log capturing both the request and the response.
func (s *ClientService) DiscoverSync(ctx context.Context, req *ClientDiscoverRequest) (json.RawMessage, error) {
	if s.cfg.CDSDiscoverURL == "" {
		return json.RawMessage(`{"message":{"catalogs":[]}}`), nil
	}

	start := time.Now()
	msgID := uuid.New()
	txnID := uuid.New()
	s.recordDiscoverer(ctx, txnID, req.BuyerSubject)

	textQuery := req.TextSearch
	if req.ProviderName != "" {
		textQuery = textQuery + " " + req.ProviderName
	}
	intent := map[string]any{
		"textSearch": strings.TrimSpace(textQuery),
	}

	becknReq := map[string]any{
		"context": map[string]any{
			"version":       "2.0.0",
			"action":        "discover",
			"timestamp":     start.UTC().Format(time.RFC3339),
			"messageId":     msgID.String(),
			"transactionId": txnID.String(),
			"bapId":         s.cfg.BapID,
			"bapUri":        s.cfg.BapURI,
			"networkId":     s.cfg.NetworkID,
		},
		"message": map[string]any{
			"intent": intent,
		},
	}

	reqJSON, _ := json.Marshal(becknReq)

	var (
		respBody   []byte
		callErr    error
		httpStatus int
	)

	discoverURL := s.cfg.CDSDiscoverURL

	// Deferred block: DB log + logharbour ACK/ERR entry.
	defer func() {
		dur := time.Since(start)
		ms := int32(dur.Milliseconds())
		ack := dbsqlc.NullAckStatus{AckStatus: dbsqlc.AckStatusACK, Valid: true}
		if callErr != nil {
			ack = dbsqlc.NullAckStatus{AckStatus: dbsqlc.AckStatusNACK, Valid: true}
			s.logBecknErr("discover", txnID.String(), discoverURL, callErr, dur)
		} else {
			s.logBecknAck("discover", txnID.String(), httpStatus, respBody, dur)
		}
		q := dbsqlc.New(s.pool)
		if err := q.InsertMessageLog(ctx, dbsqlc.InsertMessageLogParams{
			TransactionID:        pgtype.UUID{Bytes: txnID, Valid: true},
			MessageID:            msgID,
			Action:               dbsqlc.BecknActionDiscover,
			Direction:            dbsqlc.MessageDirectionOUTBOUND,
			Url:                  strPtr(discoverURL),
			BapID:                strPtr(s.cfg.BapID),
			BapUri:               strPtr(s.cfg.BapURI),
			NetworkID:            strPtr(s.cfg.NetworkID),
			AckStatus:            ack,
			RequestPayload:       reqJSON,
			ResponsePayload:      json.RawMessage(respBody),
			ErrorMessage:         errStrPtr(callErr),
			ProcessingDurationMs: &ms,
		}); err != nil {
			s.lh.WithModule("beckn_client").Warn().Error(err).Log("failed to write discover message log")
		}
	}()

	s.logBecknOut("discover", http.MethodGet, discoverURL, txnID.String(), msgID.String(), reqJSON)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, discoverURL, bytes.NewBuffer(reqJSON))
	if err != nil {
		callErr = fmt.Errorf("build discover request: %w", err)
		return nil, callErr
	}
	httpReq.Header.Set("Content-Type", "application/json")

	if s.cfg.BapPrivateKey != "" {
		authHeader, authErr := becknAuthHeader(reqJSON, s.cfg.BapKeyID, s.cfg.BapPrivateKey)
		if authErr != nil {
			callErr = fmt.Errorf("build Beckn Authorization header: %w", authErr)
			return nil, callErr
		}
		httpReq.Header.Set("Authorization", authHeader)
	}

	resp, err := s.client.Do(httpReq)
	if err != nil {
		callErr = fmt.Errorf("discover HTTP call failed: %w", err)
		return nil, callErr
	}
	defer resp.Body.Close() //nolint:errcheck

	httpStatus = resp.StatusCode

	respBody, err = io.ReadAll(resp.Body)
	if err != nil {
		callErr = fmt.Errorf("read discover response: %w", err)
		return nil, callErr
	}

	if resp.StatusCode != http.StatusOK {
		callErr = fmt.Errorf("CDS returned status %d: %s", resp.StatusCode, string(respBody))
		return nil, callErr
	}

	return respBody, nil
}

// Select initiates a Select action to the ONIX BAP adapter.
// Builds a Beckn v2 message.contract from the frontend's SelectItem list.
// BPP identity is taken from the first item's BppID/BppURI (populated from the
// catalog discover response) so the request is routed to the BPP that owns the
// resource. Server config values are used only as a fallback.
//
// buyerSubject is the requesting buyer's Zitadel subject, resolved by
// internal/auth.OptionalAuth when a valid token was presented -- nil if
// not (auth is a frontend-only gate here, so this is best-effort, not
// required). It's recorded once on the transactions row so order history
// can later be scoped to this buyer; see UpsertTransaction.
func (s *ClientService) Select(ctx context.Context, req *ClientSelectRequest, buyerSubject *string) (string, error) {
	txnID := uuid.New()
	msgID := uuid.New()

	// Resolve BPP identity from the catalog-supplied values; fall back to config.
	bppID := s.cfg.BppID
	bppURI := s.cfg.BppURI
	if len(req.Items) > 0 {
		if req.Items[0].BppID != "" {
			bppID = req.Items[0].BppID
		}
		if req.Items[0].BppURI != "" {
			bppURI = req.Items[0].BppURI
		}
	}

	msgJSON, err := buildSelectMessage(req.Items, s.cfg.BapID)
	if err != nil {
		return "", fmt.Errorf("build select message: %w", err)
	}

	becknReq := BecknRequest{
		Context: s.newContext("select", txnID.String(), msgID.String(), bppID, bppURI),
		Message: json.RawMessage(msgJSON),
	}

	q := dbsqlc.New(s.pool)
	if err := q.UpsertTransaction(ctx, dbsqlc.UpsertTransactionParams{
		TransactionID: txnID,
		BapID:         s.cfg.BapID,
		NetworkID:     &s.cfg.NetworkID,
		BppID:         &bppID,
		BppUri:        &bppURI,
		Status:        dbsqlc.TransactionStatusSELECTSENT,
		BuyerSubject:  buyerSubject,
	}); err != nil {
		s.lh.WithModule("client_svc").Warn().Error(err).Log("upsert transaction failed")
	}

	if err := s.sendToBPP(ctx, s.cfg.BapCallerURL, "select", txnID, msgID, s.cfg.NetworkID, becknReq); err != nil {
		return "", err
	}

	return txnID.String(), nil
}

// buildSelectMessage constructs the Beckn v2 message.contract JSON for a select call.
func buildSelectMessage(items []SelectItem, bapID string) ([]byte, error) {
	if len(items) == 0 {
		return nil, fmt.Errorf("at least one item required")
	}

	// Use first item's provider for the provider participant (single-provider per select).
	providerID := items[0].ProviderID
	if providerID == "" {
		providerID = "provider-default"
	}
	providerName := items[0].ProviderName
	buyerID := "buyer-" + bapID

	// Build commitments, one per item.
	commitments := make([]map[string]interface{}, 0, len(items))
	commitmentIDs := make([]string, 0, len(items))
	for i, item := range items {
		cid := fmt.Sprintf("commitment-%s", item.ResourceID)
		commitmentIDs = append(commitmentIDs, cid)

		qty := item.Quantity
		if qty <= 0 {
			qty = 1
		}

		// Build a sanitized offerAttributes containing only @context, @type and policies.
		// The raw offerAttributes from the CDS discover response may contain fields
		// (e.g. price.applicableQuantity.minValue) that the adapter's strict schema rejects.
		offerAttr := sanitizeOfferAttributes(item.OfferAttributes)

		offerName := item.OfferName
		if offerName == "" {
			offerName = item.ResourceID
		}

		offer := map[string]interface{}{
			"id":          item.OfferID,
			"resourceIds": []string{item.ResourceID},
			"descriptor":  map[string]interface{}{"name": offerName},
			"provider": map[string]interface{}{
				"id":         providerID,
				"descriptor": map[string]interface{}{"name": providerName},
			},
			"offerAttributes": offerAttr,
		}
		if hasJSONItems(item.OfferConsiderations) {
			offer["considerations"] = item.OfferConsiderations
		}

		resource := map[string]interface{}{
			"id":       item.ResourceID,
			"quantity": map[string]interface{}{"unitCode": "EA", "unitQuantity": qty},
		}
		var detail struct {
			Descriptor         json.RawMessage `json:"descriptor"`
			ResourceAttributes json.RawMessage `json:"resourceAttributes"`
		}
		if json.Unmarshal(item.Resource, &detail) == nil {
			if isJSONObject(detail.Descriptor) {
				resource["descriptor"] = detail.Descriptor
			}
			if isJSONObject(detail.ResourceAttributes) {
				resource["resourceAttributes"] = detail.ResourceAttributes
			}
		}

		commitments = append(commitments, map[string]interface{}{
			"id":        cid,
			"status":    map[string]interface{}{"descriptor": map[string]interface{}{"code": "DRAFT"}},
			"resources": []map[string]interface{}{resource},
			"offer":     offer,
			"commitmentAttributes": map[string]interface{}{
				"@context":   "https://schema.ion.id/release1/schema/extension/trade/TradeCommitment/v1/context.jsonld",
				"@type":      "ion:IONTradeCommitment",
				"lineId":     fmt.Sprintf("LINE-%03d", i+1),
				"resourceId": item.ResourceID,
				"quantity":   map[string]interface{}{"unitCode": "EA", "unitQuantity": qty},
			},
		})
	}

	contract := map[string]interface{}{
		"status": map[string]interface{}{"code": "DRAFT"},
		"participants": []map[string]interface{}{
			{
				"id":         providerID,
				"descriptor": map[string]interface{}{"name": providerName},
				"participantAttributes": map[string]interface{}{
					"@context": "https://schema.ion.id/release1/schema/extension/trade/TradeProvider/v1/context.jsonld",
					"@type":    "ion:IONTradeProvider",
					"id":       providerID,
					"descriptor": map[string]interface{}{
						"name": providerName,
					},
				},
			},
			{
				"id":         buyerID,
				"descriptor": map[string]interface{}{"name": "Buyer"},
				"participantAttributes": map[string]interface{}{
					"@context": "https://raw.githubusercontent.com/beckn/schemas/main/schema/Consumer/v2.0/context.jsonld",
					"@type":    "Consumer",
					"role":     "buyer",
					"person":   map[string]interface{}{"id": buyerID, "name": "Buyer"},
				},
			},
		},
		"commitments": commitments,
		"performance": []map[string]interface{}{
			{
				"id":            "f1",
				"status":        map[string]interface{}{"code": "PENDING"},
				"commitmentIds": commitmentIDs,
				"performanceAttributes": map[string]interface{}{
					"@context":                  "https://schema.ion.id/release1/schema/extension/trade/TradePerformance/v1/context.jsonld",
					"@type":                     "ion:IONTradePerformance",
					"supportedPerformanceModes": []string{"DELIVERY"},
					"deliveryDetails": map[string]interface{}{
						"address": map[string]interface{}{
							"streetAddress":   "TBD",
							"addressLocality": "TBD",
							"addressRegion":   "TBD",
							"postalCode":      "000000",
							"addressCountry":  "IN",
						},
						"contact": map[string]interface{}{
							"name":  "Buyer",
							"phone": "+910000000000",
						},
					},
					"operatingHours": []map[string]interface{}{
						{
							"daysOfWeek": []int{1, 2, 3, 4, 5, 6},
							"timeRange":  map[string]string{"start": "09:00", "end": "21:00"},
						},
					},
					"installationScheduling": map[string]interface{}{"available": false},
				},
			},
		},
	}

	return json.Marshal(map[string]interface{}{"contract": contract})
}

// isJSONObject reports whether raw is a non-empty JSON object.
func isJSONObject(raw json.RawMessage) bool {
	var m map[string]json.RawMessage
	return json.Unmarshal(raw, &m) == nil && len(m) > 0
}

// hasJSONItems reports whether raw is a non-empty JSON array.
func hasJSONItems(raw json.RawMessage) bool {
	var items []json.RawMessage
	return json.Unmarshal(raw, &items) == nil && len(items) > 0
}

// sanitizeOfferAttributes extracts only @context, @type, and policies from the raw
// offerAttributes discovered via the CDS. The adapter's strict JSON-LD schema rejects
// fields like price.applicableQuantity.minValue that the CDS uses but the adapter does not.
func sanitizeOfferAttributes(raw json.RawMessage) map[string]interface{} {
	const (
		offerCtx  = "https://schema.ion.id/release1/schema/extension/trade/TradeOffer/v1/context.jsonld"
		offerType = "ion:IONTradeOffer"
	)

	out := map[string]interface{}{
		"@context": offerCtx,
		"@type":    offerType,
	}

	if len(raw) <= 2 {
		return out
	}

	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return out
	}

	// Preserve @context / @type from the discover response if present.
	if v, ok := decoded["@context"]; ok {
		var s string
		if json.Unmarshal(v, &s) == nil && s != "" {
			out["@context"] = s
		}
	}
	if v, ok := decoded["@type"]; ok {
		var s string
		if json.Unmarshal(v, &s) == nil && s != "" {
			out["@type"] = s
		}
	}

	// Carry policies through — returns and cancellation are safe per adapter schema.
	if v, ok := decoded["policies"]; ok {
		var policies interface{}
		if json.Unmarshal(v, &policies) == nil {
			out["policies"] = policies
		}
	}

	return out
}

// billingInfo holds the buyer billing details submitted by the frontend.
// Address fields follow Schema.org naming to map directly into the Beckn
// performanceAttributes.deliveryDetails.address structure.
type billingInfo struct {
	Name            string `json:"name"`
	Email           string `json:"email"`
	Phone           string `json:"phone"`
	StreetAddress   string `json:"streetAddress"`
	AddressLocality string `json:"addressLocality"`
	AddressRegion   string `json:"addressRegion"`
	PostalCode      string `json:"postalCode"`
	AddressCountry  string `json:"addressCountry"`
}

// Init initiates an Init action to the ONIX BAP adapter.
// It loads the on_select contract snapshot, patches it with the buyer's real
// billing and delivery details, then sends the full contract as message.contract.
func (s *ClientService) Init(ctx context.Context, req *ClientInitRequest) error {
	txnID, _ := uuid.Parse(req.TransactionID)
	msgID := uuid.New()
	q := dbsqlc.New(s.pool)

	// Resolve BPP identity from the stored transaction (set during select).
	txn, err := q.GetTransaction(ctx, txnID)
	if err != nil {
		return fmt.Errorf("transaction %s not found: %w", req.TransactionID, err)
	}
	bppID := s.cfg.BppID
	bppURI := s.cfg.BppURI
	if txn.BppID != nil && *txn.BppID != "" {
		bppID = *txn.BppID
	}
	if txn.BppUri != nil && *txn.BppUri != "" {
		bppURI = *txn.BppUri
	}

	msgJSON, err := s.buildInitMessage(ctx, q, txnID, req)
	if err != nil {
		return fmt.Errorf("build init message: %w", err)
	}

	becknReq := BecknRequest{
		Context: s.newContext("init", req.TransactionID, msgID.String(), bppID, bppURI),
		Message: json.RawMessage(msgJSON),
	}

	err = q.UpsertTransaction(ctx, dbsqlc.UpsertTransactionParams{
		TransactionID: txnID,
		BapID:         s.cfg.BapID,
		NetworkID:     &s.cfg.NetworkID,
		BppID:         &bppID,
		BppUri:        &bppURI,
		Status:        dbsqlc.TransactionStatusINITSENT,
	})
	if err != nil {
		return err
	}

	return s.sendToBPP(ctx, s.cfg.BapCallerURL, "init", txnID, msgID, s.cfg.NetworkID, becknReq)
}

// buildInitMessage loads the on_select contract snapshot and patches it with real
// buyer billing/delivery details, returning a {"contract": ...} JSON blob.
func (s *ClientService) buildInitMessage(ctx context.Context, q *dbsqlc.Queries, txnID uuid.UUID, req *ClientInitRequest) ([]byte, error) {
	var billing billingInfo
	if err := json.Unmarshal(req.Billing, &billing); err != nil {
		return nil, fmt.Errorf("parse billing: %w", err)
	}

	snapshot, err := q.GetLatestContractSnapshot(ctx, txnID)
	if err != nil || len(snapshot.Contract) == 0 || string(snapshot.Contract) == "null" {
		return nil, fmt.Errorf("no on_select contract snapshot found for txn %s", txnID)
	}

	// Parse as a generic map so we update only the fields we care about while
	// preserving the full BPP-enriched contract (commitments, resources, etc.).
	var contract map[string]interface{}
	if err := json.Unmarshal(snapshot.Contract, &contract); err != nil {
		return nil, fmt.Errorf("parse contract snapshot: %w", err)
	}

	if err := checkQuoteAvailable(contract); err != nil {
		return nil, err
	}

	patchBuyerParticipant(contract, s.cfg.BapID, billing)
	patchPerformanceDelivery(contract, billing)

	return json.Marshal(map[string]interface{}{"contract": contract})
}

// ErrQuoteUnavailable means the seller's quote (on_select) can't be ordered:
// it marked an item it no longer has as CLOSED, or priced nothing at all.
var ErrQuoteUnavailable = errors.New("the seller can no longer supply this order")

// checkQuoteAvailable returns ErrQuoteUnavailable, naming the items, unless
// every commitment is available and the contract carries a price.
func checkQuoteAvailable(contract map[string]interface{}) error {
	var closed []string
	commitments, _ := contract["commitments"].([]interface{})
	for _, raw := range commitments {
		c, _ := raw.(map[string]interface{})
		status, _ := c["status"].(map[string]interface{})
		desc, _ := status["descriptor"].(map[string]interface{})
		if code, _ := desc["code"].(string); code != "CLOSED" {
			continue
		}
		name := ""
		if resources, _ := c["resources"].([]interface{}); len(resources) > 0 {
			r, _ := resources[0].(map[string]interface{})
			name, _ = r["id"].(string)
		}
		closed = append(closed, name)
	}
	if len(closed) > 0 {
		return fmt.Errorf("%w: unavailable items %s", ErrQuoteUnavailable, strings.Join(closed, ", "))
	}
	if considerations, _ := contract["consideration"].([]interface{}); len(considerations) == 0 {
		return fmt.Errorf("%w: the quote has no price", ErrQuoteUnavailable)
	}
	return nil
}

// patchBuyerParticipant updates the buyer participant's person details with
// the actual billing name and phone number.
func patchBuyerParticipant(contract map[string]interface{}, bapID string, b billingInfo) {
	participants, _ := contract["participants"].([]interface{})
	buyerID := "buyer-" + bapID
	for _, raw := range participants {
		p, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		if id, _ := p["id"].(string); id != buyerID {
			continue
		}
		attrs, ok := p["participantAttributes"].(map[string]interface{})
		if !ok {
			attrs = map[string]interface{}{}
			p["participantAttributes"] = attrs
		}
		person, _ := attrs["person"].(map[string]interface{})
		if person == nil {
			person = map[string]interface{}{}
		}
		if b.Name != "" {
			person["name"] = b.Name
		}
		if b.Email != "" {
			person["email"] = b.Email
		}
		if b.Phone != "" {
			person["telephone"] = b.Phone
		}
		attrs["person"] = person
	}
}

// patchPerformanceDelivery replaces the placeholder delivery address and contact
// in every performance block with real values from the buyer's billing form.
func patchPerformanceDelivery(contract map[string]interface{}, b billingInfo) {
	performances, _ := contract["performance"].([]interface{})
	for _, raw := range performances {
		p, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		attrs, ok := p["performanceAttributes"].(map[string]interface{})
		if !ok {
			attrs = map[string]interface{}{}
			p["performanceAttributes"] = attrs
		}
		country := b.AddressCountry
		if country == "" {
			country = "IN"
		}
		attrs["deliveryDetails"] = map[string]interface{}{
			"address": map[string]interface{}{
				"streetAddress":   b.StreetAddress,
				"addressLocality": b.AddressLocality,
				"addressRegion":   b.AddressRegion,
				"postalCode":      b.PostalCode,
				"addressCountry":  country,
			},
			"contact": map[string]interface{}{
				"name":  b.Name,
				"phone": b.Phone,
			},
		}
	}
}

// Confirm initiates a Confirm action to the ONIX BAP adapter.
// It loads the latest contract snapshot (from on_init) and sends the full
// contract as message.contract, satisfying the schema's required commitments field.
func (s *ClientService) Confirm(ctx context.Context, req *ClientConfirmRequest) error {
	txnID, _ := uuid.Parse(req.TransactionID)
	msgID := uuid.New()
	q := dbsqlc.New(s.pool)

	// Resolve BPP identity from the stored transaction.
	txn, err := q.GetTransaction(ctx, txnID)
	if err != nil {
		return fmt.Errorf("transaction %s not found: %w", req.TransactionID, err)
	}
	bppID := s.cfg.BppID
	bppURI := s.cfg.BppURI
	if txn.BppID != nil && *txn.BppID != "" {
		bppID = *txn.BppID
	}
	if txn.BppUri != nil && *txn.BppUri != "" {
		bppURI = *txn.BppUri
	}

	// Load the on_init contract snapshot (most recent, contains BPP-assigned contract id).
	snapshot, err := q.GetLatestContractSnapshot(ctx, txnID)
	if err != nil || len(snapshot.Contract) == 0 || string(snapshot.Contract) == "null" {
		return fmt.Errorf("no contract snapshot found for confirm txn %s", txnID)
	}

	var contract map[string]interface{}
	if err := json.Unmarshal(snapshot.Contract, &contract); err != nil {
		return fmt.Errorf("parse contract snapshot: %w", err)
	}
	if err := attachSettlement(contract); err != nil {
		return fmt.Errorf("attach settlement for txn %s: %w", txnID, err)
	}

	msgJSON, err := json.Marshal(map[string]interface{}{"contract": contract})
	if err != nil {
		return fmt.Errorf("marshal confirm message: %w", err)
	}

	becknReq := BecknRequest{
		Context: s.newContext("confirm", req.TransactionID, msgID.String(), bppID, bppURI),
		Message: json.RawMessage(msgJSON),
	}

	err = q.UpsertTransaction(ctx, dbsqlc.UpsertTransactionParams{
		TransactionID: txnID,
		BapID:         s.cfg.BapID,
		NetworkID:     &s.cfg.NetworkID,
		BppID:         &bppID,
		BppUri:        &bppURI,
		Status:        dbsqlc.TransactionStatusCONFIRMSENT,
	})
	if err != nil {
		return err
	}

	return s.sendToBPP(ctx, s.cfg.BapCallerURL, "confirm", txnID, msgID, s.cfg.NetworkID, becknReq)
}

// mockGatewayName identifies the stand-in payment gateway: there is no real
// gateway yet, so Pay & Confirm is treated as a successful payment and a UUID
// is generated in place of the gateway's transaction reference.
const mockGatewayName = "mock-gateway"

// attachSettlement adds a COMMITTED settlement (ION TradeSettlement
// attributes) discharging the contract's first consideration, carrying the
// payment proof the BPP needs to activate the contract on confirm.
func attachSettlement(contract map[string]interface{}) error {
	considerations, _ := contract["consideration"].([]interface{})
	if len(considerations) == 0 {
		return fmt.Errorf("contract has no consideration to settle")
	}
	consideration, _ := considerations[0].(map[string]interface{})
	considerationID, _ := consideration["id"].(string)
	attrs, _ := consideration["considerationAttributes"].(map[string]interface{})
	amount, okAmount := attrs["totalAmount"].(float64)
	currency, okCurrency := attrs["currency"].(string)
	if considerationID == "" || !okAmount || !okCurrency || currency == "" {
		return fmt.Errorf("consideration is missing id, totalAmount or currency")
	}

	contract["settlements"] = []interface{}{
		map[string]interface{}{
			"id":              "settlement-" + uuid.New().String()[:8],
			"considerationId": considerationID,
			"status":          "COMMITTED",
			"settlementAttributes": map[string]interface{}{
				"@context":      "https://schema.ion.id/release1/schema/extension/trade/TradeSettlement/v1/context.jsonld",
				"@type":         "ion:IONTradeSettlement",
				"method":        "ONLINE",
				"settledAt":     time.Now().UTC().Format(time.RFC3339),
				"settledAmount": amount,
				"currency":      currency,
				"gateway": map[string]interface{}{
					"name":          mockGatewayName,
					"transactionId": uuid.New().String(),
				},
			},
		},
	}
	return nil
}

// RequestStatus sends a Beckn status request for an active transaction.
func (s *ClientService) RequestStatus(ctx context.Context, req *ClientRequestStatusRequest) error {
	txnID, _ := uuid.Parse(req.TransactionID)
	msgID := uuid.New()
	q := dbsqlc.New(s.pool)

	txn, err := q.GetTransaction(ctx, txnID)
	if err != nil {
		return fmt.Errorf("transaction %s not found: %w", req.TransactionID, err)
	}
	bppID, bppURI := s.resolveBPP(txn)

	snapshot, err := q.GetLatestContractSnapshot(ctx, txnID)
	if err != nil {
		return fmt.Errorf("no snapshot for txn %s: %w", txnID, err)
	}

	// Extract the full contract from the snapshot so the adapter gets
	// the required `commitments` array and other required fields.
	var contractEnvelope struct {
		Message struct {
			Contract json.RawMessage `json:"contract"`
		} `json:"message"`
	}
	var contractPayload json.RawMessage
	if err := json.Unmarshal(snapshot.Contract, &contractEnvelope); err == nil && len(contractEnvelope.Message.Contract) > 2 {
		contractPayload = contractEnvelope.Message.Contract
	} else {
		contractPayload = snapshot.Contract
	}

	msgJSON, _ := json.Marshal(map[string]interface{}{
		"contract": json.RawMessage(contractPayload),
	})
	becknReq := BecknRequest{
		Context: s.newContext("status", req.TransactionID, msgID.String(), bppID, bppURI),
		Message: json.RawMessage(msgJSON),
	}
	err = q.UpsertTransaction(ctx, dbsqlc.UpsertTransactionParams{
		TransactionID: txnID,
		BapID:         s.cfg.BapID,
		NetworkID:     &s.cfg.NetworkID,
		BppID:         &bppID,
		BppUri:        &bppURI,
		Status:        dbsqlc.TransactionStatusSTATUSSENT,
	})
	if err != nil {
		return err
	}
	return s.sendToBPP(ctx, s.cfg.BapCallerURL, "status", txnID, msgID, s.cfg.NetworkID, becknReq)
}

// Cancel sends a Beckn cancel request for an active transaction.
func (s *ClientService) Cancel(ctx context.Context, req *ClientCancelRequest) error {
	txnID, _ := uuid.Parse(req.TransactionID)
	msgID := uuid.New()
	q := dbsqlc.New(s.pool)

	txn, err := q.GetTransaction(ctx, txnID)
	if err != nil {
		return fmt.Errorf("transaction %s not found: %w", req.TransactionID, err)
	}
	bppID, bppURI := s.resolveBPP(txn)

	snapshot, err := q.GetLatestContractSnapshot(ctx, txnID)
	if err != nil {
		return fmt.Errorf("no snapshot for txn %s: %w", txnID, err)
	}

	// Extract the full contract from the snapshot so the adapter gets
	// the required `commitments` array and other required fields.
	var contractEnvelope struct {
		Message struct {
			Contract json.RawMessage `json:"contract"`
		} `json:"message"`
	}
	var contractPayload json.RawMessage
	if err := json.Unmarshal(snapshot.Contract, &contractEnvelope); err == nil &&
		len(contractEnvelope.Message.Contract) > 2 {
		contractPayload = contractEnvelope.Message.Contract
	} else {
		// Snapshot already IS the contract object (not wrapped in message)
		contractPayload = snapshot.Contract
	}

	// Declare what is cancelled and why (ION TradeContract
	// contractAttributes.cancellation): every still-active commitment.
	var contract map[string]interface{}
	if err := json.Unmarshal(contractPayload, &contract); err != nil || contract == nil {
		return fmt.Errorf("parse contract snapshot: %v", err)
	}
	var activeIDs []string
	for _, raw := range asSlice(contract["commitments"]) {
		if cm := asObj(raw); cm != nil && commitmentStatusCode(cm) != "CLOSED" {
			activeIDs = append(activeIDs, asStr(cm["id"]))
		}
	}
	if err := checkCancellationCutoff(contract, activeIDs); err != nil {
		return err
	}
	setCancellation(contract, req.ReasonCode, activeIDs)

	msgJSON, _ := json.Marshal(map[string]interface{}{"contract": contract})
	becknReq := BecknRequest{
		Context: s.newContext("cancel", req.TransactionID, msgID.String(), bppID, bppURI),
		Message: json.RawMessage(msgJSON),
	}
	err = q.UpsertTransaction(ctx, dbsqlc.UpsertTransactionParams{
		TransactionID: txnID,
		BapID:         s.cfg.BapID,
		NetworkID:     &s.cfg.NetworkID,
		BppID:         &bppID,
		BppUri:        &bppURI,
		Status:        dbsqlc.TransactionStatusCANCELSENT,
	})
	if err != nil {
		return err
	}
	return s.sendToBPP(ctx, s.cfg.BapCallerURL, "cancel", txnID, msgID, s.cfg.NetworkID, becknReq)
}

// CancelItems cancels the requested units of a confirmed order. When they add
// up to everything still active it sends /cancel; otherwise it sends /update
// with the latest contract, each chosen commitment's quantity lowered by the
// cancelled units (or its status CLOSED when all of it goes) -- the BPP turns
// that difference into a partial cancellation. Returns the new transaction status.
func (s *ClientService) CancelItems(ctx context.Context, req *ClientCancelItemsRequest) (string, error) {
	txnID, err := uuid.Parse(req.TransactionID)
	if err != nil {
		return "", fmt.Errorf("invalid transaction_id: %w", err)
	}
	snapshot, err := dbsqlc.New(s.pool).GetLatestContractSnapshot(ctx, txnID)
	if err != nil {
		return "", fmt.Errorf("no snapshot for txn %s: %w", txnID, err)
	}
	var contract map[string]interface{}
	if err := json.Unmarshal(unwrapContract(snapshot.Contract), &contract); err != nil || contract == nil {
		return "", fmt.Errorf("parse contract snapshot: %v", err)
	}

	active := map[string]map[string]interface{}{}
	for _, raw := range asSlice(contract["commitments"]) {
		cm := asObj(raw)
		if cm != nil && commitmentStatusCode(cm) != "CLOSED" {
			active[asStr(cm["id"])] = cm
		}
	}

	cancelQty := map[string]float64{}
	for _, it := range req.Items {
		cm, ok := active[it.CommitmentID]
		if !ok {
			return "", fmt.Errorf("commitment %q is not an active item of this order", it.CommitmentID)
		}
		have := commitmentQuantity(cm)
		q := it.Quantity
		if q <= 0 {
			q = have
		}
		if cancelQty[it.CommitmentID]+q > have {
			return "", fmt.Errorf("cannot cancel %v of %q: only %v ordered", q, it.CommitmentID, have)
		}
		cancelQty[it.CommitmentID] += q
	}

	chosen := make([]string, 0, len(cancelQty))
	for id := range cancelQty {
		chosen = append(chosen, id)
	}
	if err := checkCancellationCutoff(contract, chosen); err != nil {
		return "", err
	}

	full := true
	for id, cm := range active {
		if cancelQty[id] < commitmentQuantity(cm) {
			full = false
			break
		}
	}
	if _, ok := cancellationReasons[req.ReasonCode]; !ok {
		return "", fmt.Errorf("unknown cancellation reason %q", req.ReasonCode)
	}
	if full {
		if err := s.Cancel(ctx, &ClientCancelRequest{TransactionID: req.TransactionID, ReasonCode: req.ReasonCode}); err != nil {
			return "", err
		}
		return string(dbsqlc.TransactionStatusCANCELSENT), nil
	}

	var cancelledIDs []string
	for id, q := range cancelQty {
		cancelledIDs = append(cancelledIDs, id)
		cm := active[id]
		if remaining := commitmentQuantity(cm) - q; remaining > 0 {
			setCommitmentQuantity(cm, remaining)
		} else {
			cm["status"] = map[string]interface{}{"descriptor": map[string]interface{}{"code": "CLOSED", "name": "Cancelled"}}
		}
	}
	sort.Strings(cancelledIDs)
	setCancellation(contract, req.ReasonCode, cancelledIDs)
	contractJSON, err := json.Marshal(contract)
	if err != nil {
		return "", err
	}
	if err := s.Update(ctx, &ClientUpdateRequest{TransactionID: req.TransactionID, Contract: contractJSON}); err != nil {
		return "", err
	}
	return string(dbsqlc.TransactionStatusUPDATESENT), nil
}

// ReturnItems requests a return of the given units of a delivered order: it
// checks each item's offer policies.returns (allowed, delivered, still inside
// the window counted from delivery) and sends /update with the latest
// contract, the returned quantities lowered (or CLOSED when all of an item
// goes back) and a new RETURN performance (Return_Initiated). The BPP applies
// the same policy and answers Return_Approved -- with the restocking fee and
// refund -- or Return_Rejected.
func (s *ClientService) ReturnItems(ctx context.Context, req *ClientReturnItemsRequest) (string, error) {
	txnID, err := uuid.Parse(req.TransactionID)
	if err != nil {
		return "", fmt.Errorf("invalid transaction_id: %w", err)
	}
	snapshot, err := dbsqlc.New(s.pool).GetLatestContractSnapshot(ctx, txnID)
	if err != nil {
		return "", fmt.Errorf("no snapshot for txn %s: %w", txnID, err)
	}
	var contract map[string]interface{}
	if err := json.Unmarshal(unwrapContract(snapshot.Contract), &contract); err != nil || contract == nil {
		return "", fmt.Errorf("parse contract snapshot: %v", err)
	}

	stage, _ := fulfillmentStage(contract)
	delivered := s.deliveredAt(ctx, txnID)
	active := map[string]map[string]interface{}{}
	for _, raw := range asSlice(contract["commitments"]) {
		if cm := asObj(raw); cm != nil && commitmentStatusCode(cm) != "CLOSED" {
			active[asStr(cm["id"])] = cm
		}
	}
	returnQty := map[string]float64{}
	for _, it := range req.Items {
		cm, ok := active[it.CommitmentID]
		if !ok {
			return "", fmt.Errorf("commitment %q is not an active item of this order", it.CommitmentID)
		}
		if err := returnBlocked(cm, stage, delivered, time.Now()); err != nil {
			return "", fmt.Errorf("%s cannot be returned: %v", it.CommitmentID, err)
		}
		have := commitmentQuantity(cm)
		q := it.Quantity
		if q <= 0 {
			q = have
		}
		if returnQty[it.CommitmentID]+q > have {
			return "", fmt.Errorf("cannot return %v of %q: only %v ordered", q, it.CommitmentID, have)
		}
		returnQty[it.CommitmentID] += q
	}

	var ids []string
	for id, q := range returnQty {
		ids = append(ids, id)
		cm := active[id]
		if remaining := commitmentQuantity(cm) - q; remaining > 0 {
			setCommitmentQuantity(cm, remaining)
		} else {
			cm["status"] = map[string]interface{}{"descriptor": map[string]interface{}{"code": "CLOSED", "name": "Returned"}}
		}
	}
	sort.Strings(ids)

	perfIDs := map[string]bool{}
	for _, p := range asSlice(contract["performance"]) {
		perfIDs[asStr(asObj(p)["id"])] = true
	}
	perfID := ""
	for n := 1; ; n++ {
		if perfID = fmt.Sprintf("return-%d", n); !perfIDs[perfID] {
			break
		}
	}
	contract["performance"] = append(asSlice(contract["performance"]), map[string]interface{}{
		"id":            perfID,
		"commitmentIds": ids,
		"status":        map[string]interface{}{"code": "Return_Initiated", "name": "Return requested"},
		"performanceAttributes": map[string]interface{}{
			"@context":                  "https://schema.ion.id/release1/schema/extension/trade/TradePerformance/v1/context.jsonld",
			"@type":                     "ion:IONTradePerformance",
			"type":                      "RETURN",
			"supportedPerformanceModes": []string{"DELIVERY"},
		},
	})

	contractJSON, err := json.Marshal(contract)
	if err != nil {
		return "", err
	}
	if err := s.Update(ctx, &ClientUpdateRequest{TransactionID: req.TransactionID, Contract: contractJSON}); err != nil {
		return "", err
	}
	return string(dbsqlc.TransactionStatusUPDATESENT), nil
}

// deliveredAt is when this BAP first saw the order as Delivered (the earliest
// snapshot whose performance says so); zero when never seen.
func (s *ClientService) deliveredAt(ctx context.Context, txnID uuid.UUID) time.Time {
	var t *time.Time
	_ = s.pool.QueryRow(ctx, `
		SELECT min(created_at) FROM contract_snapshots
		WHERE transaction_id = $1 AND contract->'performance' @> '[{"status":{"code":"Delivered"}}]'`, txnID).Scan(&t)
	if t == nil {
		return time.Time{}
	}
	return *t
}

// fulfillmentStage is the furthest stage the contract's performances reached.
func fulfillmentStage(contract map[string]interface{}) (int, string) {
	stage, name := stagePending, "PENDING"
	for _, p := range asSlice(contract["performance"]) {
		code := asStr(asObj(asObj(p)["status"])["code"])
		if r := stageRank(code); r > stage {
			stage, name = r, code
		}
	}
	return stage, name
}

// returnBlocked explains why a commitment can't be returned under its offer's
// policies.returns (the BPP enforces the same rule), or returns nil.
func returnBlocked(cm map[string]interface{}, stage int, deliveredAt, now time.Time) error {
	pol := asObj(asObj(asObj(asObj(cm["offer"])["offerAttributes"])["policies"])["returns"])
	if allowed, _ := pol["allowed"].(bool); !allowed {
		return fmt.Errorf("the offer does not accept returns")
	}
	if stage < stageDelivered {
		return fmt.Errorf("the order has not been delivered yet")
	}
	if w := asStr(pol["window"]); w != "" && !deliveredAt.IsZero() {
		d, err := parseISODuration(w)
		if err != nil {
			return fmt.Errorf("invalid return window %q", w)
		}
		if closes := deliveredAt.Add(d); now.After(closes) {
			return fmt.Errorf("the %s return window closed at %s", w, closes.UTC().Format(time.RFC3339))
		}
	}
	return nil
}

// parseISODuration parses an ISO 8601 duration such as P7D, PT2H or P1W2DT3H
// (years as 365 days, months as 30).
func parseISODuration(s string) (time.Duration, error) {
	if !strings.HasPrefix(s, "P") || len(s) < 3 {
		return 0, fmt.Errorf("not an ISO 8601 duration")
	}
	var total time.Duration
	inTime, num := false, ""
	units := map[bool]map[byte]time.Duration{
		false: {'Y': 365 * 24 * time.Hour, 'M': 30 * 24 * time.Hour, 'W': 7 * 24 * time.Hour, 'D': 24 * time.Hour},
		true:  {'H': time.Hour, 'M': time.Minute, 'S': time.Second},
	}
	for i := 1; i < len(s); i++ {
		ch := s[i]
		switch {
		case ch == 'T':
			inTime = true
		case (ch >= '0' && ch <= '9') || ch == '.':
			num += string(ch)
		default:
			unit, ok := units[inTime][ch]
			if !ok || num == "" {
				return 0, fmt.Errorf("bad component %q", string(ch))
			}
			var v float64
			if _, err := fmt.Sscanf(num, "%g", &v); err != nil {
				return 0, err
			}
			total += time.Duration(v * float64(unit))
			num = ""
		}
	}
	if num != "" {
		return 0, fmt.Errorf("trailing number without unit")
	}
	return total, nil
}

// cancellationReasons are ION's cancellation reason codes (TradeOffer
// policies.cancellation.allowedReasons) with their display names.
var cancellationReasons = map[string]string{
	"CHANGE_OF_MIND":           "Change of mind",
	"WRONG_ITEM_ORDERED":       "Wrong item ordered",
	"ORDERED_WRONG_VARIANT":    "Ordered wrong variant",
	"FOUND_BETTER_PRICE":       "Found a better price",
	"DELIVERY_TOO_LONG":        "Delivery takes too long",
	"INCORRECT_ADDRESS":        "Incorrect address",
	"DUPLICATE_ORDER":          "Duplicate order",
	"PAYMENT_METHOD_CHANGE":    "Payment method change",
	"ITEM_NO_LONGER_NEEDED":    "Item no longer needed",
	"SELLER_UNRESPONSIVE":      "Seller unresponsive",
	"BUYER_FRAUD_SUSPECTED":    "Buyer fraud suspected",
	"QUALITY_ISSUE_AT_PREVIEW": "Quality issue at preview",
}

// Fulfillment stages, in order (performance status codes the seller sets:
// PENDING → Packed → Shipped → Delivered; returns only follow delivery).
const (
	stagePending = iota
	stagePacked
	stageShipped
	stageDelivered
)

func stageRank(code string) int {
	switch c := strings.ToUpper(code); {
	case c == "PACKED":
		return stagePacked
	case c == "DISPATCHED" || c == "SHIPPED" || c == "IN_TRANSIT" || c == "OUT_FOR_DELIVERY":
		return stageShipped
	case c == "DELIVERED" || strings.HasPrefix(c, "RETURN"):
		return stageDelivered
	}
	return stagePending
}

// checkCancellationCutoff refuses a cancellation once the order has passed
// any chosen commitment's offer policies.cancellation.cutoffEvent (e.g.
// BEFORE_PACKING and the order is already Packed). The BPP enforces the same
// rule; checking here gives the buyer the reason before anything is sent.
func checkCancellationCutoff(contract map[string]interface{}, commitmentIDs []string) error {
	stage, stageName := stagePending, "PENDING"
	for _, p := range asSlice(contract["performance"]) {
		code := asStr(asObj(asObj(p)["status"])["code"])
		if r := stageRank(code); r > stage {
			stage, stageName = r, code
		}
	}
	chosen := map[string]bool{}
	for _, id := range commitmentIDs {
		chosen[id] = true
	}
	for _, raw := range asSlice(contract["commitments"]) {
		cm := asObj(raw)
		if cm == nil || !chosen[asStr(cm["id"])] {
			continue
		}
		pol := asObj(asObj(asObj(asObj(cm["offer"])["offerAttributes"])["policies"])["cancellation"])
		if allowed, ok := pol["allowed"].(bool); ok && !allowed {
			return fmt.Errorf("this item's offer does not allow cancellation")
		}
		if stage >= stageDelivered {
			return fmt.Errorf("the order is already %s: a delivered item is returned, not cancelled", stageName)
		}
		cutoff := asStr(pol["cutoffEvent"])
		passed := false
		switch cutoff {
		case "BEFORE_CONFIRMATION":
			passed = true
		case "BEFORE_PACKING":
			passed = stage >= stagePacked
		case "BEFORE_DISPATCH", "BEFORE_SHIPMENT":
			passed = stage >= stageShipped
		}
		if passed {
			return fmt.Errorf("cancellation is no longer possible: the offer allows it only %s and the order is %s",
				strings.ToLower(strings.ReplaceAll(cutoff, "_", " ")), stageName)
		}
	}
	return nil
}

// setCancellation declares the cancellation in the contract's
// contractAttributes (ION TradeContract): reason Descriptor and the affected
// commitment IDs. Other contractAttributes fields are kept.
func setCancellation(contract map[string]interface{}, reasonCode string, commitmentIDs []string) {
	attrs := asObj(contract["contractAttributes"])
	if attrs == nil {
		attrs = map[string]interface{}{
			"@context": "https://schema.ion.id/release1/schema/extension/trade/TradeContract/v1/context.jsonld",
			"@type":    "ion:IONTradeContract",
		}
		contract["contractAttributes"] = attrs
	}
	reason := map[string]interface{}{"code": reasonCode}
	if name, ok := cancellationReasons[reasonCode]; ok {
		reason["name"] = name
	}
	attrs["cancellation"] = map[string]interface{}{"reason": reason, "commitmentIds": commitmentIDs}
}

// unwrapContract returns the contract object of a snapshot, which is stored
// either bare or wrapped as {"message":{"contract":…}}.
func unwrapContract(raw json.RawMessage) json.RawMessage {
	var env struct {
		Message struct {
			Contract json.RawMessage `json:"contract"`
		} `json:"message"`
	}
	if json.Unmarshal(raw, &env) == nil && len(env.Message.Contract) > 2 {
		return env.Message.Contract
	}
	return raw
}

func commitmentStatusCode(cm map[string]interface{}) string {
	return asStr(asObj(asObj(cm["status"])["descriptor"])["code"])
}

// commitmentQuantity reads resources[0].quantity.unitQuantity, falling back to
// commitmentAttributes.quantity.unitQuantity, then 1.
func commitmentQuantity(cm map[string]interface{}) float64 {
	if rs := asSlice(cm["resources"]); len(rs) > 0 {
		if q, _ := asObj(asObj(rs[0])["quantity"])["unitQuantity"].(float64); q > 0 {
			return q
		}
	}
	if q, _ := asObj(asObj(cm["commitmentAttributes"])["quantity"])["unitQuantity"].(float64); q > 0 {
		return q
	}
	return 1
}

func setCommitmentQuantity(cm map[string]interface{}, q float64) {
	for _, r := range asSlice(cm["resources"]) {
		if qm := asObj(asObj(r)["quantity"]); qm != nil {
			qm["unitQuantity"] = q
		}
	}
	if qm := asObj(asObj(cm["commitmentAttributes"])["quantity"]); qm != nil {
		qm["unitQuantity"] = q
	}
}

func asObj(v interface{}) map[string]interface{} { m, _ := v.(map[string]interface{}); return m }
func asSlice(v interface{}) []interface{}        { s, _ := v.([]interface{}); return s }
func asStr(v interface{}) string                 { s, _ := v.(string); return s }

// Update sends a Beckn update request with partial contract changes.
func (s *ClientService) Update(ctx context.Context, req *ClientUpdateRequest) error {
	txnID, _ := uuid.Parse(req.TransactionID)
	msgID := uuid.New()
	q := dbsqlc.New(s.pool)

	txn, err := q.GetTransaction(ctx, txnID)
	if err != nil {
		return fmt.Errorf("transaction %s not found: %w", req.TransactionID, err)
	}
	bppID, bppURI := s.resolveBPP(txn)

	msgJSON, _ := json.Marshal(map[string]interface{}{"contract": req.Contract})
	becknReq := BecknRequest{
		Context: s.newContext("update", req.TransactionID, msgID.String(), bppID, bppURI),
		Message: json.RawMessage(msgJSON),
	}
	err = q.UpsertTransaction(ctx, dbsqlc.UpsertTransactionParams{
		TransactionID: txnID,
		BapID:         s.cfg.BapID,
		NetworkID:     &s.cfg.NetworkID,
		BppID:         &bppID,
		BppUri:        &bppURI,
		Status:        dbsqlc.TransactionStatusUPDATESENT,
	})
	if err != nil {
		return err
	}
	return s.sendToBPP(ctx, s.cfg.BapCallerURL, "update", txnID, msgID, s.cfg.NetworkID, becknReq)
}

// Rate sends a Beckn rate request with the provided rating inputs.
func (s *ClientService) Rate(ctx context.Context, req *ClientRateRequest) error {
	txnID, _ := uuid.Parse(req.TransactionID)
	msgID := uuid.New()
	q := dbsqlc.New(s.pool)

	txn, err := q.GetTransaction(ctx, txnID)
	if err != nil {
		return fmt.Errorf("transaction %s not found: %w", req.TransactionID, err)
	}
	bppID, bppURI := s.resolveBPP(txn)

	// Wire shape mirrors the Beckn v2 RatingInput schema exactly
	// (additionalProperties=false, required: target, range) — no
	// ratingCategory or top-level id/descriptor; those don't exist in the
	// schema, only target.id/target.descriptor do.
	type ratingTargetWire struct {
		ID         string          `json:"id,omitempty"`
		Descriptor json.RawMessage `json:"descriptor,omitempty"`
	}
	type ratingInputWire struct {
		Target       ratingTargetWire `json:"target"`
		Range        interface{}      `json:"range"`
		FeedbackForm interface{}      `json:"feedbackFormSubmission,omitempty"`
	}
	inputs := make([]ratingInputWire, 0, len(req.RatingInputs))
	for _, ri := range req.RatingInputs {
		w := ratingInputWire{
			Target: ratingTargetWire{ID: ri.ID, Descriptor: ri.Descriptor},
			Range:  ri.Range,
		}
		if ri.FeedbackFormSubmission != nil {
			w.FeedbackForm = ri.FeedbackFormSubmission
		}
		inputs = append(inputs, w)
	}

	msgJSON, _ := json.Marshal(map[string]interface{}{"ratingInputs": inputs})
	becknReq := BecknRequest{
		Context: s.newContext("rate", req.TransactionID, msgID.String(), bppID, bppURI),
		Message: json.RawMessage(msgJSON),
	}
	err = q.UpsertTransaction(ctx, dbsqlc.UpsertTransactionParams{
		TransactionID: txnID,
		BapID:         s.cfg.BapID,
		NetworkID:     &s.cfg.NetworkID,
		BppID:         &bppID,
		BppUri:        &bppURI,
		Status:        dbsqlc.TransactionStatusRATESENT,
	})
	if err != nil {
		return err
	}
	return s.sendToBPP(ctx, s.cfg.BapCallerURL, "rate", txnID, msgID, s.cfg.NetworkID, becknReq)
}

// Support sends a Beckn support request.
func (s *ClientService) Support(ctx context.Context, req *ClientSupportRequest) error {
	txnID, _ := uuid.Parse(req.TransactionID)
	msgID := uuid.New()
	q := dbsqlc.New(s.pool)

	txn, err := q.GetTransaction(ctx, txnID)
	if err != nil {
		return fmt.Errorf("transaction %s not found: %w", req.TransactionID, err)
	}
	bppID, bppURI := s.resolveBPP(txn)

	orderID := req.OrderID
	if orderID == "" {
		orderID = req.TransactionID
	}
	msgJSON, _ := json.Marshal(map[string]interface{}{
		"support": map[string]interface{}{
			"orderId": orderID,
			"descriptor": map[string]string{
				"shortDesc": req.Description,
			},
		},
	})
	becknReq := BecknRequest{
		Context: s.newContext("support", req.TransactionID, msgID.String(), bppID, bppURI),
		Message: json.RawMessage(msgJSON),
	}
	err = q.UpsertTransaction(ctx, dbsqlc.UpsertTransactionParams{
		TransactionID: txnID,
		BapID:         s.cfg.BapID,
		NetworkID:     &s.cfg.NetworkID,
		BppID:         &bppID,
		BppUri:        &bppURI,
		Status:        dbsqlc.TransactionStatusSUPPORTSENT,
	})
	if err != nil {
		return err
	}
	return s.sendToBPP(ctx, s.cfg.BapCallerURL, "support", txnID, msgID, s.cfg.NetworkID, becknReq)
}

// resolveBPP returns the BPP ID and URI for a transaction, falling back to server config.
func (s *ClientService) resolveBPP(txn dbsqlc.GetTransactionRow) (bppID, bppURI string) {
	bppID = s.cfg.BppID
	bppURI = s.cfg.BppURI
	if txn.BppID != nil && *txn.BppID != "" {
		bppID = *txn.BppID
	}
	if txn.BppUri != nil && *txn.BppUri != "" {
		bppURI = *txn.BppUri
	}
	return
}

// GetStatus returns the latest state for a transaction.
func (s *ClientService) GetStatus(ctx context.Context, txnIDStr string) (*ClientStatusResponse, error) {
	txnID, err := uuid.Parse(txnIDStr)
	if err != nil {
		return nil, err
	}

	q := dbsqlc.New(s.pool)
	txn, err := q.GetTransaction(ctx, txnID)
	if err != nil {
		return nil, err
	}

	// Get latest snapshot if available
	snapshot, _ := q.GetLatestContractSnapshot(ctx, txnID)

	resp := &ClientStatusResponse{
		TransactionID: txnIDStr,
		Status:        string(txn.Status),
		UpdatedAt:     txn.UpdatedAt.Time,
	}

	if snapshot.ID != [16]byte{} {
		resp.Contract = snapshot.Contract
	}

	return resp, nil
}

// GetStatusForBuyer is GetStatus scoped to a specific buyer -- returns an
// error (surfaced by the handler as 404) if the transaction doesn't belong
// to buyerSubject, same as if it didn't exist, so a buyer can't view
// another buyer's order by guessing/knowing its transaction UUID.
func (s *ClientService) GetStatusForBuyer(ctx context.Context, txnIDStr, buyerSubject string) (*ClientStatusResponse, error) {
	txnID, err := uuid.Parse(txnIDStr)
	if err != nil {
		return nil, err
	}

	q := dbsqlc.New(s.pool)
	txn, err := q.GetTransactionForBuyer(ctx, dbsqlc.GetTransactionForBuyerParams{
		TransactionID: txnID,
		BuyerSubject:  &buyerSubject,
	})
	if err != nil {
		return nil, err
	}

	snapshot, _ := q.GetLatestContractSnapshot(ctx, txnID)

	resp := &ClientStatusResponse{
		TransactionID: txnIDStr,
		Status:        string(txn.Status),
		UpdatedAt:     txn.UpdatedAt.Time,
	}

	if snapshot.ID != [16]byte{} {
		resp.Contract = snapshot.Contract
	}

	return resp, nil
}

// GetCatalog returns a mock catalog (for now) or triggers a search.
func (s *ClientService) GetCatalog(ctx context.Context) (json.RawMessage, error) {
	// Simple mock food catalog for the Instamart-style UI.
	return json.RawMessage(`[
		{"id":"p1","name":"Organic Avocados","price":120,"category":"Produce","image":"https://images.unsplash.com/photo-1523049673857-eb18f1d7b578?w=400&auto=format&fit=crop&q=60"},
		{"id":"p2","name":"Oat Milk (1L)","price":250,"category":"Dairy","image":"https://images.unsplash.com/photo-1634140785135-2465e9754ce4?w=400&auto=format&fit=crop&q=60"},
		{"id":"p3","name":"Greek Yogurt","price":80,"category":"Dairy","image":"https://images.unsplash.com/photo-1488477181946-6428a0291777?w=400&auto=format&fit=crop&q=60"},
		{"id":"p4","name":"Whole Wheat Bread","price":45,"category":"Bakery","image":"https://images.unsplash.com/photo-1549931319-a545dcf3bc73?w=400&auto=format&fit=crop&q=60"},
		{"id":"p5","name":"Dark Chocolate (70%)","price":150,"category":"Snacks","image":"https://images.unsplash.com/photo-1511381939415-e44015466834?w=400&auto=format&fit=crop&q=60"}
	]`), nil
}

// --- Helpers ---

func (s *ClientService) newContext(action, txnID, msgID, bppID, bppURI string) BecknContext {
	return BecknContext{
		Version:       "2.0.0",
		Action:        action,
		Timestamp:     time.Now().UTC().Format(time.RFC3339),
		MessageID:     msgID,
		TransactionID: txnID,
		BapID:         s.cfg.BapID,
		BapURI:        s.cfg.BapURI,
		BppID:         bppID,
		BppURI:        bppURI,
		NetworkID:     s.cfg.NetworkID,
		// Matches the client timeout above (see its comment) -- advertises
		// the same real budget this call is actually given.
		TTL: "PT90S",
	}
}

// sendToBPP transmits a Beckn request to the ONIX adapter and logs both the
// outbound request and the adapter ACK response to beckn_message_log and logharbour.
func (s *ClientService) sendToBPP(ctx context.Context, callerURL, action string, txnID, msgID uuid.UUID, networkID string, req BecknRequest) error {
	targetURL := fmt.Sprintf("%s/%s", callerURL, action)
	reqJSON, _ := json.Marshal(req)

	start := time.Now()

	var (
		respBody   []byte
		callErr    error
		httpStatus int
	)

	// Deferred block: DB log + logharbour ACK/ERR entry.
	defer func() {
		dur := time.Since(start)
		ms := int32(dur.Milliseconds())
		ack := dbsqlc.NullAckStatus{AckStatus: dbsqlc.AckStatusACK, Valid: true}
		if callErr != nil {
			ack = dbsqlc.NullAckStatus{AckStatus: dbsqlc.AckStatusNACK, Valid: true}
			s.logBecknErr(action, txnID.String(), targetURL, callErr, dur)
		} else {
			s.logBecknAck(action, txnID.String(), httpStatus, respBody, dur)
		}
		q := dbsqlc.New(s.pool)
		if err := q.InsertMessageLog(ctx, dbsqlc.InsertMessageLogParams{
			TransactionID:        pgtype.UUID{Bytes: txnID, Valid: true},
			MessageID:            msgID,
			Action:               dbsqlc.BecknAction(action),
			Direction:            dbsqlc.MessageDirectionOUTBOUND,
			Url:                  strPtr(targetURL),
			BapID:                strPtr(s.cfg.BapID),
			BapUri:               strPtr(s.cfg.BapURI),
			BppID:                strPtr(req.Context.BppID),
			BppUri:               strPtr(req.Context.BppURI),
			NetworkID:            &networkID,
			AckStatus:            ack,
			RequestPayload:       reqJSON,
			ResponsePayload:      json.RawMessage(respBody),
			ErrorMessage:         errStrPtr(callErr),
			ProcessingDurationMs: &ms,
		}); err != nil {
			s.lh.WithModule("beckn_client").Warn().Error(err).Log("failed to log outbound message")
		}
	}()

	s.logBecknOut(action, http.MethodPost, targetURL, txnID.String(), msgID.String(), reqJSON)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, bytes.NewBuffer(reqJSON))
	if err != nil {
		callErr = err
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(httpReq)
	if err != nil {
		callErr = err
		return err
	}
	defer resp.Body.Close() //nolint:errcheck

	httpStatus = resp.StatusCode
	respBody, _ = io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		callErr = fmt.Errorf("adapter returned status %d: %s", resp.StatusCode, string(respBody))
		return callErr
	}

	return nil
}

// --- Beckn call structured logging helpers ---

// becknOutActivity is the structured payload recorded before an outbound Beckn call.
type becknOutActivity struct {
	Action  string          `json:"action"`
	Method  string          `json:"method"`
	URL     string          `json:"url"`
	TxnID   string          `json:"txn_id"`
	MsgID   string          `json:"msg_id"`
	Request json.RawMessage `json:"request"`
}

// becknAckActivity is the structured payload recorded after a successful Beckn call.
type becknAckActivity struct {
	Action   string          `json:"action"`
	TxnID    string          `json:"txn_id"`
	HTTPCode int             `json:"http_code"`
	DurMs    int64           `json:"dur_ms"`
	Response json.RawMessage `json:"response"`
}

// becknErrActivity is the structured payload recorded when a Beckn call fails.
type becknErrActivity struct {
	Action string `json:"action"`
	TxnID  string `json:"txn_id"`
	URL    string `json:"url"`
	DurMs  int64  `json:"dur_ms"`
	Error  string `json:"error"`
}

// logBecknOut prints the outbound Beckn request as pretty JSON to stdout.
func (s *ClientService) logBecknOut(action, method, url, txnID, msgID string, body []byte) {
	entry := becknOutActivity{
		Action:  action,
		Method:  method,
		URL:     url,
		TxnID:   txnID,
		MsgID:   msgID,
		Request: json.RawMessage(body),
	}
	prettyLog(fmt.Sprintf("→ OUT  %-8s | txn=%.8s… | %s %s", strings.ToUpper(action), txnID, method, url), entry)
}

// logBecknAck prints the Beckn adapter ACK response as pretty JSON to stdout.
func (s *ClientService) logBecknAck(action, txnID string, status int, body []byte, dur time.Duration) {
	entry := becknAckActivity{
		Action:   action,
		TxnID:    txnID,
		HTTPCode: status,
		DurMs:    dur.Milliseconds(),
		Response: json.RawMessage(body),
	}
	prettyLog(fmt.Sprintf("← ACK  %-8s | txn=%.8s… | http=%d | dur=%dms", strings.ToUpper(action), txnID, status, dur.Milliseconds()), entry)
}

// logBecknErr prints the Beckn call failure as pretty JSON to stdout.
func (s *ClientService) logBecknErr(action, txnID, url string, callErr error, dur time.Duration) {
	entry := becknErrActivity{
		Action: action,
		TxnID:  txnID,
		URL:    url,
		DurMs:  dur.Milliseconds(),
		Error:  callErr.Error(),
	}
	prettyLog(fmt.Sprintf("← ERR  %-8s | txn=%.8s… | dur=%dms | %s", strings.ToUpper(action), txnID, dur.Milliseconds(), callErr.Error()), entry)
}

// prettyLog prints a labelled pretty-JSON block to stdout via the standard log package.
func prettyLog(label string, v interface{}) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		log.Printf("[beckn] %s\n(marshal error: %v)", label, err)
		return
	}
	log.Printf("[beckn] %s\n%s", label, string(b))
}

// --- Generic helpers ---

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func errStrPtr(err error) *string {
	if err == nil {
		return nil
	}
	s := err.Error()
	return &s
}
