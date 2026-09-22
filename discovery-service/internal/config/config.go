// Package config loads discovery-service's runtime configuration from
// environment variables (populated locally via .env — see .env.example at
// the repo root). Every configurable value has a sane default so the
// service runs with zero configuration.
package config

import (
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/remiges-tushar/discovery-service/internal/beckn"
	"github.com/remiges-tushar/discovery-service/internal/constants"
)

const (
	defaultPort                    = "8080"
	defaultLogLevel                = "info"
	defaultDispatchClientTimeout   = 10 * time.Second
	defaultDispatchDeliveryTimeout = 10 * time.Second

	// defaultWorkerPoolSize/QueueSize bound the background on_discover
	// dispatch pool (internal/worker) — see CONTEXT.md D9. These are
	// deliberately modest for a single-instance M1 deployment; raise them
	// with real traffic data rather than guessing.
	defaultWorkerPoolSize  = 10
	defaultWorkerQueueSize = 100

	// defaultMaxRequestBodyBytes caps a single /discover request body.
	defaultMaxRequestBodyBytes = 1 << 20 // 1 MiB

	// HTTP server timeouts. Unset, Go's http.Server has NO timeouts at
	// all on these — a classic footgun that lets a slow/malicious client
	// hold a connection (and its goroutine) open indefinitely. Explicit
	// defaults here close that gap.
	defaultHTTPReadHeaderTimeout = 5 * time.Second
	defaultHTTPReadTimeout       = 10 * time.Second
	defaultHTTPWriteTimeout      = 10 * time.Second
	defaultHTTPIdleTimeout       = 60 * time.Second

	// defaultShutdownTimeout bounds how long graceful shutdown waits for
	// in-flight HTTP requests and background dispatch jobs to drain
	// before giving up — see CONTEXT.md D9.
	defaultShutdownTimeout = 15 * time.Second

	// defaultSigningKeyID/Validity — see Config.SigningKeyID/SigningValidity
	// and CONTEXT.md D15.
	defaultSigningKeyID    = "key-1"
	defaultSigningValidity = 5 * time.Minute

	// defaultCatalogRefreshInterval/FetchTimeout — see
	// Config.CatalogRefreshInterval/CatalogFetchTimeout and CONTEXT.md D17.
	defaultCatalogRefreshInterval = 60 * time.Second
	defaultCatalogFetchTimeout    = 10 * time.Second
)

// Config holds all runtime-configurable values for the service.
type Config struct {
	// Port the HTTP server listens on inside the container/process.
	Port string
	// DispatchClientTimeout bounds the underlying HTTP client used to
	// deliver on_discover callbacks.
	DispatchClientTimeout time.Duration
	// DispatchDeliveryTimeout bounds a single on_discover delivery attempt
	// via a context deadline.
	DispatchDeliveryTimeout time.Duration

	// WorkerPoolSize is the number of goroutines running background
	// on_discover dispatch jobs concurrently.
	WorkerPoolSize int
	// WorkerQueueSize is how many dispatch jobs may be queued waiting for
	// a free worker before new ones are dropped (with a logged warning).
	WorkerQueueSize int

	// MaxRequestBodyBytes caps how much of a /discover request body is
	// read before it's rejected.
	MaxRequestBodyBytes int64

	// HTTP server timeouts — see defaultHTTP* above.
	HTTPReadHeaderTimeout time.Duration
	HTTPReadTimeout       time.Duration
	HTTPWriteTimeout      time.Duration
	HTTPIdleTimeout       time.Duration

	// ShutdownTimeout bounds graceful shutdown drain time.
	ShutdownTimeout time.Duration

	// LogLevel selects the minimum logharbour priority to emit — see
	// internal/logging.ParsePriority for accepted values
	// (debug2/debug1/debug0|debug/info/warn/err|error/crit/sec). Setting
	// this to a debug tier also enables per-request "hit" logging.
	LogLevel string

	// SubscriberID/SubscriberURI are THIS SERVICE'S OWN registered Beckn
	// network identity — a Discovery Service is its own network
	// participant (it can aggregate catalogs from many Provider
	// Platforms, not just this repo's own bpp), so it has its own
	// identity, entirely unrelated to this reference app's BAP/BPP
	// identities. Deliberately NOT named after "Bpp"/"Bap" — this field
	// used to be called BppID/BppURI, which wrongly suggested it was, or
	// should be, the reference app's BPP's identity; it never was. See
	// CONTEXT.md D20/D21/D22 for that history. Stamped onto the
	// on_discover response context (and any catalog that doesn't already
	// declare its own) as the default bppId/bppUri whenever an incoming
	// /discover request doesn't already address a specific BPP — see
	// service.BuildFixedOnDiscover and CONTEXT.md D13 (that field is
	// legitimately named bppId/bppUri on the wire, per the Beckn v2
	// schema — only this config-level identity was misnamed). Empty by
	// default; set these (or SubscriberKeyID below) once this service has
	// its own dedicated Ed25519 keypair, whether or not it's registered
	// on a network yet — see SubscriberKeyID and main.go's buildSigner.
	SubscriberID  string
	SubscriberURI string

	// SubscriberKeyID is an optional combined
	// "{subscriberId}|{uniqueKeyId}|{algorithm}" form of this service's
	// OWN identity above — the same combined shape this repo's other Go
	// apps use for BAP_KEY_ID/BPP_KEY_ID (see
	// bap-application/internal/clientsvc/becknsign.go), for consistency,
	// but never actually reusing either of THEIR values: this is a wholly
	// separate, dedicated identity for Discovery Service, not shared with
	// anyone. When set, main.go's buildSigner splits it (beckn.SplitKeyID)
	// and uses its parts in place of SubscriberID and SigningKeyID below.
	// Leave unset to keep using those two directly (e.g. in tests).
	SubscriberKeyID string

	// OnDiscoverPath is appended to a BAP's context.bapUri to form the
	// on_discover callback URL. Defaults to the spec-correct
	// "/on_discover" (constants.OnDiscoverPath). Override via
	// ON_DISCOVER_PATH only when a specific counterpart's registered
	// bapUri doesn't already include its full receiver route (an
	// observed real-world case with an ONIX bapTxnReceiver expecting
	// "/bap/receiver/on_discover") — see CONTEXT.md D14. This is a
	// stopgap: the durable fix is resolving the real callback URL from a
	// registry lookup instead of trusting/guessing at bapUri (D3/M4).
	OnDiscoverPath string

	// SigningPrivateKeyBase64 is a base64-encoded 32-byte Ed25519 seed
	// used to sign outbound requests (see internal/signing, CONTEXT.md
	// D15). Empty by default: main.go generates a fresh ephemeral key
	// pair on every start instead, logs its public key, and signs with
	// that — good enough to stop a real BAP rejecting us for a missing
	// signature, but nobody can actually verify it against a registry
	// entry until a real persistent key is configured here and published.
	SigningPrivateKeyBase64 string
	// SigningKeyID is the "{uniqueKeyId}" segment of the signature's
	// keyId (see beckn.FormatSignature).
	SigningKeyID string
	// SigningValidity is how long a signature is valid for
	// (created→expires).
	SigningValidity time.Duration

	// TrustedCallerKeyID/TrustedCallerPublicKeyBase64 identify the single
	// caller allowed to POST /discover — see internal/signing.Verifier.
	// Deliberately named "caller", not "Bap": whoever is actually
	// configured here (in this reference deployment, our own BAP, since
	// its onix bapTxnCaller is what signs the outbound /discover request)
	// is a deployment choice, not something Discovery Service should
	// assume by role name — a real Discovery Service in a live network
	// could equally be called by any authorized participant. Verifying
	// ANY caller at all requires knowing two things about them: who they
	// claim to be (an identifier) and their public key (to check the
	// signature math) — that's inherent to signature verification, not
	// optional. TrustedCallerKeyID is a public identifier, safe to share
	// (in this deployment, reused from BAP_KEY_ID as-is).
	// TrustedCallerPublicKeyBase64 is deliberately the caller's PUBLIC key
	// ONLY (never a private key): this service only ever needs to verify
	// as that caller, never sign as it, so it must never be handed a
	// credential capable of doing that — see CONTEXT.md D21. Both must be
	// set for inbound signature verification to run; if either is empty,
	// /discover accepts requests without checking the Authorization
	// header at all (logged as a warning) — matching this service's
	// existing fail-open-with-a-warning posture for unconfigured signing
	// (see buildSigner in cmd/discovery/main.go).
	TrustedCallerKeyID           string
	TrustedCallerPublicKeyBase64 string

	// CatalogIndexURLs is a list of Provider Node catalog index URLs
	// (e.g. "https://bpp.example.com/index/becknCatalogs.index.json")
	// discovery-service crawls for real catalog data — see CONTEXT.md
	// D19 (internal/dedicrawl). Each entry must expose the real,
	// spec-shaped index/baseline/change-file documents that package maps
	// into beckn.Catalog (plain or gzip-compressed; digest-verified but
	// NOT signature-verified — see internal/dedicrawl's package doc).
	// Empty by default: with no sources configured, the embedded demo
	// catalog (data/catalog.json) is served instead, unchanged from M1.
	CatalogIndexURLs []string
	// CatalogRefreshInterval is how often each configured index URL is
	// re-crawled in the background after the initial fetch.
	CatalogRefreshInterval time.Duration
	// CatalogFetchTimeout bounds a single HTTP call (index, baseline, or
	// change file) to a catalog source.
	CatalogFetchTimeout time.Duration
}

// Addr returns the listen address (":<port>") for http.ListenAndServe.
func (c Config) Addr() string {
	return ":" + c.Port
}

// Load reads configuration from the environment, falling back to defaults
// for anything unset or invalid. Before reading, it applies a .env file
// found in the working directory or one of its ancestors (see
// loadDotEnv) — real environment variables always take precedence.
func Load() Config {
	loadDotEnv()

	cfg := Config{
		Port:                    getString("PORT", defaultPort),
		DispatchClientTimeout:   getSeconds("DISPATCH_CLIENT_TIMEOUT_SECONDS", defaultDispatchClientTimeout),
		DispatchDeliveryTimeout: getSeconds("DISPATCH_DELIVERY_TIMEOUT_SECONDS", defaultDispatchDeliveryTimeout),

		WorkerPoolSize:  getInt("WORKER_POOL_SIZE", defaultWorkerPoolSize),
		WorkerQueueSize: getInt("WORKER_QUEUE_SIZE", defaultWorkerQueueSize),

		MaxRequestBodyBytes: getInt64("MAX_REQUEST_BODY_BYTES", defaultMaxRequestBodyBytes),

		HTTPReadHeaderTimeout: getSeconds("HTTP_READ_HEADER_TIMEOUT_SECONDS", defaultHTTPReadHeaderTimeout),
		HTTPReadTimeout:       getSeconds("HTTP_READ_TIMEOUT_SECONDS", defaultHTTPReadTimeout),
		HTTPWriteTimeout:      getSeconds("HTTP_WRITE_TIMEOUT_SECONDS", defaultHTTPWriteTimeout),
		HTTPIdleTimeout:       getSeconds("HTTP_IDLE_TIMEOUT_SECONDS", defaultHTTPIdleTimeout),

		ShutdownTimeout: getSeconds("SHUTDOWN_TIMEOUT_SECONDS", defaultShutdownTimeout),

		LogLevel: getString("LOG_LEVEL", defaultLogLevel),

		SubscriberID:    getString("SUBSCRIBER_ID", ""),
		SubscriberURI:   getString("SUBSCRIBER_URI", ""),
		SubscriberKeyID: getString("SUBSCRIBER_KEY_ID", ""),

		OnDiscoverPath: getString("ON_DISCOVER_PATH", constants.OnDiscoverPath),

		SigningPrivateKeyBase64: getString("SIGNING_PRIVATE_KEY_BASE64", ""),
		SigningKeyID:            getString("SIGNING_KEY_ID", defaultSigningKeyID),
		SigningValidity:         getSeconds("SIGNING_VALIDITY_SECONDS", defaultSigningValidity),

		TrustedCallerKeyID:           getString("TRUSTED_CALLER_KEY_ID", ""),
		TrustedCallerPublicKeyBase64: getString("TRUSTED_CALLER_PUBLIC_KEY_BASE64", ""),

		CatalogIndexURLs:       getStringSlice("CATALOG_INDEX_URLS"),
		CatalogRefreshInterval: getSeconds("CATALOG_REFRESH_INTERVAL_SECONDS", defaultCatalogRefreshInterval),
		CatalogFetchTimeout:    getSeconds("CATALOG_FETCH_TIMEOUT_SECONDS", defaultCatalogFetchTimeout),
	}

	// SubscriberKeyID (see Config.SubscriberKeyID) is the authoritative
	// identity when set, overriding SubscriberID/SigningKeyID rather than
	// requiring them separately duplicated in .env.
	if cfg.SubscriberKeyID != "" {
		if subscriberID, uniqueKeyID, _, err := beckn.SplitKeyID(cfg.SubscriberKeyID); err == nil {
			cfg.SubscriberID = subscriberID
			cfg.SigningKeyID = uniqueKeyID
		} else {
			slog.Warn("invalid SUBSCRIBER_KEY_ID, falling back to SUBSCRIBER_ID/SIGNING_KEY_ID", "error", err)
		}
	}

	return cfg
}

// getStringSlice parses a comma-separated env var into a trimmed,
// non-empty-entry slice. Returns nil (not an empty non-nil slice) when the
// var is unset or contains only empty/whitespace entries, so callers can
// treat "unset" and "explicitly empty" the same way (len(...) == 0).
func getStringSlice(key string) []string {
	v := os.Getenv(key)
	if v == "" {
		return nil
	}
	var out []string
	for part := range strings.SplitSeq(v, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func getString(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getSeconds(key string, fallback time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	seconds, err := strconv.Atoi(v)
	if err != nil {
		slog.Warn("invalid duration env var, using default", "key", key, "value", v, "default", fallback)
		return fallback
	}
	return time.Duration(seconds) * time.Second
}

func getInt(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		slog.Warn("invalid integer env var, using default", "key", key, "value", v, "default", fallback)
		return fallback
	}
	return n
}

func getInt64(key string, fallback int64) int64 {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		slog.Warn("invalid integer env var, using default", "key", key, "value", v, "default", fallback)
		return fallback
	}
	return n
}
