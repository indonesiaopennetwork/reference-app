package qrsvc

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/ion/winroom/bap/internal/auth"
)

// Handler serves /api/v1/qr.
type Handler struct {
	svc *Service
}

// NewHandler builds the QR handler.
func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Register wires the QR routes onto the /api/v1 group (which already runs
// auth.OptionalAuth).
func (h *Handler) Register(v1 *gin.RouterGroup) {
	qr := v1.Group("/qr")
	qr.GET("/settings", h.HandleSettings)
	qr.GET("/packets/:id", h.HandlePacket)
	qr.POST("/resolve", h.HandleResolve)
	qr.POST("/quote", h.HandleQuote)
	qr.POST("/checkout", h.HandleCheckout)
	qr.POST("/orders/:id/place", h.HandlePlaceOrder)
	qr.GET("/orders/:id", h.HandleOrder)
	qr.POST("/orders/:id/status", h.HandleRefreshStatus)
	qr.POST("/orders/:id/cancel", h.HandleCancel)
	qr.POST("/orders/:id/rate", h.HandleRate)
	qr.POST("/orders/:id/support", h.HandleSupport)
	qr.GET("/orders/:id/beckn", h.HandleBecknMessages)
	qr.POST("/coverage", h.HandleCoverage)
	qr.GET("/products", h.HandleProducts)
	qr.GET("/packet-orders/:packet_id", h.HandlePacketOrders)
	qr.Any("/admin/*path", h.HandleAdmin)
}

func subject(c *gin.Context) *string {
	if s, ok := auth.SubjectFromContext(c); ok && s != "" {
		return &s
	}
	return nil
}

func guestToken(c *gin.Context) string { return c.GetHeader(GuestTokenHeader) }

// fail writes err as an ErrorResponse.
func fail(c *gin.Context, svc *Service, err error) {
	var ae *apiError
	if errors.As(err, &ae) {
		resp := ErrorResponse{Error: ae.msg, Code: ae.code}
		if ae.data != nil {
			resp.Data, _ = json.Marshal(ae.data)
		}
		c.JSON(ae.status, resp)
		return
	}
	svc.log().Err().Error(err).Log("QR request failed")
	c.JSON(http.StatusInternalServerError, ErrorResponse{Error: "Something went wrong, try again", Code: "INTERNAL"})
}

func bind(c *gin.Context, v any) bool {
	if err := c.ShouldBindJSON(v); err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{Error: err.Error(), Code: "INVALID_REQUEST"})
		return false
	}
	return true
}

// SettingsResponse is what the QR pages need to know about this BAP.
type SettingsResponse struct {
	// The scan simulator's demo scenarios are available.
	DemoEnabled bool `json:"demo_enabled"`
}

// HandleSettings tells the QR pages which options this BAP has.
//
//	@Summary		QR checkout settings
//	@ID				qrSettings
//	@Tags			qr
//	@Produce		json
//	@Success		200	{object}	SettingsResponse
//	@Router			/api/v1/qr/settings [get]
func (h *Handler) HandleSettings(c *gin.Context) {
	c.JSON(http.StatusOK, SettingsResponse{DemoEnabled: h.svc.cfg.QRDemoEnabled})
}

// HandlePacket returns a packet's public view.
//
//	@Summary		Read a QR packet
//	@ID				qrPacket
//	@Description	The packet a QR code points to (qr-service, read with this BAP's service key), in any status. The buyer page shows it while resolve runs.
//	@Tags			qr
//	@Produce		json
//	@Param			id	path		string	true	"Packet id"
//	@Success		200	{object}	PacketView
//	@Failure		404	{object}	ErrorResponse	"PACKET_NOT_FOUND"
//	@Failure		502	{object}	ErrorResponse	"QR_SERVICE_UNAVAILABLE"
//	@Failure		503	{object}	ErrorResponse	"QR_NOT_CONFIGURED"
//	@Router			/api/v1/qr/packets/{id} [get]
func (h *Handler) HandlePacket(c *gin.Context) {
	p, err := h.svc.Packet(c.Request.Context(), c.Param("id"))
	if err != nil {
		fail(c, h.svc, err)
		return
	}
	c.JSON(http.StatusOK, p)
}

// HandleResolve finds the merchants for a scanned packet.
//
//	@Summary		Resolve a scanned QR packet
//	@ID				qrResolve
//	@Description	Reads the packet, runs discover around its node and ranks the merchants that stock every item. One merchant: FAST_CHECKOUT (the buyer goes straight to choosing items). Several: SELECT_MERCHANT. None: NO_MERCHANT with the reason. Returns the guest token later calls need.
//	@Tags			qr
//	@Accept			json
//	@Produce		json
//	@Param			request	body		ResolveRequest	true	"Request body"
//	@Success		200		{object}	ResolveResponse
//	@Failure		404		{object}	ErrorResponse	"PACKET_NOT_FOUND"
//	@Failure		409		{object}	ErrorResponse	"PACKET_NOT_ACTIVE"
//	@Failure		502		{object}	ErrorResponse	"DISCOVERY_FAILED / QR_SERVICE_UNAVAILABLE"
//	@Router			/api/v1/qr/resolve [post]
func (h *Handler) HandleResolve(c *gin.Context) {
	var req ResolveRequest
	if !bind(c, &req) {
		return
	}
	resp, err := h.svc.Resolve(c.Request.Context(), req, subject(c), c.GetHeader("User-Agent"), c.ClientIP())
	if err != nil {
		fail(c, h.svc, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}

// HandleQuote turns a candidate into a transaction with the seller's quote.
//
//	@Summary		Quote a merchant for a QR packet (Beckn select)
//	@ID				qrQuote
//	@Description	Sends select for the items the buyer chose from the candidate (with quantities) and waits for on_select. The chosen items' subtotal must reach the packet's minimum basket. An item the seller can no longer supply fails the quote (409 QUOTE_UNAVAILABLE).
//	@Tags			qr
//	@Accept			json
//	@Produce		json
//	@Param			X-QR-Guest-Token	header		string			true	"Guest token from resolve"
//	@Param			request				body		QuoteRequest	true	"Request body"
//	@Success		200					{object}	QuoteResponse
//	@Failure		400					{object}	ErrorResponse	"INVALID_ITEMS"
//	@Failure		403					{object}	ErrorResponse	"INVALID_GUEST_TOKEN"
//	@Failure		409					{object}	ErrorResponse	"QUOTE_UNAVAILABLE"
//	@Failure		422					{object}	ErrorResponse	"BELOW_MIN_BASKET"
//	@Failure		410					{object}	ErrorResponse	"CANDIDATE_EXPIRED"
//	@Failure		504					{object}	ErrorResponse	"QUOTE_TIMEOUT"
//	@Router			/api/v1/qr/quote [post]
func (h *Handler) HandleQuote(c *gin.Context) {
	var req QuoteRequest
	if !bind(c, &req) {
		return
	}
	resp, err := h.svc.Quote(c.Request.Context(), req, guestToken(c), subject(c))
	if err != nil {
		fail(c, h.svc, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}

// HandleCheckout sends init with the buyer's details.
//
//	@Summary		Check out a QR order (Beckn init)
//	@ID				qrCheckout
//	@Description	Sends init with the buyer's name and phone and the packet's node (plus room/unit) as the delivery address, or self pickup, and waits for on_init. A changed total answers 409 QUOTE_CHANGED with the new pricing; the order then waits to be placed at that total.
//	@Tags			qr
//	@Accept			json
//	@Produce		json
//	@Param			X-QR-Guest-Token	header		string			true	"Guest token from resolve"
//	@Param			request				body		CheckoutRequest	true	"Request body"
//	@Success		200					{object}	CheckoutResponse
//	@Failure		400					{object}	ErrorResponse	"INVALID_PHONE / NODE_DELIVERY_NOT_ALLOWED"
//	@Failure		409					{object}	ErrorResponse	"QUOTE_CHANGED (data: CheckoutResponse) / INVALID_STATE"
//	@Router			/api/v1/qr/checkout [post]
func (h *Handler) HandleCheckout(c *gin.Context) {
	var req CheckoutRequest
	if !bind(c, &req) {
		return
	}
	resp, err := h.svc.Checkout(c.Request.Context(), req, guestToken(c))
	if err != nil {
		fail(c, h.svc, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}

// HandlePlaceOrder sends confirm for a checked-out order.
//
//	@Summary		Place a QR order (Beckn confirm)
//	@ID				qrPlaceOrder
//	@Description	Sends confirm for an order whose on_init is in (AWAITING_CONFIRM), at the total the buyer agreed to. Payment is not collected in this flow yet.
//	@Tags			qr
//	@Accept			json
//	@Produce		json
//	@Param			X-QR-Guest-Token	header		string				true	"Guest token from resolve"
//	@Param			id					path		string				true	"Transaction id"
//	@Param			request				body		PlaceOrderRequest	true	"Request body"
//	@Success		200					{object}	OrderView
//	@Failure		409					{object}	ErrorResponse	"QUOTE_CHANGED / INVALID_STATE"
//	@Router			/api/v1/qr/orders/{id}/place [post]
func (h *Handler) HandlePlaceOrder(c *gin.Context) {
	var req PlaceOrderRequest
	if !bind(c, &req) {
		return
	}
	resp, err := h.svc.PlaceOrder(c.Request.Context(), c.Param("id"), req, guestToken(c))
	if err != nil {
		fail(c, h.svc, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}

// HandleOrder returns a QR order's progress.
//
//	@Summary		Read a QR order
//	@ID				qrOrder
//	@Tags			qr
//	@Produce		json
//	@Param			X-QR-Guest-Token	header		string	true	"Guest token from resolve"
//	@Param			id					path		string	true	"Transaction id"
//	@Success		200					{object}	OrderView
//	@Router			/api/v1/qr/orders/{id} [get]
func (h *Handler) HandleOrder(c *gin.Context) {
	resp, err := h.svc.Order(c.Request.Context(), c.Param("id"), guestToken(c))
	if err != nil {
		fail(c, h.svc, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}

// HandleAdmin forwards the packet console's calls to qr-service's admin API:
// /api/v1/qr/admin/packets[/...] → {QR_SERVICE_URL}/api/v1/packets[/...].
// The caller must be logged in. qr-service is called with QR_ADMIN_TOKEN when
// set, else with the caller's own bearer token (which then needs
// qr-service's ion_admin role).
//
//	@Summary		Packet console proxy to qr-service
//	@ID				qrAdmin
//	@Description	Any method. Responses are qr-service's own (alya envelope, or raw PNG/SVG for qr.png / qr.svg).
//	@Tags			qr
//	@Param			path	path	string	true	"packets, packets/{id}, packets/{id}/validate, ..."
//	@Success		200
//	@Failure		401	{object}	ErrorResponse
//	@Security		BearerAuth
//	@Router			/api/v1/qr/admin/{path} [get]
func (h *Handler) HandleAdmin(c *gin.Context) {
	svc := h.svc
	if !svc.qr.configured() {
		fail(c, svc, newErr(503, "QR_NOT_CONFIGURED", "QR packets are not configured on this BAP (QR_SERVICE_URL)"))
		return
	}
	if subject(c) == nil {
		fail(c, svc, newErr(401, "LOGIN_REQUIRED", "Log in to manage QR packets"))
		return
	}
	path := strings.TrimPrefix(c.Param("path"), "/")
	// Only qr-service's packet API, and no ".." that would step outside it.
	if (path != "packets" && !strings.HasPrefix(path, "packets/")) || strings.Contains(path, "..") {
		fail(c, svc, newErr(404, "ROUTE_NOT_FOUND", "Unknown QR admin route"))
		return
	}
	target := svc.cfg.QRServiceURL + "/api/v1/" + path
	if q := c.Request.URL.RawQuery; q != "" {
		target += "?" + q
	}
	req, err := http.NewRequestWithContext(c.Request.Context(), c.Request.Method, target, io.LimitReader(c.Request.Body, 1<<20))
	if err != nil {
		fail(c, svc, err)
		return
	}
	if ct := c.GetHeader("Content-Type"); ct != "" {
		req.Header.Set("Content-Type", ct)
	}
	req.Header.Set("Accept", c.GetHeader("Accept"))
	if svc.cfg.QRAdminToken != "" {
		req.Header.Set("Authorization", "Bearer "+svc.cfg.QRAdminToken)
	} else {
		req.Header.Set("Authorization", c.GetHeader("Authorization"))
	}
	resp, err := svc.qr.http.Do(req)
	if err != nil {
		svc.log().Warn().Error(err).Log("qr-service admin call failed")
		fail(c, svc, newErr(502, "QR_SERVICE_UNAVAILABLE", "The QR service is unavailable"))
		return
	}
	defer resp.Body.Close() //nolint:errcheck
	for _, k := range []string{"Content-Type", "Content-Disposition", "Cache-Control", "Retry-After"} {
		if v := resp.Header.Get(k); v != "" {
			c.Header(k, v)
		}
	}
	c.Status(resp.StatusCode)
	_, _ = io.Copy(c.Writer, io.LimitReader(resp.Body, maxQRServiceResponse))
}

// HandlePacketOrders lists a packet's QR orders for the packet console.
//
//	@Summary		List a packet's QR orders
//	@ID				qrPacketOrders
//	@Description	The checkouts started from a packet's QR code, newest first (at most 100). Login required.
//	@Tags			qr
//	@Produce		json
//	@Param			packet_id	path		string	true	"Packet id"
//	@Success		200			{array}		PacketOrder
//	@Failure		401			{object}	ErrorResponse
//	@Security		BearerAuth
//	@Router			/api/v1/qr/packet-orders/{packet_id} [get]
func (h *Handler) HandlePacketOrders(c *gin.Context) {
	if subject(c) == nil {
		fail(c, h.svc, newErr(401, "LOGIN_REQUIRED", "Log in to see QR orders"))
		return
	}
	orders, err := h.svc.PacketOrders(c.Request.Context(), c.Param("packet_id"))
	if err != nil {
		fail(c, h.svc, err)
		return
	}
	c.JSON(http.StatusOK, orders)
}
