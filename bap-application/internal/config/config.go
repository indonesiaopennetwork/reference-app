package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

const (
	// DiscoverMethodGet selects ClientService.DiscoverSync — direct to
	// discovery-service, bypassing onix-bap.
	DiscoverMethodGet = "GET"
	// DiscoverMethodPost selects ClientService.Discover — the real async
	// Beckn contract, routed through onix-bap like every other action.
	DiscoverMethodPost = "POST"
)

// Config holds all runtime configuration for the BAP application.
type Config struct {
	AppPort string
	AppEnv  string

	DBHost     string
	DBPort     string
	DBName     string
	DBUser     string
	DBPassword string
	DBSSLMode  string

	LogLevel string

	// Beckn network identity for this BAP.
	BapID     string // Subscriber ID registered on the Beckn network
	BapURI    string // Callback URL the BAP exposes (where BPP sends on_* callbacks)
	NetworkID string // Beckn network identifier

	// BPP identity — used inside the Beckn context fields (bppId / bppUri).
	BppID  string // BPP subscriber ID  (context.bppId)
	BppURI string // BPP receiver URL   (context.bppUri, e.g. https://bpptest.remiges.tech/bpp/receiver)

	// BapCallerURL is the ONIX BAP caller base URL the BAP actually POSTs to.
	// The action is appended: <BapCallerURL>/select, <BapCallerURL>/init,
	// <BapCallerURL>/discover, etc. — clientsvc.ClientService.Discover (the
	// async path, POST /api/v1/discover) goes through this same
	// adapter-mediated path (sendToBPP) as every other action; onix-bap
	// signs the outbound request and its bapTxnReceiver verifies the
	// on_discover callback against the network registry, same as any
	// other on_* callback. This is intentionally different from BppURI,
	// which stays in the Beckn context.
	BapCallerURL string

	// CDSDiscoverURL is the full URL of discovery-service's own /discover
	// endpoint, called DIRECTLY — bypassing onix-bap entirely. Used only
	// by clientsvc.ClientService.DiscoverSync (selected when
	// DiscoverMethod is "GET"), kept alongside the async, onix-routed
	// Discover ("POST") as a fast, reliable fallback: it doesn't depend
	// on discovery-service's identity being resolvable through onix-bap's
	// network registry (see discovery-service/CONTEXT.md D20/D21), at the
	// cost of not being the spec's real adapter-mediated topology.
	// Example: http://discovery-service:8087/discover. Leave blank in
	// environments without a live discovery service (returns an empty
	// catalog list, only relevant when DiscoverMethod is "GET").
	CDSDiscoverURL string

	// DiscoverMethod selects which of the two discover implementations
	// GET /api/v1/discover actually calls — exactly one is active at a
	// time. "GET" (DiscoverMethodGet, the default) → DiscoverSync, direct
	// to discovery-service. "POST" (DiscoverMethodPost) → Discover, the
	// real async contract via onix-bap. Named after the HTTP method each
	// implementation used before this switch existed, not the method the
	// browser sends — the browser-facing route is always a plain GET
	// either way. Env: DISCOVER_METHOD.
	DiscoverMethod string

	// BapPrivateKey is the base64-encoded Ed25519 private key (64 bytes) or seed (32 bytes)
	// used to sign outbound Beckn requests. Required when CDSDiscoverURL is set
	// (DiscoverSync signs for itself, since it bypasses onix-bap's own signing).
	// Env: BAP_PRIVATE_KEY
	BapPrivateKey string

	// BapKeyID is the Beckn signing key identifier in the format
	// "<subscriberID>|<uniqueKeyID>|ed25519". Env: BAP_KEY_ID
	BapKeyID string

	// ZitadelIssuer/ZitadelInternalURL configure the optional buyer-token
	// verifier (see internal/auth) -- the same Zitadel app that gates the
	// seller flow in bpp-application. Unlike bpp, a missing/unreachable
	// Zitadel here is not fatal: buyer auth is a non-blocking, best-effort
	// identity resolution, not a hard requirement to serve traffic.
	ZitadelIssuer      string
	ZitadelInternalURL string

	// FinderFee* is the fee this BAP asks for bringing an order, proposed in
	// init as finderFee on the settlement object (package settlement): FinderFeeType
	// PERCENT (of FinderFeeAppliesTo: ITEM_TOTAL | ORDER_TOTAL) or AMOUNT.
	// The PJP pays it to this BAP after reconciliation.
	// Env: FINDER_FEE_TYPE, FINDER_FEE_VALUE, FINDER_FEE_APPLIES_TO.
	FinderFeeType      string
	FinderFeeValue     float64
	FinderFeeAppliesTo string

	// ReconcileBeforeWindow lets every open paid contract be sent to
	// /reconcile right away, without waiting for its settlement window to
	// pass (for testing). Set by RECONCILE_WAIT_FOR_SETTLEMENT_WINDOW=false;
	// the default (true) waits. It only affects sending: a BAP answering a
	// /reconcile still rejects contracts whose window hasn't passed.
	ReconcileBeforeWindow bool

	// SettlementAccount* is this BAP's bank account for PJP payouts (its
	// finder fee), declared in init as a settlementAccounts entry on the
	// settlement object with holderRole BAP. Env: SETTLEMENT_BANK_CODE,
	// SETTLEMENT_ACCOUNT_NUMBER, SETTLEMENT_ACCOUNT_NAME.
	SettlementBankCode      string
	SettlementAccountNumber string
	SettlementAccountName   string
}

// Load reads all config values from the environment and returns a validated Config.
func Load() (*Config, error) {
	cfg := &Config{
		AppPort: getEnv("APP_PORT", "8083"),
		AppEnv:  getEnv("APP_ENV", "development"),

		DBHost:     getEnv("DB_HOST", "localhost"),
		DBPort:     getEnv("DB_PORT", "5432"),
		DBName:     getEnv("DB_NAME", "bap"),
		DBUser:     getEnv("DB_USER", "postgres"),
		DBPassword: os.Getenv("DB_PASSWORD"),
		DBSSLMode:  getEnv("DB_SSLMODE", "disable"),

		LogLevel: getEnv("LOG_LEVEL", "info"),

		BapID:     os.Getenv("BAP_ID"),
		BapURI:    os.Getenv("BAP_URI"),
		NetworkID: os.Getenv("NETWORK_ID"),

		BppID:        os.Getenv("BPP_ID"),
		BppURI:       os.Getenv("BPP_URI"),
		BapCallerURL: getEnv("BAP_CALLER_URL", os.Getenv("ADAPTER_URL")),

		CDSDiscoverURL: os.Getenv("CDS_DISCOVER_URL"),
		DiscoverMethod: strings.ToUpper(getEnv("DISCOVER_METHOD", DiscoverMethodGet)),

		BapPrivateKey: os.Getenv("BAP_PRIVATE_KEY"),
		BapKeyID:      os.Getenv("BAP_KEY_ID"),

		ZitadelIssuer:      getEnv("ZITADEL_ISSUER", "http://localhost:8090"),
		ZitadelInternalURL: os.Getenv("ZITADEL_INTERNAL_URL"),

		FinderFeeType:      strings.ToUpper(getEnv("FINDER_FEE_TYPE", "PERCENT")),
		FinderFeeAppliesTo: strings.ToUpper(getEnv("FINDER_FEE_APPLIES_TO", "ITEM_TOTAL")),

		ReconcileBeforeWindow:   strings.EqualFold(os.Getenv("RECONCILE_WAIT_FOR_SETTLEMENT_WINDOW"), "false"),
		SettlementBankCode:      getEnv("SETTLEMENT_BANK_CODE", "MANDIRI"),
		SettlementAccountNumber: getEnv("SETTLEMENT_ACCOUNT_NUMBER", "1370000000001"),
		SettlementAccountName:   os.Getenv("SETTLEMENT_ACCOUNT_NAME"),
	}

	fee, err := strconv.ParseFloat(getEnv("FINDER_FEE_VALUE", "3"), 64)
	if err != nil || fee < 0 {
		return nil, fmt.Errorf("FINDER_FEE_VALUE must be a non-negative number, got %q", os.Getenv("FINDER_FEE_VALUE"))
	}
	cfg.FinderFeeValue = fee
	if cfg.SettlementAccountName == "" {
		cfg.SettlementAccountName = cfg.BapID
	}

	if cfg.ZitadelInternalURL == "" {
		cfg.ZitadelInternalURL = cfg.ZitadelIssuer
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

// DatabaseURL builds a pgx-compatible DSN from the config fields.
func (c *Config) DatabaseURL() string {
	return fmt.Sprintf(
		"host=%s port=%s dbname=%s user=%s password=%s sslmode=%s",
		c.DBHost, c.DBPort, c.DBName, c.DBUser, c.DBPassword, c.DBSSLMode,
	)
}

// DBPortInt returns the DB port as an integer.
func (c *Config) DBPortInt() int {
	p, _ := strconv.Atoi(c.DBPort)
	return p
}

// IsProduction reports whether the application is running in production mode.
func (c *Config) IsProduction() bool {
	return c.AppEnv == "production"
}

func (c *Config) validate() error {
	if c.DBName == "" {
		return fmt.Errorf("DB_NAME is required")
	}
	if c.DBUser == "" {
		return fmt.Errorf("DB_USER is required")
	}
	if c.BapID == "" {
		return fmt.Errorf("BAP_ID is required")
	}
	if c.BapURI == "" {
		return fmt.Errorf("BAP_URI is required")
	}
	if c.NetworkID == "" {
		return fmt.Errorf("NETWORK_ID is required")
	}
	if c.BppID == "" {
		return fmt.Errorf("BPP_ID is required")
	}
	if c.BppURI == "" {
		return fmt.Errorf("BPP_URI is required")
	}
	if c.BapCallerURL == "" {
		return fmt.Errorf("BAP_CALLER_URL is required")
	}
	if c.DiscoverMethod != DiscoverMethodGet && c.DiscoverMethod != DiscoverMethodPost {
		return fmt.Errorf("DISCOVER_METHOD must be %q or %q, got %q", DiscoverMethodGet, DiscoverMethodPost, c.DiscoverMethod)
	}
	return nil
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
