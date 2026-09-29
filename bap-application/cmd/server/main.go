package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
	"github.com/remiges-tech/logharbour/logharbour"

	dbsqlc "github.com/ion/winroom/bap/db/sqlc"
	"github.com/ion/winroom/bap/internal/auth"
	"github.com/ion/winroom/bap/internal/config"
	"github.com/ion/winroom/bap/internal/server"
	"github.com/ion/winroom/bap/internal/sweep"
)

func main() {
	if err := godotenv.Load(); err != nil {
		fmt.Fprintln(os.Stderr, "no .env file found, using process environment")
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		os.Exit(1)
	}

	// --- Logging ---
	lctx := logharbour.NewLoggerContext(logLevel(cfg.LogLevel))
	lh := logharbour.NewLogger(lctx, "bap", os.Stdout)
	lh.WithModule("main").Log("BAP starting")

	// --- Database ---
	poolCfg, err := pgxpool.ParseConfig(cfg.DatabaseURL())
	if err != nil {
		lh.WithModule("main").Crit().Error(err).Log("failed to parse database URL")
		os.Exit(1)
	}

	ctx := context.Background()
	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		lh.WithModule("main").Crit().Error(err).Log("failed to create database pool")
		os.Exit(1)
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		lh.WithModule("main").Crit().Error(err).Log("database ping failed")
		os.Exit(1)
	}
	lh.WithModule("main").Log("database connected")

	// --- BAP identity seed ---
	if err := registerBAPIdentity(ctx, pool, cfg); err != nil {
		lh.WithModule("main").Crit().Error(err).Log("failed to register BAP identity")
		os.Exit(1)
	}
	lh.WithModule("main").Log("BAP identity registered: " + cfg.BapID)

	// --- Zitadel token verifier (buyer auth, optional) --------------------
	// Unlike bpp-application, a failure here is not fatal: buyer login is a
	// frontend-only gate (see bap-frontend/src/auth.js) and order-history
	// scoping is best-effort, so BAP should keep serving traffic even if
	// Zitadel is unreachable -- requests just won't resolve a buyer identity.
	verifier, err := newAuthVerifierWithRetry(ctx, cfg.ZitadelIssuer, cfg.ZitadelInternalURL, 10, 3*time.Second)
	if err != nil {
		lh.WithModule("main").Warn().Error(err).Log("failed to reach Zitadel for token verification; continuing without buyer auth")
		verifier = nil
	} else {
		lh.WithModule("main").Log("Zitadel token verifier ready")
	}

	// --- HTTP server ---
	if cfg.IsProduction() {
		gin.SetMode(gin.ReleaseMode)
	}

	r := gin.New()
	r.Use(gin.Recovery())

	server.RegisterRoutes(r, pool, cfg, lh, verifier)

	addr := fmt.Sprintf(":%s", cfg.AppPort)
	lh.WithModule("main").Log("server listening on " + addr)

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	// Recovers transactions abandoned mid-update (see internal/sweep's own
	// package doc) -- a single background goroutine, stopped on shutdown
	// alongside everything else.
	sweepCtx, stopSweep := context.WithCancel(context.Background())
	defer stopSweep()
	go sweep.RunLoop(sweepCtx, pool, lh, sweep.DefaultInterval, sweep.DefaultStaleAfter)

	go func() {
		if err := r.Run(addr); err != nil {
			lh.WithModule("main").Crit().Error(err).Log("server stopped")
			os.Exit(1)
		}
	}()

	<-quit
	lh.WithModule("main").Log("shutdown signal received, exiting")
}

// registerBAPIdentity seeds the bap_identities row on startup.
func registerBAPIdentity(ctx context.Context, pool *pgxpool.Pool, cfg *config.Config) error {
	q := dbsqlc.New(pool)
	return q.UpsertBAPIdentity(ctx, dbsqlc.UpsertBAPIdentityParams{
		BapID:      cfg.BapID,
		BapUri:     cfg.BapURI,
		NetworkIds: []string{cfg.NetworkID},
	})
}

// newAuthVerifierWithRetry retries Zitadel's OIDC discovery fetch, since a
// freshly-started Zitadel container can take longer to finish its
// first-instance bootstrap than docker-compose's healthcheck-based
// depends_on waits for.
func newAuthVerifierWithRetry(ctx context.Context, issuer, internalURL string, attempts int, delay time.Duration) (*auth.Verifier, error) {
	var lastErr error
	for i := 0; i < attempts; i++ {
		v, err := auth.NewVerifier(ctx, issuer, internalURL)
		if err == nil {
			return v, nil
		}
		lastErr = err
		time.Sleep(delay)
	}
	return nil, lastErr
}

// logLevel maps the LOG_LEVEL env string to a logharbour priority constant.
func logLevel(level string) logharbour.LogPriority {
	switch level {
	case "debug2":
		return logharbour.Debug2
	case "debug1":
		return logharbour.Debug1
	case "debug0":
		return logharbour.Debug0
	case "warn":
		return logharbour.Warn
	case "err":
		return logharbour.Err
	case "crit":
		return logharbour.Crit
	case "sec":
		return logharbour.Sec
	default:
		return logharbour.Info
	}
}
