package handler

// File: buxgalteriya_bank_import.go
//
// TT Buxgalteriya ERP §8.1 — Bank-mijoz statement import.
//
// Parses the most common Uzbek bank export format ("1C Bank-client" .txt) into
// bank_statement_transactions rows and attempts naive auto-matching against
// existing contacts (by INN) and open payments.
//
// The 1C format is a KEY=VALUE flat file with sections delimited by
//   СекцияДокумент=<type>
//   ...
//   КонецДокумента
//
// We support the minimum fields needed for AP/AR reconciliation. Unsupported
// fields are ignored; unsupported documents are skipped with a warning.

import (
	"bufio"
	"crypto/sha256"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
)

// parseMoney parses an amount from a 1C export, tolerating thousands
// separators (regular space, non-breaking space, narrow no-break space) and a
// comma decimal separator. The old code only swapped "," for "." so values
// like "1 234,56" failed to parse and were silently saved as 0.
func parseMoney(val string) (float64, error) {
	r := strings.NewReplacer(
		" ", "",
		" ", "", // non-breaking space
		" ", "", // narrow no-break space
		",", ".",
	)
	return strconv.ParseFloat(r.Replace(strings.TrimSpace(val)), 64)
}

type parsedBankTxn struct {
	LineNumber          int
	DocNumber           string
	DocDate             time.Time
	CounterpartyName    string
	CounterpartyINN     string
	CounterpartyAccount string
	BankName            string
	BankBIC             string
	Amount              float64
	Direction           string // in | out
	Purpose             string

	// Raw payer/recipient parties. Direction and the counterparty are resolved
	// from these at end-of-document by matching against our own account, so
	// that our own INN/account is never mistaken for the counterparty's.
	payerName, payerINN, payerAccount                string
	recipientName, recipientINN, recipientAccount    string
	payerBank, payerBIC, recipientBank, recipientBIC string
}

type bankImportResponse struct {
	ImportID         uuid.UUID       `json:"import_id"`
	StatementDate    *string         `json:"statement_date,omitempty"`
	OpeningBalance   float64         `json:"opening_balance"`
	ClosingBalance   float64         `json:"closing_balance"`
	TotalCredit      float64         `json:"total_credit"`
	TotalDebit       float64         `json:"total_debit"`
	TransactionCount int             `json:"transaction_count"`
	MatchedCount     int             `json:"matched_count"`
	UnmatchedCount   int             `json:"unmatched_count"`
	Transactions     []parsedBankTxn `json:"transactions"`
	Warnings         []string        `json:"warnings,omitempty"`
}

// ImportBankStatement1C accepts a multipart form with "file" field containing
// the 1C Bank-client .txt export. The file is parsed in memory.
// (Renamed from ImportBankStatement to avoid collision with the pre-existing
// per-account CSV importer in finance.go.)
func (h *Handler) ImportBankStatement1C(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	userID, _ := middleware.GetUserID(c)

	file, fileHeader, err := c.Request.FormFile("file")
	if err != nil {
		response.BadRequest(c, "file is required")
		return
	}
	defer file.Close()

	content, err := io.ReadAll(file)
	if err != nil {
		response.InternalError(c, "Failed to read file")
		return
	}

	parsed, meta, warnings := parseBankClientTxt(string(content))

	// Duplicate-statement guard: reject a re-upload of the same file (same
	// content hash for this tenant) instead of doubling every transaction.
	contentHash := fmt.Sprintf("%x", sha256.Sum256(content))
	var existingImportID uuid.UUID
	if err := h.db.QueryRow(
		`SELECT id FROM bank_statement_imports WHERE tenant_id = $1 AND content_hash = $2 ORDER BY created_at DESC LIMIT 1`,
		tenantID, contentHash,
	).Scan(&existingImportID); err == nil {
		response.Success(c, bankImportResponse{
			ImportID:         existingImportID,
			OpeningBalance:   meta.OpeningBalance,
			ClosingBalance:   meta.ClosingBalance,
			Warnings:         append(warnings, "Bu fayl allaqachon import qilingan (dublikat). Mavjud import qaytarildi."),
			TransactionCount: len(parsed),
		})
		return
	}

	importID := uuid.New()
	now := time.Now()

	orgID, _ := middleware.GetOrganizationID(c)
	var orgPtr *uuid.UUID
	if orgID != uuid.Nil {
		orgPtr = &orgID
	}

	// Truncate raw_content to 500KB to stay friendly to the DB
	raw := string(content)
	if len(raw) > 500000 {
		raw = raw[:500000]
	}

	var stmtDate interface{}
	if !meta.StatementDate.IsZero() {
		stmtDate = meta.StatementDate
	}

	_, err = h.db.Exec(`
		INSERT INTO bank_statement_imports (
			id, tenant_id, organization_id, format, file_name, file_size, raw_content,
			statement_date, opening_balance, closing_balance,
			total_credit, total_debit, transaction_count,
			status, imported_by, imported_at, created_at, content_hash
		) VALUES ($1, $2, $3, '1c_txt', $4, $5, $6, $7, $8, $9, $10, $11, $12, 'imported', $13, $14, $14, $15)
	`, importID, tenantID, orgPtr, fileHeader.Filename, len(content), raw,
		stmtDate, meta.OpeningBalance, meta.ClosingBalance,
		meta.TotalCredit, meta.TotalDebit, len(parsed),
		userID, now, contentHash)
	if err != nil {
		h.log.Error("ImportBankStatement: insert import failed", "error", err)
		response.InternalError(c, "Failed to save import")
		return
	}

	// Insert transactions + naive counterparty matching by INN
	matched := 0
	for i, t := range parsed {
		var matchedContactID interface{}
		if t.CounterpartyINN != "" {
			var cid uuid.UUID
			if err := h.db.QueryRow(`
				SELECT id FROM contacts
				WHERE tenant_id = $1 AND tax_id = $2 AND deleted_at IS NULL
				LIMIT 1
			`, tenantID, t.CounterpartyINN).Scan(&cid); err == nil {
				matchedContactID = cid
				matched++
			}
		}

		matchStatus := "unmatched"
		if matchedContactID != nil {
			matchStatus = "suggested"
		}

		var docDateArg interface{}
		if !t.DocDate.IsZero() {
			docDateArg = t.DocDate
		}

		_, err := h.db.Exec(`
			INSERT INTO bank_statement_transactions (
				id, import_id, line_number, doc_number, doc_date,
				counterparty_name, counterparty_inn, counterparty_account,
				bank_name, bank_bic, amount, direction, purpose,
				matched_contact_id, match_status, created_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
		`, uuid.New(), importID, i+1, t.DocNumber, docDateArg,
			t.CounterpartyName, t.CounterpartyINN, t.CounterpartyAccount,
			t.BankName, t.BankBIC, t.Amount, t.Direction, t.Purpose,
			matchedContactID, matchStatus, now)
		if err != nil {
			h.log.Error("ImportBankStatement: insert txn failed", "error", err, "line", i+1)
		}
	}

	// Update import totals
	_, _ = h.db.Exec(`
		UPDATE bank_statement_imports
		SET matched_count = $1, unmatched_count = $2
		WHERE id = $3
	`, matched, len(parsed)-matched, importID)

	resp := bankImportResponse{
		ImportID:         importID,
		OpeningBalance:   meta.OpeningBalance,
		ClosingBalance:   meta.ClosingBalance,
		TotalCredit:      meta.TotalCredit,
		TotalDebit:       meta.TotalDebit,
		TransactionCount: len(parsed),
		MatchedCount:     matched,
		UnmatchedCount:   len(parsed) - matched,
		Transactions:     parsed,
		Warnings:         warnings,
	}
	if !meta.StatementDate.IsZero() {
		s := meta.StatementDate.Format("2006-01-02")
		resp.StatementDate = &s
	}

	response.Success(c, resp)
}

// ListBankImports returns recent statement imports for the tenant.
func (h *Handler) ListBankImports(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	rows, err := h.db.Query(`
		SELECT id, file_name, statement_date, opening_balance, closing_balance,
		       total_credit, total_debit, transaction_count, matched_count, unmatched_count,
		       status, imported_at
		FROM bank_statement_imports
		WHERE tenant_id = $1
		ORDER BY imported_at DESC
		LIMIT 100
	`, tenantID)
	if err != nil {
		response.InternalError(c, "Failed to list imports")
		return
	}
	defer rows.Close()

	out := make([]gin.H, 0)
	for rows.Next() {
		var (
			id                                        uuid.UUID
			fileName                                  string
			stmtDate                                  *time.Time
			opening, closing, totalCredit, totalDebit float64
			txnCount, matched, unmatched              int
			status                                    string
			importedAt                                time.Time
		)
		if err := rows.Scan(&id, &fileName, &stmtDate, &opening, &closing,
			&totalCredit, &totalDebit, &txnCount, &matched, &unmatched,
			&status, &importedAt); err != nil {
			continue
		}
		row := gin.H{
			"id": id, "file_name": fileName,
			"opening_balance": opening, "closing_balance": closing,
			"total_credit": totalCredit, "total_debit": totalDebit,
			"transaction_count": txnCount, "matched_count": matched, "unmatched_count": unmatched,
			"status": status, "imported_at": importedAt,
		}
		if stmtDate != nil {
			row["statement_date"] = stmtDate.Format("2006-01-02")
		}
		out = append(out, row)
	}
	response.Success(c, out)
}

// ---------------------------------------------------------------------------
// Parser
// ---------------------------------------------------------------------------

type bankStatementMeta struct {
	StatementDate  time.Time
	OpeningBalance float64
	ClosingBalance float64
	TotalCredit    float64
	TotalDebit     float64
}

// parseBankClientTxt parses the 1C Bank-client .txt export format.
// Documents are separated by "СекцияДокумент=" ... "КонецДокумента".
func parseBankClientTxt(content string) ([]parsedBankTxn, bankStatementMeta, []string) {
	var (
		out      []parsedBankTxn
		meta     bankStatementMeta
		warnings []string
	)

	scanner := bufio.NewScanner(strings.NewReader(content))
	scanner.Buffer(make([]byte, 1024*1024), 10*1024*1024)

	var (
		inDoc      bool
		cur        parsedBankTxn
		myAccount  string // "РасчСчет=" from file header identifies our account
		lineNumber int
	)

	for scanner.Scan() {
		rawLine := scanner.Text()
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}

		eq := strings.IndexByte(line, '=')
		key := line
		val := ""
		if eq >= 0 {
			key = strings.TrimSpace(line[:eq])
			val = strings.TrimSpace(line[eq+1:])
		}

		switch {
		case key == "СекцияДокумент":
			inDoc = true
			lineNumber++
			cur = parsedBankTxn{LineNumber: lineNumber}

		case key == "КонецДокумента":
			if !inDoc {
				continue
			}
			inDoc = false

			// Resolve direction and counterparty by matching our own account.
			// If we are the payer → money OUT, counterparty is the recipient.
			// If we are the recipient → money IN, counterparty is the payer.
			switch {
			case myAccount != "" && cur.payerAccount == myAccount:
				cur.Direction = "out"
				cur.CounterpartyName = cur.recipientName
				cur.CounterpartyINN = cur.recipientINN
				cur.CounterpartyAccount = cur.recipientAccount
				cur.BankName = cur.recipientBank
				cur.BankBIC = cur.recipientBIC
			case myAccount != "" && cur.recipientAccount == myAccount:
				cur.Direction = "in"
				cur.CounterpartyName = cur.payerName
				cur.CounterpartyINN = cur.payerINN
				cur.CounterpartyAccount = cur.payerAccount
				cur.BankName = cur.payerBank
				cur.BankBIC = cur.payerBIC
			default:
				// Neither side matched our account (or we don't know it):
				// fall back to the purpose text, and pick the counterparty as
				// the opposite party to the inferred direction.
				if strings.HasPrefix(strings.ToLower(cur.Purpose), "оплата") ||
					strings.HasPrefix(strings.ToLower(cur.Purpose), "to'lov") {
					cur.Direction = "out"
					cur.CounterpartyName = cur.recipientName
					cur.CounterpartyINN = cur.recipientINN
					cur.CounterpartyAccount = cur.recipientAccount
				} else {
					cur.Direction = "in"
					cur.CounterpartyName = cur.payerName
					cur.CounterpartyINN = cur.payerINN
					cur.CounterpartyAccount = cur.payerAccount
				}
			}

			if cur.Direction == "in" {
				meta.TotalCredit += cur.Amount
			} else {
				meta.TotalDebit += cur.Amount
			}
			out = append(out, cur)

		case !inDoc:
			// Header fields
			switch key {
			case "ДатаНачала", "ДатаСформирования":
				if t, err := parseDotDate(val); err == nil && meta.StatementDate.IsZero() {
					meta.StatementDate = t
				}
			case "НачальныйОстаток":
				if v, err := parseMoney(val); err == nil {
					meta.OpeningBalance = v
				}
			case "КонечныйОстаток":
				if v, err := parseMoney(val); err == nil {
					meta.ClosingBalance = v
				}
			case "РасчСчет":
				if myAccount == "" {
					myAccount = val
				}
			}

		case inDoc:
			switch key {
			case "Номер":
				cur.DocNumber = val
			case "Дата":
				if t, err := parseDotDate(val); err == nil {
					cur.DocDate = t
				}
			case "Сумма":
				if v, err := parseMoney(val); err == nil {
					cur.Amount = v
				}
			case "Плательщик":
				cur.payerName = val
			case "ПлательщикИНН":
				cur.payerINN = val
			case "ПлательщикРасчСчет":
				cur.payerAccount = val
			case "ПлательщикБанк1":
				cur.payerBank = val
			case "ПлательщикБИК":
				cur.payerBIC = val
			case "Получатель":
				cur.recipientName = val
			case "ПолучательИНН":
				cur.recipientINN = val
			case "ПолучательРасчСчет":
				cur.recipientAccount = val
			case "ПолучательБанк1":
				if cur.recipientBank == "" {
					cur.recipientBank = val
				}
			case "ПолучательБИК":
				if cur.recipientBIC == "" {
					cur.recipientBIC = val
				}
			case "НазначениеПлатежа":
				cur.Purpose = val
			}
		}
	}
	if err := scanner.Err(); err != nil {
		warnings = append(warnings, fmt.Sprintf("scan error: %v", err))
	}
	if len(out) == 0 {
		warnings = append(warnings, "no transactions parsed — file may be in an unsupported format")
	}
	return out, meta, warnings
}

// parseDotDate parses "DD.MM.YYYY" used by 1C exports.
func parseDotDate(s string) (time.Time, error) {
	return time.Parse("02.01.2006", s)
}
