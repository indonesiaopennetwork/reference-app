// Package grievance is the ION Grievance v1 pack (ion-specs
// releases/release1/schema/common/Grievance/v1): the IONGrievance event
// carried in message.support.supportAttributes of /support and /on_support,
// the rules the pack README lists under "Rules enforced by ONIX", and the
// state a grievance is in after its events.
//
// The package is shared by bap-application and bpp-application and kept
// identical in both (internal/grievance); TestSameAsOtherApp checks it.
//
// Pending Beckn change: Beckn 2.0.0 Support has no supportAttributes slot
// (additionalProperties: false), so ONIX rejects these messages until the
// slot proposed in protocol-specifications-v2#203 is released and vendored.
package grievance

import "encoding/json"

const (
	// Context and Type are the JSON-LD @context and @type of IONGrievance.
	Context = "https://schema.ion.id/release1/schema/common/Grievance/v1/context.jsonld"
	Type    = "ion:IONGrievance"

	// DescriptorCode is Support.descriptor.code on every grievance message.
	DescriptorCode = "GRIEVANCE"

	// Currency is the only IONPayment currency.
	Currency = "IDR"
)

// Event types.
const (
	Opened             = "OPENED"
	Acknowledged       = "ACKNOWLEDGED"
	StatusChanged      = "STATUS_CHANGED"
	ResolutionProposed = "RESOLUTION_PROPOSED"
	ResolutionDecided  = "RESOLUTION_DECIDED"
	ActionCompleted    = "ACTION_COMPLETED"
	Resolved           = "RESOLVED"
	Objection          = "OBJECTION"
	Closed             = "CLOSED"
)

// Case statuses.
const (
	StatusOpen       = "OPEN"
	StatusInProgress = "IN_PROGRESS"
	StatusResolved   = "RESOLVED"
	StatusClosed     = "CLOSED"
)

// Sides and authorization types.
const (
	SideBuyer  = "BUYER"
	SideSeller = "SELLER"

	ExplicitAgreement = "EXPLICIT_AGREEMENT"
	PolicyAuthorized  = "POLICY_AUTHORIZED"
	Unilateral        = "UNILATERAL"

	Accepted = "ACCEPTED"
	Rejected = "REJECTED"

	MethodNone = "NONE"
)

// Categories the apps offer. category is free SCREAMING_SNAKE_CASE in the
// pack; these are the ones named there.
const (
	DamagedItem        = "DAMAGED_ITEM"
	WrongItem          = "WRONG_ITEM"
	MissingItem        = "MISSING_ITEM"
	LateDelivery       = "LATE_DELIVERY"
	WrongReturn        = "WRONG_RETURN"
	ReturnRejected     = "RETURN_REJECTED"
	ReturnPickupFailed = "RETURN_PICKUP_FAILED"
	ReturnQCDisputed   = "RETURN_QC_DISPUTED"
	RefundNotReceived  = "REFUND_NOT_RECEIVED"
	Other              = "OTHER"
)

// LinkedCategories must link the return, refund or contract they are about
// in relatedReferences (CONTRACT, PERFORMANCE or SETTLEMENT).
var LinkedCategories = map[string]bool{
	ReturnRejected: true, ReturnPickupFailed: true, ReturnQCDisputed: true, RefundNotReceived: true,
}

// Grievance is IONGrievance: one event of a grievance.
type Grievance struct {
	Context           string             `json:"@context"`
	Type              string             `json:"@type"`
	GrievanceID       string             `json:"grievanceId"`
	EventType         string             `json:"eventType"`
	CaseStatus        string             `json:"caseStatus"`
	SubStatus         string             `json:"subStatus,omitempty"`
	OccurredAt        string             `json:"occurredAt"`
	OpenedAt          string             `json:"openedAt,omitempty"`
	OriginatingNpID   string             `json:"originatingNpId"`
	RaisedBy          *Party             `json:"raisedBy,omitempty"`
	RaisedAgainst     *Subject           `json:"raisedAgainst,omitempty"`
	AssignedNpID      string             `json:"assignedNpId,omitempty"`
	Category          string             `json:"category,omitempty"`
	Description       string             `json:"description,omitempty"`
	Affected          []AffectedLine     `json:"affected,omitempty"`
	PolicyRef         string             `json:"policyRef,omitempty"`
	ChannelMode       string             `json:"channelMode,omitempty"`
	Resolution        *Resolution        `json:"resolution,omitempty"`
	Decision          *Decision          `json:"decision,omitempty"`
	Execution         *Execution         `json:"execution,omitempty"`
	Explanation       string             `json:"explanation,omitempty"`
	RelatedReferences []RelatedReference `json:"relatedReferences,omitempty"`
}

// Party is GrievanceParty: who raised the grievance.
type Party struct {
	Side    string `json:"side"`
	NpID    string `json:"npId"`
	PartyID string `json:"partyId,omitempty"`
}

// Subject is GrievanceSubject: who the grievance is about.
type Subject struct {
	Role    string `json:"role"`
	PartyID string `json:"partyId,omitempty"`
}

// AffectedLine is an order line (commitmentId) or performance concerned.
type AffectedLine struct {
	CommitmentID  string `json:"commitmentId,omitempty"`
	PerformanceID string `json:"performanceId,omitempty"`
	Quantity      int    `json:"quantity,omitempty"`
}

// FinancialEffect is the money an action moves.
type FinancialEffect struct {
	EffectType       string  `json:"effectType"`
	Amount           float64 `json:"amount"`
	Currency         string  `json:"currency"`
	AllocationBasis  string  `json:"allocationBasis"`
	LiablePartyID    string  `json:"liablePartyId,omitempty"`
	AllocationReason string  `json:"allocationReason,omitempty"`
	PolicyRef        string  `json:"policyRef,omitempty"`
	SettlementRef    string  `json:"settlementRef,omitempty"`
}

// Action is RemedyAction.
type Action struct {
	ActionID        string           `json:"actionId"`
	Type            string           `json:"type"`
	Description     string           `json:"description"`
	Affected        []AffectedLine   `json:"affected,omitempty"`
	FinancialEffect *FinancialEffect `json:"financialEffect,omitempty"`
	ExecutionMethod string           `json:"executionMethod"`
	SectorSubtype   string           `json:"sectorSubtype,omitempty"`
}

// Verification is the designated verifier's confirmation a
// POLICY_AUTHORIZED resolution rests on (e.g. the QC of a returned item).
type Verification struct {
	VerifiedByNpID        string `json:"verifiedByNpId"`
	VerificationReference string `json:"verificationReference"`
	VerifiedAt            string `json:"verifiedAt"`
	Method                string `json:"method"`
}

// Resolution is one version of a proposal.
type Resolution struct {
	ProposalID        string        `json:"proposalId"`
	Version           int           `json:"version"`
	ProposedByNpID    string        `json:"proposedByNpId"`
	AuthorizationType string        `json:"authorizationType"`
	PolicyRef         string        `json:"policyRef,omitempty"`
	Verification      *Verification `json:"verification,omitempty"`
	FundingNpID       string        `json:"fundingNpId,omitempty"`
	Actions           []Action      `json:"actions"`
}

// Decision accepts or rejects one whole resolution version.
type Decision struct {
	ProposalID    string `json:"proposalId"`
	Version       int    `json:"version"`
	DecidedByNpID string `json:"decidedByNpId"`
	Result        string `json:"result"`
	DecidedAt     string `json:"decidedAt"`
	Reason        string `json:"reason,omitempty"`
}

// ActionExecution records one executed action.
type ActionExecution struct {
	ActionID           string `json:"actionId"`
	Status             string `json:"status"`
	CompletedAt        string `json:"completedAt"`
	ExecutedByNpID     string `json:"executedByNpId"`
	ExecutionReference string `json:"executionReference,omitempty"`
}

// Execution lists executed actions.
type Execution struct {
	ActionExecutions []ActionExecution `json:"actionExecutions"`
}

// RelatedReference points at a record in another flow.
type RelatedReference struct {
	RefType string `json:"refType"`
	RefID   string `json:"refId"`
}

// Support is message.support of a grievance /support or /on_support.
// Channels keep their Beckn meaning: Attributes entries (with @context and
// @type) for the contact channels, or left out.
type Support struct {
	OrderID           string            `json:"orderId,omitempty"`
	Descriptor        *Descriptor       `json:"descriptor,omitempty"`
	Channels          []json.RawMessage `json:"channels,omitempty" swaggertype:"array,object"`
	SupportAttributes json.RawMessage   `json:"supportAttributes,omitempty" swaggertype:"object"`
}

// Descriptor is Support.descriptor.
type Descriptor struct {
	Code      string `json:"code,omitempty"`
	Name      string `json:"name,omitempty"`
	ShortDesc string `json:"shortDesc,omitempty"`
}

// Parse reads supportAttributes as an IONGrievance. ok is false when the
// message carries no grievance (a plain Beckn support request).
func (s Support) Parse() (g *Grievance, ok bool, err error) {
	if len(s.SupportAttributes) == 0 || string(s.SupportAttributes) == "null" {
		return nil, false, nil
	}
	g = &Grievance{}
	if err := json.Unmarshal(s.SupportAttributes, g); err != nil {
		return nil, true, err
	}
	return g, true, nil
}
