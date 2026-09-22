// Command discovery runs the Beckn v2 Discovery Service HTTP server.
package main

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/remiges-tech/logharbour/logharbour"
	"github.com/remiges-tushar/discovery-service/internal/beckn"
	"github.com/remiges-tushar/discovery-service/internal/config"
	"github.com/remiges-tushar/discovery-service/internal/dedicrawl"
	"github.com/remiges-tushar/discovery-service/internal/dispatch"
	"github.com/remiges-tushar/discovery-service/internal/handlers"
	"github.com/remiges-tushar/discovery-service/internal/logging"
	"github.com/remiges-tushar/discovery-service/internal/service"
	"github.com/remiges-tushar/discovery-service/internal/signing"
	"github.com/remiges-tushar/discovery-service/internal/worker"
)

func main() {
	cfg := config.Load()

	priority := logging.ParsePriority(cfg.LogLevel)
	logger := logging.New("discovery-service", priority, logging.IsDebugLevel(priority))

	// signer is *signing.Signer, concretely -- kept as a concrete type
	// (not an interface) so the nil-vs-real check below is unambiguous.
	// Assigning a nil *signing.Signer directly to an interface-typed
	// variable would produce a non-nil interface wrapping a nil pointer
	// (a classic Go footgun) -- reqSigner/ackSigner below are built
	// explicitly to stay genuinely nil when signer is, instead.
	signer := buildSigner(cfg, logger)
	var reqSigner dispatch.RequestSigner
	var ackSigner handlers.AckSigner
	if signer != nil {
		reqSigner = signer
		ackSigner = signer
	}

	pool := worker.NewPool(cfg.WorkerPoolSize, cfg.WorkerQueueSize, logger)
	dispatcher := dispatch.NewHTTPDispatcher(&http.Client{Timeout: cfg.DispatchClientTimeout}, cfg.OnDiscoverPath, reqSigner)

	catalogStore, stopCatalogRefresh := buildCatalogStore(cfg, logger)
	defer stopCatalogRefresh()

	svc := service.NewDiscoverService(dispatcher, pool, cfg.DispatchDeliveryTimeout, logger, cfg.SubscriberID, cfg.SubscriberURI, catalogStore)
	// ackSigner also signs the Ack/NACK's CounterSignature (body field)
	// and NFH-007's Signature response header -- same identity, same
	// key, every direction -- see handlers.AckSigner.
	discoverHandler := handlers.NewDiscoverHandler(svc, cfg.MaxRequestBodyBytes, logger, ackSigner)

	verifyDiscover := handlers.VerifyMiddleware(buildVerifier(cfg, logger), ackSigner, cfg.MaxRequestBodyBytes, logger)

	mux := http.NewServeMux()
	mux.Handle("POST /discover", verifyDiscover(discoverHandler))
	mux.Handle("GET /discover", verifyDiscover(http.HandlerFunc(discoverHandler.ServeSync)))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	// LoggingMiddleware wraps every route (including /healthz) so every
	// hit is visible in the console at the default log level — see
	// CONTEXT.md D10. DiscoverHandler additionally logs Beckn-specific
	// context (transactionId, messageId, ...) on top of this.
	var handler http.Handler = mux
	handler = handlers.LoggingMiddleware(logger)(handler)

	// Explicit timeouts: an http.Server with none set will hold a slow or
	// malicious client's connection (and the goroutine serving it) open
	// indefinitely — a classic resource-exhaustion footgun.
	srv := &http.Server{
		Addr:              cfg.Addr(),
		Handler:           handler,
		ReadHeaderTimeout: cfg.HTTPReadHeaderTimeout,
		ReadTimeout:       cfg.HTTPReadTimeout,
		WriteTimeout:      cfg.HTTPWriteTimeout,
		IdleTimeout:       cfg.HTTPIdleTimeout,
	}

	serverErr := make(chan error, 1)
	go func() {
		logger.LogActivity("discovery-service listening", map[string]any{
			"addr":     cfg.Addr(),
			"logLevel": cfg.LogLevel,
		})
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
			return
		}
		serverErr <- nil
	}()

	// Graceful shutdown: stop accepting new connections on SIGINT/SIGTERM
	// (Ctrl+C locally, or what `docker compose down` / a k8s pod eviction
	// actually sends), then drain both in-flight HTTP requests and queued
	// on_discover dispatch jobs before exiting — bounded by
	// cfg.ShutdownTimeout so a stuck job can't hang the process forever.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	select {
	case err := <-serverErr:
		if err != nil {
			logger.Crit().LogActivity("server exited unexpectedly", map[string]any{"error": err.Error()})
			os.Exit(1)
		}
		return
	case sig := <-stop:
		logger.LogActivity("shutdown signal received", map[string]any{"signal": sig.String()})
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Err().LogActivity("HTTP server did not shut down cleanly", map[string]any{"error": err.Error()})
	}
	if err := pool.Shutdown(shutdownCtx); err != nil {
		logger.Err().LogActivity("background dispatch pool did not drain in time", map[string]any{"error": err.Error()})
	}

	logger.LogActivity("discovery-service stopped", nil)
}

// buildSigner returns a signer for outbound requests (see CONTEXT.md
// D15), or nil if no subscriber identity is configured — signing without
// one would produce a Signature keyId with an empty first segment, which
// fails the spec's own pattern, and no receiver could attribute the
// signature to anyone anyway.
//
// cfg.SubscriberID/cfg.SigningKeyID are already resolved by config.Load()
// from whichever of SUBSCRIBER_ID/SIGNING_KEY_ID or the combined
// SUBSCRIBER_KEY_ID (this service's own dedicated identity) was
// configured.
func buildSigner(cfg config.Config, logger *logharbour.Logger) *signing.Signer {
	if cfg.SubscriberID == "" {
		logger.Warn().LogActivity("no subscriber identity configured (SUBSCRIBER_ID/SUBSCRIBER_KEY_ID) — outbound on_discover requests will be unsigned", nil)
		return nil
	}

	kp, ephemeral, err := signing.LoadOrGenerateKeyPair(cfg.SigningPrivateKeyBase64)
	if err != nil {
		logger.Crit().LogActivity("failed to initialize signing key", map[string]any{"error": err.Error()})
		os.Exit(1)
	}
	logger.LogActivity("signing key initialized", map[string]any{
		"ephemeral":       ephemeral,
		"publicKeyBase64": base64.StdEncoding.EncodeToString(kp.Public),
		"subscriberId":    cfg.SubscriberID,
		"keyId":           cfg.SigningKeyID,
	})
	if ephemeral {
		logger.Warn().LogActivity("using an ephemeral signing key — nothing can verify these signatures until a persistent SIGNING_PRIVATE_KEY_BASE64 is configured and its public key published to a registry", nil)
	}

	return signing.NewSigner(kp, cfg.SubscriberID, cfg.SigningKeyID, cfg.SigningValidity)
}

// buildVerifier returns a Verifier for inbound /discover requests, or nil
// if TRUSTED_CALLER_KEY_ID/TRUSTED_CALLER_PUBLIC_KEY_BASE64 aren't both
// configured — in which case VerifyMiddleware passes every request
// through unchecked (logged as a warning there and here).
//
// TrustedCallerKeyID is a public identifier (in this deployment, reused
// from BAP_KEY_ID as-is — see .env.example). TrustedCallerPublicKeyBase64
// is deliberately the caller's PUBLIC key only, never its private key:
// this service only ever needs to verify as the trusted caller, never
// sign as it, so it must never be given a credential capable of doing
// that — see CONTEXT.md D21 (an earlier version of this wrongly derived
// the public key from the BAP's PRIVATE key, which meant handing that
// private key to this service too).
func buildVerifier(cfg config.Config, logger *logharbour.Logger) *signing.Verifier {
	if cfg.TrustedCallerKeyID == "" || cfg.TrustedCallerPublicKeyBase64 == "" {
		logger.Warn().LogActivity("no trusted caller configured (TRUSTED_CALLER_KEY_ID/TRUSTED_CALLER_PUBLIC_KEY_BASE64) — /discover will accept requests without verifying their signature", nil)
		return nil
	}

	subscriberID, uniqueKeyID, _, err := beckn.SplitKeyID(cfg.TrustedCallerKeyID)
	if err != nil {
		logger.Crit().LogActivity("invalid TRUSTED_CALLER_KEY_ID", map[string]any{"error": err.Error()})
		os.Exit(1)
	}

	verifier, err := signing.NewVerifier(cfg.TrustedCallerPublicKeyBase64, subscriberID, uniqueKeyID)
	if err != nil {
		logger.Crit().LogActivity("failed to initialize signature verifier", map[string]any{"error": err.Error()})
		os.Exit(1)
	}
	logger.LogActivity("inbound signature verification enabled", map[string]any{
		"trustedSubscriberId": subscriberID,
		"trustedKeyId":        uniqueKeyID,
	})
	return verifier
}

// buildCatalogStore returns the CatalogStore /discover serves from, and a
// stop function to cancel any background refresh goroutine it started
// (call it during shutdown; safe to call even when nothing was started).
//
// With no CATALOG_INDEX_URLS configured, it returns the zero-config
// embedded demo catalog (service.EmbeddedCatalogStore), unchanged from
// M1. Otherwise it builds a dedicrawl.Store, does one synchronous Refresh
// now (bounded by CatalogFetchTimeout per HTTP call) so the first real
// requests already see fresh data if the configured index URLs are
// reachable, then starts background polling at CatalogRefreshInterval —
// see CONTEXT.md D19.
func buildCatalogStore(cfg config.Config, logger *logharbour.Logger) (service.CatalogStore, func()) {
	if len(cfg.CatalogIndexURLs) == 0 {
		return service.EmbeddedCatalogStore{}, func() {}
	}

	logger.LogActivity("catalog index urls configured, crawling for real catalog data", map[string]any{
		"indexUrls":       cfg.CatalogIndexURLs,
		"refreshInterval": cfg.CatalogRefreshInterval.String(),
	})

	crawler := dedicrawl.NewCrawler(&http.Client{Timeout: cfg.CatalogFetchTimeout}, logger, nil, nil, 0)
	store := dedicrawl.NewStore(crawler, cfg.CatalogIndexURLs, cfg.CatalogRefreshInterval, logger)

	initialCtx, initialCancel := context.WithTimeout(context.Background(), cfg.CatalogFetchTimeout*time.Duration(len(cfg.CatalogIndexURLs)+1))
	store.Refresh(initialCtx)
	initialCancel()

	refreshCtx, stopRefresh := context.WithCancel(context.Background())
	go store.Start(refreshCtx)

	return store, stopRefresh
}
