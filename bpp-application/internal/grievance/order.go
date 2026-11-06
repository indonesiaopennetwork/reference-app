package grievance

// Order is what a grievance needs to know about the order it is about, read
// from the Beckn contract both apps keep: the lines a grievance can name,
// and the returns and refunds a return or refund grievance links.
type Order struct {
	ContractID   string      `json:"contractId"`
	Status       string      `json:"status"`
	ProviderID   string      `json:"providerId"`
	ProviderName string      `json:"providerName"`
	Currency     string      `json:"currency"`
	Lines        []OrderLine `json:"lines"`
	Returns      []Return    `json:"returns"`
	Refunds      []Refund    `json:"refunds"`
	// PolicyRef is the order's grievance SLA policy
	// (offerAttributes.policies.grievanceSla.policyRef), or the default.
	PolicyRef string `json:"policyRef"`
	// ReturnPolicyRef is the offers' returns policyRef, when they name one.
	ReturnPolicyRef string `json:"returnPolicyRef,omitempty"`
	// Returnable is whether any offer allows returns (policies.returns.allowed).
	Returnable bool `json:"returnable"`
}

// OrderLine is a commitment.
type OrderLine struct {
	CommitmentID string  `json:"commitmentId"`
	ResourceID   string  `json:"resourceId,omitempty"`
	Name         string  `json:"name"`
	Quantity     float64 `json:"quantity"`
	UnitPrice    float64 `json:"unitPrice"`
	Status       string  `json:"status,omitempty"`
}

// Return is a RETURN performance.
type Return struct {
	PerformanceID string   `json:"performanceId"`
	Status        string   `json:"status"`
	CommitmentIDs []string `json:"commitmentIds"`
	Reason        string   `json:"reason,omitempty"`
}

// Refund is a settlement that carries a refund.
type Refund struct {
	SettlementID string  `json:"settlementId"`
	Amount       float64 `json:"amount"`
	Currency     string  `json:"currency"`
	Status       string  `json:"status"`
	SettledAt    string  `json:"settledAt,omitempty"`
	TimelineDays int     `json:"timelineDays,omitempty"`
	Reference    string  `json:"reference,omitempty"`
	// Source is a goodwill or grievance refund's origin (e.g. GRIEVANCE).
	Source string `json:"source,omitempty"`
}

// ReadOrder reads contract (the Beckn Contract object).
func ReadOrder(contract map[string]any) Order {
	o := Order{
		ContractID: str(contract["id"]), Status: str(asMap(contract["status"])["code"]),
		Lines: []OrderLine{}, Returns: []Return{}, Refunds: []Refund{}, Currency: Currency,
	}
	for _, c := range slice(contract["commitments"]) {
		cm := asMap(c)
		offer := asMap(cm["offer"])
		attrs := asMap(cm["commitmentAttributes"])
		if o.ProviderID == "" {
			p := asMap(offer["provider"])
			o.ProviderID = str(p["id"])
			o.ProviderName = str(asMap(p["descriptor"])["name"])
		}
		qty := num(asMap(attrs["quantity"])["unitQuantity"])
		if qty == 0 {
			for _, r := range slice(cm["resources"]) {
				qty += num(asMap(asMap(r)["quantity"])["unitQuantity"])
			}
		}
		price := asMap(attrs["price"])
		if cur := str(price["currency"]); cur != "" {
			o.Currency = cur
		}
		status := str(asMap(asMap(cm["status"])["descriptor"])["code"])
		if status == "" {
			status = str(asMap(cm["status"])["code"])
		}
		o.Lines = append(o.Lines, OrderLine{
			CommitmentID: str(cm["id"]), ResourceID: str(attrs["resourceId"]),
			Name: str(asMap(offer["descriptor"])["name"]), Quantity: qty, UnitPrice: num(price["value"]), Status: status,
		})
		policies := asMap(asMap(offer["offerAttributes"])["policies"])
		if ref := str(asMap(policies["grievanceSla"])["policyRef"]); ref != "" && o.PolicyRef == "" {
			o.PolicyRef = ref
		}
		returns := asMap(policies["returns"])
		if ref := str(returns["policyRef"]); ref != "" && o.ReturnPolicyRef == "" {
			o.ReturnPolicyRef = ref
		}
		if b, ok := returns["allowed"].(bool); ok && b {
			o.Returnable = true
		}
	}
	if o.PolicyRef == "" {
		o.PolicyRef = DefaultPolicyRef
	}
	for _, p := range slice(contract["performance"]) {
		pm := asMap(p)
		if str(asMap(pm["performanceAttributes"])["type"]) != "RETURN" {
			continue
		}
		r := Return{PerformanceID: str(pm["id"]), Status: str(asMap(pm["status"])["code"]), CommitmentIDs: []string{}}
		r.Reason = str(asMap(pm["status"])["shortDesc"])
		for _, id := range slice(pm["commitmentIds"]) {
			r.CommitmentIDs = append(r.CommitmentIDs, str(id))
		}
		o.Returns = append(o.Returns, r)
	}
	for _, s := range slice(contract["settlements"]) {
		sm := asMap(s)
		attrs := asMap(sm["settlementAttributes"])
		refund := asMap(attrs["refund"])
		if refund == nil {
			continue
		}
		cur := str(attrs["currency"])
		if cur == "" {
			cur = o.Currency
		}
		o.Refunds = append(o.Refunds, Refund{
			SettlementID: str(sm["id"]), Amount: num(refund["amount"]), Currency: cur,
			Status: str(refund["status"]), SettledAt: str(attrs["settledAt"]),
			TimelineDays: int(num(refund["timelineDays"])), Reference: str(asMap(attrs["gateway"])["transactionId"]),
			Source: str(refund["source"]),
		})
	}
	return o
}

// Line returns the order line commitmentID, or nil.
func (o Order) Line(commitmentID string) *OrderLine {
	for i := range o.Lines {
		if o.Lines[i].CommitmentID == commitmentID {
			return &o.Lines[i]
		}
	}
	return nil
}

// Paid is what the buyer paid for qty of line commitmentID (0 if unknown).
func (o Order) Paid(commitmentID string, qty int) float64 {
	l := o.Line(commitmentID)
	if l == nil {
		return 0
	}
	if qty <= 0 || float64(qty) > l.Quantity {
		return l.UnitPrice * l.Quantity
	}
	return l.UnitPrice * float64(qty)
}

func asMap(v any) map[string]any { m, _ := v.(map[string]any); return m }
func slice(v any) []any          { s, _ := v.([]any); return s }
func str(v any) string           { s, _ := v.(string); return s }
func num(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	}
	return 0
}
