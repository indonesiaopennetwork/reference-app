package auth

// The types in this file only describe requests/responses for the swag API
// docs (`task docs:api`); no code uses them. Keep them in step with the
// handlers and middleware they describe.

// ErrorResponse is the {"error": "..."} body that RequireToken, RequireSeller
// and the dashboard handlers send on failure.
type ErrorResponse struct {
	// Error message, often the raw Go error text.
	Error string `json:"error" binding:"required" example:"invalid or expired token"`
}

// NoProviderResponse is RequireSeller's 403: a valid token with no seller
// profile (users row) behind it. Call POST /api/v1/auth/bootstrap first.
type NoProviderResponse struct {
	Error string `json:"error" binding:"required" example:"no seller profile for this account"`
	Code  string `json:"code" binding:"required" enums:"NO_PROVIDER" example:"NO_PROVIDER"`
}
