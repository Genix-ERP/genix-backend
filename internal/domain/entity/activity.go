package entity

import (
	"database/sql"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// ActivityType represents the type of activity
type ActivityType string

const (
	ActivityTypeCall     ActivityType = "call"
	ActivityTypeMeeting  ActivityType = "meeting"
	ActivityTypeEmail    ActivityType = "email"
	ActivityTypeNote     ActivityType = "note"
	ActivityTypeTask     ActivityType = "task"
	ActivityTypeFollowUp ActivityType = "follow_up"
)

// ActivityStatus represents the status of an activity
type ActivityStatus string

const (
	ActivityStatusPlanned    ActivityStatus = "planned"
	ActivityStatusInProgress ActivityStatus = "in_progress"
	ActivityStatusCompleted  ActivityStatus = "completed"
	ActivityStatusCancelled  ActivityStatus = "cancelled"
)

// ActivityOutcome represents the outcome of an activity
type ActivityOutcome string

const (
	ActivityOutcomeSuccessful   ActivityOutcome = "successful"
	ActivityOutcomeUnsuccessful ActivityOutcome = "unsuccessful"
	ActivityOutcomeNoAnswer     ActivityOutcome = "no_answer"
	ActivityOutcomeRescheduled  ActivityOutcome = "rescheduled"
)

// ActivityAttendee represents an attendee of an activity
type ActivityAttendee struct {
	UserID uuid.UUID `json:"user_id,omitempty"`
	Email  string    `json:"email"`
	Name   string    `json:"name"`
	Status string    `json:"status"` // pending, accepted, declined, tentative
}

// Activity represents a CRM activity (call, meeting, email, note, etc.)
type Activity struct {
	ID               uuid.UUID        `json:"id" db:"id"`
	TenantID         uuid.UUID        `json:"tenant_id" db:"tenant_id"`
	ActivityType     ActivityType     `json:"activity_type" db:"activity_type"`
	Subject          string           `json:"subject" db:"subject"`
	Description      *string          `json:"description,omitempty" db:"description"`
	RelatedType      *string          `json:"related_type,omitempty" db:"related_type"`
	RelatedID        *uuid.UUID       `json:"related_id,omitempty" db:"related_id"`
	LeadID           *uuid.UUID       `json:"lead_id,omitempty" db:"lead_id"`
	ContactID        *uuid.UUID       `json:"contact_id,omitempty" db:"contact_id"`
	OpportunityID    *uuid.UUID       `json:"opportunity_id,omitempty" db:"opportunity_id"`
	StartDatetime    *time.Time       `json:"start_datetime,omitempty" db:"start_datetime"`
	EndDatetime      *time.Time       `json:"end_datetime,omitempty" db:"end_datetime"`
	DurationMinutes  *int             `json:"duration_minutes,omitempty" db:"duration_minutes"`
	Location         *string          `json:"location,omitempty" db:"location"`
	IsAllDay         bool             `json:"is_all_day" db:"is_all_day"`
	Status           ActivityStatus   `json:"status" db:"status"`
	Outcome          *ActivityOutcome `json:"outcome,omitempty" db:"outcome"`
	OutcomeNotes     *string          `json:"outcome_notes,omitempty" db:"outcome_notes"`
	AssignedTo       *uuid.UUID       `json:"assigned_to,omitempty" db:"assigned_to"`
	Attendees        json.RawMessage  `json:"attendees" db:"attendees" swaggertype:"object"`
	ReminderDatetime *time.Time       `json:"reminder_datetime,omitempty" db:"reminder_datetime"`
	ReminderSent     bool             `json:"reminder_sent" db:"reminder_sent"`
	Priority         string           `json:"priority" db:"priority"`
	IsPrivate        bool             `json:"is_private" db:"is_private"`
	Tags             json.RawMessage  `json:"tags" db:"tags" swaggertype:"object"`
	CustomFields     json.RawMessage  `json:"custom_fields" db:"custom_fields" swaggertype:"object"`
	CreatedBy        *uuid.UUID       `json:"created_by,omitempty" db:"created_by"`
	CreatedAt        time.Time        `json:"created_at" db:"created_at"`
	UpdatedAt        time.Time        `json:"updated_at" db:"updated_at"`
	DeletedAt        sql.NullTime     `json:"-" db:"deleted_at"`

	// Populated from joins
	LeadName        *string `json:"lead_name,omitempty" db:"lead_name"`
	ContactName     *string `json:"contact_name,omitempty" db:"contact_name"`
	OpportunityName *string `json:"opportunity_name,omitempty" db:"opportunity_name"`
	AssignedToName  *string `json:"assigned_to_name,omitempty" db:"assigned_to_name"`
}

// CreateActivityInput represents input for creating an activity
type CreateActivityInput struct {
	ActivityType     string             `json:"activity_type" binding:"required,oneof=call meeting email note task follow_up"`
	Subject          string             `json:"subject" binding:"required,min=1,max=255"`
	Description      string             `json:"description,omitempty"`
	RelatedType      string             `json:"related_type,omitempty"`
	RelatedID        string             `json:"related_id,omitempty"`
	LeadID           string             `json:"lead_id,omitempty"`
	ContactID        string             `json:"contact_id,omitempty"`
	OpportunityID    string             `json:"opportunity_id,omitempty"`
	StartDatetime    string             `json:"start_datetime,omitempty"`
	EndDatetime      string             `json:"end_datetime,omitempty"`
	DurationMinutes  *int               `json:"duration_minutes,omitempty"`
	Location         string             `json:"location,omitempty"`
	IsAllDay         bool               `json:"is_all_day,omitempty"`
	Status           string             `json:"status,omitempty"`
	AssignedTo       string             `json:"assigned_to,omitempty"`
	Attendees        []ActivityAttendee `json:"attendees,omitempty"`
	ReminderDatetime string             `json:"reminder_datetime,omitempty"`
	Priority         string             `json:"priority,omitempty"`
	IsPrivate        bool               `json:"is_private,omitempty"`
	Tags             []string           `json:"tags,omitempty"`
}

// UpdateActivityInput represents input for updating an activity
type UpdateActivityInput struct {
	ActivityType     *string            `json:"activity_type,omitempty"`
	Subject          *string            `json:"subject,omitempty"`
	Description      *string            `json:"description,omitempty"`
	LeadID           *string            `json:"lead_id,omitempty"`
	ContactID        *string            `json:"contact_id,omitempty"`
	OpportunityID    *string            `json:"opportunity_id,omitempty"`
	StartDatetime    *string            `json:"start_datetime,omitempty"`
	EndDatetime      *string            `json:"end_datetime,omitempty"`
	DurationMinutes  *int               `json:"duration_minutes,omitempty"`
	Location         *string            `json:"location,omitempty"`
	IsAllDay         *bool              `json:"is_all_day,omitempty"`
	Status           *string            `json:"status,omitempty"`
	Outcome          *string            `json:"outcome,omitempty"`
	OutcomeNotes     *string            `json:"outcome_notes,omitempty"`
	AssignedTo       *string            `json:"assigned_to,omitempty"`
	Attendees        []ActivityAttendee `json:"attendees,omitempty"`
	ReminderDatetime *string            `json:"reminder_datetime,omitempty"`
	Priority         *string            `json:"priority,omitempty"`
	IsPrivate        *bool              `json:"is_private,omitempty"`
	Tags             []string           `json:"tags,omitempty"`
}

// ActivityListFilter represents filters for listing activities
type ActivityListFilter struct {
	Search         string `form:"search"`
	ActivityType   string `form:"activity_type"`
	Status         string `form:"status"`
	LeadID         string `form:"lead_id"`
	ContactID      string `form:"contact_id"`
	OpportunityID  string `form:"opportunity_id"`
	AssignedTo     string `form:"assigned_to"`
	DateFrom       string `form:"date_from"`
	DateTo         string `form:"date_to"`
	Priority       string `form:"priority"`
	IncludePrivate bool   `form:"include_private"`
}

// ActivityResponse represents the API response for an activity
type ActivityResponse struct {
	ID               uuid.UUID          `json:"id"`
	ActivityType     ActivityType       `json:"activity_type"`
	Subject          string             `json:"subject"`
	Description      *string            `json:"description,omitempty"`
	LeadID           *uuid.UUID         `json:"lead_id,omitempty"`
	LeadName         *string            `json:"lead_name,omitempty"`
	ContactID        *uuid.UUID         `json:"contact_id,omitempty"`
	ContactName      *string            `json:"contact_name,omitempty"`
	OpportunityID    *uuid.UUID         `json:"opportunity_id,omitempty"`
	OpportunityName  *string            `json:"opportunity_name,omitempty"`
	StartDatetime    *time.Time         `json:"start_datetime,omitempty"`
	EndDatetime      *time.Time         `json:"end_datetime,omitempty"`
	DurationMinutes  *int               `json:"duration_minutes,omitempty"`
	Location         *string            `json:"location,omitempty"`
	IsAllDay         bool               `json:"is_all_day"`
	Status           ActivityStatus     `json:"status"`
	Outcome          *ActivityOutcome   `json:"outcome,omitempty"`
	OutcomeNotes     *string            `json:"outcome_notes,omitempty"`
	AssignedTo       *uuid.UUID         `json:"assigned_to,omitempty"`
	AssignedToName   *string            `json:"assigned_to_name,omitempty"`
	Attendees        []ActivityAttendee `json:"attendees,omitempty"`
	ReminderDatetime *time.Time         `json:"reminder_datetime,omitempty"`
	Priority         string             `json:"priority"`
	IsPrivate        bool               `json:"is_private"`
	Tags             []string           `json:"tags"`
	CreatedAt        time.Time          `json:"created_at"`
	UpdatedAt        time.Time          `json:"updated_at"`
}

// ActivityStats represents activity statistics
type ActivityStats struct {
	TotalActivities int            `json:"total_activities"`
	ByType          map[string]int `json:"by_type"`
	ByStatus        map[string]int `json:"by_status"`
	CompletedToday  int            `json:"completed_today"`
	PlannedToday    int            `json:"planned_today"`
	OverdueCount    int            `json:"overdue_count"`
}

// ActivityTimeline represents an activity in a timeline view
type ActivityTimeline struct {
	ID            uuid.UUID      `json:"id"`
	ActivityType  ActivityType   `json:"activity_type"`
	Subject       string         `json:"subject"`
	Description   *string        `json:"description,omitempty"`
	Status        ActivityStatus `json:"status"`
	Datetime      time.Time      `json:"datetime"`
	RelatedType   *string        `json:"related_type,omitempty"`
	RelatedID     *uuid.UUID     `json:"related_id,omitempty"`
	RelatedName   *string        `json:"related_name,omitempty"`
	CreatedBy     *uuid.UUID     `json:"created_by,omitempty"`
	CreatedByName *string        `json:"created_by_name,omitempty"`
}
