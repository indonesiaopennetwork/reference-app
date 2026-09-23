package beckn

import (
	"regexp"
	"testing"
)

// specSignaturePattern is the exact pattern from ion-specs
// schema/core/v2/api/v2.0.0/beckn.yaml#/components/schemas/Signature.
// Signature (and therefore CounterSignature, which is `allOf: [Signature]`)
// is a STRING matching this pattern — not a JSON object. This test is the
// spec-compliance guardrail: if FormatSignature's output ever stops
// matching this, every Ack we send is malformed per the ION/Beckn v2
// contract.
var specSignaturePattern = regexp.MustCompile(
	`^Signature keyId="[^|"]+\|[^|"]+\|[^"]+",algorithm="[^"]+",created="\d+",expires="\d+",headers="[^"]+",signature="[A-Za-z0-9+/]+=*"$`,
)

func TestFormatSignature_MatchesSpecPattern(t *testing.T) {
	sig := FormatSignature("bpp.example.com", "key-1", "ed25519", 1735900000, 1735900300, "(created) (expires) digest", "AbC123+/==")

	if !specSignaturePattern.MatchString(string(sig)) {
		t.Fatalf("FormatSignature output does not match the spec pattern.\ngot:  %s\nwant: match %s", sig, specSignaturePattern)
	}
}

func TestFormatSignature_KeyIdIsSubscriberPipeUniqueKeyPipeAlgorithm(t *testing.T) {
	sig := FormatSignature("bpp.example.com", "key-1", "ed25519", 1, 2, "h", "c2ln")
	want := `Signature keyId="bpp.example.com|key-1|ed25519",algorithm="ed25519",created="1",expires="2",headers="h",signature="c2ln"`
	if string(sig) != want {
		t.Fatalf("FormatSignature() =\n%s\nwant:\n%s", sig, want)
	}
}

func TestParseSignature_RoundTripsFormatSignature(t *testing.T) {
	sig := FormatSignature("bap.example.com", "key-1", "ed25519", 1735900000, 1735900300, "(created) (expires) digest", "QWJDMTIz")

	parsed, err := ParseSignature(string(sig))
	if err != nil {
		t.Fatalf("ParseSignature() error = %v", err)
	}

	if parsed.SubscriberID != "bap.example.com" {
		t.Errorf("SubscriberID = %q, want bap.example.com", parsed.SubscriberID)
	}
	if parsed.UniqueKeyID != "key-1" {
		t.Errorf("UniqueKeyID = %q, want key-1", parsed.UniqueKeyID)
	}
	if parsed.Algorithm != "ed25519" {
		t.Errorf("Algorithm = %q, want ed25519", parsed.Algorithm)
	}
	if parsed.Created != 1735900000 {
		t.Errorf("Created = %d, want 1735900000", parsed.Created)
	}
	if parsed.Expires != 1735900300 {
		t.Errorf("Expires = %d, want 1735900300", parsed.Expires)
	}
	if parsed.Headers != "(created) (expires) digest" {
		t.Errorf("Headers = %q, want %q", parsed.Headers, "(created) (expires) digest")
	}
	if string(parsed.Signature) != "AbC123" {
		t.Errorf("Signature = %q, want decoded AbC123", parsed.Signature)
	}
}

func TestParseSignature_RejectsMalformedHeader(t *testing.T) {
	cases := map[string]string{
		"missing keyId":         `Signature algorithm="ed25519",created="1",expires="2",headers="h",signature="c2ln"`,
		"keyId missing segment": `Signature keyId="onlyone",algorithm="ed25519",created="1",expires="2",headers="h",signature="c2ln"`,
		"missing signature":     `Signature keyId="a|b|ed25519",algorithm="ed25519",created="1",expires="2",headers="h"`,
		"non-base64 signature":  `Signature keyId="a|b|ed25519",algorithm="ed25519",created="1",expires="2",headers="h",signature="not base64!"`,
		"non-numeric created":   `Signature keyId="a|b|ed25519",algorithm="ed25519",created="abc",expires="2",headers="h",signature="c2ln"`,
	}
	for name, header := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseSignature(header); err == nil {
				t.Fatalf("ParseSignature(%q) error = nil, want an error", header)
			}
		})
	}
}
