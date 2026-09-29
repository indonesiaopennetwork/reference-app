package updatesvc

import "encoding/json"

// redactForLog returns a copy of a Beckn payload with known-PII fields
// masked, safe for logharbour/beckn_message_log -- never the original raw
// bytes, which still get stored unredacted in contract_update_requests /
// contracts (real order data the seller UI needs to actually fulfill the
// order) and are only ever surfaced through the existing,
// seller-authenticated dashboard endpoints.
//
// Mirrors bap-application/internal/on_update_svc's own redactForLog exactly
// -- same known PII location (performanceAttributes.deliveryDetails), same
// reasoning for why it's not a generic name-keyed heuristic.
func redactForLog(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return raw
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return raw
	}
	redactInPlace(v)
	out, err := json.Marshal(v)
	if err != nil {
		return raw
	}
	return out
}

const redactedPlaceholder = "[REDACTED]"

func redactInPlace(v any) {
	switch node := v.(type) {
	case map[string]any:
		if dd, ok := node["deliveryDetails"].(map[string]any); ok {
			if _, ok := dd["address"]; ok {
				dd["address"] = redactedPlaceholder
			}
			if _, ok := dd["contact"]; ok {
				dd["contact"] = redactedPlaceholder
			}
		}
		for _, child := range node {
			redactInPlace(child)
		}
	case []any:
		for _, child := range node {
			redactInPlace(child)
		}
	}
}
