package entity

import (
	"database/sql"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// =====================================================
// CONSTRUCTION WBS (Work Breakdown Structure)
// =====================================================

// ConstructionWBS represents a work breakdown structure item
type ConstructionWBS struct {
	ID         int64         `json:"id" db:"id"`
	TenantID   uuid.UUID     `json:"tenant_id" db:"tenant_id"`
	ProjectID  int64         `json:"project_id" db:"project_id"`
	BuildingID sql.NullInt64 `json:"building_id" db:"building_id"`
	ParentID   sql.NullInt64 `json:"parent_id" db:"parent_id"`

	// Identification
	Code        string         `json:"code" db:"code"`
	Name        string         `json:"name" db:"name"`
	Description sql.NullString `json:"description" db:"description"`

	// Planned dates
	DateStartPlan   sql.NullTime `json:"date_start_plan" db:"date_start_plan"`
	DateEndPlan     sql.NullTime `json:"date_end_plan" db:"date_end_plan"`
	DateStartActual sql.NullTime `json:"date_start_actual" db:"date_start_actual"`
	DateEndActual   sql.NullTime `json:"date_end_actual" db:"date_end_actual"`

	// Budget & Progress
	BudgetAmount float64 `json:"budget_amount" db:"budget_amount"`
	Progress     float64 `json:"progress" db:"progress"`

	// Ordering & assignment
	SortOrder     int           `json:"sort_order" db:"sort_order"`
	ResponsibleID uuid.NullUUID `json:"responsible_id" db:"responsible_id"`
	IsActive      bool          `json:"is_active" db:"is_active"`

	CreatedDate time.Time `json:"created_date" db:"created_date"`
	UpdatedDate time.Time `json:"updated_date" db:"updated_date"`

	// Computed fields (not in DB)
	ResponsibleName string `json:"responsible_name,omitempty" db:"responsible_name"`
	ChildrenCount   int    `json:"children_count,omitempty" db:"children_count"`
	BuildingName    string `json:"building_name,omitempty" db:"building_name"`
}

// MarshalJSON custom marshaler for ConstructionWBS
func (w ConstructionWBS) MarshalJSON() ([]byte, error) {
	return json.Marshal(&struct {
		ID              int64       `json:"id"`
		TenantID        uuid.UUID   `json:"tenant_id"`
		ProjectID       int64       `json:"project_id"`
		BuildingID      interface{} `json:"building_id"`
		ParentID        interface{} `json:"parent_id"`
		Code            string      `json:"code"`
		Name            string      `json:"name"`
		Description     interface{} `json:"description"`
		DateStartPlan   interface{} `json:"date_start_plan"`
		DateEndPlan     interface{} `json:"date_end_plan"`
		DateStartActual interface{} `json:"date_start_actual"`
		DateEndActual   interface{} `json:"date_end_actual"`
		BudgetAmount    float64     `json:"budget_amount"`
		Progress        float64     `json:"progress"`
		SortOrder       int         `json:"sort_order"`
		ResponsibleID   interface{} `json:"responsible_id"`
		IsActive        bool        `json:"is_active"`
		CreatedDate     time.Time   `json:"created_date"`
		UpdatedDate     time.Time   `json:"updated_date"`
		ResponsibleName string      `json:"responsible_name,omitempty"`
		ChildrenCount   int         `json:"children_count,omitempty"`
		BuildingName    string      `json:"building_name,omitempty"`
	}{
		ID:              w.ID,
		TenantID:        w.TenantID,
		ProjectID:       w.ProjectID,
		BuildingID:      nullInt64Value(w.BuildingID),
		ParentID:        nullInt64Value(w.ParentID),
		Code:            w.Code,
		Name:            w.Name,
		Description:     nullStringValue(w.Description),
		DateStartPlan:   nullTimeValue(w.DateStartPlan),
		DateEndPlan:     nullTimeValue(w.DateEndPlan),
		DateStartActual: nullTimeValue(w.DateStartActual),
		DateEndActual:   nullTimeValue(w.DateEndActual),
		BudgetAmount:    w.BudgetAmount,
		Progress:        w.Progress,
		SortOrder:       w.SortOrder,
		ResponsibleID:   nullUUIDValue(w.ResponsibleID),
		IsActive:        w.IsActive,
		CreatedDate:     w.CreatedDate,
		UpdatedDate:     w.UpdatedDate,
		ResponsibleName: w.ResponsibleName,
		ChildrenCount:   w.ChildrenCount,
		BuildingName:    w.BuildingName,
	})
}

// CreateWBSInput represents input for creating a WBS item
type CreateWBSInput struct {
	BuildingID    int64   `json:"building_id"`
	ParentID      int64   `json:"parent_id"`
	Code          string  `json:"code"`
	Name          string  `json:"name" binding:"required"`
	Description   string  `json:"description"`
	DateStartPlan string  `json:"date_start_plan"`
	DateEndPlan   string  `json:"date_end_plan"`
	BudgetAmount  float64 `json:"budget_amount"`
	SortOrder     int     `json:"sort_order"`
	ResponsibleID string  `json:"responsible_id"`
}

// UpdateWBSInput represents input for updating a WBS item
type UpdateWBSInput struct {
	BuildingID      *int64   `json:"building_id"`
	ParentID        *int64   `json:"parent_id"`
	Code            *string  `json:"code"`
	Name            *string  `json:"name"`
	Description     *string  `json:"description"`
	DateStartPlan   *string  `json:"date_start_plan"`
	DateEndPlan     *string  `json:"date_end_plan"`
	DateStartActual *string  `json:"date_start_actual"`
	DateEndActual   *string  `json:"date_end_actual"`
	BudgetAmount    *float64 `json:"budget_amount"`
	Progress        *float64 `json:"progress"`
	SortOrder       *int     `json:"sort_order"`
	ResponsibleID   *string  `json:"responsible_id"`
	IsActive        *bool    `json:"is_active"`
}

// ReorderWBSInput represents input for reordering WBS items
type ReorderWBSInput struct {
	Items []WBSOrderItem `json:"items" binding:"required,dive"`
}

// WBSOrderItem represents a single WBS item reorder entry
type WBSOrderItem struct {
	ID        int64 `json:"id" binding:"required"`
	SortOrder int   `json:"sort_order"`
	ParentID  int64 `json:"parent_id"`
}

// =====================================================
// CONSTRUCTION ACTIVITY LOG
// =====================================================

// ConstructionActivityLog represents an activity log entry
type ConstructionActivityLog struct {
	ID        int64         `json:"id" db:"id"`
	TenantID  uuid.UUID     `json:"tenant_id" db:"tenant_id"`
	ProjectID int64         `json:"project_id" db:"project_id"`
	UserID    uuid.NullUUID `json:"user_id" db:"user_id"`

	ActionType   string          `json:"action_type" db:"action_type"`
	Description  string          `json:"description" db:"description"`
	RelatedModel sql.NullString  `json:"related_model" db:"related_model"`
	RelatedID    sql.NullInt64   `json:"related_id" db:"related_id"`
	Metadata     json.RawMessage `json:"metadata" db:"metadata"`

	CreatedAt time.Time `json:"created_at" db:"created_at"`

	// Computed fields
	UserName string `json:"user_name,omitempty" db:"user_name"`
}

// MarshalJSON custom marshaler for ConstructionActivityLog
func (a ConstructionActivityLog) MarshalJSON() ([]byte, error) {
	return json.Marshal(&struct {
		ID           int64           `json:"id"`
		TenantID     uuid.UUID       `json:"tenant_id"`
		ProjectID    int64           `json:"project_id"`
		UserID       interface{}     `json:"user_id"`
		ActionType   string          `json:"action_type"`
		Description  string          `json:"description"`
		RelatedModel interface{}     `json:"related_model"`
		RelatedID    interface{}     `json:"related_id"`
		Metadata     json.RawMessage `json:"metadata"`
		CreatedAt    time.Time       `json:"created_at"`
		UserName     string          `json:"user_name,omitempty"`
	}{
		ID:           a.ID,
		TenantID:     a.TenantID,
		ProjectID:    a.ProjectID,
		UserID:       nullUUIDValue(a.UserID),
		ActionType:   a.ActionType,
		Description:  a.Description,
		RelatedModel: nullStringValue(a.RelatedModel),
		RelatedID:    nullInt64Value(a.RelatedID),
		Metadata:     a.Metadata,
		CreatedAt:    a.CreatedAt,
		UserName:     a.UserName,
	})
}
