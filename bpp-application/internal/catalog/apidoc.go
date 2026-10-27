package catalog

// The types in this file only describe requests/responses for the swag API
// docs (`task docs:api`); no code uses them. Keep them in step with
// PublishHandler.Handle.

// PublishAckResponse is POST /api/v1/catalog/publish's alya success envelope
// (wscutils.NewSuccessResponse(PublishAck)). The error envelope is
// server.WscErrorResponse.
type PublishAckResponse struct {
	Status string     `json:"status" binding:"required" enums:"success" example:"success"`
	Data   PublishAck `json:"data" binding:"required"`
	// Always null on success.
	Messages []any `json:"messages" swaggertype:"array,object" extensions:"x-nullable"`
}
