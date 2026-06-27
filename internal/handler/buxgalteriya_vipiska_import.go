package handler

// File: buxgalteriya_vipiska_import.go
//
// TZ "Bank ko'chirmasini (vipiska) ERP tizimiga yuklash" — Phase 1: parse the
// bank's Excel statement (Turonbank vipiska format), auto-classify every
// operation to its ERP debit/credit accounts, store it, and return the rows for
// the review screen. NO journal posting happens here — that is Phase 2.
//
// Excel layout (1-based rows):
//   1  <MFO> / <bank name>                         | <date>
//   2  Сведения о работе счета с DD.MM.YYYY по DD.MM.YYYY
//   3  Счет: <20-digit acct>  "<company>"  ИНН: <inn>
//   4  Остаток на начало периода: X | Остаток на конец периода: Y
//   5  Дата | Счет/ИНН | № док-та | Оп | МФО | Оборот Дебет | Оборот Кредит | Назначение
//   6+ operations
//   last  Итоговый оборот за период | ... | <debet total> | <kredit total>
//
// Bank Dr/Cr is REVERSED vs the ERP: bank Kredit (money in) -> Dt 5110;
// bank Debet (money out) -> Cr 5110. amount = Kredit - Debet.

import (
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/xuri/excelize/v2"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
)

const vipiskaBankAccountCode = "5110" // ERP settlement (bank) account

type vipiskaTxn struct {
	LineNumber          int     `json:"line_number"`
	DocNumber           string  `json:"doc_number"`
	DocDate             string  `json:"doc_date"`
	OpCode              string  `json:"op_code"`
	MFO                 string  `json:"mfo"`
	CounterpartyAccount string  `json:"counterparty_account"`
	CounterpartyINN     string  `json:"counterparty_inn"`
	CounterpartyName    string  `json:"counterparty_name"`
	AccountPrefix       string  `json:"account_prefix"`
	BankDebet           float64 `json:"bank_debet"`
	BankKredit          float64 `json:"bank_kredit"`
	Amount              float64 `json:"amount"`
	Direction           string  `json:"direction"` // in | out
	PurposeCode         string  `json:"purpose_code"`
	Purpose             string  `json:"purpose"`
	Category            string  `json:"category"`
	DebetCode           string  `json:"debet_account_code"`
	KreditCode          string  `json:"kredit_account_code"`
	Status              string  `json:"status"` // suggested | unmatched

	docDateT     time.Time
	targetCode   string
	debetAcctID  *uuid.UUID
	kreditAcctID *uuid.UUID
	contactID    *uuid.UUID
}

type vipiskaMeta struct {
	Account        string
	INN            string
	PeriodFrom     time.Time
	PeriodTo       time.Time
	OpeningBalance float64
	ClosingBalance float64
	TotalDebet     float64
	TotalKredit    float64
}

type classRule struct {
	matchType  string
	matchValue string
	direction  string
	targetCode string
	category   string
}

var (
	reAcctNum  = regexp.MustCompile(`\d{16,24}`)
	reINN      = regexp.MustCompile(`(?i)ИНН[:\s]*([0-9]{6,14})`)
	rePeriod   = regexp.MustCompile(`(\d{2}\.\d{2}\.\d{4}).{0,6}?(\d{2}\.\d{2}\.\d{4})`)
	reOpenBal  = regexp.MustCompile(`(?i)начал[оа][^:]*:\s*([0-9][0-9 .,\x{00a0}]*)`)
	reCloseBal = regexp.MustCompile(`(?i)конец[^:]*:\s*([0-9][0-9 .,\x{00a0}]*)`)
)

// ImportBankVipiska accepts a multipart form with "file" (the bank .xlsx),
// parses + classifies it, stores the import, and returns the review rows.
func (h *Handler) ImportBankVipiska(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	userID, _ := middleware.GetUserID(c)
	orgID, _ := middleware.GetOrganizationID(c)
	var orgPtr *uuid.UUID
	var orgArg interface{}
	if orgID != uuid.Nil {
		orgPtr = &orgID
		orgArg = orgID
	}

	fileHdr, err := c.FormFile("file")
	if err != nil {
		response.BadRequest(c, "file is required")
		return
	}
	f, err := fileHdr.Open()
	if err != nil {
		response.InternalError(c, "Failed to open file")
		return
	}
	defer f.Close()

	xl, err := excelize.OpenReader(f)
	if err != nil {
		response.BadRequest(c, "Could not read the Excel file: "+err.Error())
		return
	}
	defer xl.Close()
	sheets := xl.GetSheetList()
	if len(sheets) == 0 {
		response.BadRequest(c, "Excel file has no sheets")
		return
	}
	rows, err := xl.GetRows(sheets[0])
	if err != nil {
		response.BadRequest(c, "Could not read sheet rows: "+err.Error())
		return
	}

	txns, meta, warnings := parseVipiska(rows)
	if len(txns) == 0 {
		response.BadRequest(c, "No operations were parsed — is this a Turonbank vipiska Excel?")
		return
	}

	// Resolve the bank account (5110) once.
	bankID, bankCode := h.resolveAccountIDByCode(tenantID, orgArg, vipiskaBankAccountCode)

	// Load classification rules (tenant-specific first, then global defaults).
	rules := h.loadClassificationRules(tenantID)

	// Classify + resolve accounts.
	for i := range txns {
		t := &txns[i]
		classifyVipiska(t, rules)
		if t.targetCode != "" {
			tid, tcode := h.resolveAccountIDByCode(tenantID, orgArg, t.targetCode)
			if t.Direction == "in" {
				// money in: Dt bank / Cr target
				t.DebetCode, t.KreditCode = bankCode, tcode
				if bankID != uuid.Nil {
					b := bankID
					t.debetAcctID = &b
				}
				if tid != uuid.Nil {
					t.kreditAcctID = &tid
				}
			} else {
				// money out: Dt target / Cr bank
				t.DebetCode, t.KreditCode = tcode, bankCode
				if tid != uuid.Nil {
					t.debetAcctID = &tid
				}
				if bankID != uuid.Nil {
					b := bankID
					t.kreditAcctID = &b
				}
			}
			if t.DebetCode == "" {
				t.DebetCode = ternaryStr(t.Direction == "in", bankCode, t.targetCode)
			}
			if t.KreditCode == "" {
				t.KreditCode = ternaryStr(t.Direction == "in", t.targetCode, bankCode)
			}
			t.Status = "suggested"
		} else {
			t.Status = "unmatched"
		}
		// Best-effort contact match by INN (for display + later posting).
		if t.CounterpartyINN != "" {
			var cid uuid.UUID
			if e := h.db.QueryRow(`SELECT id FROM contacts WHERE tenant_id=$1 AND tax_id=$2 AND deleted_at IS NULL LIMIT 1`,
				tenantID, t.CounterpartyINN).Scan(&cid); e == nil {
				t.contactID = &cid
			}
		}
	}

	// Persist the import + lines.
	importID := uuid.New()
	now := time.Now()
	var stmtDate interface{}
	if !meta.PeriodTo.IsZero() {
		stmtDate = meta.PeriodTo
	}
	suggested := 0
	for _, t := range txns {
		if t.Status == "suggested" {
			suggested++
		}
	}

	if _, err := h.db.Exec(`
		INSERT INTO bank_statement_imports (
			id, tenant_id, organization_id, format, file_name, file_size,
			statement_date, opening_balance, closing_balance,
			total_credit, total_debit, transaction_count, matched_count, unmatched_count,
			status, imported_by, imported_at, created_at
		) VALUES ($1,$2,$3,'excel_vipiska',$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,'imported',$14,$15,$15)`,
		importID, tenantID, orgPtr, fileHdr.Filename, fileHdr.Size,
		stmtDate, meta.OpeningBalance, meta.ClosingBalance,
		meta.TotalKredit, meta.TotalDebet, len(txns), suggested, len(txns)-suggested,
		userID, now); err != nil {
		h.log.Error("vipiska import: insert header", "error", err)
		response.InternalError(c, "Failed to save import")
		return
	}

	for _, t := range txns {
		var docDateArg interface{}
		if !t.docDateT.IsZero() {
			docDateArg = t.docDateT
		}
		if _, err := h.db.Exec(`
			INSERT INTO bank_statement_transactions (
				id, import_id, line_number, doc_number, doc_date,
				counterparty_name, counterparty_inn, counterparty_account,
				op_code, mfo, account_prefix, amount, direction,
				purpose, purpose_code, category,
				debet_account_id, kredit_account_id, debet_account_code, kredit_account_code,
				matched_contact_id, match_status, created_at
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23)`,
			uuid.New(), importID, t.LineNumber, t.DocNumber, docDateArg,
			t.CounterpartyName, t.CounterpartyINN, t.CounterpartyAccount,
			t.OpCode, t.MFO, t.AccountPrefix, t.Amount, t.Direction,
			t.Purpose, t.PurposeCode, nullIfEmpty(t.Category),
			t.debetAcctID, t.kreditAcctID, nullIfEmpty(t.DebetCode), nullIfEmpty(t.KreditCode),
			t.contactID, t.Status, now); err != nil {
			h.log.Error("vipiska import: insert line", "error", err, "line", t.LineNumber)
		}
	}

	// Balance integrity check (informational warning only).
	expectedClose := meta.OpeningBalance + meta.TotalKredit - meta.TotalDebet
	balanceOK := absf(expectedClose-meta.ClosingBalance) < 0.01

	response.Success(c, gin.H{
		"import_id":         importID,
		"account":           meta.Account,
		"inn":               meta.INN,
		"opening_balance":   meta.OpeningBalance,
		"closing_balance":   meta.ClosingBalance,
		"total_credit":      meta.TotalKredit,
		"total_debit":       meta.TotalDebet,
		"transaction_count": len(txns),
		"matched_count":     suggested,
		"unmatched_count":   len(txns) - suggested,
		"balance_ok":        balanceOK,
		"transactions":      txns,
		"warnings":          warnings,
	})
}

// GetBankVipiskaTransactions returns the stored lines of an import for review.
func (h *Handler) GetBankVipiskaTransactions(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	importID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid import id")
		return
	}
	// Ownership check.
	var exists bool
	_ = h.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM bank_statement_imports WHERE id=$1 AND tenant_id=$2)`, importID, tenantID).Scan(&exists)
	if !exists {
		response.NotFound(c, "Import not found")
		return
	}
	rows, err := h.db.Query(`
		SELECT line_number, doc_number, doc_date, counterparty_name, counterparty_inn,
		       counterparty_account, COALESCE(op_code,''), COALESCE(mfo,''), COALESCE(account_prefix,''),
		       amount, direction, COALESCE(purpose,''), COALESCE(purpose_code,''), COALESCE(category,''),
		       COALESCE(debet_account_code,''), COALESCE(kredit_account_code,''), match_status
		FROM bank_statement_transactions
		WHERE import_id=$1 ORDER BY line_number ASC`, importID)
	if err != nil {
		response.InternalError(c, "Failed to load transactions")
		return
	}
	defer rows.Close()
	out := make([]gin.H, 0)
	for rows.Next() {
		var (
			ln                                         int
			doc, name, inn, acct, op, mfo, pfx         string
			amount                                     float64
			dir, purpose, pcode, cat, dcode, kcode, st string
			docDate                                    *time.Time
		)
		if rows.Scan(&ln, &doc, &docDate, &name, &inn, &acct, &op, &mfo, &pfx,
			&amount, &dir, &purpose, &pcode, &cat, &dcode, &kcode, &st) != nil {
			continue
		}
		row := gin.H{
			"line_number": ln, "doc_number": doc, "counterparty_name": name,
			"counterparty_inn": inn, "counterparty_account": acct, "op_code": op, "mfo": mfo,
			"account_prefix": pfx, "amount": amount, "direction": dir, "purpose": purpose,
			"purpose_code": pcode, "category": cat, "debet_account_code": dcode,
			"kredit_account_code": kcode, "status": st,
		}
		if docDate != nil {
			row["doc_date"] = docDate.Format("2006-01-02")
		}
		out = append(out, row)
	}
	response.Success(c, out)
}

// ---------------------------------------------------------------------------
// Parsing
// ---------------------------------------------------------------------------

func parseVipiska(rows [][]string) ([]vipiskaTxn, vipiskaMeta, []string) {
	var meta vipiskaMeta
	var warnings []string
	var txns []vipiskaTxn

	// Find the header row (contains "Дата" and "Назначение").
	headerIdx := -1
	for i, r := range rows {
		joined := strings.ToLower(strings.Join(r, " "))
		if strings.Contains(joined, "дата") && strings.Contains(joined, "назначение") {
			headerIdx = i
			break
		}
	}

	// Metadata from the rows above the header.
	for i := 0; i < len(rows) && (headerIdx < 0 || i < headerIdx); i++ {
		line := strings.Join(rows[i], " ")
		low := strings.ToLower(line)
		if strings.Contains(low, "счет:") || strings.Contains(low, "счёт:") {
			if m := reAcctNum.FindString(line); m != "" && meta.Account == "" {
				meta.Account = m
			}
			if m := reINN.FindStringSubmatch(line); len(m) > 1 {
				meta.INN = m[1]
			}
		}
		if strings.Contains(low, "сведения о работе") || strings.Contains(low, " с ") {
			if m := rePeriod.FindStringSubmatch(line); len(m) > 2 {
				meta.PeriodFrom, _ = parseDotDate(m[1])
				meta.PeriodTo, _ = parseDotDate(m[2])
			}
		}
		// Balances — robust to merged or split cells (parse off the joined line).
		if strings.Contains(low, "остаток") || strings.Contains(low, "начал") || strings.Contains(low, "конец") {
			if m := reOpenBal.FindStringSubmatch(line); len(m) > 1 && meta.OpeningBalance == 0 {
				meta.OpeningBalance = parseVipiskaAmount(m[1])
			}
			if m := reCloseBal.FindStringSubmatch(line); len(m) > 1 && meta.ClosingBalance == 0 {
				meta.ClosingBalance = parseVipiskaAmount(m[1])
			}
		}
	}

	if headerIdx < 0 {
		warnings = append(warnings, "header row not found")
		return txns, meta, warnings
	}

	line := 0
	for i := headerIdx + 1; i < len(rows); i++ {
		r := rows[i]
		if len(r) == 0 {
			continue
		}
		first := strings.TrimSpace(cell(r, 0))
		low := strings.ToLower(first)
		// Totals row ends the statement.
		if strings.Contains(low, "итог") {
			meta.TotalDebet = parseVipiskaAmount(cell(r, 5))
			meta.TotalKredit = parseVipiskaAmount(cell(r, 6))
			break
		}
		if first == "" {
			continue
		}
		debet := parseVipiskaAmount(cell(r, 5))
		kredit := parseVipiskaAmount(cell(r, 6))
		if debet == 0 && kredit == 0 {
			continue
		}
		line++
		t := vipiskaTxn{
			LineNumber: line,
			DocNumber:  strings.TrimSpace(cell(r, 2)),
			OpCode:     strings.TrimSpace(cell(r, 3)),
			MFO:        strings.TrimSpace(cell(r, 4)),
			BankDebet:  debet,
			BankKredit: kredit,
		}
		// Date: "25.06.2026 09:25:54"
		if dt, err := parseDotDate(strings.Fields(first)[0]); err == nil {
			t.docDateT = dt
			t.DocDate = dt.Format("2006-01-02")
		}
		// Счет/ИНН/Bank: "<acct>/<inn>/<bank name>"
		parts := strings.SplitN(cell(r, 1), "/", 3)
		if len(parts) > 0 {
			t.CounterpartyAccount = strings.TrimSpace(parts[0])
			if len(t.CounterpartyAccount) >= 5 {
				t.AccountPrefix = t.CounterpartyAccount[:5]
			}
		}
		if len(parts) > 1 {
			t.CounterpartyINN = strings.TrimSpace(parts[1])
		}
		if len(parts) > 2 {
			t.CounterpartyName = strings.TrimSpace(parts[2])
		}
		// Purpose: "00111~text"
		purpose := strings.TrimSpace(cell(r, 7))
		if idx := strings.Index(purpose, "~"); idx >= 0 {
			t.PurposeCode = strings.TrimSpace(purpose[:idx])
			t.Purpose = strings.TrimSpace(purpose[idx+1:])
		} else {
			t.Purpose = purpose
		}
		// Direction + amount (ERP convention): in = received.
		if kredit > 0 {
			t.Direction = "in"
			t.Amount = kredit
		} else {
			t.Direction = "out"
			t.Amount = debet
		}
		txns = append(txns, t)
	}

	// Fallback totals if no explicit row.
	if meta.TotalDebet == 0 && meta.TotalKredit == 0 {
		for _, t := range txns {
			if t.Direction == "in" {
				meta.TotalKredit += t.Amount
			} else {
				meta.TotalDebet += t.Amount
			}
		}
	}
	return txns, meta, warnings
}

func classifyVipiska(t *vipiskaTxn, rules []classRule) {
	purposeLow := strings.ToLower(t.Purpose + " " + t.CounterpartyName)
	for _, r := range rules {
		if r.direction != "any" && r.direction != t.Direction {
			continue
		}
		match := false
		switch r.matchType {
		case "account_prefix":
			match = t.AccountPrefix == r.matchValue || strings.HasPrefix(t.CounterpartyAccount, r.matchValue)
		case "inn":
			match = t.CounterpartyINN == r.matchValue
		case "keyword":
			match = strings.Contains(purposeLow, r.matchValue)
		}
		if match {
			t.Category = r.category
			t.targetCode = r.targetCode
			return
		}
	}
}

func (h *Handler) loadClassificationRules(tenantID uuid.UUID) []classRule {
	rows, err := h.db.Query(`
		SELECT match_type, lower(match_value), direction, target_account_code, COALESCE(category,'')
		FROM bank_classification_rules
		WHERE is_active = true AND (tenant_id = $1 OR tenant_id IS NULL)
		ORDER BY (tenant_id IS NULL), priority ASC`, tenantID)
	if err != nil {
		h.log.Error("vipiska: load rules", "error", err)
		return nil
	}
	defer rows.Close()
	var out []classRule
	for rows.Next() {
		var r classRule
		if rows.Scan(&r.matchType, &r.matchValue, &r.direction, &r.targetCode, &r.category) == nil {
			out = append(out, r)
		}
	}
	return out
}

// resolveAccountIDByCode finds an account by exact numeric code in the org
// (leaf preferred). Returns Nil + "" if not found.
func (h *Handler) resolveAccountIDByCode(tenantID uuid.UUID, orgArg interface{}, code string) (uuid.UUID, string) {
	var id uuid.UUID
	var realCode string
	err := h.db.QueryRow(`
		SELECT id, code FROM accounts
		WHERE tenant_id = $1 AND deleted_at IS NULL
		  AND ($2::uuid IS NULL OR organization_id = $2)
		  AND regexp_replace(code,'[^0-9]','','g') = $3
		ORDER BY COALESCE(is_leaf,true) DESC
		LIMIT 1`, tenantID, orgArg, code).Scan(&id, &realCode)
	if err != nil {
		return uuid.Nil, code // fall back to the bare code for display
	}
	return id, realCode
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func cell(r []string, i int) string {
	if i < len(r) {
		return r[i]
	}
	return ""
}

// parseVipiskaAmount strips thousands spaces/commas and parses a float.
func parseVipiskaAmount(s string) float64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	s = strings.ReplaceAll(s, " ", "")
	s = strings.ReplaceAll(s, " ", "")
	if strings.Contains(s, ",") && !strings.Contains(s, ".") {
		s = strings.ReplaceAll(s, ",", ".")
	} else {
		s = strings.ReplaceAll(s, ",", "")
	}
	v, _ := strconv.ParseFloat(s, 64)
	return v
}

func absf(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

func ternaryStr(cond bool, a, b string) string {
	if cond {
		return a
	}
	return b
}
