package handler

// File: buxgalteriya_validation.go
//
// Helpers enforcing TT Buxgalteriya ERP §4 business rules at the application layer.
// The same rules are enforced at the database layer by migration 319 triggers.
// We validate up-front so the buxgalter gets a precise, readable error before the
// transaction burns a journal number.

import (
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/genixerp/genix-backend/internal/domain/entity"
)

// parseOptionalUUID converts a *string from request input into a *uuid.UUID,
// returning nil when the string is nil, empty, or malformed. Malformed IDs are
// silently dropped so the spec's analytics presence rules will fire instead of
// a generic "invalid uuid" message.
func parseOptionalUUID(s *string) *uuid.UUID {
	if s == nil || *s == "" {
		return nil
	}
	u, err := uuid.Parse(*s)
	if err != nil {
		return nil
	}
	return &u
}

// accountRules captures the subset of account metadata needed for validation.
type accountRules struct {
	ID                 uuid.UUID
	Code               string
	Name               string
	IsLeaf             bool
	MandatoryAnalytics bool
	AnalyticsTypes     []string
}

// validateJournalLines checks the is_leaf (§4.2) and mandatory_analytics (§4.5)
// rules for every line of a pending journal entry. It batches the account lookup
// into a single query.
//
// Returns an empty string when everything is valid, else a user-facing message.
func (h *Handler) validateJournalLines(tenantID uuid.UUID, lines []entity.CreateJournalEntryLineInput) string {
	if len(lines) == 0 {
		return "Journal entry must have at least one line"
	}

	// Collect unique account IDs as strings for pq.Array with uuid[] cast.
	accountIDSet := make(map[string]bool, len(lines))
	accountIDStrs := make([]string, 0, len(lines))
	for i, line := range lines {
		if _, err := uuid.Parse(line.AccountID); err != nil {
			return fmt.Sprintf("Line %d: invalid account ID", i+1)
		}
		if !accountIDSet[line.AccountID] {
			accountIDSet[line.AccountID] = true
			accountIDStrs = append(accountIDStrs, line.AccountID)
		}
	}

	// Load account metadata in one query
	accountMeta := make(map[string]*accountRules, len(accountIDStrs))
	rows, err := h.db.Query(`
		SELECT id, code, name, COALESCE(is_leaf, true),
		       COALESCE(mandatory_analytics, false),
		       COALESCE(analytics_types, '[]'::jsonb)::text
		FROM accounts
		WHERE id = ANY($1::uuid[]) AND tenant_id = $2 AND deleted_at IS NULL
	`, pq.Array(accountIDStrs), tenantID)
	if err != nil {
		h.log.Error("validateJournalLines: account lookup failed", "error", err)
		return "Failed to validate accounts"
	}
	defer rows.Close()

	for rows.Next() {
		var rule accountRules
		var analyticsJSON string
		if err := rows.Scan(&rule.ID, &rule.Code, &rule.Name, &rule.IsLeaf,
			&rule.MandatoryAnalytics, &analyticsJSON); err != nil {
			continue
		}
		_ = json.Unmarshal([]byte(analyticsJSON), &rule.AnalyticsTypes)
		accountMeta[rule.ID.String()] = &rule
	}

	// Per-line validation
	for i, line := range lines {
		rule, ok := accountMeta[line.AccountID]
		if !ok {
			return fmt.Sprintf("Line %d: account %s not found", i+1, line.AccountID)
		}

		// TT §4.2 — only leaf accounts can be posted to
		if !rule.IsLeaf {
			return fmt.Sprintf(
				"Line %d: account %s (%s) is a group account — postings are only allowed on leaf accounts (TT §4.2)",
				i+1, rule.Code, rule.Name,
			)
		}

		// TT §4.5 — mandatory analytics must be filled
		if rule.MandatoryAnalytics {
			for _, dim := range rule.AnalyticsTypes {
				var missing bool
				switch dim {
				case "kontragent":
					missing = line.ContactID == nil || *line.ContactID == ""
				case "shartnoma":
					missing = line.ContractID == nil || *line.ContractID == ""
				case "ombor":
					missing = line.WarehouseID == nil || *line.WarehouseID == ""
				case "xodim":
					missing = line.EmployeeID == nil || *line.EmployeeID == ""
				}
				if missing {
					return fmt.Sprintf(
						"Line %d: account %s (%s) requires analytics dimension '%s' (TT §4.5)",
						i+1, rule.Code, rule.Name, dim,
					)
				}
			}
		}
	}

	return ""
}

// dbNullable is a tiny util to convert *string → sql.NullString.
func dbNullable(s *string) sql.NullString {
	if s == nil || *s == "" {
		return sql.NullString{Valid: false}
	}
	return sql.NullString{String: *s, Valid: true}
}
