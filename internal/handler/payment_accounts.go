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
// The fix is to stop guessing. A bank or cash journal already knows which
// account it settles through — that is what the journal IS — so the journal is
// asked first and the guessing is only a last resort for journals that predate
// anyone filling it in.

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

	// 3. The transit / suspense account ("Tranzit schyot" on the journal
	//    form). Not ideal — it is meant for money in flight — but a tenant who
	//    has filled only this field has still told us where this journal
	//    settles, and refusing the payment teaches them nothing.
	_ = q.QueryRow(`
		SELECT COALESCE(suspense_account_id, '00000000-0000-0000-0000-000000000000')
		FROM journals WHERE id = $1 AND tenant_id = $2`, journalID, tenantID).Scan(&id)
	if acc := resolveLeafAccount(q, id); acc != uuid.Nil {
		return acc
	}

	return uuid.Nil
}

// settlementAccountFallback is the guess, kept only for journals with nothing
// configured.
//
// The candidate lists are wider than the originals in both directions: the
// names now include what Uzbek and Russian charts actually call these
// accounts, and the codes cover the NAS §21 family rather than one member of
// it. "5110 Hisob-kitob schyoti" is a child of "5100 Bank hisob raqamlari", so
// a chart that only defines the parent used to match nothing at all.
func (h *Handler) settlementAccountFallback(q dbQuerier, tenantID uuid.UUID, orgID *uuid.UUID, method string) uuid.UUID {
	cashFirst := []string{"kassa", "cash", "касса"}
	bankFirst := []string{"hisob-kitob", "bank hisob", "bank account", "расчётный", "расчетный", "bank"}

	names := bankFirst
	codes := []string{"5110", "5100", "5130", "5010"}
	if method == "cash" {
		names = cashFirst
		codes = []string{"5010", "5000", "5110", "5100"}
	}

	for _, n := range names {
		if id := findAccount(q, tenantID, orgID, n, ""); id != uuid.Nil {
			return id
		}
	}
	for _, code := range codes {
		if id := findAccount(q, tenantID, orgID, "\x00no-name-match\x00", code); id != uuid.Nil {
			return id
		}
	}
	return uuid.Nil
}

// resolveReceivableAccount finds the AR control account.
//
// 4010 is standard enough in NAS §21 that the code lookup nearly always wins,
// but the English-only name search was the sole fallback and matched nothing on
// an Uzbek chart.
func (h *Handler) resolveReceivableAccount(q dbQuerier, tenantID uuid.UUID, orgID *uuid.UUID) uuid.UUID {
	for _, n := range []string{"accounts receivable", "xaridorlar", "debitor", "дебитор", "покупател"} {
		if id := findAccount(q, tenantID, orgID, n, ""); id != uuid.Nil {
			return id
		}
	}
	for _, code := range []string{"4010", "4000"} {
		if id := findAccount(q, tenantID, orgID, "\x00no-name-match\x00", code); id != uuid.Nil {
			return id
		}
	}
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
