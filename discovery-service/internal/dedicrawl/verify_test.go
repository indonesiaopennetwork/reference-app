package dedicrawl

import "testing"

func TestDigest_MatchesRealObservedFormat(t *testing.T) {
	// "sha-256:" + hex(SHA-256(content)) — confirmed against a real,
	// live catalog file's own declared digest (see CONTEXT.md D19), not
	// derived from the spec's original (BLAKE2b-512) choice.
	content := []byte("hello world")
	want := "sha-256:b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9"
	if got := Digest(content); got != want {
		t.Fatalf("Digest(%q) = %q, want %q", content, got, want)
	}
}

func TestVerifyDigest_DetectsMismatch(t *testing.T) {
	content := []byte("hello world")
	want := Digest(content)

	if err := VerifyDigest(content, want); err != nil {
		t.Fatalf("expected matching digest to verify, got %v", err)
	}
	if err := VerifyDigest([]byte("tampered"), want); err == nil {
		t.Fatal("expected mismatched content to fail digest verification")
	}
}
