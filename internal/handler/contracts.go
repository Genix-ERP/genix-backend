package handler

// Shartnomalar (Contracts) module — core CRUD, lifecycle transitions,
// registry stats and numbering. Related child resources (amendments,
// files, links, invoices, tasks, activity) live in contracts_related.go;
// AI extraction/summary in contracts_ai.go.
//
// The physical table is procurement_contracts (see migration 443 and
// docs/shartnomalar-audit.md for why). vendor_id is the counterparty for
// both directions: 'income' (kirim — customer contracts) and 'expense'
// (chiqim — vendor contracts).

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/genixerp/genix-backend/internal/domain/entity"
	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// contractSelectColumns is the shared projection for list/get so both scan
// through scanContractRow.
const contractSelectColumns = `
	c.id, c.contract_number, c.title, c.vendor_id, COALESCE(v.name, c.vendor_name, ''),
	c.direction, c.contract_type, c.status, c.start_date, c.end_date, c.signed_date,
	COALESCE(c.value, 0), COALESCE(c.currency, 'UZS'), c.terms, c.description,
	COALESCE(c.auto_renewal, false), COALESCE(c.renewal_term_days, 0), c.notes,
	c.responsible_employee_id,
	TRIM(COALESCE(e.first_name, '') || ' ' || COALESCE(e.last_name, '')),
	c.archived_at, c.created_by, c.created_at, c.updated_at,
	COALESCE(am.delta_sum, 0), COALESCE(am.cnt, 0), COALESCE(cf.cnt, 0),
	COALESCE(inv.paid_total, 0)`

const contractFromClause = `
	FROM procurement_contracts c
	LEFT JOIN contacts v ON v.id = c.vendor_id AND v.tenant_id = c.tenant_id
	LEFT JOIN employees e ON e.id = c.responsible_employee_id AND e.tenant_id = c.tenant_id
	LEFT JOIN LATERAL (
		SELECT COALESCE(SUM(a.amount_delta), 0) AS delta_sum, COUNT(*) AS cnt
		FROM contract_amendments a
		WHERE a.contract_id = c.id AND a.deleted_at IS NULL
	) am ON true
	LEFT JOIN LATERAL (
		SELECT COUNT(*) AS cnt FROM contract_files f
		WHERE f.contract_id = c.id AND f.deleted_at IS NULL
	) cf ON true
	LEFT JOIN LATERAL (
		SELECT COALESCE(SUM(x.amount_paid), 0) AS paid_total FROM (
			SELECT si.amount_paid FROM sales_invoices si
			WHERE si.contract_id = c.id AND si.deleted_at IS NULL AND si.status <> 'cancelled'
			UNION ALL
			SELECT pi.amount_paid FROM purchase_invoices pi
			WHERE pi.contract_id = c.id AND pi.deleted_at IS NULL AND pi.status <> 'cancelled'
		) x
	) inv ON true`

// scanContractRow scans one row of the shared projection into a response.
func scanContractRow(scan func(dest ...interface{}) error) (*entity.ContractResponse, error) {
	var (
		r              entity.ContractResponse
		vendorID       sql.NullString
		endDate        sql.NullTime
		signedDate     sql.NullTime
		terms          sql.NullString
		description    sql.NullString
		notes          sql.NullString
		responsibleID  sql.NullString
		archivedAt     sql.NullTime
		createdBy      sql.NullString
		amendmentDelta float64
		direction      string
	)
	err := scan(
		&r.ID, &r.ContractNumber, &r.Title, &vendorID, &r.VendorName,
		&direction, &r.ContractType, &r.Status, &r.StartDate, &endDate, &signedDate,
		&r.Value, &r.Currency, &terms, &description,
		&r.AutoRenewal, &r.RenewalTermDays, &notes,
		&responsibleID, &r.ResponsibleEmployeeName,
		&archivedAt, &createdBy, &r.CreatedAt, &r.UpdatedAt,
		&amendmentDelta, &r.AmendmentCount, &r.FileCount,
		&r.PaidTotal,
	)
	if err != nil {
		return nil, err
	}
	r.Direction = entity.ContractDirection(direction)
	if vendorID.Valid {
		if id, err := uuid.Parse(vendorID.String); err == nil {
			r.VendorID = &id
		}
	}
	if endDate.Valid {
		d := endDate.Time
		r.EndDate = &d
		// Calendar-day difference (both truncated to dates).
		today := time.Now().Truncate(24 * time.Hour)
		days := int(d.Truncate(24*time.Hour).Sub(today).Hours() / 24)
		r.DaysToExpiry = &days
	}
	if signedDate.Valid {
		d := signedDate.Time
		r.SignedDate = &d
	}
	if terms.Valid {
		r.Terms = &terms.String
	}
	if description.Valid {
		r.Description = &description.String
	}
	if notes.Valid {
		r.Notes = &notes.String
	}
	if responsibleID.Valid {
		if id, err := uuid.Parse(responsibleID.String); err == nil {
			r.ResponsibleEmployeeID = &id
		}
	}
	if archivedAt.Valid {
		t := archivedAt.Time
		r.ArchivedAt = &t
	}
	if createdBy.Valid {
		if id, err := uuid.Parse(createdBy.String); err == nil {
			r.CreatedBy = &id
		}
	}
	r.EffectiveAmount = r.Value + amendmentDelta
	r.Outstanding = r.EffectiveAmount - r.PaidTotal
	r.AllowedTransitions = entity.ContractTransitions[r.Status]
	if r.AllowedTransitions == nil {
		r.AllowedTransitions = []entity.ContractStatus{}
	}
	return &r, nil
}

var contractSortColumns = map[string]string{
	"created_at":      "c.created_at",
	"contract_number": "c.contract_number",
	"title":           "c.title",
	"value":           "c.value",
	"start_date":      "c.start_date",
	"end_date":        "c.end_date",
	"status":          "c.status",
	"vendor_name":     "COALESCE(v.name, c.vendor_name)",
}

// ListContracts returns the contracts registry with filters.
// GET /contracts?page&limit&search&status&vendor_id&direction&responsible_employee_id&expiring_within&archived&sort_by&sort_order
func (h *Handler) ListContracts(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 20
	}

	where := []string{"c.tenant_id = $1", "c.deleted_at IS NULL"}
	args := []interface{}{tenantID}

	arg := func(v interface{}) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}

	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		where = append(where, "c.organization_id = "+arg(orgID))
	}

	switch c.Query("archived") {
	case "true":
		where = append(where, "c.archived_at IS NOT NULL")
	case "all":
	default:
		where = append(where, "c.archived_at IS NULL")
	}

	if status := c.Query("status"); status != "" {
		where = append(where, "c.status = "+arg(status))
	}
	if direction := c.Query("direction"); direction != "" {
		where = append(where, "c.direction = "+arg(direction))
	}
	if vendorID := c.Query("vendor_id"); vendorID != "" {
		where = append(where, "c.vendor_id = "+arg(vendorID))
	}
	if respID := c.Query("responsible_employee_id"); respID != "" {
		where = append(where, "c.responsible_employee_id = "+arg(respID))
	}
	if days := c.Query("expiring_within"); days != "" {
		if n, err := strconv.Atoi(days); err == nil && n > 0 {
			where = append(where, "c.status = 'active'")
			where = append(where, "c.end_date IS NOT NULL AND c.end_date >= CURRENT_DATE AND c.end_date <= CURRENT_DATE + "+arg(n)+" * INTERVAL '1 day'")
		}
	} else if c.Query("expiring_soon") == "true" { // legacy param
		where = append(where, "c.status = 'active'")
		where = append(where, "c.end_date IS NOT NULL AND c.end_date >= CURRENT_DATE AND c.end_date <= CURRENT_DATE + INTERVAL '30 days'")
	}
	if search := strings.TrimSpace(c.Query("search")); search != "" {
		p := arg("%" + search + "%")
		where = append(where, fmt.Sprintf("(c.contract_number ILIKE %s OR c.title ILIKE %s OR COALESCE(v.name, c.vendor_name, '') ILIKE %s)", p, p, p))
	}

	orderBy := "c.created_at"
	if col, okS := contractSortColumns[c.Query("sort_by")]; okS {
		orderBy = col
	}
	orderDir := "DESC"
	if strings.EqualFold(c.Query("sort_order"), "asc") {
		orderDir = "ASC"
	}

	whereClause := " WHERE " + strings.Join(where, " AND ")

	var total int
	countQuery := "SELECT COUNT(*) FROM procurement_contracts c LEFT JOIN contacts v ON v.id = c.vendor_id AND v.tenant_id = c.tenant_id" + whereClause
	if err := h.db.QueryRow(countQuery, args...).Scan(&total); err != nil {
		h.log.Error("Failed to count contracts", "error", err)
		response.InternalError(c, "Failed to list contracts")
		return
	}

	query := "SELECT " + contractSelectColumns + contractFromClause + whereClause +
		fmt.Sprintf(" ORDER BY %s %s, c.id", orderBy, orderDir) +
		" LIMIT " + arg(limit) + " OFFSET " + arg((page-1)*limit)

	rows, err := h.db.Query(query, args...)
	if err != nil {
		h.log.Error("Failed to list contracts", "error", err)
		response.InternalError(c, "Failed to list contracts")
		return
	}
	defer rows.Close()

	contracts := []entity.ContractResponse{}
	for rows.Next() {
		r, err := scanContractRow(rows.Scan)
		if err != nil {
			h.log.Error("Failed to scan contract", "error", err)
			response.InternalError(c, "Failed to list contracts")
			return
		}
		contracts = append(contracts, *r)
	}

	response.Paginated(c, contracts, page, limit, total)
}

// GetContractStats returns the stat-card numbers for the registry page.
// GET /contracts/stats
func (h *Handler) GetContractStats(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	args := []interface{}{tenantID}
	orgFilter := ""
	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		args = append(args, orgID)
		orgFilter = " AND c.organization_id = $2"
	}

	var s entity.ContractStats
	err := h.db.QueryRow(`
		SELECT
			COUNT(*),
			COUNT(*) FILTER (WHERE c.status = 'active'),
			COUNT(*) FILTER (WHERE c.status = 'active' AND c.end_date IS NOT NULL
				AND c.end_date >= CURRENT_DATE AND c.end_date <= CURRENT_DATE + INTERVAL '30 days'),
			COALESCE(SUM(COALESCE(c.value, 0) + am.delta_sum) FILTER (WHERE c.status = 'active'), 0),
			COALESCE(SUM(COALESCE(c.value, 0) + am.delta_sum - inv.paid_total) FILTER (WHERE c.status = 'active'), 0)
		FROM procurement_contracts c
		LEFT JOIN LATERAL (
			SELECT COALESCE(SUM(a.amount_delta), 0) AS delta_sum
			FROM contract_amendments a WHERE a.contract_id = c.id AND a.deleted_at IS NULL
		) am ON true
		LEFT JOIN LATERAL (
			SELECT COALESCE(SUM(x.amount_paid), 0) AS paid_total FROM (
				SELECT si.amount_paid FROM sales_invoices si
				WHERE si.contract_id = c.id AND si.deleted_at IS NULL AND si.status <> 'cancelled'
				UNION ALL
				SELECT pi.amount_paid FROM purchase_invoices pi
				WHERE pi.contract_id = c.id AND pi.deleted_at IS NULL AND pi.status <> 'cancelled'
			) x
		) inv ON true
		WHERE c.tenant_id = $1 AND c.deleted_at IS NULL AND c.archived_at IS NULL`+orgFilter,
		args...,
	).Scan(&s.Total, &s.Active, &s.ExpiringSoon, &s.ActiveTotalValue, &s.Outstanding)
	if err != nil {
		h.log.Error("Failed to compute contract stats", "error", err)
		response.InternalError(c, "Failed to compute stats")
		return
	}

	response.Success(c, s)
}

// nextContractNumber computes the next CNT-YYYY-NNNN for the tenant.
func (h *Handler) nextContractNumber(tenantID uuid.UUID) string {
	year := time.Now().Format("2006")
	var maxN sql.NullInt64
	h.db.QueryRow(`
		SELECT MAX((regexp_match(contract_number, '^CNT-\d{4}-(\d+)$'))[1]::bigint)
		FROM procurement_contracts
		WHERE tenant_id = $1 AND contract_number LIKE 'CNT-' || $2 || '-%'
	`, tenantID, year).Scan(&maxN)
	return fmt.Sprintf("CNT-%s-%04d", year, maxN.Int64+1)
}

// GetNextContractNumber suggests the next auto number for the create form.
// GET /contracts/next-number
func (h *Handler) GetNextContractNumber(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	response.Success(c, gin.H{"contract_number": h.nextContractNumber(tenantID)})
}

// contractAudit writes one activity row for the contract's history feed.
func (h *Handler) contractAudit(tenantID, userID, contractID uuid.UUID, action string, oldValues, newValues map[string]interface{}) {
	oldJSON, _ := json.Marshal(oldValues)
	newJSON, _ := json.Marshal(newValues)
	if _, err := h.db.Exec(`
		INSERT INTO audit_logs (id, tenant_id, user_id, action, entity_type, entity_id, old_values, new_values, created_at)
		VALUES ($1, $2, $3, $4, 'contract', $5, $6, $7, $8)
	`, uuid.New(), tenantID, nullableUUID(userID), action, contractID, oldJSON, newJSON, time.Now()); err != nil {
		h.log.Error("Failed to write contract audit log", "error", err, "contract_id", contractID)
	}
}

// validateContractEnums checks direction / contract_type values.
func validateContractEnums(direction, contractType string) error {
	if direction != "" && direction != string(entity.ContractDirectionIncome) && direction != string(entity.ContractDirectionExpense) {
		return fmt.Errorf("direction must be 'income' or 'expense'")
	}
	switch contractType {
	case "", string(entity.ContractTypeFixed), string(entity.ContractTypeAnnual), string(entity.ContractTypeMonthly), string(entity.ContractTypeProject):
		return nil
	}
	return fmt.Errorf("invalid contract_type")
}

// CreateContract creates a new contract (always in 'draft').
// POST /contracts
func (h *Handler) CreateContract(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	userID, _ := middleware.GetUserID(c)

	var input entity.CreateContractInput
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid contract input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	direction := input.Direction
	if direction == "" {
		direction = string(entity.ContractDirectionExpense)
	}
	contractType := input.ContractType
	if contractType == "" {
		contractType = string(entity.ContractTypeFixed)
	}
	if err := validateContractEnums(direction, contractType); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	vendorID, err := uuid.Parse(input.VendorID)
	if err != nil {
		response.BadRequest(c, "Invalid counterparty ID")
		return
	}
	// The counterparty may be of any contacts type — direction, not contact
	// type, defines the money flow (a 'customer' can hold a kirim contract).
	var vendorName string
	err = h.db.QueryRow(`SELECT name FROM contacts WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL`,
		vendorID, tenantID).Scan(&vendorName)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Counterparty")
		return
	}
	if err != nil {
		h.log.Error("Failed to verify counterparty", "error", err)
		response.InternalError(c, "Failed to create contract")
		return
	}

	var responsibleID *uuid.UUID
	if input.ResponsibleEmployeeID != "" {
		rid, err := uuid.Parse(input.ResponsibleEmployeeID)
		if err != nil {
			response.BadRequest(c, "Invalid responsible employee ID")
			return
		}
		var exists bool
		if err := h.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM employees WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL)`,
			rid, tenantID).Scan(&exists); err != nil || !exists {
			response.NotFound(c, "Responsible employee")
			return
		}
		responsibleID = &rid
	}

	startDate, err := time.Parse("2006-01-02", input.StartDate)
	if err != nil {
		response.BadRequest(c, "Invalid start date")
		return
	}
	var endDate *time.Time
	if input.EndDate != "" {
		ed, err := time.Parse("2006-01-02", input.EndDate)
		if err != nil {
			response.BadRequest(c, "Invalid end date")
			return
		}
		if ed.Before(startDate) {
			response.BadRequest(c, "End date cannot be before start date")
			return
		}
		endDate = &ed
	}
	var signedDate *time.Time
	if input.SignedDate != "" {
		sd, err := time.Parse("2006-01-02", input.SignedDate)
		if err != nil {
			response.BadRequest(c, "Invalid signed date")
			return
		}
		signedDate = &sd
	}

	currency := input.Currency
	if currency == "" {
		currency = "UZS"
	}

	var terms, description, notes *string
	if input.Terms != "" {
		terms = &input.Terms
	}
	if input.Description != "" {
		description = &input.Description
	}
	if input.Notes != "" {
		notes = &input.Notes
	}

	orgID, _ := middleware.GetOrganizationID(c)
	var orgIDPtr *uuid.UUID
	if orgID != uuid.Nil {
		orgIDPtr = &orgID
	}

	id := uuid.New()
	now := time.Now()

	insert := func(number string) error {
		_, err := h.db.Exec(`
			INSERT INTO procurement_contracts (
				id, tenant_id, organization_id, contract_number, title, vendor_id, vendor_name,
				direction, contract_type, status, start_date, end_date, signed_date,
				value, currency, terms, description, auto_renewal, renewal_term_days,
				notes, responsible_employee_id, created_by, created_at, updated_at
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24)
		`, id, tenantID, orgIDPtr, number, input.Title, vendorID, vendorName,
			direction, contractType, entity.ContractStatusDraft, startDate, endDate, signedDate,
			input.Value, currency, terms, description, input.AutoRenewal, input.RenewalTermDays,
			notes, responsibleID, nullableUUID(userID), now, now)
		return err
	}

	contractNumber := strings.TrimSpace(input.ContractNumber)
	if contractNumber != "" {
		if err := insert(contractNumber); err != nil {
			if strings.Contains(err.Error(), "duplicate key") {
				response.Conflict(c, "A contract with this number already exists")
				return
			}
			h.log.Error("Failed to insert contract", "error", err)
			response.InternalError(c, "Failed to create contract")
			return
		}
	} else {
		// Auto-number with a small retry window against concurrent creates.
		inserted := false
		for attempt := 0; attempt < 3; attempt++ {
			contractNumber = h.nextContractNumber(tenantID)
			if err := insert(contractNumber); err != nil {
				if strings.Contains(err.Error(), "duplicate key") {
					continue
				}
				h.log.Error("Failed to insert contract", "error", err)
				response.InternalError(c, "Failed to create contract")
				return
			}
			inserted = true
			break
		}
		if !inserted {
			response.Conflict(c, "Could not allocate a contract number, please retry")
			return
		}
	}

	h.contractAudit(tenantID, userID, id, "create", nil, map[string]interface{}{
		"contract_number": contractNumber,
		"title":           input.Title,
		"vendor_name":     vendorName,
		"value":           input.Value,
		"direction":       direction,
	})

	h.EmitWorkflowEvent(tenantID, "contracts.created", map[string]interface{}{
		"record_id":       id.String(),
		"contract_number": contractNumber,
		"title":           input.Title,
		"contact_name":    vendorName,
		"value":           input.Value,
		"direction":       direction,
	})

	h.respondWithContract(c, tenantID, id, true)
}

// respondWithContract loads the shared projection for one contract and
// writes it as the response (created=true → 201).
func (h *Handler) respondWithContract(c *gin.Context, tenantID, id uuid.UUID, created bool) {
	row := h.db.QueryRow("SELECT "+contractSelectColumns+contractFromClause+
		" WHERE c.id = $1 AND c.tenant_id = $2 AND c.deleted_at IS NULL", id, tenantID)
	r, err := scanContractRow(row.Scan)
	if err != nil {
		h.log.Error("Failed to load contract after write", "error", err, "id", id)
		response.InternalError(c, "Failed to load contract")
		return
	}
	if created {
		response.Created(c, r)
		return
	}
	response.Success(c, r)
}

// GetContract returns one contract with rollups.
// GET /contracts/:id
func (h *Handler) GetContract(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid contract ID")
		return
	}

	row := h.db.QueryRow("SELECT "+contractSelectColumns+contractFromClause+
		" WHERE c.id = $1 AND c.tenant_id = $2 AND c.deleted_at IS NULL", id, tenantID)
	r, scanErr := scanContractRow(row.Scan)
	if scanErr == sql.ErrNoRows {
		response.NotFound(c, "Contract")
		return
	}
	if scanErr != nil {
		h.log.Error("Failed to get contract", "error", scanErr)
		response.InternalError(c, "Failed to get contract")
		return
	}
	response.Success(c, r)
}

// UpdateContract edits contract fields. Status is NOT accepted here —
// use POST /contracts/:id/status (validated transitions).
// PUT /contracts/:id
func (h *Handler) UpdateContract(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	userID, _ := middleware.GetUserID(c)
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid contract ID")
		return
	}

	var input entity.UpdateContractInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input")
		return
	}

	var exists bool
	if err := h.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM procurement_contracts WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL)`,
		id, tenantID).Scan(&exists); err != nil || !exists {
		response.NotFound(c, "Contract")
		return
	}

	updates := []string{}
	args := []interface{}{}
	changes := map[string]interface{}{}
	arg := func(field string, v interface{}) {
		args = append(args, v)
		updates = append(updates, fmt.Sprintf("%s = $%d", field, len(args)))
		changes[field] = v
	}

	if input.Direction != nil || input.ContractType != nil {
		d, t := "", ""
		if input.Direction != nil {
			d = *input.Direction
		}
		if input.ContractType != nil {
			t = *input.ContractType
		}
		if err := validateContractEnums(d, t); err != nil {
			response.BadRequest(c, err.Error())
			return
		}
	}

	if input.ContractNumber != nil && strings.TrimSpace(*input.ContractNumber) != "" {
		arg("contract_number", strings.TrimSpace(*input.ContractNumber))
	}
	if input.Title != nil && *input.Title != "" {
		arg("title", *input.Title)
	}
	if input.VendorID != nil && *input.VendorID != "" {
		vid, err := uuid.Parse(*input.VendorID)
		if err != nil {
			response.BadRequest(c, "Invalid counterparty ID")
			return
		}
		var vendorName string
		err = h.db.QueryRow(`SELECT name FROM contacts WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL`,
			vid, tenantID).Scan(&vendorName)
		if err == sql.ErrNoRows {
			response.NotFound(c, "Counterparty")
			return
		}
		if err != nil {
			response.InternalError(c, "Failed to update contract")
			return
		}
		arg("vendor_id", vid)
		arg("vendor_name", vendorName)
	}
	if input.Direction != nil && *input.Direction != "" {
		arg("direction", *input.Direction)
	}
	if input.ContractType != nil && *input.ContractType != "" {
		arg("contract_type", *input.ContractType)
	}
	if input.StartDate != nil && *input.StartDate != "" {
		sd, err := time.Parse("2006-01-02", *input.StartDate)
		if err != nil {
			response.BadRequest(c, "Invalid start date")
			return
		}
		arg("start_date", sd)
	}
	if input.EndDate != nil {
		if *input.EndDate == "" {
			updates = append(updates, "end_date = NULL")
			changes["end_date"] = nil
		} else {
			ed, err := time.Parse("2006-01-02", *input.EndDate)
			if err != nil {
				response.BadRequest(c, "Invalid end date")
				return
			}
			arg("end_date", ed)
		}
	}
	if input.SignedDate != nil {
		if *input.SignedDate == "" {
			updates = append(updates, "signed_date = NULL")
			changes["signed_date"] = nil
		} else {
			sd, err := time.Parse("2006-01-02", *input.SignedDate)
			if err != nil {
				response.BadRequest(c, "Invalid signed date")
				return
			}
			arg("signed_date", sd)
		}
	}
	if input.Value != nil {
		if *input.Value < 0 {
			response.BadRequest(c, "Value cannot be negative")
			return
		}
		arg("value", *input.Value)
	}
	if input.Currency != nil && *input.Currency != "" {
		arg("currency", *input.Currency)
	}
	if input.Terms != nil {
		arg("terms", *input.Terms)
	}
	if input.Description != nil {
		arg("description", *input.Description)
	}
	if input.AutoRenewal != nil {
		arg("auto_renewal", *input.AutoRenewal)
	}
	if input.RenewalTermDays != nil {
		arg("renewal_term_days", *input.RenewalTermDays)
	}
	if input.Notes != nil {
		arg("notes", *input.Notes)
	}
	if input.ResponsibleEmployeeID != nil {
		if *input.ResponsibleEmployeeID == "" {
			updates = append(updates, "responsible_employee_id = NULL")
			changes["responsible_employee_id"] = nil
		} else {
			rid, err := uuid.Parse(*input.ResponsibleEmployeeID)
			if err != nil {
				response.BadRequest(c, "Invalid responsible employee ID")
				return
			}
			var respExists bool
			if err := h.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM employees WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL)`,
				rid, tenantID).Scan(&respExists); err != nil || !respExists {
				response.NotFound(c, "Responsible employee")
				return
			}
			arg("responsible_employee_id", rid)
		}
	}

	if len(updates) == 0 {
		h.respondWithContract(c, tenantID, id, false)
		return
	}

	args = append(args, time.Now())
	updates = append(updates, fmt.Sprintf("updated_at = $%d", len(args)))
	args = append(args, id, tenantID)

	query := fmt.Sprintf("UPDATE procurement_contracts SET %s WHERE id = $%d AND tenant_id = $%d AND deleted_at IS NULL",
		strings.Join(updates, ", "), len(args)-1, len(args))
	if _, err := h.db.Exec(query, args...); err != nil {
		if strings.Contains(err.Error(), "duplicate key") {
			response.Conflict(c, "A contract with this number already exists")
			return
		}
		h.log.Error("Failed to update contract", "error", err)
		response.InternalError(c, "Failed to update contract")
		return
	}

	h.contractAudit(tenantID, userID, id, "update", nil, changes)
	h.respondWithContract(c, tenantID, id, false)
}

// changeContractStatus applies one validated transition and emits events.
func (h *Handler) changeContractStatus(c *gin.Context, target entity.ContractStatus) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	userID, _ := middleware.GetUserID(c)
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid contract ID")
		return
	}

	var currentStatus, contractNumber, vendorName string
	err = h.db.QueryRow(`
		SELECT c.status, c.contract_number, COALESCE(v.name, c.vendor_name, '')
		FROM procurement_contracts c
		LEFT JOIN contacts v ON v.id = c.vendor_id AND v.tenant_id = c.tenant_id
		WHERE c.id = $1 AND c.tenant_id = $2 AND c.deleted_at IS NULL
	`, id, tenantID).Scan(&currentStatus, &contractNumber, &vendorName)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Contract")
		return
	}
	if err != nil {
		h.log.Error("Failed to load contract status", "error", err)
		response.InternalError(c, "Failed to change status")
		return
	}

	from := entity.ContractStatus(currentStatus)
	if from == target {
		h.respondWithContract(c, tenantID, id, false)
		return
	}
	if !entity.CanTransition(from, target) {
		response.BadRequest(c, fmt.Sprintf("Cannot change status from '%s' to '%s'", from, target))
		return
	}

	now := time.Now()
	// Entering 'active' stamps signed_date if it was never recorded.
	if target == entity.ContractStatusActive {
		_, err = h.db.Exec(`
			UPDATE procurement_contracts
			SET status = $1, signed_date = COALESCE(signed_date, CURRENT_DATE), updated_at = $2
			WHERE id = $3 AND tenant_id = $4 AND deleted_at IS NULL
		`, target, now, id, tenantID)
	} else {
		_, err = h.db.Exec(`
			UPDATE procurement_contracts SET status = $1, updated_at = $2
			WHERE id = $3 AND tenant_id = $4 AND deleted_at IS NULL
		`, target, now, id, tenantID)
	}
	if err != nil {
		h.log.Error("Failed to change contract status", "error", err)
		response.InternalError(c, "Failed to change status")
		return
	}

	h.contractAudit(tenantID, userID, id, "status_change",
		map[string]interface{}{"status": from},
		map[string]interface{}{"status": target})

	h.EmitWorkflowEvent(tenantID, "contracts.status_changed", map[string]interface{}{
		"record_id":       id.String(),
		"contract_number": contractNumber,
		"contact_name":    vendorName,
		"old_status":      string(from),
		"new_status":      string(target),
	})

	h.respondWithContract(c, tenantID, id, false)
}

// ChangeContractStatus handles POST /contracts/:id/status {"status": "..."}.
func (h *Handler) ChangeContractStatus(c *gin.Context) {
	var input entity.ContractStatusInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input")
		return
	}
	target := entity.ContractStatus(input.Status)
	if _, known := entity.ContractTransitions[target]; !known {
		response.BadRequest(c, "Unknown status")
		return
	}
	h.changeContractStatus(c, target)
}

// ActivateContract is a legacy alias for transitioning to 'active'.
// POST /contracts/:id/activate
func (h *Handler) ActivateContract(c *gin.Context) {
	h.changeContractStatus(c, entity.ContractStatusActive)
}

// TerminateContract is a legacy alias for transitioning to 'cancelled'.
// POST /contracts/:id/terminate
func (h *Handler) TerminateContract(c *gin.Context) {
	h.changeContractStatus(c, entity.ContractStatusCancelled)
}

// ArchiveContract hides a contract from the registry without deleting it.
// POST /contracts/:id/archive
func (h *Handler) ArchiveContract(c *gin.Context) {
	h.setContractArchived(c, true)
}

// UnarchiveContract restores an archived contract.
// POST /contracts/:id/unarchive
func (h *Handler) UnarchiveContract(c *gin.Context) {
	h.setContractArchived(c, false)
}

func (h *Handler) setContractArchived(c *gin.Context, archived bool) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	userID, _ := middleware.GetUserID(c)
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid contract ID")
		return
	}

	var value interface{}
	action := "unarchive"
	if archived {
		value = time.Now()
		action = "archive"
	}
	res, err := h.db.Exec(`
		UPDATE procurement_contracts SET archived_at = $1, updated_at = $2
		WHERE id = $3 AND tenant_id = $4 AND deleted_at IS NULL
	`, value, time.Now(), id, tenantID)
	if err != nil {
		h.log.Error("Failed to archive contract", "error", err)
		response.InternalError(c, "Failed to archive contract")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		response.NotFound(c, "Contract")
		return
	}

	h.contractAudit(tenantID, userID, id, action, nil, nil)
	h.respondWithContract(c, tenantID, id, false)
}

// DeleteContract soft-deletes a contract. Only draft or cancelled
// contracts can be deleted; anything that was in force must be archived
// instead so its history and financial links survive.
// DELETE /contracts/:id
func (h *Handler) DeleteContract(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	userID, _ := middleware.GetUserID(c)
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid contract ID")
		return
	}

	var currentStatus string
	err = h.db.QueryRow(`SELECT status FROM procurement_contracts WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL`,
		id, tenantID).Scan(&currentStatus)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Contract")
		return
	}
	if err != nil {
		h.log.Error("Failed to check contract", "error", err)
		response.InternalError(c, "Failed to delete contract")
		return
	}

	if currentStatus != string(entity.ContractStatusDraft) && currentStatus != string(entity.ContractStatusCancelled) {
		response.BadRequest(c, "Only draft or cancelled contracts can be deleted — archive instead")
		return
	}

	now := time.Now()
	if _, err := h.db.Exec(`UPDATE procurement_contracts SET deleted_at = $1, updated_at = $1 WHERE id = $2 AND tenant_id = $3`,
		now, id, tenantID); err != nil {
		h.log.Error("Failed to delete contract", "error", err)
		response.InternalError(c, "Failed to delete contract")
		return
	}

	h.contractAudit(tenantID, userID, id, "delete", nil, nil)
	response.Success(c, gin.H{"message": "Contract deleted successfully"})
}
