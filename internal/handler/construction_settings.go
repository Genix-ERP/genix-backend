package handler

import (
	"database/sql"
	"strconv"
	"time"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// =====================================================
// CONSTRUCTION SETTINGS: COST CATEGORIES & ACCOUNT MAPPING
// =====================================================

// ListCostCategories returns construction cost categories for the tenant
func (h *Handler) ListCostCategories(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	// Auto-seed defaults if none exist
	h.seedDefaultCostCategories(tenantID)

	rows, err := h.db.Query(`
		SELECT id, tenant_id, code, name,
		       default_debit_account_id, default_credit_account_id,
		       is_active, created_at
		FROM construction_cost_categories
		WHERE tenant_id = $1 AND is_active = true
		ORDER BY id ASC
	`, tenantID)
	if err != nil {
		h.log.Error("Failed to list cost categories", "error", err)
		response.InternalError(c, "Failed to list cost categories")
		return
	}
	defer rows.Close()

	type CostCategory struct {
		ID                    int64      `json:"id"`
		TenantID              string     `json:"tenant_id"`
		Code                  string     `json:"code"`
		Name                  string     `json:"name"`
		DefaultDebitAccountID *uuid.UUID `json:"default_debit_account_id"`
		DefaultCreditAccountID *uuid.UUID `json:"default_credit_account_id"`
		IsActive              bool       `json:"is_active"`
		CreatedAt             time.Time  `json:"created_at"`
	}

	cats := []CostCategory{}
	for rows.Next() {
		var cat CostCategory
		if err := rows.Scan(
			&cat.ID, &cat.TenantID, &cat.Code, &cat.Name,
			&cat.DefaultDebitAccountID, &cat.DefaultCreditAccountID,
			&cat.IsActive, &cat.CreatedAt,
		); err != nil {
			continue
		}
		cats = append(cats, cat)
	}

	response.Success(c, cats)
}

// CreateCostCategory creates a new construction cost category
func (h *Handler) CreateCostCategory(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	var req struct {
		Code                   string `json:"code" binding:"required"`
		Name                   string `json:"name" binding:"required"`
		DefaultDebitAccountID  string `json:"default_debit_account_id"`
		DefaultCreditAccountID string `json:"default_credit_account_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	var id int64
	err := h.db.QueryRow(`
		INSERT INTO construction_cost_categories
		    (tenant_id, code, name, default_debit_account_id, default_credit_account_id, is_active, created_at)
		VALUES ($1, $2, $3, $4, $5, true, NOW())
		RETURNING id
	`,
		tenantID, req.Code, req.Name,
		nullUUIDFromVal(req.DefaultDebitAccountID),
		nullUUIDFromVal(req.DefaultCreditAccountID),
	).Scan(&id)
	if err != nil {
		h.log.Error("Failed to create cost category", "error", err)
		response.InternalError(c, "Failed to create cost category")
		return
	}

	response.Success(c, map[string]interface{}{"id": id, "message": "Cost category created"})
}

// UpdateCostCategory updates a cost category
func (h *Handler) UpdateCostCategory(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid ID")
		return
	}

	var req struct {
		Name                   string `json:"name"`
		DefaultDebitAccountID  string `json:"default_debit_account_id"`
		DefaultCreditAccountID string `json:"default_credit_account_id"`
		IsActive               *bool  `json:"is_active"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	_, err = h.db.Exec(`
		UPDATE construction_cost_categories
		SET name = COALESCE(NULLIF($1,''), name),
		    default_debit_account_id  = CASE WHEN $2::text = '' THEN default_debit_account_id ELSE $2::uuid END,
		    default_credit_account_id = CASE WHEN $3::text = '' THEN default_credit_account_id ELSE $3::uuid END,
		    is_active = COALESCE($4, is_active)
		WHERE id = $5 AND tenant_id = $6
	`, req.Name, req.DefaultDebitAccountID, req.DefaultCreditAccountID, req.IsActive, id, tenantID)
	if err != nil {
		h.log.Error("Failed to update cost category", "error", err)
		response.InternalError(c, "Failed to update cost category")
		return
	}

	response.Success(c, map[string]interface{}{"message": "Cost category updated"})
}

// GetAccountMapping returns the construction account mapping for the tenant
func (h *Handler) GetAccountMapping(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	rows, err := h.db.Query(`
		SELECT m.mapping_key, m.account_id, a.code, a.name
		FROM construction_account_mapping m
		JOIN accounts a ON a.id = m.account_id
		WHERE m.tenant_id = $1
		ORDER BY m.mapping_key ASC
	`, tenantID)
	if err != nil {
		h.log.Error("Failed to get account mapping", "error", err)
		response.InternalError(c, "Failed to get account mapping")
		return
	}
	defer rows.Close()

	type MappingEntry struct {
		Key         string `json:"key"`
		AccountID   string `json:"account_id"`
		AccountCode string `json:"account_code"`
		AccountName string `json:"account_name"`
	}

	result := []MappingEntry{}
	for rows.Next() {
		var e MappingEntry
		if err := rows.Scan(&e.Key, &e.AccountID, &e.AccountCode, &e.AccountName); err != nil {
			continue
		}
		result = append(result, e)
	}

	response.Success(c, result)
}

// UpsertAccountMapping sets construction account mappings (array of {key, account_id})
func (h *Handler) UpsertAccountMapping(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	var req []struct {
		Key       string `json:"key" binding:"required"`
		AccountID string `json:"account_id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	now := time.Now()
	for _, entry := range req {
		accountID, err := uuid.Parse(entry.AccountID)
		if err != nil {
			response.BadRequest(c, "Invalid account_id: "+entry.AccountID)
			return
		}
		_, err = h.db.Exec(`
			INSERT INTO construction_account_mapping (tenant_id, mapping_key, account_id, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $4)
			ON CONFLICT (tenant_id, mapping_key)
			DO UPDATE SET account_id = EXCLUDED.account_id, updated_at = EXCLUDED.updated_at
		`, tenantID, entry.Key, accountID, now)
		if err != nil {
			h.log.Error("Failed to upsert account mapping", "error", err, "key", entry.Key)
			response.InternalError(c, "Failed to save mapping for key: "+entry.Key)
			return
		}
	}

	response.Success(c, map[string]interface{}{"message": "Account mapping saved"})
}

// =====================================================
// INTERNAL HELPERS
// =====================================================

// getConstructionMappedAccount looks up construction_account_mapping first,
// then falls back to findAccount() heuristic.
func (h *Handler) getConstructionMappedAccount(tenantID uuid.UUID, orgIDPtr *uuid.UUID, mappingKey string, fallbackName string, fallbackCode string) uuid.UUID {
	var accountID uuid.UUID
	err := h.db.QueryRow(`
		SELECT account_id FROM construction_account_mapping
		WHERE tenant_id = $1 AND mapping_key = $2
	`, tenantID, mappingKey).Scan(&accountID)
	if err == nil && accountID != uuid.Nil {
		return accountID
	}
	return findAccount(h.db, tenantID, orgIDPtr, fallbackName, fallbackCode)
}

// seedDefaultCostCategories inserts default categories if none exist for a tenant
func (h *Handler) seedDefaultCostCategories(tenantID uuid.UUID) {
	var count int
	_ = h.db.QueryRow(`SELECT COUNT(*) FROM construction_cost_categories WHERE tenant_id = $1`, tenantID).Scan(&count)
	if count > 0 {
		return
	}

	defaults := []struct {
		code string
		name string
	}{
		{"materials", "Materiallar (Qurilish materiallari)"},
		{"labor", "Mehnat haqi (Ish haqi)"},
		{"equipment", "Texnika va uskunalar"},
		{"overhead", "Umumiy xarajatlar (Overhead)"},
	}

	for _, d := range defaults {
		_, err := h.db.Exec(`
			INSERT INTO construction_cost_categories (tenant_id, code, name, is_active, created_at)
			VALUES ($1, $2, $3, true, NOW())
			ON CONFLICT (tenant_id, code) DO NOTHING
		`, tenantID, d.code, d.name)
		if err != nil {
			_ = err // best-effort
		}
	}
}

// ensureConstructionJournal ensures a CONST journal exists for the tenant, returning its ID
func (h *Handler) ensureConstructionJournal(tenantID uuid.UUID, orgIDPtr *uuid.UUID) uuid.UUID {
	var journalID uuid.UUID
	err := h.db.QueryRow(`
		SELECT id FROM journals
		WHERE tenant_id = $1 AND code = 'CONST' AND deleted_at IS NULL
		LIMIT 1
	`, tenantID).Scan(&journalID)
	if err == nil {
		return journalID
	}

	// Try to find any suitable journal as fallback
	err = h.db.QueryRow(`
		SELECT id FROM journals
		WHERE tenant_id = $1 AND code IN ('STOCK','INV','MISC','GENERAL','GEN') AND deleted_at IS NULL
		ORDER BY CASE code WHEN 'STOCK' THEN 0 WHEN 'INV' THEN 1 WHEN 'MISC' THEN 2 ELSE 3 END
		LIMIT 1
	`, tenantID).Scan(&journalID)
	if err == nil {
		return journalID
	}

	// Create a CONST journal
	orgVal := interface{}(nil)
	if orgIDPtr != nil {
		orgVal = *orgIDPtr
	}
	var nextNum = 1
	newID := uuid.New()
	insertErr := h.db.QueryRow(`
		INSERT INTO journals (id, tenant_id, organization_id, code, name, type, next_number, is_active, created_at, updated_at)
		VALUES ($1, $2, $3, 'CONST', 'Construction Journal', 'general', $4, true, NOW(), NOW())
		ON CONFLICT DO NOTHING
		RETURNING id
	`, newID, tenantID, orgVal, nextNum).Scan(&journalID)
	if insertErr != nil {
		// Return zero UUID — callers handle this gracefully (best-effort)
		_ = insertErr
	}
	if journalID == uuid.Nil {
		// Re-query in case of conflict
		_ = h.db.QueryRow(`SELECT id FROM journals WHERE tenant_id = $1 AND code = 'CONST' LIMIT 1`, tenantID).Scan(&journalID)
	}

	return journalID
}

// getNextJournalNumber returns the next_number for a journal and increments it
func (h *Handler) getNextJournalNumber(tx interface {
	QueryRow(string, ...interface{}) *sql.Row
	Exec(string, ...interface{}) (sql.Result, error)
}, journalID uuid.UUID) int {
	var n int
	_ = tx.QueryRow(`SELECT COALESCE(next_number, 1) FROM journals WHERE id = $1`, journalID).Scan(&n)
	_, _ = tx.Exec(`UPDATE journals SET next_number = next_number + 1, updated_at = NOW() WHERE id = $1`, journalID)
	return n
}
