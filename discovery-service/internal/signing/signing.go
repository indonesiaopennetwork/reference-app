// Package signing implements the Beckn v2 HTTP Signature profile
// (beckn.yaml#/components/schemas/Signature — draft-cavage-http-signatures-12
// as profiled by BECKN-006), both for OUTBOUND requests this service sends
// (the on_discover callback, via Signer) and for INBOUND verification of
// incoming /discover requests (via Verifier — see CONTEXT.md D15/M4). The
// inbound side trusts a single, statically-configured caller identity and
// public key rather than doing a live registry lookup — good enough for a
// reference deployment with one known BAP caller; a real multi-caller
// deployment would resolve the caller's public key from a registry lookup
// on keyId instead.
package signing

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"time"

	"golang.org/x/crypto/blake2b"

	"github.com/remiges-tushar/discovery-service/internal/beckn"
)

// KeyPair is an Ed25519 signing key pair.
type KeyPair struct {
	Private ed25519.PrivateKey
	Public  ed25519.PublicKey
}

// GenerateKeyPair creates a fresh random Ed25519 key pair.
func GenerateKeyPair() (KeyPair, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return KeyPair{}, fmt.Errorf("generate ed25519 key pair: %w", err)
	}
	return KeyPair{Private: priv, Public: pub}, nil
}

// LoadKeyPair reconstructs a KeyPair from a base64-encoded Ed25519 seed
// (32 bytes before encoding) — see config.Config.SigningPrivateKey.
func LoadKeyPair(base64Seed string) (KeyPair, error) {
	seed, err := base64.StdEncoding.DecodeString(base64Seed)
	if err != nil {
		return KeyPair{}, fmt.Errorf("decode base64 signing key: %w", err)
	}
	if len(seed) != ed25519.SeedSize {
		return KeyPair{}, fmt.Errorf("signing key seed is %d bytes, want %d", len(seed), ed25519.SeedSize)
	}
	priv := ed25519.NewKeyFromSeed(seed)
	return KeyPair{Private: priv, Public: priv.Public().(ed25519.PublicKey)}, nil
}

// LoadOrGenerateKeyPair loads a key pair from base64Seed if non-empty,
// otherwise generates a fresh ephemeral one. ephemeral reports which
// happened, so callers can log accordingly — an ephemeral key changes on
// every process restart, so nothing else can ever have it registered to
// verify against; it only stops a receiver rejecting us for a *missing*
// signature, not one it can actually verify. See CONTEXT.md D15.
func LoadOrGenerateKeyPair(base64Seed string) (kp KeyPair, ephemeral bool, err error) {
	if base64Seed == "" {
		kp, err = GenerateKeyPair()
		return kp, true, err
	}
	kp, err = LoadKeyPair(base64Seed)
	return kp, false, err
}

// Signer signs outbound request bodies with a fixed key pair and
// identity, producing a beckn.Signature per the spec's format.
type Signer struct {
	keyPair      KeyPair
	subscriberID string
	uniqueKeyID  string
	validity     time.Duration
}

// NewSigner builds a Signer. subscriberID/uniqueKeyID form the
// "{subscriberId}|{uniqueKeyId}|{algorithm}" keyId (algorithm is always
// "ed25519", appended automatically — see beckn.FormatSignature).
// validity is how long the signature is valid for (created→expires).
func NewSigner(keyPair KeyPair, subscriberID, uniqueKeyID string, validity time.Duration) *Signer {
	return &Signer{keyPair: keyPair, subscriberID: subscriberID, uniqueKeyID: uniqueKeyID, validity: validity}
}

// Sign computes a BLAKE2b-512 digest of body, builds the signing string
// per beckn.yaml's Signature description —
//
//	(created): {ts}\n(expires): {ts}\ndigest: BLAKE-512={base64Digest}
//
// — signs it with Ed25519, and returns the assembled Signature string
// (suitable for both the Authorization header and, when this service
// gains real inbound verification, a CounterSignature).
func (s *Signer) Sign(body []byte) (beckn.Signature, error) {
	digest := blake2b.Sum512(body)
	digestB64 := base64.StdEncoding.EncodeToString(digest[:])

	created := time.Now().Unix()
	expires := created + int64(s.validity.Seconds())

	signingString := fmt.Sprintf("(created): %d\n(expires): %d\ndigest: BLAKE-512=%s", created, expires, digestB64)
	sigBytes := ed25519.Sign(s.keyPair.Private, []byte(signingString))
	sigB64 := base64.StdEncoding.EncodeToString(sigBytes)

	return beckn.FormatSignature(
		s.subscriberID, s.uniqueKeyID, "ed25519",
		created, expires,
		"(created) (expires) digest",
		sigB64,
	), nil
}

// Verifier checks an inbound Authorization header against a single trusted
// caller identity and public key (see package doc).
type Verifier struct {
	subscriberID string
	uniqueKeyID  string
	publicKey    ed25519.PublicKey
	// clockSkew tolerates minor clock drift between this service and the
	// caller when checking the signature's created/expires window.
	clockSkew time.Duration
}

// NewVerifier builds a Verifier trusting exactly one caller identity
// (subscriberID/uniqueKeyID) and its base64-encoded 32-byte Ed25519 public
// key.
func NewVerifier(publicKeyBase64, subscriberID, uniqueKeyID string) (*Verifier, error) {
	pub, err := base64.StdEncoding.DecodeString(publicKeyBase64)
	if err != nil {
		return nil, fmt.Errorf("decode base64 public key: %w", err)
	}
	if len(pub) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("public key is %d bytes, want %d", len(pub), ed25519.PublicKeySize)
	}
	return &Verifier{
		subscriberID: subscriberID,
		uniqueKeyID:  uniqueKeyID,
		publicKey:    ed25519.PublicKey(pub),
		clockSkew:    5 * time.Minute,
	}, nil
}

// Verify parses authHeader (an Authorization header value formatted per
// beckn.FormatSignature) and checks it was produced by the trusted caller
// over exactly this body: keyId must match the configured identity, the
// created/expires window must cover now (within clockSkew), and the
// Ed25519 signature must verify against a signing string rebuilt from the
// header's created/expires and a BLAKE2b-512 digest computed from body —
// the same construction Signer.Sign uses, so a body tampered with in
// transit fails here even though the header itself parses fine.
func (v *Verifier) Verify(authHeader string, body []byte) error {
	parsed, err := beckn.ParseSignature(authHeader)
	if err != nil {
		return fmt.Errorf("parse Authorization header: %w", err)
	}

	if parsed.SubscriberID != v.subscriberID || parsed.UniqueKeyID != v.uniqueKeyID {
		return fmt.Errorf("untrusted keyId %q|%q (want %q|%q)", parsed.SubscriberID, parsed.UniqueKeyID, v.subscriberID, v.uniqueKeyID)
	}
	if parsed.Algorithm != "ed25519" {
		return fmt.Errorf("unsupported algorithm %q", parsed.Algorithm)
	}

	now := time.Now().Unix()
	skew := int64(v.clockSkew.Seconds())
	if now < parsed.Created-skew {
		return fmt.Errorf("signature not yet valid: created=%d now=%d", parsed.Created, now)
	}
	if now > parsed.Expires+skew {
		return fmt.Errorf("signature expired: expires=%d now=%d", parsed.Expires, now)
	}

	digest := blake2b.Sum512(body)
	digestB64 := base64.StdEncoding.EncodeToString(digest[:])
	signingString := fmt.Sprintf("(created): %d\n(expires): %d\ndigest: BLAKE-512=%s", parsed.Created, parsed.Expires, digestB64)

	if !ed25519.Verify(v.publicKey, []byte(signingString), parsed.Signature) {
		return fmt.Errorf("signature verification failed")
	}
	return nil
}
