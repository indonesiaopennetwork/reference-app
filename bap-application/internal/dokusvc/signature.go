// Package dokusvc is a client for DOKU Checkout
// (https://developers.doku.com/accept-payments/doku-checkout), the hosted
// payment page DOKU's sandbox/production API uses to collect payment for an
// order: this app creates a checkout session for an amount, DOKU returns a
// URL to redirect the buyer to, and DOKU later POSTs a signed async
// notification once the buyer pays (or fails to).
package dokusvc

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
)

// digest is DOKU's "Digest" request component: base64(sha256(body)). Used
// both when signing an outbound request and when verifying an inbound
// notification's signature.
func digest(body []byte) string {
	sum := sha256.Sum256(body)
	return base64.StdEncoding.EncodeToString(sum[:])
}

// signatureComponents builds the newline-joined component string DOKU signs
// (see "Signature Component from Request Header" in DOKU's docs): one
// "Name:value" line per component, no trailing newline. requestTarget is the
// path of the endpoint being called -- for an outbound request, the DOKU API
// path (e.g. /checkout/v1/payment); for verifying an inbound notification,
// the path DOKU was configured to POST it to.
func signatureComponents(clientID, requestID, requestTimestamp, requestTarget, digestValue string) string {
	return fmt.Sprintf(
		"Client-Id:%s\nRequest-Id:%s\nRequest-Timestamp:%s\nRequest-Target:%s\nDigest:%s",
		clientID, requestID, requestTimestamp, requestTarget, digestValue,
	)
}

// sign returns the "HMACSHA256=<base64>" Signature header value: HMAC-SHA256
// of components, keyed by secretKey, base64-encoded.
func sign(secretKey, components string) string {
	mac := hmac.New(sha256.New, []byte(secretKey))
	mac.Write([]byte(components))
	return "HMACSHA256=" + base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// verify reports whether signature (the raw header value, "HMACSHA256=...")
// matches what sign computes for the same components -- compared in
// constant time.
func verify(secretKey, components, signature string) bool {
	return hmac.Equal([]byte(sign(secretKey, components)), []byte(signature))
}
