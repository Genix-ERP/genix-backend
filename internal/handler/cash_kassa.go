package handler

// Kassa — cash registers, PKO/RKO cash orders and the cash book.
//
// These ten handlers were literal stubs (`response.Success(c, []interface{}{})`
// and `{"message": "…"}`) that executed no SQL at all: the Kassa tab was
// permanently empty, "create" returned a ghost row that vanished on refresh,
// and "confirm" was a no-op the UI reported as success. The three tables have
// existed since migration 003 — only the HTTP layer was missing.
//
// Confirming an order is the only place money moves, and it does four things in
// one transaction: flip draft→confirmed (guarded so a double-confirm can't
// double-credit), post a journal entry, move the register balance, and roll the
// day's cash-book row.

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
)

// ---------------------------------------------------------------- registers --

func (h *Handler) ListCashRegisters(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	page, pageSize := kassaPaging(c)

	where := " WHERE cr.tenant_id = $1 AND cr.deleted_at IS NULL"
	args := []interface{}{tenantID}
	if oid, okOrg := middleware.GetOrganizationID(c); okOrg && oid != uuid.Nil {
		where += fmt.Sprintf(" AND cr.organization_id = $%d", len(args)+1)
		args = append(args, oid)
	}
	if s := strings.TrimSpace(c.Query("search")); s != "" {
		where += fmt.Sprintf(" AND (cr.name ILIKE $%d OR COALESCE(cr.code,'') ILIKE $%d)", len(args)+1, len(args)+1)
		args = append(args, "%"+s+"%")
	}
	switch strings.ToLower(c.Query("is_active")) {
	case "true", "1":
		where += " AND cr.is_active = true"
	case "false", "0":
		where += " AND cr.is_active = false"
	}

	var total int
	if err := h.db.QueryRow(`SELECT COUNT(*) FROM cash_registers cr`+where, args...).Scan(&total); err != nil {
		h.log.Error("cash registers count failed", "error", err)
		response.InternalError(c, "Failed to list cash registers")
		return
	}

	rows, err := h.db.Query(fmt.Sprintf(`
		SELECT cr.id, cr.name, COALESCE(cr.code,''), cr.currency,
		       COALESCE(cr.limit_amount,0), COALESCE(cr.current_balance,0),
		       cr.is_active, cr.created_at
		FROM cash_registers cr`+where+`
		ORDER BY cr.name ASC LIMIT %d OFFSET %d`, pageSize, (page-1)*pageSize), args...)
	if err != nil {
		h.log.Error("cash registers query failed", "error", err)
		response.InternalError(c, "Failed to list cash registers")
		return
	}
	defer rows.Close()

	out := []gin.H{}
	for rows.Next() {
		var id uuid.UUID
		var name, code, currency string
		var limitAmt, balance float64
		var active bool
		var createdAt time.Time
		if rows.Scan(&id, &name, &code, &currency, &limitAmt, &balance, &active, &createdAt) != nil {
			continue
		}
		out = append(out, gin.H{
			"id": id.String(), "name": name, "code": code, "currency": currency,
			"limit_amount": limitAmt, "current_balance": balance,
			"is_active": active, "created_at": createdAt.Format(time.RFC3339),
		})
	}
	response.Paginated(c, out, page, pageSize, total)
}

func (h *Handler) CreateCashRegister(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	userID, _ := middleware.GetUserID(c)
	var orgArg interface{}
	if oid, okOrg := middleware.GetOrganizationID(c); okOrg && oid != uuid.Nil {
		orgArg = oid
	}

	var in struct {
		Name        string   `json:"name"`
		Code        string   `json:"code"`
		Currency    string   `json:"currency"`
		LimitAmount *float64 `json:"limit_amount"`
		IsActive    *bool    `json:"is_active"`
	}
	if err := c.ShouldBindJSON(&in); err != nil || strings.TrimSpace(in.Name) == "" {
		response.BadRequest(c, "name is required")
		return
	}
	if in.Currency == "" {
		in.Currency = "UZS"
	}
	limitAmt := 0.0
	if in.LimitAmount != nil {
		limitAmt = *in.LimitAmount
	}
	active := true
	if in.IsActive != nil {
		active = *in.IsActive
	}

	id := uuid.New()
	if _, err := h.db.Exec(`
		INSERT INTO cash_registers
		  (id, tenant_id, organization_id, name, code, currency, limit_amount,
		   current_balance, is_active, created_by, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,0,$8,$9,now(),now())`,
		id, tenantID, orgArg, in.Name, nullIfEmpty(in.Code), in.Currency, limitAmt, active, userID); err != nil {
		h.log.Error("create cash register failed", "error", err)
		response.InternalError(c, "Failed to create cash register")
		return
	}
	// Return the created ROW, not a message — the client re-parses the response
	// into its list; a `{"message": …}` body produced a blank ghost entry.
	h.respondCashRegister(c, tenantID, id, true)
}

func (h *Handler) GetCashRegister(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid cash register ID")
		return
	}
	h.respondCashRegister(c, tenantID, id, false)
}

func (h *Handler) UpdateCashRegister(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid cash register ID")
		return
	}
	var in struct {
		Name        *string  `json:"name"`
		Code        *string  `json:"code"`
		Currency    *string  `json:"currency"`
		LimitAmount *float64 `json:"limit_amount"`
		IsActive    *bool    `json:"is_active"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		response.BadRequest(c, "Invalid input")
		return
	}

	sets := []string{"updated_at = now()"}
	args := []interface{}{}
	add := func(col string, v interface{}) {
		args = append(args, v)
		sets = append(sets, fmt.Sprintf("%s = $%d", col, len(args)))
	}
	if in.Name != nil {
		add("name", *in.Name)
	}
	if in.Code != nil {
		add("code", nullIfEmpty(*in.Code))
	}
	if in.Currency != nil {
		add("currency", *in.Currency)
	}
	if in.LimitAmount != nil {
		add("limit_amount", *in.LimitAmount)
	}
	if in.IsActive != nil {
		add("is_active", *in.IsActive)
	}
	args = append(args, id, tenantID)

	res, err := h.db.Exec(fmt.Sprintf(
		`UPDATE cash_registers SET %s WHERE id = $%d AND tenant_id = $%d AND deleted_at IS NULL`,
		strings.Join(sets, ", "), len(args)-1, len(args)), args...)
	if err != nil {
		h.log.Error("update cash register failed", "error", err)
		response.InternalError(c, "Failed to update cash register")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		response.NotFound(c, "Cash register")
		return
	}
	h.respondCashRegister(c, tenantID, id, false)
}

func (h *Handler) respondCashRegister(c *gin.Context, tenantID, id uuid.UUID, created bool) {
	var name, code, currency string
	var limitAmt, balance float64
	var active bool
	var createdAt time.Time
	err := h.db.QueryRow(`
		SELECT name, COALESCE(code,''), currency, COALESCE(limit_amount,0),
		       COALESCE(current_balance,0), is_active, created_at
		FROM cash_registers WHERE id=$1 AND tenant_id=$2 AND deleted_at IS NULL`,
		id, tenantID).Scan(&name, &code, &currency, &limitAmt, &balance, &active, &createdAt)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Cash register")
		return
	}
	if err != nil {
		response.InternalError(c, "Failed to load cash register")
		return
	}
	body := gin.H{
		"id": id.String(), "name": name, "code": code, "currency": currency,
		"limit_amount": limitAmt, "current_balance": balance,
		"is_active": active, "created_at": createdAt.Format(time.RFC3339),
	}
	if created {
		response.Created(c, body)
		return
	}
	response.Success(c, body)
}

// ------------------------------------------------------------------- orders --

func (h *Handler) ListCashOrders(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	page, pageSize := kassaPaging(c)

	where := " WHERE co.tenant_id = $1 AND co.deleted_at IS NULL"
	args := []interface{}{tenantID}
	addArg := func(clause string, v interface{}) {
		args = append(args, v)
		where += fmt.Sprintf(clause, len(args))
	}
	if oid, okOrg := middleware.GetOrganizationID(c); okOrg && oid != uuid.Nil {
		addArg(" AND co.organization_id = $%d", oid)
	}
	if v := c.Query("cash_register_id"); v != "" {
		if rid, e := uuid.Parse(v); e == nil {
			addArg(" AND co.cash_register_id = $%d", rid)
		}
	}
	if v := strings.ToLower(strings.TrimSpace(c.Query("order_type"))); v == "pko" || v == "rko" {
		addArg(" AND co.order_type = $%d", v)
	}
	if v := c.Query("order_date"); v != "" {
		addArg(" AND co.order_date = $%d", v)
	}
	if v := c.Query("date_from"); v != "" {
		addArg(" AND co.order_date >= $%d", v)
	}
	if v := c.Query("date_to"); v != "" {
		addArg(" AND co.order_date <= $%d", v)
	}
	if v := c.Query("status"); v != "" {
		addArg(" AND co.status = $%d", v)
	}
	if s := strings.TrimSpace(c.Query("search")); s != "" {
		args = append(args, "%"+s+"%")
		where += fmt.Sprintf(" AND (co.order_number ILIKE $%d OR co.description ILIKE $%d OR COALESCE(ct.name,'') ILIKE $%d)",
			len(args), len(args), len(args))
	}

	const joins = `
		FROM cash_orders co
		LEFT JOIN contacts ct ON ct.id = co.partner_id
		LEFT JOIN accounts ac ON ac.id = co.account_id`

	var total int
	if err := h.db.QueryRow(`SELECT COUNT(*)`+joins+where, args...).Scan(&total); err != nil {
		h.log.Error("cash orders count failed", "error", err)
		response.InternalError(c, "Failed to list cash orders")
		return
	}

	rows, err := h.db.Query(fmt.Sprintf(`
		SELECT co.id, co.cash_register_id, co.order_number, co.order_type, co.order_date,
		       co.amount, co.currency, co.partner_id, COALESCE(ct.name,''),
		       co.account_id, COALESCE(ac.code,''), co.description, co.status,
		       co.cashier_id, co.created_at`+joins+where+`
		ORDER BY co.order_date DESC, co.created_at DESC LIMIT %d OFFSET %d`,
		pageSize, (page-1)*pageSize), args...)
	if err != nil {
		h.log.Error("cash orders query failed", "error", err)
		response.InternalError(c, "Failed to list cash orders")
		return
	}
	defer rows.Close()

	out := []gin.H{}
	for rows.Next() {
		out = append(out, scanCashOrderRow(rows))
	}
	response.Paginated(c, out, page, pageSize, total)
}

func scanCashOrderRow(rows *sql.Rows) gin.H {
	var id, registerID uuid.UUID
	var partnerID, accountID, cashierID sql.NullString
	var number, orderType, currency, partnerName, accountCode, description, status string
	var amount float64
	var orderDate, createdAt time.Time
	if rows.Scan(&id, &registerID, &number, &orderType, &orderDate, &amount, &currency,
		&partnerID, &partnerName, &accountID, &accountCode, &description, &status,
		&cashierID, &createdAt) != nil {
		return gin.H{}
	}
	return gin.H{
		"id": id.String(), "cash_register_id": registerID.String(),
		"order_number": number, "order_type": orderType,
		"order_date": orderDate.Format("2006-01-02"), "amount": amount, "currency": currency,
		"partner_id": partnerID.String, "partner_name": partnerName,
		"account_id": accountID.String, "account_code": accountCode,
		"description": description, "status": status, "cashier_id": cashierID.String,
		"created_at": createdAt.Format(time.RFC3339),
	}
}

func (h *Handler) CreateCashOrder(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	userID, _ := middleware.GetUserID(c)
	var orgArg interface{}
	if oid, okOrg := middleware.GetOrganizationID(c); okOrg && oid != uuid.Nil {
		orgArg = oid
	}

	// The client sends partner_name / account_code as strings, not ids.
	var in struct {
		CashRegisterID string  `json:"cash_register_id"`
		OrderType      string  `json:"order_type"`
		OrderDate      string  `json:"order_date"`
		Amount         float64 `json:"amount"`
		Currency       string  `json:"currency"`
		Description    string  `json:"description"`
		PartnerName    string  `json:"partner_name"`
		PartnerID      string  `json:"partner_id"`
		AccountCode    string  `json:"account_code"`
		AccountID      string  `json:"account_id"`
		CashierID      string  `json:"cashier_id"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		response.BadRequest(c, "Invalid input")
		return
	}
	orderType := strings.ToLower(strings.TrimSpace(in.OrderType))
	if orderType != "pko" && orderType != "rko" {
		response.BadRequest(c, "order_type must be pko or rko")
		return
	}
	if in.Amount <= 0 {
		response.BadRequest(c, "amount must be greater than zero")
		return
	}
	registerID, err := uuid.Parse(in.CashRegisterID)
	if err != nil {
		response.BadRequest(c, "cash_register_id is required")
		return
	}
	var regExists bool
	if e := h.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM cash_registers
		WHERE id=$1 AND tenant_id=$2 AND deleted_at IS NULL)`, registerID, tenantID).Scan(&regExists); e != nil || !regExists {
		response.NotFound(c, "Cash register")
		return
	}
	orderDate := in.OrderDate
	if orderDate == "" {
		orderDate = time.Now().Format("2006-01-02")
	}
	if in.Currency == "" {
		in.Currency = "UZS"
	}

	// Resolve partner: explicit id wins, else resolve-or-create by name.
	var partnerArg interface{}
	if in.PartnerID != "" {
		if pid, e := uuid.Parse(in.PartnerID); e == nil {
			partnerArg = pid
		}
	}
	if partnerArg == nil && strings.TrimSpace(in.PartnerName) != "" {
		var pid uuid.UUID
		e := h.db.QueryRow(`SELECT id FROM contacts
			WHERE tenant_id=$1 AND deleted_at IS NULL AND lower(name)=lower($2) LIMIT 1`,
			tenantID, strings.TrimSpace(in.PartnerName)).Scan(&pid)
		if e == sql.ErrNoRows {
			pid = uuid.New()
			if _, ie := h.db.Exec(`INSERT INTO contacts
				(id, tenant_id, organization_id, type, name, is_active, created_by, created_at, updated_at)
				VALUES ($1,$2,$3,'customer',$4,true,$5,now(),now())`,
				pid, tenantID, orgArg, strings.TrimSpace(in.PartnerName), userID); ie == nil {
				partnerArg = pid
			}
		} else if e == nil {
			partnerArg = pid
		}
	}

	// Resolve account by explicit id or by code.
	var accountArg interface{}
	if in.AccountID != "" {
		if aid, e := uuid.Parse(in.AccountID); e == nil {
			accountArg = aid
		}
	}
	if accountArg == nil && strings.TrimSpace(in.AccountCode) != "" {
		var aid uuid.UUID
		if e := h.db.QueryRow(`SELECT id FROM accounts
			WHERE tenant_id=$1 AND deleted_at IS NULL AND code=$2 LIMIT 1`,
			tenantID, strings.TrimSpace(in.AccountCode)).Scan(&aid); e == nil {
			accountArg = aid
		}
	}

	var cashierArg interface{} = userID
	if in.CashierID != "" {
		if cid, e := uuid.Parse(in.CashierID); e == nil {
			cashierArg = cid
		}
	}

	prefix := "PKO"
	if orderType == "rko" {
		prefix = "RKO"
	}
	seq := nextEntryNumberSeq(h.db, tenantID, orgArg, prefix, 1)
	orderNumber := fmt.Sprintf("%s-%06d", prefix, seq)

	id := uuid.New()
	if _, err := h.db.Exec(`
		INSERT INTO cash_orders
		  (id, tenant_id, organization_id, cash_register_id, order_number, order_type,
		   order_date, amount, currency, partner_id, account_id, description,
		   cashier_id, status, created_by, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,'draft',$14,now(),now())`,
		id, tenantID, orgArg, registerID, orderNumber, orderType, orderDate, in.Amount,
		in.Currency, partnerArg, accountArg, in.Description, cashierArg, userID); err != nil {
		h.log.Error("create cash order failed", "error", err)
		response.InternalError(c, "Failed to create cash order")
		return
	}
	h.respondCashOrder(c, tenantID, id, true)
}

func (h *Handler) GetCashOrder(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid cash order ID")
		return
	}
	h.respondCashOrder(c, tenantID, id, false)
}

func (h *Handler) UpdateCashOrder(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid cash order ID")
		return
	}
	var in struct {
		Amount      *float64 `json:"amount"`
		OrderDate   *string  `json:"order_date"`
		Description *string  `json:"description"`
		AccountCode *string  `json:"account_code"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		response.BadRequest(c, "Invalid input")
		return
	}

	sets := []string{"updated_at = now()"}
	args := []interface{}{}
	add := func(col string, v interface{}) {
		args = append(args, v)
		sets = append(sets, fmt.Sprintf("%s = $%d", col, len(args)))
	}
	if in.Amount != nil {
		if *in.Amount <= 0 {
			response.BadRequest(c, "amount must be greater than zero")
			return
		}
		add("amount", *in.Amount)
	}
	if in.OrderDate != nil {
		add("order_date", *in.OrderDate)
	}
	if in.Description != nil {
		add("description", *in.Description)
	}
	if in.AccountCode != nil && strings.TrimSpace(*in.AccountCode) != "" {
		var aid uuid.UUID
		if e := h.db.QueryRow(`SELECT id FROM accounts
			WHERE tenant_id=$1 AND deleted_at IS NULL AND code=$2 LIMIT 1`,
			tenantID, strings.TrimSpace(*in.AccountCode)).Scan(&aid); e == nil {
			add("account_id", aid)
		}
	}
	args = append(args, id, tenantID)

	// Only a draft may be edited — a confirmed order already has a posted
	// journal entry and a moved register balance behind it.
	res, err := h.db.Exec(fmt.Sprintf(
		`UPDATE cash_orders SET %s WHERE id=$%d AND tenant_id=$%d AND deleted_at IS NULL AND status='draft'`,
		strings.Join(sets, ", "), len(args)-1, len(args)), args...)
	if err != nil {
		h.log.Error("update cash order failed", "error", err)
		response.InternalError(c, "Failed to update cash order")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		response.BadRequest(c, "Only draft cash orders can be edited")
		return
	}
	h.respondCashOrder(c, tenantID, id, false)
}

// DeleteCashOrder soft-deletes a DRAFT order. Confirmed orders are refused.
func (h *Handler) DeleteCashOrder(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid cash order ID")
		return
	}
	res, err := h.db.Exec(`UPDATE cash_orders SET deleted_at = now(), updated_at = now()
		WHERE id=$1 AND tenant_id=$2 AND deleted_at IS NULL AND status='draft'`, id, tenantID)
	if err != nil {
		h.log.Error("delete cash order failed", "error", err)
		response.InternalError(c, "Failed to delete cash order")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		response.BadRequest(c, "Only draft cash orders can be deleted")
		return
	}
	response.Success(c, gin.H{"id": id.String(), "deleted": true})
}

// ConfirmCashOrder posts the order: draft→confirmed, journal entry, register
// balance, cash-book roll — all in one transaction.
func (h *Handler) ConfirmCashOrder(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	userID, _ := middleware.GetUserID(c)
	var orgArg interface{}
	if oid, okOrg := middleware.GetOrganizationID(c); okOrg && oid != uuid.Nil {
		orgArg = oid
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid cash order ID")
		return
	}

	tx, err := h.db.Begin()
	if err != nil {
		response.InternalError(c, "Failed to confirm cash order")
		return
	}
	defer tx.Rollback()

	// The status guard lives INSIDE the update: 0 rows affected means it was
	// already confirmed, so a double-tap cannot double-credit the register.
	var registerID uuid.UUID
	var accountID sql.NullString
	var orderType, orderNumber, description string
	var amount float64
	var orderDate time.Time
	err = tx.QueryRow(`
		UPDATE cash_orders SET status='confirmed', updated_at=now()
		WHERE id=$1 AND tenant_id=$2 AND deleted_at IS NULL AND status='draft'
		RETURNING cash_register_id, account_id, order_type, order_number, description, amount, order_date`,
		id, tenantID).Scan(&registerID, &accountID, &orderType, &orderNumber, &description, &amount, &orderDate)
	if err == sql.ErrNoRows {
		response.BadRequest(c, "Only draft cash orders can be confirmed")
		return
	}
	if err != nil {
		h.log.Error("confirm cash order failed", "error", err)
		response.InternalError(c, "Failed to confirm cash order")
		return
	}

	// Cash account of the register (5010 family); counter account from the order.
	var cashAccountID uuid.UUID
	_ = tx.QueryRow(`SELECT id FROM accounts
		WHERE tenant_id=$1 AND deleted_at IS NULL AND code LIKE '5010%'
		ORDER BY code LIMIT 1`, tenantID).Scan(&cashAccountID)

	var journalEntryID *uuid.UUID
	if cashAccountID != uuid.Nil && accountID.Valid {
		counterID, perr := uuid.Parse(accountID.String)
		if perr == nil {
			jeID := uuid.New()
			seq := nextEntryNumberSeq(tx, tenantID, orgArg, "CASH", 1)
			entryNumber := fmt.Sprintf("CASH-%06d", seq)
			if _, e := tx.Exec(`INSERT INTO journal_entries
				(id, tenant_id, organization_id, entry_number, entry_date, description,
				 reference, status, total_debit, total_credit, created_by, created_at, updated_at)
				VALUES ($1,$2,$3,$4,$5,$6,$7,'posted',$8,$8,$9,now(),now())`,
				jeID, tenantID, orgArg, entryNumber, orderDate, description, orderNumber, amount, userID); e == nil {
				// PKO: money in  → Dt cash / Kt counter
				// RKO: money out → Dt counter / Kt cash
				dt, ct := cashAccountID, counterID
				if orderType == "rko" {
					dt, ct = counterID, cashAccountID
				}
				tx.Exec(`INSERT INTO journal_entry_lines
					(id, journal_entry_id, account_id, description, debit_amount, credit_amount, line_number, created_at)
					VALUES ($1,$2,$3,$4,$5,0,1,now())`, uuid.New(), jeID, dt, description, amount)
				tx.Exec(`INSERT INTO journal_entry_lines
					(id, journal_entry_id, account_id, description, debit_amount, credit_amount, line_number, created_at)
					VALUES ($1,$2,$3,$4,0,$5,2,now())`, uuid.New(), jeID, ct, description, amount)
				tx.Exec(`UPDATE cash_orders SET journal_entry_id=$1 WHERE id=$2`, jeID, id)
				journalEntryID = &jeID
			}
		}
	}

	// Register balance and the day's cash-book row.
	delta := amount
	incomeDelta, expenseDelta := amount, 0.0
	if orderType == "rko" {
		delta = -amount
		incomeDelta, expenseDelta = 0.0, amount
	}
	if _, e := tx.Exec(`UPDATE cash_registers
		SET current_balance = COALESCE(current_balance,0) + $1, updated_at = now()
		WHERE id=$2 AND tenant_id=$3`, delta, registerID, tenantID); e != nil {
		h.log.Error("cash register balance update failed", "error", e)
		response.InternalError(c, "Failed to confirm cash order")
		return
	}

	// closing_balance is GENERATED — never write it.
	if _, e := tx.Exec(`
		INSERT INTO cash_book_entries
		  (id, tenant_id, cash_register_id, entry_date, opening_balance, total_income, total_expense, created_at, updated_at)
		VALUES ($1,$2,$3,$4,
		        COALESCE((SELECT cb.opening_balance + cb.total_income - cb.total_expense
		                  FROM cash_book_entries cb
		                  WHERE cb.tenant_id=$2 AND cb.cash_register_id=$3 AND cb.entry_date < $4
		                  ORDER BY cb.entry_date DESC LIMIT 1), 0),
		        $5,$6,now(),now())
		ON CONFLICT (tenant_id, cash_register_id, entry_date) DO UPDATE
		SET total_income  = cash_book_entries.total_income  + EXCLUDED.total_income,
		    total_expense = cash_book_entries.total_expense + EXCLUDED.total_expense,
		    updated_at    = now()`,
		uuid.New(), tenantID, registerID, orderDate, incomeDelta, expenseDelta); e != nil {
		h.log.Error("cash book upsert failed", "error", e)
		response.InternalError(c, "Failed to confirm cash order")
		return
	}

	if err := tx.Commit(); err != nil {
		response.InternalError(c, "Failed to confirm cash order")
		return
	}

	out := gin.H{"id": id.String(), "status": "confirmed", "order_number": orderNumber}
	if journalEntryID != nil {
		out["journal_entry_id"] = journalEntryID.String()
	}
	response.Success(c, out)
}

func (h *Handler) respondCashOrder(c *gin.Context, tenantID, id uuid.UUID, created bool) {
	rows, err := h.db.Query(`
		SELECT co.id, co.cash_register_id, co.order_number, co.order_type, co.order_date,
		       co.amount, co.currency, co.partner_id, COALESCE(ct.name,''),
		       co.account_id, COALESCE(ac.code,''), co.description, co.status,
		       co.cashier_id, co.created_at
		FROM cash_orders co
		LEFT JOIN contacts ct ON ct.id = co.partner_id
		LEFT JOIN accounts ac ON ac.id = co.account_id
		WHERE co.id=$1 AND co.tenant_id=$2 AND co.deleted_at IS NULL`, id, tenantID)
	if err != nil {
		response.InternalError(c, "Failed to load cash order")
		return
	}
	defer rows.Close()
	if !rows.Next() {
		response.NotFound(c, "Cash order")
		return
	}
	body := scanCashOrderRow(rows)
	if created {
		response.Created(c, body)
		return
	}
	response.Success(c, body)
}

// ---------------------------------------------------------------- cash book --

func (h *Handler) GetCashBook(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	page, pageSize := kassaPaging(c)

	// ── Which till ──────────────────────────────────────────────────────
	registerID := c.Query("cash_register_id")
	registerName := ""
	registerCount := 0
	if registerID == "" {
		// Default to the tenant's first active register so the screen isn't
		// empty when the client hasn't picked one yet. The fallback is silent
		// by nature — with several kassas the caller gets ONE till's book and
		// nothing said which — so the resolved register rides on every row and
		// a client can label the book and offer a switcher.
		var rid uuid.UUID
		var rname string
		if e := h.db.QueryRow(`SELECT id, name FROM cash_registers
			WHERE tenant_id=$1 AND deleted_at IS NULL AND is_active = true
			ORDER BY name LIMIT 1`, tenantID).Scan(&rid, &rname); e == nil {
			registerID = rid.String()
			registerName = rname
		}
	} else if rid, e := uuid.Parse(registerID); e == nil {
		_ = h.db.QueryRow(`SELECT name FROM cash_registers WHERE id=$1 AND tenant_id=$2`,
			rid, tenantID).Scan(&registerName)
	}
	_ = h.db.QueryRow(`SELECT COUNT(*) FROM cash_registers
		WHERE tenant_id=$1 AND deleted_at IS NULL AND is_active = true`, tenantID).Scan(&registerCount)

	// ── The book, DERIVED from the events (kassa_ledger.go) ─────────────
	// Not read from the materialised cash_book_entries table: that table was
	// only ever written by ConfirmCashOrder, so the book showed PKO/RKO orders
	// and silently omitted every cash transaction — which is precisely why the
	// web and mobile totals disagreed. Deriving also survives the hard DELETE
	// on cash_transactions, which a running total in a side table cannot.
	args := []interface{}{tenantID}

	registerFilter := "TRUE"
	if rid, e := uuid.Parse(registerID); e == nil {
		args = append(args, rid)
		registerFilter = fmt.Sprintf("mv.register_id = $%d", len(args))
	}

	// The date window is applied ONLY in the outer select. Filtering before the
	// window function would restart the running balance at zero on every page —
	// the same "starts from 0 and drags every balance negative" failure the web
	// balance chain had.
	dateFilter := ""
	if v := c.Query("date_from"); v != "" {
		args = append(args, v)
		dateFilter += fmt.Sprintf(" AND d >= $%d", len(args))
	}
	if v := c.Query("date_to"); v != "" {
		args = append(args, v)
		dateFilter += fmt.Sprintf(" AND d <= $%d", len(args))
	}

	book := `
		WITH mv AS (` + kassaMovementSQL + `),
		daily AS (
			SELECT mv.d, SUM(mv.income) AS inc, SUM(mv.expense) AS exp
			FROM mv WHERE ` + registerFilter + `
			GROUP BY mv.d
		),
		cum AS (
			SELECT d, inc, exp,
			       COALESCE(SUM(inc - exp) OVER (
			           ORDER BY d ROWS BETWEEN UNBOUNDED PRECEDING AND 1 PRECEDING
			       ), 0) AS opening
			FROM daily
		)
		SELECT d, opening, inc, exp, opening + inc - exp AS closing
		FROM cum
		WHERE TRUE` + dateFilter

	var total int
	if err := h.db.QueryRow(`SELECT COUNT(*) FROM (`+book+`) t`, args...).Scan(&total); err != nil {
		h.log.Error("cash book count failed", "error", err)
		response.InternalError(c, "Failed to load cash book")
		return
	}

	rows, err := h.db.Query(fmt.Sprintf(book+`
		ORDER BY d DESC LIMIT %d OFFSET %d`, pageSize, (page-1)*pageSize), args...)
	if err != nil {
		h.log.Error("cash book query failed", "error", err)
		response.InternalError(c, "Failed to load cash book")
		return
	}
	defer rows.Close()

	out := []gin.H{}
	for rows.Next() {
		var d time.Time
		var opening, income, expense, closing float64
		if rows.Scan(&d, &opening, &income, &expense, &closing) != nil {
			continue
		}
		out = append(out, gin.H{
			"entry_date": d.Format("2006-01-02"), "opening_balance": opening,
			"total_income": income, "total_expense": expense, "closing_balance": closing,
			// Which till this row belongs to. Per-row rather than a sibling of
			// `data`, because `data` is a list in the shipped clients and
			// turning it into an object would break both.
			"cash_register_id": registerID, "cash_register_name": registerName,
			"active_register_count": registerCount,
		})
	}
	response.Paginated(c, out, page, pageSize, total)
}

// kassaPaging parses page/page_size with the house defaults (20, cap 100 —
// clamped to the cap, never falling back to the default).
func kassaPaging(c *gin.Context) (int, int) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	if page < 1 {
		page = 1
	}
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	if pageSize < 1 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}
	return page, pageSize
}
