package dedicrawl

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// digestPrefix matches the format observed on real catalog index/file
// digests: "sha-256:" + hex(SHA-256(content)), computed over the
// decompressed file content. This is NOT what protocol-specifications-v2
// originally specified (BLAKE2b-512) — see CONTEXT.md D19: real Provider
// Node tooling observed in the wild uses plain SHA-256, so this package
// matches what's actually served rather than the spec's original choice.
const digestPrefix = "sha-256:"

// Digest computes the observed real-world content digest over raw
// (already-decompressed) bytes.
func Digest(content []byte) string {
	sum := sha256.Sum256(content)
	return digestPrefix + hex.EncodeToString(sum[:])
}

// VerifyDigest reports an error if content's digest doesn't match want
// (as declared by an index entry's baseline/change/latest FileRef).
func VerifyDigest(content []byte, want string) error {
	got := Digest(content)
	if got != want {
		return fmt.Errorf("digest mismatch: want %s, got %s", want, got)
	}
	return nil
}
