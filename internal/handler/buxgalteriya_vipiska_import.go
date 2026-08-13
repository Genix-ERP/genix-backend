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
	"bytes"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/xuri/excelize/v2"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
)

const vipiskaBankAccountCode = "5110" // ERP settlement (bank) account

type vipiskaTxn struct {
	ID                  string  `json:"id"`
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
	MatchedContactName  string  `json:"matched_contact_name"`
	DebetAccountID      string  `json:"debet_account_id"`
	KreditAccountID     string  `json:"kredit_account_id"`

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
	const maxVipiskaSize = 20 << 20 // 20MB — a real vipiska is well under 1MB
	if fileHdr.Size > maxVipiskaSize {
		response.BadRequest(c, "File is too large (max 20MB)")
		return
	}
	f, err := fileHdr.Open()
	if err != nil {
		response.InternalError(c, "Failed to open file")
		return
	}
	defer f.Close()

	content, err := io.ReadAll(io.LimitReader(f, maxVipiskaSize+1))
	if err != nil {
		response.InternalError(c, "Failed to read file")
		return
	}
	if len(content) > maxVipiskaSize {
		response.BadRequest(c, "File is too large (max 20MB)")
		return
	}

	// Duplicate-statement guard, same contract as the 1C importer: a re-upload
	// of the same file (double-click, retry after a timeout) must return the
	// existing import instead of creating every pending line twice — "confirm
	// all" on a doubled import posts every payment twice into the GL.
	contentHash := fmt.Sprintf("%x", sha256.Sum256(content))
	var existingImportID uuid.UUID
	if err := h.db.QueryRow(
		`SELECT id FROM bank_statement_imports WHERE tenant_id = $1 AND content_hash = $2 ORDER BY created_at DESC LIMIT 1`,
		tenantID, contentHash,
	).Scan(&existingImportID); err == nil {
		response.Success(c, gin.H{
			"id":        existingImportID,
			"import_id": existingImportID,
			"duplicate": true,
			"warnings":  []string{"Bu fayl allaqachon import qilingan (dublikat). Mavjud import qaytarildi."},
		})
		return
	}

	xl, err := excelize.OpenReader(bytes.NewReader(content))
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

	// Classify + resolve accounts. An operation is "Detected" only when BOTH the
	// bank account and the classified target account actually exist in this
	// org's chart — otherwise the accounts are left BLANK for manual selection
	// (never defaulted to a code that has no real account behind it).
	for i := range txns {
		t := &txns[i]
		t.ID = uuid.New().String()
		classifyVipiska(t, rules)

		var tid uuid.UUID
		var tcode string
		if t.targetCode != "" {
			tid, tcode = h.resolveAccountIDByCode(tenantID, orgArg, t.targetCode)
		}
		if t.Direction == "in" {
			// money in: Dt bank / Cr target
			if bankID != uuid.Nil {
				b := bankID
				t.debetAcctID = &b
				t.DebetCode = bankCode
			}
			if tid != uuid.Nil {
				k := tid
				t.kreditAcctID = &k
				t.KreditCode = tcode
			}
		} else {
			// money out: Dt target / Cr bank
			if tid != uuid.Nil {
				d := tid
				t.debetAcctID = &d
				t.DebetCode = tcode
			}
			if bankID != uuid.Nil {
				b := bankID
				t.kreditAcctID = &b
				t.KreditCode = bankCode
			}
		}
		if t.debetAcctID != nil {
			t.DebetAccountID = t.debetAcctID.String()
		}
		if t.kreditAcctID != nil {
			t.KreditAccountID = t.kreditAcctID.String()
		}
		if t.debetAcctID != nil && t.kreditAcctID != nil {
			t.Status = "suggested" // Detected — both accounts resolved
		} else {
			t.Status = "unmatched" // Undetected — needs a manual account
		}

		// Best-effort contact match by INN (links the bank line to a known
		// customer/vendor record so later posting can attribute the partner).
		if t.CounterpartyINN != "" {
			var cid uuid.UUID
			var cname string
			if e := h.db.QueryRow(`SELECT id, COALESCE(name,'') FROM contacts WHERE tenant_id=$1 AND tax_id=$2 AND deleted_at IS NULL LIMIT 1`,
				tenantID, t.CounterpartyINN).Scan(&cid, &cname); e == nil {
				t.contactID = &cid
				t.MatchedContactName = cname
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

	// Link the import to the ERP bank account whose account_number matches the
	// statement's own account line — this is what lets the vipiska world and
	// the per-account bank screens meet at all.
	var stmtBankAccountID interface{}
	if meta.Account != "" {
		var baID uuid.UUID
		if e := h.db.QueryRow(`SELECT id FROM bank_accounts WHERE tenant_id = $1 AND account_number = $2 AND deleted_at IS NULL LIMIT 1`,
			tenantID, meta.Account).Scan(&baID); e == nil {
			stmtBankAccountID = baID
		}
	}

	// Header + lines commit atomically. The old per-line h.db.Exec loop logged
	// and swallowed failures, so an import could claim N operations while the
	// review screen showed fewer — and the missing rows could never be posted.
	dbTx, err := h.db.Begin()
	if err != nil {
		response.InternalError(c, "Failed to save import")
		return
	}
	defer dbTx.Rollback()

	if _, err := dbTx.Exec(`
		INSERT INTO bank_statement_imports (
			id, tenant_id, organization_id, bank_account_id, format, file_name, file_size, content_hash,
			statement_date, opening_balance, closing_balance,
			total_credit, total_debit, transaction_count, matched_count, unmatched_count,
			status, imported_by, imported_at, created_at
		) VALUES ($1,$2,$3,$4,'excel_vipiska',$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,'imported',$16,$17,$17)`,
		importID, tenantID, orgPtr, stmtBankAccountID, fileHdr.Filename, fileHdr.Size, contentHash,
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
		if _, err := dbTx.Exec(`
			INSERT INTO bank_statement_transactions (
				id, import_id, line_number, doc_number, doc_date,
				counterparty_name, counterparty_inn, counterparty_account,
				op_code, mfo, account_prefix, amount, direction,
				purpose, purpose_code, category,
				debet_account_id, kredit_account_id, debet_account_code, kredit_account_code,
				matched_contact_id, match_status, created_at
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23)`,
			t.ID, importID, t.LineNumber, t.DocNumber, docDateArg,
			t.CounterpartyName, t.CounterpartyINN, t.CounterpartyAccount,
			t.OpCode, t.MFO, t.AccountPrefix, t.Amount, t.Direction,
			t.Purpose, t.PurposeCode, nullIfEmpty(t.Category),
			t.debetAcctID, t.kreditAcctID, nullIfEmpty(t.DebetCode), nullIfEmpty(t.KreditCode),
			t.contactID, t.Status, now); err != nil {
			h.log.Error("vipiska import: insert line", "error", err, "line", t.LineNumber)
			response.InternalError(c, fmt.Sprintf("Failed to save operation line %d — import rolled back", t.LineNumber))
			return
		}
	}

	if err := dbTx.Commit(); err != nil {
		h.log.Error("vipiska import: commit", "error", err)
		response.InternalError(c, "Failed to save import")
		return
	}

	// Balance integrity check (informational warning only).
	expectedClose := meta.OpeningBalance + meta.TotalKredit - meta.TotalDebet
	balanceOK := absf(expectedClose-meta.ClosingBalance) < 0.01

	response.Success(c, gin.H{
		// Both keys, same value. `import_id` is what the yuksalish deployment
		// has always returned and what keeps the three deployments returning an
		// identical shape — the divergence between them is the whole reason
		// this port exists. `id` is what the documented client contract reads.
		// Emitting one and not the other would silently blank the other client.
		"id":                importID,
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
	// Opt-in paging via the shared helper: no params returns the full statement
	// exactly as before (the web review screen sends none), page/page_size or
	// limit are honoured when sent. A statement is one row per operation, so a
	// busy month is easily hundreds.
	query := `
		SELECT t.id::text, t.line_number, t.doc_number, t.doc_date, t.counterparty_name, t.counterparty_inn,
		       t.counterparty_account, COALESCE(t.op_code,''), COALESCE(t.mfo,''), COALESCE(t.account_prefix,''),
		       t.amount, t.direction, COALESCE(t.purpose,''), COALESCE(t.purpose_code,''), COALESCE(t.category,''),
		       COALESCE(t.debet_account_id::text,''), COALESCE(t.kredit_account_id::text,''),
		       COALESCE(t.debet_account_code,''), COALESCE(t.kredit_account_code,''),
		       t.match_status, (t.matched_journal_entry_id IS NOT NULL) AS posted,
		       COALESCE(c.name,'')
		FROM bank_statement_transactions t
		LEFT JOIN contacts c ON c.id = t.matched_contact_id
		WHERE t.import_id=$1 ORDER BY t.line_number ASC, t.id ASC`
	args := []interface{}{importID}
	paginate, page, pageSize, offset := optPagination(c)
	if paginate {
		query += " LIMIT $2 OFFSET $3"
		args = append(args, pageSize, offset)
	}

	rows, err := h.db.Query(query, args...)
	if err != nil {
		response.InternalError(c, "Failed to load transactions")
		return
	}
	defer rows.Close()
	out := make([]gin.H, 0)
	for rows.Next() {
		var (
			id, doc, name, inn, acct, op, mfo, pfx                 string
			ln                                                     int
			amount                                                 float64
			dir, purpose, pcode, cat, daid, kaid, dcode, kcode, st string
			posted                                                 bool
			cname                                                  string
			docDate                                                *time.Time
		)
		if rows.Scan(&id, &ln, &doc, &docDate, &name, &inn, &acct, &op, &mfo, &pfx,
			&amount, &dir, &purpose, &pcode, &cat, &daid, &kaid, &dcode, &kcode, &st, &posted, &cname) != nil {
			continue
		}
		row := gin.H{
			"id": id, "line_number": ln, "doc_number": doc, "counterparty_name": name,
			"counterparty_inn": inn, "counterparty_account": acct, "op_code": op, "mfo": mfo,
			"account_prefix": pfx, "amount": amount, "direction": dir, "purpose": purpose,
			"purpose_code": pcode, "category": cat,
			"debet_account_id": daid, "kredit_account_id": kaid,
			"debet_account_code": dcode, "kredit_account_code": kcode,
			"status": st, "posted": posted, "matched_contact_name": cname,
		}
		if docDate != nil {
			row["doc_date"] = docDate.Format("2006-01-02")
		}
		out = append(out, row)
	}

	if !paginate {
		response.Success(c, out)
		return
	}

	total := 0
	if err := h.db.QueryRow(
		`SELECT COUNT(*) FROM bank_statement_transactions WHERE import_id=$1`, importID,
	).Scan(&total); err != nil {
		h.log.Error("vipiska: count transactions", "error", err)
		total = len(out)
	}
	response.Paginated(c, out, page, pageSize, total)
}

// UpdateBankVipiskaLineAccounts lets the user override the auto-detected Dt/Cr
// accounts on a single line before posting. Recomputes the detected status.
func (h *Handler) UpdateBankVipiskaLineAccounts(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	lineID, err := uuid.Parse(c.Param("lineId"))
	if err != nil {
		response.BadRequest(c, "Invalid line id")
		return
	}
	var input struct {
		DebetAccountID  string `json:"debet_account_id"`
		KreditAccountID string `json:"kredit_account_id"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input")
		return
	}
	var posted bool
	if e := h.db.QueryRow(`
		SELECT (t.matched_journal_entry_id IS NOT NULL)
		FROM bank_statement_transactions t JOIN bank_statement_imports i ON i.id=t.import_id
		WHERE t.id=$1 AND i.tenant_id=$2`, lineID, tenantID).Scan(&posted); e != nil {
		response.NotFound(c, "Line not found")
		return
	}
	if posted {
		response.BadRequest(c, "This operation is already posted")
		return
	}

	// The accounts are written to the line and later POSTED verbatim, so they
	// must be validated here, not looked up best-effort: the old code fetched
	// the code for display only and accepted any UUID — including another
	// tenant's account (bank_statement_transactions has no FK on these
	// columns), whose balance a confirm would then silently mutate. Each
	// account must be this tenant's, active, and a leaf (TT §4.2).
	validated := func(raw string) (interface{}, bool) {
		if raw == "" {
			return nil, true
		}
		accID, err := uuid.Parse(raw)
		if err != nil {
			return nil, false
		}
		var cc string
		var isLeaf bool
		if e := h.db.QueryRow(`SELECT code, COALESCE(is_leaf, true) FROM accounts
			WHERE id=$1 AND tenant_id=$2 AND deleted_at IS NULL AND is_active = true`,
			accID, tenantID).Scan(&cc, &isLeaf); e != nil || !isLeaf {
			return nil, false
		}
		return nullIfEmpty(cc), true
	}
	debCode, debOK := validated(input.DebetAccountID)
	kreCode, kreOK := validated(input.KreditAccountID)
	if !debOK || !kreOK {
		response.BadRequest(c, "Hisob topilmadi yoki guruh hisobiga o'tkazma qilib bo'lmaydi (faqat oxirgi darajadagi faol hisob)")
		return
	}
	if input.DebetAccountID != "" && input.DebetAccountID == input.KreditAccountID {
		response.BadRequest(c, "Dt va Kt bir xil hisob bo'lishi mumkin emas")
		return
	}
	deb := nullUUID(input.DebetAccountID)
	kre := nullUUID(input.KreditAccountID)
	status := "unmatched"
	if deb.Valid && kre.Valid {
		status = "suggested"
	}
	if _, err := h.db.Exec(`
		UPDATE bank_statement_transactions
		SET debet_account_id=$1, kredit_account_id=$2, debet_account_code=$3, kredit_account_code=$4,
		    match_status = CASE WHEN match_status='matched' THEN match_status ELSE $5 END
		WHERE id=$6`, deb, kre, debCode, kreCode, status, lineID); err != nil {
		h.log.Error("vipiska: update line accounts", "error", err)
		response.InternalError(c, "Failed to update")
		return
	}
	response.Success(c, gin.H{"status": status})
}

// ConfirmBankVipiskaLine verifies and POSTS a single operation: it creates a
// balanced journal entry (Dt debet / Cr kredit) for the line's amount, updates
// account balances, and marks the line as posted. (TZ §5.4 "Ha" / approve.)
func (h *Handler) ConfirmBankVipiskaLine(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	userID, _ := middleware.GetUserID(c)
	lineID, err := uuid.Parse(c.Param("lineId"))
	if err != nil {
		response.BadRequest(c, "Invalid line id")
		return
	}

	var (
		amount                                    float64
		dir, docNum, cpName                       string
		debStr, kreStr, orgStr, conStr, bankAccID sql.NullString
		docDate                                   *time.Time
		posted                                    bool
	)
	err = h.db.QueryRow(`
		SELECT t.amount, t.direction, COALESCE(t.doc_number,''), COALESCE(t.counterparty_name,''),
		       t.debet_account_id::text, t.kredit_account_id::text, t.doc_date,
		       i.organization_id::text, t.matched_contact_id::text,
		       (t.matched_journal_entry_id IS NOT NULL), i.bank_account_id::text
		FROM bank_statement_transactions t JOIN bank_statement_imports i ON i.id=t.import_id
		WHERE t.id=$1 AND i.tenant_id=$2`, lineID, tenantID).Scan(
		&amount, &dir, &docNum, &cpName, &debStr, &kreStr, &docDate, &orgStr, &conStr, &posted, &bankAccID)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Line not found")
		return
	}
	if err != nil {
		h.log.Error("vipiska confirm: load line", "error", err)
		response.InternalError(c, "Failed to post")
		return
	}
	if posted {
		response.BadRequest(c, "This operation is already posted")
		return
	}
	if !debStr.Valid || !kreStr.Valid || debStr.String == "" || kreStr.String == "" {
		response.BadRequest(c, "Avval Dt va Kt hisob raqamlarini tanlang / select both Dt and Kt accounts first")
		return
	}
	if amount <= 0.001 {
		response.BadRequest(c, "Amount must be greater than zero")
		return
	}
	debID, _ := uuid.Parse(debStr.String)
	kreID, _ := uuid.Parse(kreStr.String)
	if debID == kreID {
		response.BadRequest(c, "Dt va Kt bir xil hisob bo'lishi mumkin emas")
		return
	}
	// Re-validate BOTH accounts at posting time — the stored ids may predate
	// the update-endpoint validation (or the account may have been deactivated
	// since): they must be this tenant's, active, leaf accounts, otherwise the
	// posting trigger rejects the entry with an opaque 500.
	for _, acc := range []uuid.UUID{debID, kreID} {
		var isLeaf bool
		if e := h.db.QueryRow(`SELECT COALESCE(is_leaf, true) FROM accounts
			WHERE id=$1 AND tenant_id=$2 AND deleted_at IS NULL AND is_active = true`,
			acc, tenantID).Scan(&isLeaf); e != nil {
			response.BadRequest(c, "Hisob bu kompaniyada topilmadi yoki faol emas — Dt/Kt ni qayta tanlang")
			return
		} else if !isLeaf {
			response.BadRequest(c, "Guruh hisobiga o'tkazma qilib bo'lmaydi — oxirgi darajadagi hisobni tanlang")
			return
		}
	}
	var orgArg interface{}
	if orgStr.Valid && orgStr.String != "" {
		orgArg = orgStr.String
	}
	var conArg interface{}
	if conStr.Valid && conStr.String != "" {
		conArg = conStr.String
	}
	entryDate := time.Now()
	if docDate != nil {
		entryDate = *docDate
	}

	tx, err := h.db.Begin()
	if err != nil {
		response.InternalError(c, "Failed to post")
		return
	}
	defer tx.Rollback()

	var journalID uuid.UUID
	var jprefix sql.NullString
	var jNextNumber int
	jerr := tx.QueryRow(`SELECT id, number_prefix, COALESCE(next_number, 1) FROM journals
		WHERE tenant_id=$1 AND deleted_at IS NULL AND ($2::uuid IS NULL OR organization_id=$2 OR organization_id IS NULL)
		ORDER BY CASE WHEN LOWER(COALESCE(type,''))='bank' THEN 0 WHEN code='GENERAL' THEN 1 ELSE 2 END LIMIT 1`,
		tenantID, orgArg).Scan(&journalID, &jprefix, &jNextNumber)
	if jerr != nil || journalID == uuid.Nil {
		if jerr != nil && jerr != sql.ErrNoRows {
			h.log.Error("vipiska confirm: journal lookup", "error", jerr)
			response.InternalError(c, "Failed to post")
			return
		}
		response.BadRequest(c, "No journal is configured for this company.")
		return
	}
	px := ""
	if jprefix.Valid {
		px = jprefix.String
	}
	// Numbering MUST go through nextEntryNumberSeq: entry numbers are unique
	// per (tenant, organization) across ALL journals — the old per-journal
	// MAX+1 here collided with any other journal's higher number and 500'd on
	// the unique constraint (and excluding soft-deleted rows re-created the
	// exact collision the shared helper documents).
	entrySeq := nextEntryNumberSeq(tx, tenantID, orgArg, px, jNextNumber)
	entryNumber := fmt.Sprintf("%s%06d", px, entrySeq)
	desc := "Bank vipiska: " + docNum
	if cpName != "" {
		desc += " — " + cpName
	}

	jeID := uuid.New()
	now := time.Now()
	if _, err := tx.Exec(`INSERT INTO journal_entries
		(id, tenant_id, organization_id, journal_id, entry_number, entry_date, reference, description, source_type, source_id, exchange_rate, total_debit, total_credit, status, created_by, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,'bank_vipiska',$9,1.0,$10,$10,'posted',$11,$12,$12)`,
		jeID, tenantID, orgArg, journalID, entryNumber, entryDate, nullIfEmpty(docNum), desc, lineID.String(), amount, userID, now); err != nil {
		h.log.Error("vipiska confirm: JE header", "error", err)
		response.InternalError(c, "Failed to post")
		return
	}

	// Attribute the contact to the partner (non-bank) leg.
	var debContact, kreContact interface{}
	if dir == "in" {
		kreContact = conArg
	} else {
		debContact = conArg
	}
	jel := func(acct uuid.UUID, contact interface{}, line int, debit, credit float64) error {
		_, e := tx.Exec(`INSERT INTO journal_entry_lines
			(id, journal_entry_id, line_number, account_id, contact_id, description, debit_amount, credit_amount, exchange_rate, created_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,1.0,$9)`, uuid.New(), jeID, line, acct, contact, desc, debit, credit, now)
		return e
	}
	if err := jel(debID, debContact, 1, amount, 0); err != nil {
		if friendly := friendlyPostingError(err); friendly != "" {
			response.BadRequest(c, friendly)
			return
		}
		h.log.Error("vipiska confirm: Dt line", "error", err)
		response.InternalError(c, "Failed to post")
		return
	}
	if err := jel(kreID, kreContact, 2, 0, amount); err != nil {
		if friendly := friendlyPostingError(err); friendly != "" {
			response.BadRequest(c, friendly)
			return
		}
		h.log.Error("vipiska confirm: Cr line", "error", err)
		response.InternalError(c, "Failed to post")
		return
	}

	// Update account balances (normal_balance aware), like PostJournalEntry.
	if err := updateAccountBalanceTx(tx, debID, amount, 0); err != nil {
		if friendly := negativeBalanceMessage(h, err, debID); friendly != "" {
			response.BadRequest(c, friendly)
			return
		}
		response.InternalError(c, "Failed to post")
		return
	}
	if err := updateAccountBalanceTx(tx, kreID, 0, amount); err != nil {
		if friendly := negativeBalanceMessage(h, err, kreID); friendly != "" {
			response.BadRequest(c, friendly)
			return
		}
		response.InternalError(c, "Failed to post")
		return
	}

	// The `posted` guard above is read OUTSIDE this transaction, so on its own
	// it cannot stop a double-post: two concurrent confirms — or the retry after
	// a network blip that "Confirm all" is guaranteed to produce eventually —
	// both see posted=false, both insert a journal entry, and an unguarded
	// UPDATE lets the second overwrite matched_journal_entry_id. The first entry
	// is then orphaned: still in the GL, still double-counting the amount, and
	// unreachable from the line that created it.
	//
	// Guarding on matched_journal_entry_id IS NULL inside the UPDATE makes
	// claiming the line atomic. The loser affects zero rows and rolls back its
	// own journal entry via the deferred Rollback, so exactly one entry survives
	// and the caller gets a 409 it can treat as "already done".
	claim, err := tx.Exec(`UPDATE bank_statement_transactions
		SET matched_journal_entry_id=$1, match_status='matched'
		WHERE id=$2 AND matched_journal_entry_id IS NULL`, jeID, lineID)
	if err != nil {
		h.log.Error("vipiska confirm: mark line", "error", err)
		response.InternalError(c, "Failed to post")
		return
	}
	if claimed, _ := claim.RowsAffected(); claimed == 0 {
		// Someone else posted this line between our read and this UPDATE.
		response.Conflict(c, "This operation is already posted")
		return
	}

	// Keep the journal's counter seeding future numbers past ours.
	if _, err := tx.Exec(`UPDATE journals SET next_number = GREATEST(COALESCE(next_number,1), $1 + 1) WHERE id = $2`,
		entrySeq, journalID); err != nil {
		h.log.Error("vipiska confirm: bump next_number", "error", err)
	}

	// If the import resolved to an ERP bank account (statement account number
	// matched), mirror the confirmed line as an already-reconciled
	// bank_transactions row so it appears in the Tranzaksiyalar tab and in
	// reconciliation history. Without this, the vipiska world and the
	// per-account bank world never met.
	if bankAccID.Valid && bankAccID.String != "" {
		txType := "debit"
		if dir == "in" {
			txType = "credit"
		}
		// Savepoint: the mirror row is a convenience — a failure must not
		// poison the posting transaction (a bare failed INSERT would abort it).
		if _, spErr := tx.Exec(`SAVEPOINT vipiska_mirror`); spErr == nil {
			if _, err := tx.Exec(`
				INSERT INTO bank_transactions (tenant_id, organization_id, bank_account_id, transaction_date, reference,
				                               description, amount, transaction_type, status, is_reconciled, reconciled_date,
				                               matched_journal_entry_id, created_at, updated_at)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8,'reconciled',true,$9,$10,$11,$11)`,
				tenantID, orgArg, bankAccID.String, entryDate, nullIfEmpty(docNum), desc, amount, txType,
				now.Format("2006-01-02"), jeID, now); err != nil {
				h.log.Error("vipiska confirm: mirror bank transaction", "error", err)
				_, _ = tx.Exec(`ROLLBACK TO SAVEPOINT vipiska_mirror`)
			} else {
				_, _ = tx.Exec(`RELEASE SAVEPOINT vipiska_mirror`)
			}
		}
	}

	if err := tx.Commit(); err != nil {
		h.log.Error("vipiska confirm: commit", "error", err)
		response.InternalError(c, "Failed to post")
		return
	}
	response.Success(c, gin.H{"journal_entry_id": jeID, "entry_number": entryNumber})
}

// RejectBankVipiskaLine marks an operation as not-to-post ("Yo'q"). It stays in
// the statement and can be reviewed/posted later. (TZ §5.3 "Yo'q".)
func (h *Handler) RejectBankVipiskaLine(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	lineID, err := uuid.Parse(c.Param("lineId"))
	if err != nil {
		response.BadRequest(c, "Invalid line id")
		return
	}
	res, err := h.db.Exec(`UPDATE bank_statement_transactions t
		SET match_status='ignored'
		FROM bank_statement_imports i
		WHERE t.import_id=i.id AND t.id=$1 AND i.tenant_id=$2 AND t.matched_journal_entry_id IS NULL`, lineID, tenantID)
	if err != nil {
		response.InternalError(c, "Failed to update")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		response.BadRequest(c, "Line not found or already posted")
		return
	}
	response.Success(c, gin.H{"status": "ignored"})
}

// updateAccountBalanceTx applies a posting to current_balance using the
// account's normal balance side (debit-normal: +debit-credit; credit-normal:
// +credit-debit) — identical to PostJournalEntry.
func updateAccountBalanceTx(tx *sql.Tx, accountID uuid.UUID, debit, credit float64) error {
	// 448 convention: current_balance = SUM(debit) - SUM(credit) for EVERY
	// account, no normal_balance branch. The old branch here flipped
	// credit-normal accounts the opposite way from PostJournalEntry while its
	// comment claimed parity — every imported statement line that touched a
	// payable drifted the balance by 2x the amount.
	change := debit - credit
	_, err := tx.Exec(`UPDATE accounts SET current_balance = current_balance + $1, updated_at = now() WHERE id=$2`, change, accountID)
	return err
}

// negativeBalanceMessage returns a friendly message if err is the cash/bank
// negative-balance trigger (CHECK violation), else "". The account lookup is
// tenant-agnostic only because every caller has already validated the account
// belongs to the caller's tenant; a TT-invariant text (which is not about
// balances at all) is passed through verbatim instead of being mislabeled.
func negativeBalanceMessage(h *Handler, err error, accountID uuid.UUID) string {
	if pqErr, ok := err.(*pq.Error); ok && pqErr.Code == "23514" {
		if strings.Contains(pqErr.Message, "TT §") {
			return pqErr.Message
		}
		var name, code string
		var bal float64
		_ = h.db.QueryRow(`SELECT name, code, current_balance FROM accounts WHERE id=$1`, accountID).Scan(&name, &code, &bal)
		return fmt.Sprintf("%s (%s) hisobida mablag' yetarli emas.", name, code)
	}
	return ""
}

// friendlyPostingError converts the posting triggers' RAISEd violations
// (ERRCODE 23514: TT §4.2 leaf-only, §4.5 mandatory analytics, period locks)
// into the message the trigger wrote — actionable text like "account 6010
// requires contract (shartnoma)" — instead of an opaque 500.
func friendlyPostingError(err error) string {
	if pqErr, ok := err.(*pq.Error); ok && (pqErr.Code == "23514" || pqErr.Code == "P0001") {
		return pqErr.Message
	}
	return ""
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
	// Leaf REQUIRED, not merely preferred: suggesting a group account produced
	// a line that could never be confirmed (the TT §4.2 trigger rejects the
	// posting) with nothing telling the user why. If only a group node carries
	// the code, the operation correctly stays unmatched for manual selection.
	err := h.db.QueryRow(`
		SELECT id, code FROM accounts
		WHERE tenant_id = $1 AND deleted_at IS NULL AND is_active = true
		  AND COALESCE(is_leaf, true) = true
		  AND ($2::uuid IS NULL OR organization_id = $2)
		  AND regexp_replace(code,'[^0-9]','','g') = $3
		ORDER BY code ASC
		LIMIT 1`, tenantID, orgArg, code).Scan(&id, &realCode)
	if err != nil {
		// Account not in this org's chart — leave BLANK (per TZ: undetermined
		// operations are chosen by hand, never defaulted).
		return uuid.Nil, ""
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
