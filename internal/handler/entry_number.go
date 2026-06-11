package handler

import "database/sql"

// entryNumberQuerier is satisfied by both *sql.Tx and *database.DB.
type entryNumberQuerier interface {
	QueryRow(query string, args ...interface{}) *sql.Row
}

// nextEntryNumberSeq returns the next numeric suffix for a journal entry
// number with the given prefix. Entry numbers are unique per
// (tenant, organization) across ALL journals
// (journal_entries_tenant_org_entry_number_key), so the max must be computed
// at that scope: a per-journal counter or per-journal MAX collides as soon as
// two journals share a number_prefix (all seeded journals have an empty one)
// or two call sites resolve the same prefix against different journals.
//
// seed is the journal's next_number counter; the result is never below it so
// existing sequences keep advancing.
func nextEntryNumberSeq(q entryNumberQuerier, tenantID, orgID interface{}, prefix string, seed int) int {
	var maxNum int
	if prefix != "" {
		_ = q.QueryRow(
			`SELECT COALESCE(MAX(CAST(NULLIF(REGEXP_REPLACE(entry_number, '[^0-9]', '', 'g'), '') AS BIGINT)), 0)
			 FROM journal_entries
			 WHERE tenant_id = $1 AND organization_id IS NOT DISTINCT FROM $2 AND entry_number LIKE $3 AND deleted_at IS NULL`,
			tenantID, orgID, prefix+"%",
		).Scan(&maxNum)
	} else {
		_ = q.QueryRow(
			`SELECT COALESCE(MAX(CAST(NULLIF(REGEXP_REPLACE(entry_number, '[^0-9]', '', 'g'), '') AS BIGINT)), 0)
			 FROM journal_entries
			 WHERE tenant_id = $1 AND organization_id IS NOT DISTINCT FROM $2 AND deleted_at IS NULL`,
			tenantID, orgID,
		).Scan(&maxNum)
	}
	next := maxNum + 1
	if seed > next {
		next = seed
	}
	return next
}
