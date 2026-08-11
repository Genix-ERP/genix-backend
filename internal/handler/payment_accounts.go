package handler

import (
	"github.com/google/uuid"
)

// Resolving the accounts a payment posts to.
//
// This existed as two findAccount calls inline in RecordPayment:
//
//	arAccountID   = findAccount(..., "accounts receivable", "4010")
//	cashAccountID = findAccount(..., "bank account", "5110")
//
// Both guess. They look for an ENGLISH account name, then one hardcoded code —
// and a tenant whose chart says "5100 · Bank hisob raqamlari" matches neither,
// so every payment was refused with "Payment accounts not configured" while a
// perfectly good bank journal, with that very account configured on it, sat in
// the dropdown the user had just chosen from.
//
// CORRECTION: the guessing was NOT the bug. findAccount has a tenant-wide
// exact-code step that matches a leaf 5110, which this chart has — so the
// original lookup worked. The real failure was the JOURNAL lookup, restricted
// to code IN ('CASH_RECEIPTS','CASH') so a bank journal resolved to nothing;
// that is fixed in RecordPayment and needed nothing here.
//
// What survives is only the part that is better on its own merits: a journal
// that names its own bank account should settle through THAT account, not
// through whichever account happens to carry the standard code. It matters as
// soon as a tenant runs two bank journals for two different banks — code-based
// lookup sends both to the same place.
//
// The fallback is left exactly as it was. An earlier version of this file
// widened it to match names like "bank" before trying codes, which would have
// matched "Bank kreditlari" — a loan liability — and posted customer payments
// into it.

// resolveJournalSettlementAccount returns the GL account a journal settles
// through, walking from the most explicit configuration to the least.
//
// Every candidate goes through resolveLeafAccount, because the field a user
// picks is often a GROUP: "5100 Bank hisob raqamlari" is the parent of 5110,
// 5120 and so on, and posting to a group trips the TT §4.2 invariant trigger
// and 500s the payment. Dropping to a leaf child makes the system tolerant of
// the way people actually fill these forms in.
func (h *Handler) resolveJournalSettlementAccount(q dbQuerier, tenantID, journalID uuid.UUID) uuid.UUID {
	if journalID == uuid.Nil {
		return uuid.Nil
	}

	// 1. The bank account attached to the journal, and its GL account. This is
	//    the most specific link there is: this journal moves money through
	//    that bank account, and that account posts to this GL code.
	var id uuid.UUID
	_ = q.QueryRow(`
		SELECT COALESCE(ba.account_id, '00000000-0000-0000-0000-000000000000')
		FROM journals j
		LEFT JOIN bank_accounts ba ON ba.id = j.bank_account_id
		WHERE j.id = $1 AND j.tenant_id = $2`, journalID, tenantID).Scan(&id)
	if acc := resolveLeafAccount(q, id); acc != uuid.Nil {
		return acc
	}

	// 2. The journal's own default debit account. Money arriving through this
	//    journal is a debit, so for a receipts journal this is exactly the
	//    account we want.
	_ = q.QueryRow(`
		SELECT COALESCE(default_debit_account_id, '00000000-0000-0000-0000-000000000000')
		FROM journals WHERE id = $1 AND tenant_id = $2`, journalID, tenantID).Scan(&id)
	if acc := resolveLeafAccount(q, id); acc != uuid.Nil {
		return acc
	}

	// Deliberately NOT falling back to suspense_account_id ("Tranzit
	// schyot"): that account is money IN FLIGHT, not where a settled payment
	// belongs. Using it would silently move where completed payments land for
	// any tenant that had filled the field in for its actual purpose.
	return uuid.Nil
}

// journalAccountDiagnostic explains a journal that HAS an account configured
// which still cannot be posted to.
//
// resolveLeafAccount returns nothing for a group account with no leaf
// descendants, and posting to a group itself trips the TT §4.2 invariant
// trigger — so there is genuinely nowhere valid to post, and the generic
// "fill in the journal" message would be wrong twice over: the user HAS filled
// it in, and the fix is in the chart of accounts, not the journal form.
//
// Returns "" when there is nothing to explain.
func (h *Handler) journalAccountDiagnostic(q dbQuerier, tenantID, journalID uuid.UUID) string {
	if journalID == uuid.Nil {
		return ""
	}
	var code, name string
	err := q.QueryRow(`
		SELECT a.code, a.name
		FROM journals j
		JOIN accounts a ON a.id = COALESCE(j.bank_account_id, j.default_debit_account_id, j.suspense_account_id)
		WHERE j.id = $1 AND j.tenant_id = $2 AND a.deleted_at IS NULL
		  AND COALESCE(a.is_leaf, true) = false
		  AND NOT EXISTS (
		      SELECT 1 FROM accounts ch
		      WHERE ch.parent_id = a.id AND ch.deleted_at IS NULL
		        AND COALESCE(ch.is_leaf, true) = true
		  )`, journalID, tenantID).Scan(&code, &name)
	if err != nil {
		return ""
	}
	return "jurnalga biriktirilgan \"" + code + " · " + name + "\" guruh schyoti va uning ostida " +
		"schyot yo'q — hisoblar rejasida shu guruh ostida bitta yakuniy schyot (masalan " +
		code + ".01) yarating"
}

// missingPaymentAccounts names what is actually missing.
//
// The old message listed every account the handler might have wanted —
// "(cash/bank journal, AR 4010, cash 5010 / bank 5110)" — which tells a user
// nothing about which of the three to go and configure. Reporting only the
// ones that failed turns the error into an instruction.
func missingPaymentAccounts(journal, ar, cash uuid.UUID) []string {
	var missing []string
	if journal == uuid.Nil {
		missing = append(missing, "to'lov jurnali (kassa yoki bank)")
	}
	if ar == uuid.Nil {
		missing = append(missing, "debitorlik schyoti (4010)")
	}
	if cash == uuid.Nil {
		missing = append(missing, "kassa/bank schyoti — jurnalning \"Bank hisobi\" yoki \"Tranzit schyot\" maydonini to'ldiring")
	}
	return missing
}

// ensureDefaultChart seeds the standard UzNAS chart (and default journals)
// for a tenant whose essential-account lookup has just failed.
//
// The lazy seeding this mirrors lives in ListAccounts: the chart appears the
// first time someone opens Moliya → Hisoblar rejasi. A tenant who goes
// straight to selling never opens that screen — their first contact with the
// chart of accounts was "To'lov qayd etilmadi — sozlanmagan: debitorlik
// schyoti (4010)", an error naming an account the system itself was supposed
// to have created. Posting sites call this when a required account resolves
// to nothing, then retry the lookup once.
//
// Both seeders are idempotent (ON CONFLICT on (tenant, org, code)), so
// calling this against a partially hand-built chart only fills the gaps and
// never duplicates or overwrites. It seeds h.db-side, outside the caller's
// open transaction — READ COMMITTED means the caller's retry sees the
// committed rows.
//
// Returns false when there is no organization to seed under (the chart is
// org-scoped) or nothing was attempted.
func (h *Handler) ensureDefaultChart(tenantID uuid.UUID, orgID *uuid.UUID) bool {
	if tenantID == uuid.Nil {
		return false
	}
	target := uuid.Nil
	if orgID != nil {
		target = *orgID
	}
	if target == uuid.Nil {
		// Documents posted without an organization still need the chart to
		// exist somewhere findAccount's tenant-wide steps can see: the
		// tenant's oldest organization, matching what onboarding seeds.
		_ = h.db.QueryRow(`
			SELECT id FROM organizations
			WHERE tenant_id = $1 AND deleted_at IS NULL
			ORDER BY created_at ASC LIMIT 1`, tenantID).Scan(&target)
		if target == uuid.Nil {
			return false
		}
	}

	if err := h.createDefaultChartOfAccounts(tenantID, target); err != nil {
		h.log.Warn("self-heal chart seeding failed", "error", err, "tenant_id", tenantID, "org_id", target)
		return false
	}

	// Same journal guard as ListAccounts: under 5 means broken (11 defaults).
	var journalCount int
	_ = h.db.QueryRow(`
		SELECT COUNT(*) FROM journals
		WHERE tenant_id = $1 AND organization_id = $2 AND deleted_at IS NULL`,
		tenantID, target).Scan(&journalCount)
	if journalCount < 5 {
		if err := h.createDefaultJournals(tenantID, target); err != nil {
			h.log.Warn("self-heal journal seeding failed", "error", err, "tenant_id", tenantID, "org_id", target)
		}
	}

	h.log.Info("self-healed missing chart of accounts at posting time",
		"tenant_id", tenantID, "org_id", target)
	return true
}
