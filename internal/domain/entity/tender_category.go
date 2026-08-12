package entity

import (
	"time"

	"github.com/google/uuid"
)

type TenderCategory struct {
	ID        uuid.UUID        `json:"id" db:"id"`
	TenantID  *uuid.UUID       `json:"tenant_id" db:"tenant_id"`
	ParentID  *uuid.UUID       `json:"parent_id" db:"parent_id"`
	Name      string           `json:"name" db:"name"`
	NameRu    string           `json:"name_ru" db:"name_ru"`
	Slug      string           `json:"slug" db:"slug"`
	Icon      string           `json:"icon" db:"icon"`
	Banner    string           `json:"banner" db:"banner"`
	Level     int              `json:"level" db:"level"`
	SortOrder int              `json:"sort_order" db:"sort_order"`
	IsActive  bool             `json:"is_active" db:"is_active"`
	CreatedAt time.Time        `json:"created_at" db:"created_at"`
	UpdatedAt time.Time        `json:"updated_at" db:"updated_at"`
	DeletedAt *time.Time       `json:"deleted_at,omitempty" db:"deleted_at"`
	Children  []TenderCategory `json:"children,omitempty" db:"-"`
}

type CreateTenderCategoryInput struct {
	ParentID  *uuid.UUID `json:"parent_id"`
	Name      string     `json:"name" binding:"required"`
	NameRu    string     `json:"name_ru"`
	Slug      string     `json:"slug" binding:"required"`
	Icon      string     `json:"icon"`
	Banner    string     `json:"banner"`
	Level     int        `json:"level"`
	SortOrder int        `json:"sort_order"`
}

type TenderCategoryResponse struct {
	ID       uuid.UUID                `json:"id"`
	ParentID *uuid.UUID               `json:"parent_id"`
	Name     string                   `json:"name"`
	NameRu   string                   `json:"name_ru"`
	Slug     string                   `json:"slug"`
	Icon     string                   `json:"icon"`
	Level    int                      `json:"level"`
	Children []TenderCategoryResponse `json:"children,omitempty"`
}
