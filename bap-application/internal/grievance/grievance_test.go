package grievance

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// The pack's schema.json and examples are copied from ion-specs
// (releases/release1/schema/common/Grievance/v1 and
// flows/trade/patterns/storefront/v1/examples) into testdata.

const (
	bap = "buyerapp.ion.id"
	bpp = "sellerapp.ion.id"
)

var (
	viaSupport   = Sender{Endpoint: EndpointSupport, BapID: bap, BppID: bpp}
	viaOnSupport = Sender{Endpoint: EndpointOnSupport, BapID: bap, BppID: bpp}
	t0           = time.Date(2026, 4, 19, 11, 0, 0, 0, wib)
)

// schema compiles testdata/schema.json. Its one external reference,
// IONPayment.currency, is the const IDR in the Payment pack.
func schema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	c := jsonschema.NewCompiler()
	payment := map[string]any{"components": map[string]any{"schemas": map[string]any{"IONPayment": map[string]any{
		"properties": map[string]any{"currency": map[string]any{"type": "string", "const": "IDR"}},
	}}}}
	if err := c.AddResource("https://schema.ion.id/release1/schema/common/Payment/v1/attributes.yaml", payment); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("testdata/schema.json")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if err := c.AddResource("grievance.json", doc); err != nil {
		t.Fatal(err)
	}
	s, err := c.Compile("grievance.json")
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func validate(t *testing.T, s *jsonschema.Schema, name string, v any) {
	t.Helper()
	b, _ := json.Marshal(v)
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Validate(doc); err != nil {
		t.Errorf("%s does not validate against Grievance/v1/schema.json: %v", name, err)
	}
}

type message struct {
	Context struct {
		Action    string `json:"action"`
		BapID     string `json:"bapId"`
		BppID     string `json:"bppId"`
		MessageID string `json:"messageId"`
	} `json:"context"`
	Message struct {
		Support Support `json:"support"`
	} `json:"message"`
}

func readMessage(t *testing.T, name string) message {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata/storefront", name))
	if err != nil {
		t.Fatal(err)
	}
	var m message
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// TestStorefrontFlow consumes the six Storefront grievance messages in
// order: each passes the rules on the state the earlier ones left, and the
// grievance ends RESOLVED with the refund executed.
func TestStorefrontFlow(t *testing.T) {
	s := schema(t)
	var st *State
	for _, name := range []string{
		"support_OPENED.json", "on_support_ACKNOWLEDGED.json", "on_support_RESOLUTION_PROPOSED.json",
		"support_RESOLUTION_DECIDED.json", "on_support_ACTION_COMPLETED.json", "on_support_RESOLVED.json",
	} {
		m := readMessage(t, name)
		g, ok, err := m.Message.Support.Parse()
		if err != nil || !ok {
			t.Fatalf("%s: no IONGrievance: %v", name, err)
		}
		validate(t, s, name, g)
		from := Sender{Endpoint: m.Context.Action, BapID: m.Context.BapID, BppID: m.Context.BppID}
		if err := Check(g, st, from, t0.Add(4*time.Hour)); err != nil {
			t.Fatalf("%s rejected: %v", name, err)
		}
		st = Apply(st, g, from.Side())
	}
	if st.CaseStatus != StatusResolved || st.Executions["ACT-001"].ExecutionReference != "RFD-QRIS-20260419-0044" {
		t.Fatalf("unexpected end state: %+v", st)
	}
	if st.DeemedClosed(t0.Add(24*time.Hour)) || !st.DeemedClosed(t0.Add(9*24*time.Hour)) {
		t.Error("RESOLVED is deemed closed after the 7-day objection period, not before")
	}
}

// TestPackExamplesValidate checks the pack's own examples against the schema.
func TestPackExamplesValidate(t *testing.T) {
	s := schema(t)
	files, _ := filepath.Glob("testdata/pack/*.json")
	for _, f := range files {
		raw, _ := os.ReadFile(f)
		var g Grievance
		if err := json.Unmarshal(raw, &g); err != nil {
			t.Fatal(err)
		}
		validate(t, s, filepath.Base(f), &g)
		if err := checkShape(&g); err != nil {
			t.Errorf("%s fails the per-event checks: %v", filepath.Base(f), err)
		}
	}
}

// flow runs events through Check and Apply, validating each against the schema.
type flow struct {
	t  *testing.T
	s  *jsonschema.Schema
	st *State
	at time.Time
}

func newFlow(t *testing.T) *flow { return &flow{t: t, s: schema(t), at: t0} }

func (f *flow) send(from Sender, g *Grievance) {
	f.t.Helper()
	validate(f.t, f.s, g.EventType, g)
	if err := Check(g, f.st, from, f.at); err != nil {
		f.t.Fatalf("%s rejected: %v", g.EventType, err)
	}
	f.st = Apply(f.st, g, from.Side())
	f.at = f.at.Add(time.Minute)
}

func (f *flow) reject(from Sender, g *Grievance, want string) {
	f.t.Helper()
	err := Check(g, f.st, from, f.at)
	if err == nil || !strings.Contains(err.Error(), want) {
		f.t.Fatalf("%s: want an error containing %q, got %v", g.EventType, want, err)
	}
}

func refund(id string, amount float64, basis, liable string) Action {
	return Action{
		ActionID: id, Type: "REFUND", Description: "Refund", ExecutionMethod: "SETTLEMENT",
		Affected:        []AffectedLine{{CommitmentID: "L01", Quantity: 1}},
		FinancialEffect: &FinancialEffect{EffectType: "REFUND", Amount: amount, Currency: Currency, AllocationBasis: basis, LiablePartyID: liable},
	}
}

func completed(st *State, actionID, ref string, by string, now time.Time) *Grievance {
	g := st.Next(ActionCompleted, now)
	g.Execution = &Execution{ActionExecutions: []ActionExecution{{
		ActionID: actionID, Status: "COMPLETED", CompletedAt: FormatTime(now), ExecutedByNpID: by, ExecutionReference: ref,
	}}}
	return g
}

// TestBuyerGrievanceWithRejectedVersion builds the flow with the builders the
// apps use: version 1 rejected, version 2 accepted, executed and resolved.
func TestBuyerGrievanceWithRejectedVersion(t *testing.T) {
	f := newFlow(t)
	f.send(viaSupport, Open(Opening{
		GrievanceID: "GRV-1", Side: SideBuyer, NpID: bap, AssignedNpID: bpp,
		Against: Subject{Role: "SELLER", PartyID: "PROV-1"}, Category: DamagedItem,
		Description: "Pizza arrived crushed", Affected: []AffectedLine{{CommitmentID: "L01", Quantity: 1}},
	}, f.at))
	f.send(viaOnSupport, f.st.Next(Acknowledged, f.at))
	f.send(viaOnSupport, f.st.Propose(bpp, ExplicitAgreement, []Action{refund("ACT-001", 20000, "EXPLICIT", bpp)}, f.at))
	f.send(viaSupport, f.st.Decide(bap, false, "Too little", f.at))

	// A decision on version 1 again is a decision on a decided version.
	f.reject(viaSupport, f.st.Decide(bap, true, "", f.at), "already decided")

	f.send(viaOnSupport, f.st.Propose(bpp, ExplicitAgreement, []Action{refund("ACT-001", 30000, "EXPLICIT", bpp)}, f.at))
	if f.st.Resolution.Version != 2 {
		t.Fatalf("want version 2, got %d", f.st.Resolution.Version)
	}

	// Rule 4: a decision naming version 1 is stale.
	stale := f.st.Decide(bap, true, "", f.at)
	stale.Decision.Version = 1
	f.reject(viaSupport, stale, "latest version")

	// Execution before acceptance, RESOLVED before acceptance.
	f.reject(viaOnSupport, completed(f.st, "ACT-001", "RFD-1", bpp, f.at), "not accepted")

	f.send(viaSupport, f.st.Decide(bap, true, "", f.at))

	// Rule 7: unknown action. Rule 8: RESOLVED without a reference.
	f.reject(viaOnSupport, completed(f.st, "ACT-404", "RFD-1", bpp, f.at), "unknown action")
	f.reject(viaOnSupport, f.st.Next(Resolved, f.at), "without an executionReference")

	f.send(viaOnSupport, completed(f.st, "ACT-001", "RFD-1", bpp, f.at))
	f.send(viaOnSupport, f.st.Next(Resolved, f.at))

	// Rule 10: only the originator objects or closes.
	obj := f.st.Next(Objection, f.at)
	obj.Explanation = "No refund yet"
	f.reject(viaOnSupport, obj, "only the originating participant")
	f.reject(viaOnSupport, f.st.Next(Closed, f.at), "only the originating participant")
	f.send(viaSupport, obj)
	if f.st.CaseStatus != StatusOpen || !f.st.ResolvedAt.IsZero() {
		t.Fatalf("an objection reopens the grievance: %+v", f.st)
	}
}

// TestProposerCannotDecide is rule 5.
func TestProposerCannotDecide(t *testing.T) {
	f := newFlow(t)
	f.send(viaOnSupport, Open(Opening{
		GrievanceID: "GRV-2", Side: SideSeller, NpID: bpp, AssignedNpID: bap,
		Against: Subject{Role: "BUYER"}, Category: WrongReturn, Description: "Wrong item returned",
		Related: []RelatedReference{{RefType: "PERFORMANCE", RefID: "return-1"}},
	}, f.at))
	f.send(viaSupport, f.st.Next(Acknowledged, f.at))
	f.send(viaSupport, f.st.Propose(bap, ExplicitAgreement, []Action{{
		ActionID: "ACT-001", Type: "INFORMATION_PROVIDED", Description: "The item returned is the one delivered", ExecutionMethod: MethodNone,
	}}, f.at))
	f.reject(viaSupport, f.st.Decide(bap, true, "", f.at), "own proposal")
	f.send(viaOnSupport, f.st.Decide(bpp, true, "", f.at))
	// A NONE action needs no execution reference.
	f.send(viaSupport, f.st.Next(Resolved, f.at))
	f.send(viaOnSupport, f.st.Next(Closed, f.at))
	f.reject(viaOnSupport, f.st.Next(Acknowledged, f.at), "closed")
}

// TestAllocationRules is rule 6.
func TestAllocationRules(t *testing.T) {
	f := newFlow(t)
	f.send(viaSupport, Open(Opening{
		GrievanceID: "GRV-3", Side: SideBuyer, NpID: bap, AssignedNpID: bpp,
		Against: Subject{Role: "SELLER"}, Category: LateDelivery, Description: "Two hours late",
	}, f.at))
	f.reject(viaOnSupport, f.st.Propose(bpp, ExplicitAgreement, []Action{refund("A", 1, "POLICY", bpp)}, f.at), "does not match")
	f.reject(viaOnSupport, f.st.Propose(bpp, Unilateral, []Action{refund("A", 1, "UNILATERAL", bap)}, f.at), "never the counterparty")
	// A BAP goodwill refund at its own cost is fine.
	f.send(viaSupport, f.st.Propose(bap, Unilateral, []Action{refund("A", 15000, "UNILATERAL", bap)}, f.at))
	f.send(viaSupport, completed(f.st, "A", "GW-1", bap, f.at))
	f.send(viaSupport, f.st.Next(Resolved, f.at))
}

// TestLinkedCategories: return/refund categories must link, and a refund on
// a linked settlement states settlementRef (rule 9).
func TestLinkedCategories(t *testing.T) {
	f := newFlow(t)
	open := Opening{
		GrievanceID: "GRV-4", Side: SideBuyer, NpID: bap, AssignedNpID: bpp,
		Against: Subject{Role: "SELLER"}, Category: RefundNotReceived, Description: "Refund never arrived",
	}
	f.reject(viaSupport, Open(open, f.at), "must link")
	open.Related = []RelatedReference{{RefType: "PERFORMANCE", RefID: "return-1"}, {RefType: "SETTLEMENT", RefID: "refund-1"}}
	f.send(viaSupport, Open(open, f.at))

	policy := func(settlementRef string) *Grievance {
		a := refund("ACT-001", 89000, "POLICY", bpp)
		a.FinancialEffect.PolicyRef = "ion://policy/return.standard"
		a.FinancialEffect.SettlementRef = settlementRef
		g := f.st.Propose(bpp, PolicyAuthorized, []Action{a}, f.at)
		g.Resolution.PolicyRef = "ion://policy/return.standard"
		g.Resolution.Verification = &Verification{VerifiedByNpID: bpp, VerificationReference: "return-1", VerifiedAt: FormatTime(f.at), Method: "QUALITY_CHECK_OF_RETURNED_ITEM"}
		return g
	}
	f.reject(viaOnSupport, policy(""), "needs settlementRef")
	f.send(viaOnSupport, policy("refund-1"))
	// POLICY_AUTHORIZED needs no decision before execution.
	f.send(viaOnSupport, completed(f.st, "ACT-001", "RFD-2", bpp, f.at))
	f.send(viaOnSupport, f.st.Next(Resolved, f.at))
}

func TestObjectionPeriod(t *testing.T) {
	f := newFlow(t)
	f.send(viaSupport, Open(Opening{
		GrievanceID: "GRV-5", Side: SideBuyer, NpID: bap, AssignedNpID: bpp,
		Against: Subject{Role: "SELLER"}, Category: Other, Description: "Other",
	}, f.at))
	f.send(viaOnSupport, f.st.Propose(bpp, ExplicitAgreement, []Action{{ActionID: "A", Type: "INFORMATION_PROVIDED", Description: "Explained", ExecutionMethod: MethodNone}}, f.at))
	f.send(viaSupport, f.st.Decide(bap, true, "", f.at))
	f.send(viaOnSupport, f.st.Next(Resolved, f.at))
	f.at = f.at.Add(8 * 24 * time.Hour)
	obj := f.st.Next(Objection, f.at)
	obj.Explanation = "Late"
	f.reject(viaSupport, obj, "objection period")
}

func TestNextStep(t *testing.T) {
	f := newFlow(t)
	step := func(want Step) {
		t.Helper()
		if got := f.st.NextStep(f.at); got != want {
			t.Fatalf("after %s: want %+v, got %+v", f.st.LastEvent, want, got)
		}
	}
	f.send(viaSupport, Open(Opening{
		GrievanceID: "GRV-6", Side: SideBuyer, NpID: bap, AssignedNpID: bpp,
		Against: Subject{Role: "SELLER"}, Category: MissingItem, Description: "One item missing",
	}, f.at))
	step(Step{StepAcknowledge, SideBPP})
	f.send(viaOnSupport, f.st.Next(Acknowledged, f.at))
	step(Step{StepPropose, SideBPP})
	f.send(viaOnSupport, f.st.Propose(bpp, ExplicitAgreement, []Action{refund("A", 5000, "EXPLICIT", bpp)}, f.at))
	step(Step{StepDecide, SideBAP})
	f.send(viaSupport, f.st.Decide(bap, false, "", f.at))
	step(Step{StepPropose, SideBPP})
	f.send(viaOnSupport, f.st.Propose(bpp, ExplicitAgreement, []Action{refund("A", 9000, "EXPLICIT", bpp)}, f.at))
	f.send(viaSupport, f.st.Decide(bap, true, "", f.at))
	step(Step{StepExecute, SideBPP})
	f.send(viaOnSupport, completed(f.st, "A", "RFD-9", bpp, f.at))
	step(Step{StepResolve, SideBPP})
	f.send(viaOnSupport, f.st.Next(Resolved, f.at))
	step(Step{StepConfirm, SideBAP})
	obj := f.st.Next(Objection, f.at)
	obj.Explanation = "Still missing"
	f.send(viaSupport, obj)
	step(Step{StepPropose, SideBPP})
}

// TestOneParticipantBothSides: when one participant is both the order's BAP
// and its BPP (bapId == bppId, as in the local stack), the sides are told
// apart by endpoint, and the same rules hold.
func TestOneParticipantBothSides(t *testing.T) {
	const np = "both.ion.id"
	viaBAP := Sender{Endpoint: EndpointSupport, BapID: np, BppID: np}
	viaBPP := Sender{Endpoint: EndpointOnSupport, BapID: np, BppID: np}
	f := newFlow(t)
	f.send(viaBAP, Open(Opening{
		GrievanceID: "GRV-7", Side: SideBuyer, NpID: np, AssignedNpID: np,
		Against: Subject{Role: "SELLER"}, Category: DamagedItem, Description: "Crushed",
	}, f.at))
	// The BAP side cannot acknowledge its own grievance; the BPP side can.
	f.reject(viaBAP, f.st.Next(Acknowledged, f.at), "only the responding participant")
	f.send(viaBPP, f.st.Next(Acknowledged, f.at))
	f.send(viaBPP, f.st.Propose(np, ExplicitAgreement, []Action{refund("A", 1000, "EXPLICIT", np)}, f.at))
	f.reject(viaBPP, f.st.Decide(np, true, "", f.at), "own proposal")
	f.send(viaBAP, f.st.Decide(np, true, "", f.at))
	f.send(viaBPP, completed(f.st, "A", "R-1", np, f.at))
	f.send(viaBPP, f.st.Next(Resolved, f.at))
	f.reject(viaBPP, f.st.Next(Closed, f.at), "only the originating participant")
	f.send(viaBAP, f.st.Next(Closed, f.at))
}

// TestSameAsOtherApp keeps bap-application's and bpp-application's copies of
// this package identical.
func TestSameAsOtherApp(t *testing.T) {
	wd, _ := os.Getwd()
	app := filepath.Base(filepath.Dir(filepath.Dir(wd)))
	other := map[string]string{"bap-application": "bpp-application", "bpp-application": "bap-application"}[app]
	if other == "" {
		t.Skip("not inside bap-application or bpp-application")
	}
	dir := filepath.Join(wd, "..", "..", "..", other, "internal", "grievance")
	files, _ := filepath.Glob("*.go")
	for _, name := range files {
		mine, _ := os.ReadFile(name)
		theirs, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || !bytes.Equal(mine, theirs) {
			t.Errorf("%s differs from %s/internal/grievance/%s: keep the two copies identical", name, other, name)
		}
	}
}
