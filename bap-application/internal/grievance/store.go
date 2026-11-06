package grievance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store keeps grievances in the tables both apps create (bap migration 017,
// bpp migration 039). grievance_events is the record: every event received
// or sent, each IONGrievance in full. grievances, grievance_resolutions and
// grievance_executions are rebuilt from the events that count -- received,
// or sent and not refused -- so a send the network refuses (NACK, error)
// drops out of the state and leaves it as it was.
type Store struct{ Pool *pgxpool.Pool }

// Delivery of an event.
const (
	// DeliverySending is stored before the POST and already counts, so a reply that
	// arrives before the POST returns finds it.
	DeliverySending = "SENDING"
	// DeliveryAck is received and accepted, or sent and ACKed.
	DeliveryAck = "ACK"
	// DeliveryNack is sent and refused; it does not count.
	DeliveryNack = "NACK"
	// DeliveryFailed is sent with no answer; it does not count.
	DeliveryFailed = "FAILED"
)

// ErrNacked wraps a send the receiving side answered with NACK.
var ErrNacked = errors.New("NACK")

// ErrNotFound is a grievance that does not exist.
var ErrNotFound = errors.New("grievance not found")

// Header is what a grievance is attached to: the order and its participants.
type Header struct {
	TransactionID uuid.UUID
	OrderID       string
	BapID         string
	BapURI        string
	BppID         string
	BppURI        string
	NetworkID     string
	// Extra are app-specific grievances columns set when the grievance is
	// created (buyer_subject on the BAP, contract_id and provider_id on the
	// BPP), by column name.
	Extra map[string]any
}

// Record is a grievances row.
type Record struct {
	Header
	GrievanceID     string
	OriginatingNpID string
	OriginatingSide string
	Category        string
	Description     string
	Affected        []AffectedLine
	Related         []RelatedReference
	PolicyRef       string
	CaseStatus      string
	LastEventType   string
	LastEventAt     time.Time
	OpenedAt        time.Time
	AcknowledgedAt  *time.Time
	ResolvedAt      *time.Time
	ClosedAt        *time.Time
	Channels        json.RawMessage
	SeenAt          *time.Time
	UpdatedAt       time.Time
}

// EventRecord is a grievance_events row.
type EventRecord struct {
	ID         uuid.UUID
	MessageID  uuid.UUID
	Action     string
	Direction  string
	EventType  string
	Delivery   string
	Error      string
	Event      *Grievance
	OccurredAt time.Time
	CreatedAt  time.Time
}

var recordColumns = []string{
	"grievance_id", "transaction_id", "order_id", "bap_id", "bap_uri", "bpp_id", "bpp_uri", "network_id",
	"originating_np_id", "originating_side", "category", "description", "affected", "related_references",
	"policy_ref", "case_status", "last_event_type", "last_event_at", "opened_at", "acknowledged_at",
	"resolved_at", "closed_at", "channels", "seen_at", "updated_at",
}

// Columns lists the grievances columns ScanRecord reads, each prefixed
// with prefix (e.g. "g."), for apps that query the table themselves.
func Columns(prefix string) string {
	cols := make([]string, len(recordColumns))
	for i, c := range recordColumns {
		cols[i] = prefix + c
	}
	return strings.Join(cols, ", ")
}

// ScanRecord reads a row selected with Columns. extra receives any columns
// the query selects after them.
func ScanRecord(row pgx.Row, extra ...any) (*Record, error) {
	r := &Record{}
	var affected, related []byte
	var network *string
	dest := []any{&r.GrievanceID, &r.TransactionID, &r.OrderID, &r.BapID, &r.BapURI, &r.BppID, &r.BppURI,
		&network, &r.OriginatingNpID, &r.OriginatingSide, &r.Category, &r.Description,
		&affected, &related, &r.PolicyRef, &r.CaseStatus, &r.LastEventType, &r.LastEventAt,
		&r.OpenedAt, &r.AcknowledgedAt, &r.ResolvedAt, &r.ClosedAt, &r.Channels, &r.SeenAt, &r.UpdatedAt}
	if err := row.Scan(append(dest, extra...)...); err != nil {
		return nil, err
	}
	if network != nil {
		r.NetworkID = *network
	}
	_ = json.Unmarshal(affected, &r.Affected)
	_ = json.Unmarshal(related, &r.Related)
	return r, nil
}

// Get returns a grievance and its events, oldest first.
func (s *Store) Get(ctx context.Context, id string) (*Record, []EventRecord, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	return load(ctx, tx, id, false)
}

// Replay returns the state the events that count add up to (nil if none).
func Replay(events []EventRecord) *State {
	var counted []Event
	for _, e := range events {
		if e.Delivery == DeliverySending || e.Delivery == DeliveryAck {
			counted = append(counted, Event{Side: SideOf(e.Action), Grievance: e.Event})
		}
	}
	if len(counted) == 0 {
		return nil
	}
	return Fold(counted)
}

// Receive stores an event the counterparty sent, after checking it against
// the grievance's state. A *RuleError means it must be refused (NACK).
// Receiving the same message again is a no-op. channels are the contact
// channels the message carried, if any.
func (s *Store) Receive(ctx context.Context, h Header, from Sender, msgID uuid.UUID, g *Grievance, channels []json.RawMessage, now time.Time) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	rec, events, err := load(ctx, tx, g.GrievanceID, true)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	for _, e := range events {
		if e.MessageID == msgID && e.Direction == "INBOUND" && e.EventType == g.EventType {
			return nil
		}
	}
	if rec != nil && (rec.TransactionID != h.TransactionID || rec.BapID != h.BapID || rec.BppID != h.BppID) {
		return &RuleError{Msg: "grievance " + g.GrievanceID + " belongs to another order"}
	}
	if err := Check(g, Replay(events), from, now); err != nil {
		return err
	}
	if rec == nil {
		if err := insertRecord(ctx, tx, h, g); err != nil {
			return err
		}
	}
	if err := insertEvent(ctx, tx, g, msgID, from.Endpoint, "INBOUND", DeliveryAck); err != nil {
		return err
	}
	if len(channels) > 0 {
		b, _ := json.Marshal(channels)
		if _, err := tx.Exec(ctx, `UPDATE grievances SET channels = $2 WHERE grievance_id = $1`, g.GrievanceID, b); err != nil {
			return err
		}
	}
	if err := rebuild(ctx, tx, g.GrievanceID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Send checks event g against the grievance's state, stores it as DeliverySending,
// calls post to deliver it, and records the outcome. A *RuleError means g
// breaks the rules and was not sent. When post fails the event drops out of
// the state and the error is returned (wrapping ErrNacked for a NACK).
func (s *Store) Send(ctx context.Context, h Header, from Sender, msgID uuid.UUID, g *Grievance, now time.Time, post func(context.Context) error) error {
	eventID, err := s.prepare(ctx, h, from, msgID, g, now)
	if err != nil {
		return err
	}
	sendErr := post(ctx)
	delivery, errText := DeliveryAck, ""
	if sendErr != nil {
		delivery, errText = DeliveryFailed, sendErr.Error()
		if errors.Is(sendErr, ErrNacked) {
			delivery = DeliveryNack
		}
	}
	// The outcome is recorded even if the caller's context is gone.
	bg := context.WithoutCancel(ctx)
	tx, err := s.Pool.Begin(bg)
	if err != nil {
		return errors.Join(sendErr, err)
	}
	defer tx.Rollback(bg) //nolint:errcheck
	if _, err := tx.Exec(bg, `SELECT 1 FROM grievances WHERE grievance_id = $1 FOR UPDATE`, g.GrievanceID); err != nil {
		return errors.Join(sendErr, err)
	}
	if _, err := tx.Exec(bg, `UPDATE grievance_events SET delivery = $2, error = NULLIF($3, '') WHERE id = $1`,
		eventID, delivery, errText); err != nil {
		return errors.Join(sendErr, err)
	}
	if err := rebuild(bg, tx, g.GrievanceID); err != nil {
		return errors.Join(sendErr, err)
	}
	if err := tx.Commit(bg); err != nil {
		return errors.Join(sendErr, err)
	}
	return sendErr
}

func (s *Store) prepare(ctx context.Context, h Header, from Sender, msgID uuid.UUID, g *Grievance, now time.Time) (uuid.UUID, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	rec, events, err := load(ctx, tx, g.GrievanceID, true)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return uuid.Nil, err
	}
	if err := Check(g, Replay(events), from, now); err != nil {
		return uuid.Nil, err
	}
	if rec == nil {
		if err := insertRecord(ctx, tx, h, g); err != nil {
			return uuid.Nil, err
		}
	}
	id := uuid.New()
	if err := insertEventID(ctx, tx, id, g, msgID, from.Endpoint, "OUTBOUND", DeliverySending); err != nil {
		return uuid.Nil, err
	}
	if err := rebuild(ctx, tx, g.GrievanceID); err != nil {
		return uuid.Nil, err
	}
	return id, tx.Commit(ctx)
}

// MarkSeen records that the user opened the grievance (clears "unread").
func (s *Store) MarkSeen(ctx context.Context, id string) error {
	_, err := s.Pool.Exec(ctx, `UPDATE grievances SET seen_at = NOW() WHERE grievance_id = $1`, id)
	return err
}

// OpeningMessageID is the messageId of the OPENED event: the direct
// callback of a /support OPENED (ACKNOWLEDGED) repeats it.
func OpeningMessageID(events []EventRecord) uuid.UUID {
	for _, e := range events {
		if e.EventType == Opened {
			return e.MessageID
		}
	}
	return uuid.Nil
}

func load(ctx context.Context, tx pgx.Tx, id string, lock bool) (*Record, []EventRecord, error) {
	q := `SELECT ` + Columns("") + ` FROM grievances WHERE grievance_id = $1`
	if lock {
		q += ` FOR UPDATE`
	}
	rec, err := ScanRecord(tx.QueryRow(ctx, q, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, ErrNotFound
	}
	if err != nil {
		return nil, nil, err
	}
	rows, err := tx.Query(ctx, `
		SELECT id, message_id, action, direction, event_type, delivery, COALESCE(error, ''), attributes, occurred_at, created_at
		FROM grievance_events WHERE grievance_id = $1 ORDER BY seq`, id)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var events []EventRecord
	for rows.Next() {
		var e EventRecord
		var attrs []byte
		if err := rows.Scan(&e.ID, &e.MessageID, &e.Action, &e.Direction, &e.EventType, &e.Delivery, &e.Error,
			&attrs, &e.OccurredAt, &e.CreatedAt); err != nil {
			return nil, nil, err
		}
		e.Event = &Grievance{}
		if err := json.Unmarshal(attrs, e.Event); err != nil {
			return nil, nil, fmt.Errorf("event %s: %w", e.ID, err)
		}
		events = append(events, e)
	}
	return rec, events, rows.Err()
}

func insertRecord(ctx context.Context, tx pgx.Tx, h Header, g *Grievance) error {
	opened, _ := ParseTime(g.OpenedAt)
	cols := []string{"grievance_id", "transaction_id", "order_id", "bap_id", "bap_uri", "bpp_id", "bpp_uri", "network_id",
		"originating_np_id", "originating_side", "category", "description", "policy_ref", "case_status",
		"last_event_type", "last_event_at", "opened_at"}
	var network *string
	if h.NetworkID != "" {
		network = &h.NetworkID
	}
	args := []any{g.GrievanceID, h.TransactionID, h.OrderID, h.BapID, h.BapURI, h.BppID, h.BppURI, network,
		g.OriginatingNpID, sideOf(g), g.Category, g.Description, policyOf(g), g.CaseStatus, g.EventType, opened, opened}
	extra := make([]string, 0, len(h.Extra))
	for c := range h.Extra {
		extra = append(extra, c)
	}
	sort.Strings(extra)
	for _, c := range extra {
		cols = append(cols, c)
		args = append(args, h.Extra[c])
	}
	params := make([]string, len(args))
	for i := range args {
		params[i] = fmt.Sprintf("$%d", i+1)
	}
	_, err := tx.Exec(ctx, `INSERT INTO grievances (`+strings.Join(cols, ", ")+`) VALUES (`+strings.Join(params, ", ")+`)`, args...)
	return err
}

func insertEvent(ctx context.Context, tx pgx.Tx, g *Grievance, msgID uuid.UUID, action, direction, delivery string) error {
	return insertEventID(ctx, tx, uuid.New(), g, msgID, action, direction, delivery)
}

func insertEventID(ctx context.Context, tx pgx.Tx, id uuid.UUID, g *Grievance, msgID uuid.UUID, action, direction, delivery string) error {
	attrs, err := json.Marshal(g)
	if err != nil {
		return err
	}
	occurred, err := ParseTime(g.OccurredAt)
	if err != nil {
		occurred = time.Now()
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO grievance_events (id, grievance_id, message_id, action, direction, event_type, delivery, attributes, occurred_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		id, g.GrievanceID, msgID, action, direction, g.EventType, delivery, attrs, occurred)
	return err
}

// rebuild recomputes the grievance's row, versions and executions from its
// events; with no event counting any more (a refused OPENED) the grievance
// is removed.
func rebuild(ctx context.Context, tx pgx.Tx, id string) error {
	_, events, err := load(ctx, tx, id, false)
	if err != nil {
		return err
	}
	st := Replay(events)
	if st == nil {
		_, err := tx.Exec(ctx, `DELETE FROM grievances WHERE grievance_id = $1`, id)
		return err
	}
	var opening *Grievance
	for _, e := range events {
		if e.EventType == Opened && (e.Delivery == DeliverySending || e.Delivery == DeliveryAck) {
			opening = e.Event
		}
	}
	affected, _ := json.Marshal(nonNil(opening.Affected))
	related, _ := json.Marshal(nonNilRefs(st.Related))
	if _, err := tx.Exec(ctx, `
		UPDATE grievances SET category = $2, description = $3, affected = $4, related_references = $5,
			policy_ref = $6, case_status = $7, last_event_type = $8, last_event_at = $9,
			acknowledged_at = $10, resolved_at = $11, closed_at = $12, updated_at = NOW()
		WHERE grievance_id = $1`,
		id, opening.Category, opening.Description, affected, related, st.PolicyRef, st.CaseStatus,
		st.LastEvent, st.LastEventAt, nullTime(st.AcknowledgedAt), nullTime(st.ResolvedAt), nullTime(st.ClosedAt)); err != nil {
		return err
	}

	if _, err := tx.Exec(ctx, `DELETE FROM grievance_resolutions WHERE grievance_id = $1`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM grievance_executions WHERE grievance_id = $1`, id); err != nil {
		return err
	}
	for _, r := range st.Versions {
		body, _ := json.Marshal(r)
		var d *Decision
		for i := range st.Decisions {
			if st.Decisions[i].ProposalID == r.ProposalID && st.Decisions[i].Version == r.Version {
				d = &st.Decisions[i]
			}
		}
		var result, by, reason *string
		var at *time.Time
		if d != nil {
			result, by, reason = &d.Result, &d.DecidedByNpID, &d.Reason
			if t, err := ParseTime(d.DecidedAt); err == nil {
				at = &t
			}
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO grievance_resolutions (grievance_id, proposal_id, version, proposed_by_np_id,
				authorization_type, resolution, decision_result, decided_by_np_id, decided_at, decision_reason)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, NULLIF($10, ''))`,
			id, r.ProposalID, r.Version, r.ProposedByNpID, r.AuthorizationType, body, result, by, at, reason); err != nil {
			return err
		}
	}
	if st.Resolution != nil {
		for _, e := range st.Executions {
			at, _ := ParseTime(e.CompletedAt)
			if _, err := tx.Exec(ctx, `
				INSERT INTO grievance_executions (grievance_id, proposal_id, version, action_id,
					executed_by_np_id, execution_reference, completed_at)
				VALUES ($1, $2, $3, $4, $5, NULLIF($6, ''), $7)`,
				id, st.Resolution.ProposalID, st.Resolution.Version, e.ActionID, e.ExecutedByNpID, e.ExecutionReference, at); err != nil {
				return err
			}
		}
	}
	return nil
}

func sideOf(g *Grievance) string {
	if g.RaisedBy != nil {
		return g.RaisedBy.Side
	}
	return ""
}

func policyOf(g *Grievance) string {
	if g.PolicyRef != "" {
		return g.PolicyRef
	}
	return DefaultPolicyRef
}

func nullTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

func nonNil(a []AffectedLine) []AffectedLine {
	if a == nil {
		return []AffectedLine{}
	}
	return a
}

func nonNilRefs(a []RelatedReference) []RelatedReference {
	if a == nil {
		return []RelatedReference{}
	}
	return a
}
