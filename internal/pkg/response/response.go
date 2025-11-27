package response

import (
	"net/http"

	"github.com/genixerp/genix-backend/internal/domain/entity"
	"github.com/gin-gonic/gin"
)

// Response represents a standard API response
type Response struct {
	Success bool        `json:"success"`
	Data    interface{} `json:"data,omitempty"`
	Error   *ErrorInfo  `json:"error,omitempty"`
	Meta    *Meta       `json:"meta,omitempty"`
}

// ErrorInfo represents error information
type ErrorInfo struct {
	Code    string            `json:"code"`
	Message string            `json:"message"`
	Details map[string]string `json:"details,omitempty"`
}

// Meta represents pagination and other metadata
type Meta struct {
	Page       int   `json:"page,omitempty"`
	Limit      int   `json:"limit,omitempty"`
	Total      int   `json:"total,omitempty"`
	TotalPages int   `json:"total_pages,omitempty"`
	HasNext    bool  `json:"has_next,omitempty"`
	HasPrev    bool  `json:"has_prev,omitempty"`
}

// Success sends a successful response
func Success(c *gin.Context, data interface{}) {
	c.JSON(http.StatusOK, Response{
		Success: true,
		Data:    data,
	})
}

// SuccessWithMeta sends a successful response with metadata
func SuccessWithMeta(c *gin.Context, data interface{}, pagination *entity.Pagination) {
	meta := &Meta{
		Page:       pagination.Page,
		Limit:      pagination.Limit,
		Total:      pagination.Total,
		TotalPages: pagination.Pages,
		HasNext:    pagination.HasNext,
		HasPrev:    pagination.HasPrev,
	}

	c.JSON(http.StatusOK, Response{
		Success: true,
		Data:    data,
		Meta:    meta,
	})
}

// Created sends a 201 Created response
func Created(c *gin.Context, data interface{}) {
	c.JSON(http.StatusCreated, Response{
		Success: true,
		Data:    data,
	})
}

// NoContent sends a 204 No Content response
func NoContent(c *gin.Context) {
	c.Status(http.StatusNoContent)
}

// Error sends an error response
func Error(c *gin.Context, statusCode int, code, message string) {
	c.JSON(statusCode, Response{
		Success: false,
		Error: &ErrorInfo{
			Code:    code,
			Message: message,
		},
	})
}

// ErrorWithDetails sends an error response with details
func ErrorWithDetails(c *gin.Context, statusCode int, code, message string, details map[string]string) {
	c.JSON(statusCode, Response{
		Success: false,
		Error: &ErrorInfo{
			Code:    code,
			Message: message,
			Details: details,
		},
	})
}

// BadRequest sends a 400 Bad Request response
func BadRequest(c *gin.Context, message string) {
	Error(c, http.StatusBadRequest, "BAD_REQUEST", message)
}

// BadRequestWithDetails sends a 400 Bad Request response with details
func BadRequestWithDetails(c *gin.Context, message string, details map[string]string) {
	ErrorWithDetails(c, http.StatusBadRequest, "BAD_REQUEST", message, details)
}

// ValidationError sends a 400 response for validation errors
func ValidationError(c *gin.Context, details map[string]string) {
	ErrorWithDetails(c, http.StatusBadRequest, "VALIDATION_ERROR", "Validation failed", details)
}

// Unauthorized sends a 401 Unauthorized response
func Unauthorized(c *gin.Context, message string) {
	if message == "" {
		message = "Authentication required"
	}
	Error(c, http.StatusUnauthorized, "UNAUTHORIZED", message)
}

// Forbidden sends a 403 Forbidden response
func Forbidden(c *gin.Context, message string) {
	if message == "" {
		message = "Access denied"
	}
	Error(c, http.StatusForbidden, "FORBIDDEN", message)
}

// NotFound sends a 404 Not Found response
func NotFound(c *gin.Context, resource string) {
	message := "Resource not found"
	if resource != "" {
		message = resource + " not found"
	}
	Error(c, http.StatusNotFound, "NOT_FOUND", message)
}

// Conflict sends a 409 Conflict response
func Conflict(c *gin.Context, message string) {
	Error(c, http.StatusConflict, "CONFLICT", message)
}

// UnprocessableEntity sends a 422 Unprocessable Entity response
func UnprocessableEntity(c *gin.Context, message string) {
	Error(c, http.StatusUnprocessableEntity, "UNPROCESSABLE_ENTITY", message)
}

// TooManyRequests sends a 429 Too Many Requests response
func TooManyRequests(c *gin.Context, message string) {
	if message == "" {
		message = "Rate limit exceeded"
	}
	Error(c, http.StatusTooManyRequests, "TOO_MANY_REQUESTS", message)
}

// InternalServerError sends a 500 Internal Server Error response
func InternalServerError(c *gin.Context, message string) {
	if message == "" {
		message = "An unexpected error occurred"
	}
	Error(c, http.StatusInternalServerError, "INTERNAL_ERROR", message)
}

// ServiceUnavailable sends a 503 Service Unavailable response
func ServiceUnavailable(c *gin.Context, message string) {
	if message == "" {
		message = "Service temporarily unavailable"
	}
	Error(c, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", message)
}

// Common error codes
const (
	ErrCodeBadRequest          = "BAD_REQUEST"
	ErrCodeValidation          = "VALIDATION_ERROR"
	ErrCodeUnauthorized        = "UNAUTHORIZED"
	ErrCodeForbidden           = "FORBIDDEN"
	ErrCodeNotFound            = "NOT_FOUND"
	ErrCodeConflict            = "CONFLICT"
	ErrCodeTooManyRequests     = "TOO_MANY_REQUESTS"
	ErrCodeInternalError       = "INTERNAL_ERROR"
	ErrCodeServiceUnavailable  = "SERVICE_UNAVAILABLE"
	ErrCodeInvalidCredentials  = "INVALID_CREDENTIALS"
	ErrCodeAccountLocked       = "ACCOUNT_LOCKED"
	ErrCodeAccountDisabled     = "ACCOUNT_DISABLED"
	ErrCodeTokenExpired        = "TOKEN_EXPIRED"
	ErrCodeTokenInvalid        = "TOKEN_INVALID"
	ErrCodeResourceExists      = "RESOURCE_EXISTS"
	ErrCodeInsufficientStock   = "INSUFFICIENT_STOCK"
	ErrCodeInvalidOperation    = "INVALID_OPERATION"
)
