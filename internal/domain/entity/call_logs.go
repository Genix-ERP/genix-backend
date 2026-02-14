package entity

import (
	"database/sql"
	"time"

	"github.com/google/uuid"
)

// CallType represents the type of call
type CallType string

const (
	CallTypeInbound  CallType = "inbound"
	CallTypeOutbound CallType = "outbound"
	CallTypeMissed   CallType = "missed"
)

// CallOutcome represents the outcome of a call
type CallOutcome string

const (
	CallOutcomeAnswered   CallOutcome = "answered"
	CallOutcomeNoAnswer   CallOutcome = "no_answer"
	CallOutcomeBusy       CallOutcome = "busy"
	CallOutcomeVoicemail  CallOutcome = "voicemail"
	CallOutcomeFailed     CallOutcome = "failed"
	CallOutcomeCompleted  CallOutcome = "completed"
	CallOutcomeInitiated  CallOutcome = "initiated"
)

// CallLog represents a phone call log entry
type CallLog struct {
	ID               uuid.UUID      `json:"id" db:"id"`
	TenantID         uuid.UUID      `json:"tenant_id" db:"tenant_id"`
	OrganizationID   *uuid.UUID     `json:"organization_id,omitempty" db:"organization_id"`
	ContactID        *uuid.UUID     `json:"contact_id,omitempty" db:"contact_id"`
	ContactPersonID  *uuid.UUID     `json:"contact_person_id,omitempty" db:"contact_person_id"`
	CallerNumber     string         `json:"caller_number" db:"caller_number"`
	ReceiverNumber   string         `json:"receiver_number" db:"receiver_number"`
	CallType         CallType       `json:"call_type" db:"call_type"`
	CallStartTime    time.Time      `json:"call_start_time" db:"call_start_time"`
	CallEndTime      *time.Time     `json:"call_end_time,omitempty" db:"call_end_time"`
	CallDuration     int            `json:"call_duration" db:"call_duration"` // in seconds
	CallOutcome      CallOutcome    `json:"call_outcome" db:"call_outcome"`
	RecordingURL     *string        `json:"recording_url,omitempty" db:"recording_url"`
	Notes            *string        `json:"notes,omitempty" db:"notes"`
	SentimentScore   *float64       `json:"sentiment_score,omitempty" db:"sentiment_score"` // -1 to 1
	Summary          *string        `json:"summary,omitempty" db:"summary"` // AI generated summary
	PBXCallID        *string        `json:"pbx_call_id,omitempty" db:"pbx_call_id"` // External PBX system call ID
	AgentID          *uuid.UUID     `json:"agent_id,omitempty" db:"agent_id"` // User who made/received the call
	CreatedBy        *uuid.UUID     `json:"created_by,omitempty" db:"created_by"`
	CreatedAt        time.Time      `json:"created_at" db:"created_at"`
	UpdatedAt        time.Time      `json:"updated_at" db:"updated_at"`
	DeletedAt        sql.NullTime   `json:"-" db:"deleted_at"`

	// Populated from joins
	ContactName      *string        `json:"contact_name,omitempty" db:"contact_name"`
	AgentName        *string        `json:"agent_name,omitempty" db:"agent_name"`
}

// CreateCallLogInput represents input for creating a call log
type CreateCallLogInput struct {
	ContactID        string      `json:"contact_id,omitempty"`
	ContactPersonID  string      `json:"contact_person_id,omitempty"`
	CallerNumber     string      `json:"caller_number" binding:"required"`
	ReceiverNumber   string      `json:"receiver_number,omitempty"`
	CallType         CallType    `json:"call_type" binding:"required,oneof=inbound outbound missed"`
	CallStartTime    *time.Time  `json:"call_start_time,omitempty"`
	CallEndTime      *time.Time  `json:"call_end_time,omitempty"`
	CallDuration     int         `json:"call_duration,omitempty"`
	CallOutcome      CallOutcome `json:"call_outcome,omitempty"`
	RecordingURL     string      `json:"recording_url,omitempty"`
	Notes            string      `json:"notes,omitempty"`
	SentimentScore   *float64    `json:"sentiment_score,omitempty"`
	Summary          string      `json:"summary,omitempty"`
	PBXCallID        string      `json:"pbx_call_id,omitempty"`
	CustomerName     string      `json:"customer_name,omitempty"` // For display when contact_id not provided
}

// UpdateCallLogInput represents input for updating a call log
type UpdateCallLogInput struct {
	CallEndTime      *time.Time   `json:"call_end_time,omitempty"`
	CallDuration     *int         `json:"call_duration,omitempty"`
	CallOutcome      *CallOutcome `json:"call_outcome,omitempty"`
	RecordingURL     *string      `json:"recording_url,omitempty"`
	Notes            *string      `json:"notes,omitempty"`
	SentimentScore   *float64     `json:"sentiment_score,omitempty"`
	Summary          *string      `json:"summary,omitempty"`
}

// CallLogListFilter represents filters for listing call logs
type CallLogListFilter struct {
	Search        string      `form:"search"`
	ContactID     string      `form:"contact_id"`
	CallType      CallType    `form:"call_type"`
	CallOutcome   CallOutcome `form:"call_outcome"`
	AgentID       string      `form:"agent_id"`
	DateFrom      *time.Time  `form:"date_from"`
	DateTo        *time.Time  `form:"date_to"`
	MinDuration   *int        `form:"min_duration"`
	MaxDuration   *int        `form:"max_duration"`
}

// CallLogResponse represents the API response for a call log
type CallLogResponse struct {
	ID              uuid.UUID    `json:"id"`
	ContactID       *uuid.UUID   `json:"contact_id,omitempty"`
	ContactName     *string      `json:"contact_name,omitempty"`
	CallerNumber    string       `json:"caller_number"`
	ReceiverNumber  string       `json:"receiver_number,omitempty"`
	CallType        CallType     `json:"call_type"`
	CallStartTime   time.Time    `json:"call_start_time"`
	CallEndTime     *time.Time   `json:"call_end_time,omitempty"`
	CallDuration    int          `json:"call_duration"`
	CallOutcome     CallOutcome  `json:"call_outcome"`
	RecordingURL    *string      `json:"recording_url,omitempty"`
	Notes           *string      `json:"notes,omitempty"`
	SentimentScore  *float64     `json:"sentiment_score,omitempty"`
	Summary         *string      `json:"summary,omitempty"`
	AgentID         *uuid.UUID   `json:"agent_id,omitempty"`
	AgentName       *string      `json:"agent_name,omitempty"`
	CreatedAt       time.Time    `json:"created_at"`
}

// CallLogStats represents aggregated call statistics
type CallLogStats struct {
	TotalCalls       int     `json:"total_calls"`
	InboundCalls     int     `json:"inbound_calls"`
	OutboundCalls    int     `json:"outbound_calls"`
	MissedCalls      int     `json:"missed_calls"`
	TotalDuration    int     `json:"total_duration"` // in seconds
	AvgDuration      float64 `json:"avg_duration"`   // in seconds
	AnswerRate       float64 `json:"answer_rate"`    // percentage
	AvgSentiment     float64 `json:"avg_sentiment"`  // -1 to 1
}
