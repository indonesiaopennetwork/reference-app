package grievancesvc

// The types in this file only describe responses for the swag API docs
// (`task docs:api`); the handlers write gin.H or the service types.

// ErrorResponse is an error body. code is NOT_FOUND, INVALID or SEND_FAILED
// (not delivered: nothing recorded, retry).
type ErrorResponse struct {
	Error string `json:"error" binding:"required"`
	Code  string `json:"code" binding:"required" enums:"NOT_FOUND,INVALID,SEND_FAILED"`
}

// GrievanceDetail is a grievance with its order.
type GrievanceDetail struct {
	Detail
}

// GrievanceListResponse is GET /api/v1/grievances.
type GrievanceListResponse struct {
	Items []Item `json:"items" binding:"required"`
	// Grievances waiting on the seller.
	NeedsAction int `json:"needsAction" binding:"required"`
}
