package onupdatesvc

import "encoding/json"

// redactForLog returns a copy of a Beckn payload with known-PII fields
// masked, safe to pass to LogActivity / beckn_message_log -- never the
// original raw bytes, which still get stored unredacted in
// contract_snapshots (real order data the app needs to actually ship the
// order) and can still be re-fetched through the existing, access-controlled
// order APIs.
//
// Deliberately targets the one place PII is known to live in this
// contract shape -- performanceAttributes.deliveryDetails.{address,contact}
// (see clientsvc's own patchPerformanceDelivery, which populates exactly
// these fields from the buyer's billing form) -- rather than a broad
// heuristic keyed on generic field names like "name", which would also
// blow away unrelated, non-sensitive descriptor names throughout the
// contract.
func redactForLog(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return raw
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		// Not valid JSON (or already redacted upstream) -- pass through
		// as-is rather than lose the log line entirely.
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
