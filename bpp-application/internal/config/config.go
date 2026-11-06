package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Config holds all runtime configuration for the BPP application.
// Values are read from environment variables (populated via .env in development).
type Config struct {
	AppPort string
	AppEnv  string

	DBHost     string
	DBPort     string
	DBName     string
	DBUser     string
	DBPassword string
	DBSSLMode  string

	// LogLevel controls the minimum priority emitted by logharbour.
	// Accepted values: debug2 | debug1 | debug0 | info | warn | err | crit | sec
	LogLevel string

	// Beckn network identity for this BPP.
	BppID     string // Subscriber ID registered on the Beckn network (e.g. "bpptest1.remiges.tech")
	BppURI    string // Callback URL the BPP exposes to the network (e.g. "https://bpptest1.remiges.tech/bpp/receiver")
	NetworkID string // Beckn network identifier (e.g. "beckn.one/ion-retail")

	// BppCallerURL is the ONIX BPP caller base URL used for outbound on_* callbacks.
	// Example: https://bpptest.remiges.tech/bpp/caller
	// If empty, the service derives it from BppURI by replacing /bpp/receiver with /bpp/caller.
	BppCallerURL string

	// SettlementBasis / SettlementWindow are this seller's default payout
	// terms, for an offer published without its own policies.settlement
	// (package offerterms): the event that starts the payout (ON_CONFIRMATION
	// | AFTER_SHIPMENT | AFTER_DELIVERY | AFTER_RETURN_WINDOW) and the ISO 8601
	// duration after it within which the PJP pays out. Env: SETTLEMENT_BASIS,
	// SETTLEMENT_WINDOW.
	SettlementBasis  string
	SettlementWindow string

	// Commission* is this seller's default commission (finder fee) paid to
	// the buyer app, for an offer published without its own: CommissionType
	// PERCENT (of CommissionAppliesTo: ITEM_TOTAL | ORDER_TOTAL) or AMOUNT
	// (per unit sold). Env: COMMISSION_TYPE, COMMISSION_VALUE,
	// COMMISSION_APPLIES_TO.
	CommissionType      string
	CommissionValue     float64
	CommissionAppliesTo string

	// ReconcileBeforeWindow lets every open paid contract be sent to
	// /reconcile right away, without waiting for its settlement window to
	// pass (for testing). Set by RECONCILE_WAIT_FOR_SETTLEMENT_WINDOW=false;
	// the default (true) waits. It only affects sending: a BAP answering a
	// /reconcile still rejects contracts whose window hasn't passed.
	ReconcileBeforeWindow bool

	// SettlementAccount* is this BPP's bank account for PJP payouts (the
	// order proceeds), added in on_init as a settlementAccounts entry on the
	// settlement object with holderRole BPP. Env: SETTLEMENT_BANK_CODE,
	// SETTLEMENT_ACCOUNT_NUMBER, SETTLEMENT_ACCOUNT_NAME.
	SettlementBankCode      string
	SettlementAccountNumber string
	SettlementAccountName   string

	// CDSPublishURL is the catalog discovery service endpoint that receives catalog/publish requests.
	CDSPublishURL string

	// CatalogBaseURL is the public, stable domain the DeDi discovery chain is
	// served on (same value onix-catalog-publish uses for its own
	// catalogBaseURL — see terraform/domain-mapping.tf). Optional: this
	// whole custom-domain feature is optional (see README.md), so an empty
	// value here just means PublishedBppURI() falls back to BppURI.
	CatalogBaseURL string

	// CatalogFilesBaseURL is where published catalog/index files can
	// actually be fetched from, for proxying "view index/catalog file"
	// requests server-side. Usually the same domain as CatalogBaseURL (in
	// the cloud, one public domain serves both the BPP receiver and the
	// static files) -- but kept as a separate field since locally they
	// diverge (a plain static file server, not the BPP itself). Falls back
	// to CatalogBaseURL when unset; see CatalogFilesURL().
	CatalogFilesBaseURL string

	// BppPrivateKey is the base64-encoded Ed25519 private key (64 bytes) or seed (32 bytes)
	// used to sign outbound Beckn requests. Required when CDSPublishURL is set.
	// Env: BPP_PRIVATE_KEY
	BppPrivateKey string

	// BppKeyID is the Beckn signing key identifier in the format
	// "<subscriberID>|<uniqueKeyID>|ed25519". Env: BPP_KEY_ID
	BppKeyID string

	// ZitadelIssuer is Zitadel's externally-configured issuer (its
	// ZITADEL_EXTERNALDOMAIN/PORT, e.g. http://localhost:8090) -- this is
	// the "iss" claim value tokens actually carry, and the Host header
	// Zitadel's own instance-resolution needs on every request (it's
	// host-header-based multi-tenancy; a mismatched Host means "instance
	// not found" even from a request that reaches the right container).
	// Env: ZITADEL_ISSUER
	ZitadelIssuer string

	// ZitadelInternalURL is the address this backend actually connects to
	// (e.g. http://zitadel:8080 on the docker-compose network) when that
	// differs from ZitadelIssuer -- the request still carries a Host
	// header of ZitadelIssuer's host (see internal/auth.NewVerifier), so
	// Zitadel resolves the same instance either way. Defaults to
	// ZitadelIssuer when unset (e.g. running this binary outside Docker,
	// where "the address to connect to" and "the externally-configured
	// issuer" are the same host). Env: ZITADEL_INTERNAL_URL
	ZitadelInternalURL string

	// LandingPageOrigin is the origin (scheme://host[:port]) landing-page
	// is served from — the only origin allowed to call POST
	// /api/v1/auth/bootstrap directly (every other frontend route reaches
	// this API through its own same-origin nginx proxy, needing no CORS
	// exception). Env: LANDING_PAGE_ORIGIN
	LandingPageOrigin string

	// GrievanceAutoAcknowledge acknowledges a buyer grievance (on_support
	// ACKNOWLEDGED) as soon as it arrives. With it off, the seller
	// acknowledges from the dashboard. GRIEVANCE_AUTO_ACKNOWLEDGE, default true.
	GrievanceAutoAcknowledge bool
}

// Load reads all config values from the environment and returns a validated Config.
func Load() (*Config, error) {
	cfg := &Config{
		AppPort: getEnv("APP_PORT", "8080"),
		AppEnv:  getEnv("APP_ENV", "development"),

		DBHost:     getEnv("DB_HOST", "localhost"),
		DBPort:     getEnv("DB_PORT", "5432"),
		DBName:     getEnv("DB_NAME", "bpp"),
		DBUser:     getEnv("DB_USER", "postgres"),
		DBPassword: os.Getenv("DB_PASSWORD"),
		DBSSLMode:  getEnv("DB_SSLMODE", "disable"),

		LogLevel: getEnv("LOG_LEVEL", "info"),

		BppID:        os.Getenv("BPP_ID"),
		BppURI:       os.Getenv("BPP_URI"),
		NetworkID:    os.Getenv("NETWORK_ID"),
		BppCallerURL: os.Getenv("BPP_CALLER_URL"),

		SettlementBasis:  NormalizeSettlementBasis(getEnv("SETTLEMENT_BASIS", "AFTER_DELIVERY")),
		SettlementWindow: getEnv("SETTLEMENT_WINDOW", "P1D"),

		CommissionType:      strings.ToUpper(getEnv("COMMISSION_TYPE", "PERCENT")),
		CommissionAppliesTo: strings.ToUpper(getEnv("COMMISSION_APPLIES_TO", "ITEM_TOTAL")),

		ReconcileBeforeWindow:   strings.EqualFold(os.Getenv("RECONCILE_WAIT_FOR_SETTLEMENT_WINDOW"), "false"),
		SettlementBankCode:      getEnv("SETTLEMENT_BANK_CODE", "BCA"),
		SettlementAccountNumber: getEnv("SETTLEMENT_ACCOUNT_NUMBER", "8730000000001"),
		SettlementAccountName:   os.Getenv("SETTLEMENT_ACCOUNT_NAME"),

		CDSPublishURL:       os.Getenv("CDS_PUBLISH_URL"),
		CatalogBaseURL:      os.Getenv("CATALOG_BASE_URL"),
		CatalogFilesBaseURL: os.Getenv("CATALOG_FILES_BASE_URL"),

		BppPrivateKey: os.Getenv("BPP_PRIVATE_KEY"),
		BppKeyID:      os.Getenv("BPP_KEY_ID"),

		ZitadelIssuer:            getEnv("ZITADEL_ISSUER", "http://localhost:8090"),
		ZitadelInternalURL:       os.Getenv("ZITADEL_INTERNAL_URL"),
		LandingPageOrigin:        getEnv("LANDING_PAGE_ORIGIN", "http://localhost:3002"),
		GrievanceAutoAcknowledge: getEnv("GRIEVANCE_AUTO_ACKNOWLEDGE", "true") != "false",
	}
	if cfg.ZitadelInternalURL == "" {
		cfg.ZitadelInternalURL = cfg.ZitadelIssuer
	}
	if cfg.SettlementAccountName == "" {
		cfg.SettlementAccountName = cfg.BppID
	}
	commission, err := strconv.ParseFloat(getEnv("COMMISSION_VALUE", "3"), 64)
	if err != nil || commission < 0 || (cfg.CommissionType == "PERCENT" && commission > 100) {
		return nil, fmt.Errorf("COMMISSION_VALUE must be a number from 0 (and at most 100 for PERCENT), got %q", os.Getenv("COMMISSION_VALUE"))
	}
	cfg.CommissionValue = commission
	if cfg.CommissionType != "PERCENT" && cfg.CommissionType != "AMOUNT" {
		return nil, fmt.Errorf("COMMISSION_TYPE must be PERCENT or AMOUNT, got %q", cfg.CommissionType)
	}
	if cfg.CommissionAppliesTo != "ITEM_TOTAL" && cfg.CommissionAppliesTo != "ORDER_TOTAL" {
		return nil, fmt.Errorf("COMMISSION_APPLIES_TO must be ITEM_TOTAL or ORDER_TOTAL, got %q", cfg.CommissionAppliesTo)
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

// PublishedBppURI returns the bppUri to embed in catalog/publish payloads —
// the stable public domain (CatalogBaseURL) with the receiver path appended,
// not BppURI (the Cloud Run URL used for actual select/init/confirm
// routing). Falls back to BppURI if CatalogBaseURL isn't configured.
func (c *Config) PublishedBppURI() string {
	if c.CatalogBaseURL == "" {
		return c.BppURI
	}
	return c.CatalogBaseURL + "/bpp/receiver/"
}

// CatalogFilesURL returns where published catalog/index files can actually
// be fetched from -- CatalogFilesBaseURL if set, else CatalogBaseURL (the
// cloud default, where one public domain serves both roles).
func (c *Config) CatalogFilesURL() string {
	if c.CatalogFilesBaseURL != "" {
		return c.CatalogFilesBaseURL
	}
	return c.CatalogBaseURL
}

func (c *Config) validate() error {
	if c.DBName == "" {
		return fmt.Errorf("DB_NAME is required")
	}
	if c.DBUser == "" {
		return fmt.Errorf("DB_USER is required")
	}
	if c.BppID == "" {
		return fmt.Errorf("BPP_ID is required")
	}
	if c.BppURI == "" {
		return fmt.Errorf("BPP_URI is required")
	}
	if c.NetworkID == "" {
		return fmt.Errorf("NETWORK_ID is required")
	}
	return nil
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// legacySettlementBasis maps the names used before ION's TradeSettlement
// schema defined settlementBasis to ION's.
var legacySettlementBasis = map[string]string{
	"DELIVERY":             "AFTER_DELIVERY",
	"SHIPMENT":             "AFTER_SHIPMENT",
	"RETURN_WINDOW_EXPIRY": "AFTER_RETURN_WINDOW",
}

// NormalizeSettlementBasis returns ION's name for a settlement basis, so a
// config or stored settlement that still uses an old name keeps working.
func NormalizeSettlementBasis(basis string) string {
	if ion, ok := legacySettlementBasis[basis]; ok {
		return ion
	}
	return basis
}
