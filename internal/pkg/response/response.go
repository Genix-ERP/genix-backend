package response

import (
	"net/http"
	"strings"

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
	Data    interface{}       `json:"data,omitempty"`
}

// Meta represents pagination and other metadata
// Meta is the pagination envelope. NONE of these fields carry `omitempty`:
// with it, Go drops a field when it is zero/false, so `has_next: false` and
// `total: 0` vanished from the JSON and a client could not tell "last page"
// from "field missing" — infinite scroll then either stopped early or looped.
// Emit them unconditionally.
//
// `page_size` is the name mobile clients use; `limit` is kept alongside it for
// the existing web callers. Both carry the same value.
type Meta struct {
	Page       int  `json:"page"`
	Limit      int  `json:"limit"`
	PageSize   int  `json:"page_size"`
	Total      int  `json:"total"`
	TotalPages int  `json:"total_pages"`
	HasNext    bool `json:"has_next"`
	HasPrev    bool `json:"has_prev"`
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
		PageSize:   pagination.Limit,
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

// SuccessWithPagination is an alias for SuccessWithMeta
func SuccessWithPagination(c *gin.Context, data interface{}, pagination *entity.Pagination) {
	SuccessWithMeta(c, data, pagination)
}

// Paginated sends a successful response with pagination metadata from individual values
func Paginated(c *gin.Context, data interface{}, page, limit, total int) {
	totalPages := 0
	if limit > 0 {
		totalPages = total / limit
		if total%limit > 0 {
			totalPages++
		}
	}

	meta := &Meta{
		Page:       page,
		Limit:      limit,
		PageSize:   limit,
		Total:      total,
		TotalPages: totalPages,
		HasNext:    page < totalPages,
		HasPrev:    page > 1,
	}

	c.JSON(http.StatusOK, Response{
		Success: true,
		Data:    data,
		Meta:    meta,
	})
}

// InternalError sends a 500 Internal Server Error response
func InternalError(c *gin.Context, message string) {
	InternalServerError(c, message)
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

// Error sends an error response. The message is translated to Uzbek at this
// boundary (translate.go) — handlers keep writing canonical English (which
// the logs get verbatim), the user sees Uzbek, and error CODES pass through
// untouched so clients that switch on them keep working.
func Error(c *gin.Context, statusCode int, code, message string) {
	c.JSON(statusCode, Response{
		Success: false,
		Error: &ErrorInfo{
			Code:    code,
			Message: translateUserMessage(message),
		},
	})
}

// ErrorWithDetails sends an error response with details
func ErrorWithDetails(c *gin.Context, statusCode int, code, message string, details map[string]string) {
	c.JSON(statusCode, Response{
		Success: false,
		Error: &ErrorInfo{
			Code:    code,
			Message: translateUserMessage(message),
			Details: details,
		},
	})
}

// BadRequest sends a 400 Bad Request response
func BadRequest(c *gin.Context, message string) {
	Error(c, http.StatusBadRequest, "BAD_REQUEST", message)
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

// NotFound sends a 404 Not Found response.
//
// Some callers pass a bare resource name ("Warehouse"), others pass a full
// sentence ("Project not found", "Payment not found or already paid") — the
// old unconditional `+ " not found"` turned the latter into "Project not
// found not found". Append only when the caller didn't already say it.
func NotFound(c *gin.Context, resource string) {
	message := "Resource not found"
	if resource != "" {
		if strings.Contains(strings.ToLower(resource), "not found") {
			message = resource
		} else {
			message = resource + " not found"
		}
	}
	Error(c, http.StatusNotFound, "NOT_FOUND", message)
}

// Conflict sends a 409 Conflict response
func Conflict(c *gin.Context, message string) {
	Error(c, http.StatusConflict, "CONFLICT", message)
}

// ConflictWithData sends a 409 Conflict response with additional data
func ConflictWithData(c *gin.Context, code, message string, data interface{}) {
	c.JSON(http.StatusConflict, Response{
		Success: false,
		Error: &ErrorInfo{
			Code:    code,
			Message: translateUserMessage(message),
			Data:    data,
		},
	})
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

// Common error codes
const (
	ErrCodeBadRequest         = "BAD_REQUEST"
	ErrCodeValidation         = "VALIDATION_ERROR"
	ErrCodeUnauthorized       = "UNAUTHORIZED"
	ErrCodeForbidden          = "FORBIDDEN"
	ErrCodeNotFound           = "NOT_FOUND"
	ErrCodeConflict           = "CONFLICT"
	ErrCodeTooManyRequests    = "TOO_MANY_REQUESTS"
	ErrCodeInternalError      = "INTERNAL_ERROR"
	ErrCodeServiceUnavailable = "SERVICE_UNAVAILABLE"
	ErrCodeInvalidCredentials = "INVALID_CREDENTIALS"
	ErrCodeAccountLocked      = "ACCOUNT_LOCKED"
	ErrCodeAccountDisabled    = "ACCOUNT_DISABLED"
	ErrCodeTokenExpired       = "TOKEN_EXPIRED"
	ErrCodeTokenInvalid       = "TOKEN_INVALID"
	ErrCodeResourceExists     = "RESOURCE_EXISTS"
	ErrCodeInsufficientStock  = "INSUFFICIENT_STOCK"
	ErrCodeInvalidOperation   = "INVALID_OPERATION"
)
