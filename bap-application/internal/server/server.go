package server

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/remiges-tech/logharbour/logharbour"

	"github.com/ion/winroom/bap/internal/auth"
	"github.com/ion/winroom/bap/internal/clientsvc"
	"github.com/ion/winroom/bap/internal/config"
	"github.com/ion/winroom/bap/internal/discoverwait"
	oncancelsvc "github.com/ion/winroom/bap/internal/on_cancel_svc"
	onconfirmsvc "github.com/ion/winroom/bap/internal/on_confirm_svc"
	ondiscoversvc "github.com/ion/winroom/bap/internal/on_discover_svc"
	oninitsvc "github.com/ion/winroom/bap/internal/on_init_svc"
	onratesvc "github.com/ion/winroom/bap/internal/on_rate_svc"
	onselectsvc "github.com/ion/winroom/bap/internal/on_select_svc"
	onstatussvc "github.com/ion/winroom/bap/internal/on_status_svc"
	onsupportsvc "github.com/ion/winroom/bap/internal/on_support_svc"
	onupdatesvc "github.com/ion/winroom/bap/internal/on_update_svc"
	reconcilesvc "github.com/ion/winroom/bap/internal/reconcile_svc"
)

// RegisterRoutes wires all BAP routes onto the given Gin engine. verifier
// may be nil (Zitadel unreachable at startup) -- see internal/auth.OptionalAuth.
func RegisterRoutes(r *gin.Engine, pool *pgxpool.Pool, cfg *config.Config, lh *logharbour.Logger, verifier *auth.Verifier) {
	// Health check
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	// discoverRegistry bridges the on_discover webhook below back to the
	// synchronous /api/v1/discover request that triggered it -- see
	// internal/discoverwait's package doc. One instance shared by both
	// sides for the process lifetime.
	discoverRegistry := discoverwait.NewRegistry()

	// Beckn webhook endpoints — BPP (via ONIX adapter) POSTs on_* callbacks here.
	webhook := r.Group("/api/webhook")
	{
		onDiscoverH := ondiscoversvc.NewOnDiscoverHandler(pool, cfg, lh, discoverRegistry)
		webhook.POST("/on_discover", onDiscoverH.Handle)

		onSelectH := onselectsvc.NewOnSelectHandler(pool, cfg, lh)
		webhook.POST("/on_select", onSelectH.Handle)

		onInitH := oninitsvc.NewOnInitHandler(pool, cfg, lh)
		webhook.POST("/on_init", onInitH.Handle)

		onConfirmH := onconfirmsvc.NewOnConfirmHandler(pool, cfg, lh)
		webhook.POST("/on_confirm", onConfirmH.Handle)

		onStatusH := onstatussvc.NewHandler(pool, cfg, lh)
		webhook.POST("/on_status", onStatusH.Handle)

		onCancelH := oncancelsvc.NewHandler(pool, cfg, lh)
		webhook.POST("/on_cancel", onCancelH.Handle)

		onUpdateH := onupdatesvc.NewHandler(pool, cfg, lh)
		webhook.POST("/on_update", onUpdateH.Handle)

		onRateH := onratesvc.NewHandler(pool, cfg, lh)
		webhook.POST("/on_rate", onRateH.Handle)

		onSupportH := onsupportsvc.NewHandler(pool, cfg, lh)
		webhook.POST("/on_support", onSupportH.Handle)

		// /reconcile — a BPP's settlement batch (ion-specs
		// core/api/v2.0.0/ion-additional.yaml), answered with /on_reconcile; and
		// /on_reconcile — a BPP's answer to this BAP's own /reconcile.
		reconcileH := reconcilesvc.NewHandler(pool, cfg, lh)
		webhook.POST("/reconcile", reconcileH.Handle)
		webhook.POST("/on_reconcile", reconcileH.HandleOnReconcile)
	}

	// Settlement reconciliation (the /reconciliation page) -- open, no login
	// or key required: the overview, and sending this BAP's /reconcile.
	reconAdmin := reconcilesvc.NewHandler(pool, cfg, lh)
	r.GET("/api/v1/reconciliation", reconAdmin.HandleAdminOverview)
	r.POST("/api/v1/reconciliation/send", reconAdmin.HandleSend)

	// Client-facing APIs for the React frontend. OptionalAuth resolves a
	// buyer identity from the bearer token when present, but never rejects
	// a request for lacking one -- see internal/auth.
	v1 := r.Group("/api/v1")
	v1.Use(auth.OptionalAuth(verifier))
	{
		clientH := clientsvc.NewClientHandler(pool, cfg, lh, discoverRegistry)
		v1.GET("/catalog", clientH.HandleCatalog)
		// Browser-facing contract is always GET; DISCOVER_METHOD (see
		// config.Config.DiscoverMethod) picks which implementation serves
		// it underneath -- exactly one active at a time. See
		// clientsvc.ClientHandler.HandleDiscover.
		v1.GET("/discover", clientH.HandleDiscover)
		v1.POST("/select", clientH.HandleSelect)
		v1.POST("/init", clientH.HandleInit)
		v1.POST("/confirm", clientH.HandleConfirm)
		v1.GET("/status/:id", clientH.HandleStatus)
		v1.POST("/request-status", clientH.HandleRequestStatus)
		v1.POST("/cancel", clientH.HandleCancel)
		v1.POST("/cancel-items", clientH.HandleCancelItems)
		v1.POST("/return-items", clientH.HandleReturnItems)
		v1.POST("/update", clientH.HandleUpdate)
		v1.POST("/rate", clientH.HandleRate)
		v1.POST("/support", clientH.HandleSupport)
		v1.GET("/orders", clientH.HandleListOrders)
		v1.GET("/orders/:id", clientH.HandleGetOrder)
		v1.GET("/messages", clientH.HandleListMessages)
	}
}
