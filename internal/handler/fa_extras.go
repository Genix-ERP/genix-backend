package handler

import (
	"database/sql"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ============================================================================
// Aktivlar — dashboard stats, GL reconciliation, PO capitalization, entry
// history, employee-held assets. Added by the 2026-08-03 rebuild
// (docs/aktivlar-audit.md §13).
// ============================================================================

// GetAssetStats powers the registry dashboard strip and the NBV trend chart.
// GET /api/v1/assets/stats
func (h *Handler) GetAssetStats(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		faErr(c, http.StatusUnauthorized, "UNAUTHORIZED", "Tenant topilmadi", "Тенант не найден")
		return
	}

	type statusRow struct {
		Status    string  `json:"status"`
		Count     int     `json:"count"`
		Cost      float64 `json:"cost"`
		BookValue float64 `json:"book_value"`
	}
	byStatus := []statusRow{}
	var totalCount int
	var totalCost, totalBook float64
	rows, err := h.db.Query(`
		SELECT status::text, COUNT(*), COALESCE(SUM(cost),0), COALESCE(SUM(cost - accumulated_depreciation),0)
		FROM fa_assets WHERE tenant_id=$1 AND deleted_at IS NULL
		GROUP BY status ORDER BY status`, tenantID)
	if err != nil {
		faErr(c, http.StatusInternalServerError, "QUERY_FAILED", "Xatolik", "Ошибка")
		return
	}
	for rows.Next() {
		var r statusRow
		if rows.Scan(&r.Status, &r.Count, &r.Cost, &r.BookValue) == nil {
			byStatus = append(byStatus, r)
			totalCount += r.Count
			totalCost += r.Cost
			if r.Status != "disposed" {
				totalBook += r.BookValue
			}
		}
	}
	rows.Close()

	// Depreciation posted for the current month (runs + disposal top-ups).
	thisPeriod := time.Now().Format("2006-01")
	var monthDepr float64
	h.db.QueryRow(`
		SELECT COALESCE(SUM(e.amount),0) FROM fa_depreciation_entries e
		WHERE e.tenant_id=$1 AND e.period=$2 AND e.status='active' AND e.journal_entry_id IS NOT NULL`,
		tenantID, thisPeriod).Scan(&monthDepr)

	// Fully depreciated (book value at salvage floor) among non-disposed assets.
	var fullyDepr int
	h.db.QueryRow(`
		SELECT COUNT(*) FROM fa_assets
		WHERE tenant_id=$1 AND deleted_at IS NULL AND status IN ('in_service','conserved')
		  AND accumulated_depreciation >= cost - salvage_value - 0.005`, tenantID).Scan(&fullyDepr)

	// 12-month net-book-value trend: for each month-end, cost of assets already
	// commissioned and not yet disposed minus their active accruals up to and
	// including that month. Period strings compare lexically (YYYY-MM).
	type trendRow struct {
		Period string  `json:"period"`
		NBV    float64 `json:"nbv"`
	}
	trend := []trendRow{}
	trows, err := h.db.Query(`
		WITH months AS (
			SELECT to_char(date_trunc('month', NOW()) - (interval '1 month' * g), 'YYYY-MM') AS period
			FROM generate_series(11, 0, -1) AS g
		)
		SELECT m.period,
		       COALESCE((SELECT SUM(a.cost) FROM fa_assets a
		                 WHERE a.tenant_id=$1 AND a.deleted_at IS NULL AND a.commissioning_date IS NOT NULL
		                   AND to_char(a.commissioning_date,'YYYY-MM') <= m.period
		                   AND (a.disposal_date IS NULL OR to_char(a.disposal_date,'YYYY-MM') > m.period)), 0)
		     - COALESCE((SELECT SUM(e.amount) FROM fa_depreciation_entries e
		                 JOIN fa_assets a2 ON a2.id = e.asset_id
		                 WHERE e.tenant_id=$1 AND e.status='active' AND e.period <= m.period
		                   AND a2.deleted_at IS NULL
		                   AND (a2.disposal_date IS NULL OR to_char(a2.disposal_date,'YYYY-MM') > m.period)), 0)
		FROM months m ORDER BY m.period`, tenantID)
	if err == nil {
		for trows.Next() {
			var t trendRow
			if trows.Scan(&t.Period, &t.NBV) == nil {
				trend = append(trend, t)
			}
		}
		trows.Close()
	}

	faOK(c, gin.H{
		"total_count":        totalCount,
		"total_cost":         totalCost,
		"total_book_value":   totalBook,
		"month_depreciation": monthDepr,
		"month_period":       thisPeriod,
		"fully_depreciated":  fullyDepr,
		"by_status":          byStatus,
		"nbv_trend":          trend,
	})
}

// ReconcileAssets compares the asset register with the GL, per account code:
// commissioned cost vs the 01xx balances and accumulated depreciation vs the
// 02xx balances (debit-positive convention: a contra-asset balance is
// negative, so the register value is compared against −balance). Mismatches
// are surfaced, not hidden — construction capitalizations that bypassed the
// register show up here (audit finding #10/#11).
// GET /api/v1/assets/reconcile
func (h *Handler) ReconcileAssets(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		faErr(c, http.StatusUnauthorized, "UNAUTHORIZED", "Tenant topilmadi", "Тенант не найден")
		return
	}

	type recRow struct {
		Kind        string  `json:"kind"` // 'asset' | 'depreciation'
		AccountCode string  `json:"account_code"`
		AccountName string  `json:"account_name"`
		Register    float64 `json:"register_value"`
		GLBalance   float64 `json:"gl_balance"`
		Diff        float64 `json:"diff"`
	}
	out := []recRow{}

	// Register side, grouped by effective account. Commissioned, not disposed:
	// draft assets still sit on the acquisition account, disposed left the GL.
	collect := func(kind, registerSQL string) {
		rows, err := h.db.Query(registerSQL, tenantID)
		if err != nil {
			return
		}
		defer rows.Close()
		for rows.Next() {
			var r recRow
			if rows.Scan(&r.AccountCode, &r.Register) != nil {
				continue
			}
			r.Kind = kind
			var bal sql.NullFloat64
			var name sql.NullString
			h.db.QueryRow(`SELECT COALESCE(SUM(current_balance),0), MIN(name) FROM accounts
				WHERE tenant_id=$1 AND code=$2 AND deleted_at IS NULL`, tenantID, r.AccountCode).Scan(&bal, &name)
			r.GLBalance = bal.Float64
			r.AccountName = name.String
			if kind == "depreciation" {
				r.Diff = faRound(r.Register-(-r.GLBalance), 2)
			} else {
				r.Diff = faRound(r.Register-r.GLBalance, 2)
			}
			out = append(out, r)
		}
	}

	collect("asset", `
		SELECT COALESCE(NULLIF(TRIM(a.asset_account_override), ''), c.asset_account) AS code, SUM(a.cost)
		FROM fa_assets a JOIN fa_categories c ON c.id = a.category_id
		WHERE a.tenant_id=$1 AND a.deleted_at IS NULL AND a.status IN ('in_service','conserved')
		GROUP BY 1 ORDER BY 1`)
	collect("depreciation", `
		SELECT COALESCE(NULLIF(TRIM(a.depreciation_account_override), ''), c.depreciation_account) AS code, SUM(a.accumulated_depreciation)
		FROM fa_assets a JOIN fa_categories c ON c.id = a.category_id
		WHERE a.tenant_id=$1 AND a.deleted_at IS NULL AND a.status IN ('in_service','conserved')
		  AND c.depreciation_account IS NOT NULL
		GROUP BY 1 ORDER BY 1`)

	mismatches := 0
	for _, r := range out {
		if r.Diff > 0.01 || r.Diff < -0.01 {
			mismatches++
		}
	}
	faOK(c, gin.H{"rows": out, "mismatch_count": mismatches})
}

type createAssetFromPOInput struct {
	PurchaseOrderID  string  `json:"purchase_order_id"`
	LineID           string  `json:"line_id"` // optional: prefill from one line
	Name             string  `json:"name"`
	CategoryID       string  `json:"category_id"`
	DepartmentID     string  `json:"department_id"`
	Cost             float64 `json:"cost"` // default: line_total of the chosen line
	SalvageValue     float64 `json:"salvage_value"`
	UsefulLifeMonths int     `json:"useful_life_months"`
}

// CreateAssetFromPO capitalizes equipment that was bought through Xarid: it
// creates a DRAFT asset linked to the purchase order and posts a
// RECLASSIFICATION (Дт acquisition Кт inventory) instead of the normal
// purchase entry — the PO/goods-receipt already booked Дт inventory Кт AP, so
// posting the purchase again would double the supplier debt (audit finding
// #10). Commissioning then proceeds as usual.
// POST /api/v1/assets/from-po
func (h *Handler) CreateAssetFromPO(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		faErr(c, http.StatusUnauthorized, "UNAUTHORIZED", "Tenant topilmadi", "Тенант не найден")
		return
	}
	userID, _ := middleware.GetUserID(c)
	orgID, _ := middleware.GetOrganizationID(c)
	var orgPtr *uuid.UUID
	if orgID != uuid.Nil {
		orgPtr = &orgID
	}

	var in createAssetFromPOInput
	if err := c.ShouldBindJSON(&in); err != nil {
		faErr(c, http.StatusBadRequest, "INVALID_INPUT", "Ma'lumot noto'g'ri", "Некорректные данные")
		return
	}
	poID, err := uuid.Parse(in.PurchaseOrderID)
	if err != nil {
		faErr(c, http.StatusBadRequest, "INVALID_INPUT", "PO ID noto'g'ri", "Некорректный ID заказа")
		return
	}
	catID, err := uuid.Parse(in.CategoryID)
	if err != nil {
		faErr(c, http.StatusBadRequest, "MISSING_FIELD", "Kategoriya majburiy", "Категория обязательна")
		return
	}
	deptID, err := uuid.Parse(in.DepartmentID)
	if err != nil {
		faErr(c, http.StatusBadRequest, "MISSING_FIELD", "Bo'lim majburiy", "Подразделение обязательно")
		return
	}
	if in.UsefulLifeMonths <= 0 {
		faErr(c, http.StatusBadRequest, "INVALID_LIFE", "Foydali xizmat muddati noto'g'ri", "Некорректный срок службы")
		return
	}

	var orderNumber string
	var vendorID uuid.UUID
	var orderDate time.Time
	var poStatus string
	if err := h.db.QueryRow(`SELECT order_number, vendor_id, order_date, status FROM purchase_orders
		WHERE id=$1 AND tenant_id=$2 AND deleted_at IS NULL`, poID, tenantID).
		Scan(&orderNumber, &vendorID, &orderDate, &poStatus); err != nil {
		faErr(c, http.StatusNotFound, "NOT_FOUND", "Buyurtma topilmadi", "Заказ не найден")
		return
	}
	if poStatus == "cancelled" || poStatus == "draft" {
		faErr(c, http.StatusBadRequest, "INVALID_STATUS", "Faqat tasdiqlangan/qabul qilingan buyurtmadan aktiv yaratiladi", "Актив создаётся только из подтверждённого заказа")
		return
	}

	name := strings.TrimSpace(in.Name)
	cost := in.Cost
	if in.LineID != "" {
		if lineID, e := uuid.Parse(in.LineID); e == nil {
			var lineTotal float64
			var desc, productName sql.NullString
			if h.db.QueryRow(`
				SELECT l.line_total, l.description, p.name
				FROM purchase_order_lines l
				LEFT JOIN products p ON p.id = l.product_id
				WHERE l.id=$1 AND l.purchase_order_id=$2`, lineID, poID).
				Scan(&lineTotal, &desc, &productName) == nil {
				if cost <= 0 {
					cost = lineTotal
				}
				if name == "" {
					name = coalesceStr(desc, productName)
				}
			}
		}
	}
	if name == "" || cost <= 0 {
		faErr(c, http.StatusBadRequest, "MISSING_FIELD", "Nomi va qiymati majburiy", "Название и стоимость обязательны")
		return
	}
	if in.SalvageValue < 0 || in.SalvageValue >= cost {
		faErr(c, http.StatusBadRequest, "INVALID_SALVAGE", "Likvidatsiya qiymati noto'g'ri", "Некорректная ликвидационная стоимость")
		return
	}

	// Category/department mapping must be configured (same rule as CreateAsset).
	var catAsset string
	var catDepr sql.NullString
	var catDepreciable bool
	if err := h.db.QueryRow(`SELECT asset_account, depreciation_account, depreciable FROM fa_categories WHERE id=$1 AND tenant_id=$2`,
		catID, tenantID).Scan(&catAsset, &catDepr, &catDepreciable); err != nil {
		faErr(c, http.StatusBadRequest, "MAPPING_MISSING", "Kategoriya uchun hisoblar sozlanmagan", "Счета для категории не настроены")
		return
	}
	var deptExpense string
	if err := h.db.QueryRow(`SELECT expense_account FROM fa_departments WHERE id=$1 AND tenant_id=$2`,
		deptID, tenantID).Scan(&deptExpense); err != nil {
		faErr(c, http.StatusBadRequest, "MAPPING_MISSING", "Bo'lim uchun hisob sozlanmagan", "Счёт для подразделения не настроен")
		return
	}

	// The credit side of the reclass: the first existing leaf inventory account
	// from the preference list the goods-receipt flow posts to.
	var stockCode string
	h.db.QueryRow(`
		SELECT code FROM accounts
		WHERE tenant_id=$1 AND deleted_at IS NULL AND COALESCE(is_active,true)=true AND COALESCE(is_leaf,true)=true
		  AND code IN ('1010','1030','1310','1330','1340','2910')
		ORDER BY array_position(ARRAY['1010','1030','1310','1330','1340','2910'], code)
		LIMIT 1`, tenantID).Scan(&stockCode)
	if stockCode == "" {
		faErr(c, http.StatusBadRequest, "MAPPING_MISSING", "Ombor hisobi topilmadi (1010/1030/...)", "Счёт запасов не найден")
		return
	}

	tx, err := h.db.Begin()
	if err != nil {
		faErr(c, http.StatusInternalServerError, "TX_FAILED", "Xatolik", "Ошибка")
		return
	}
	defer tx.Rollback()

	inv, err := h.nextInventoryNumber(tx, tenantID)
	if err != nil {
		faErr(c, http.StatusInternalServerError, "INV_FAILED", "Inventar raqamini yaratib bo'lmadi", "Не удалось создать инв. номер")
		return
	}
	assetID := uuid.New()
	if _, err = tx.Exec(`
		INSERT INTO fa_assets (
			id, tenant_id, organization_id, inventory_number, name, category_id, department_id,
			purchase_date, cost, salvage_value, useful_life_months, method, status,
			supplier_id, doc_number, purchase_order_id, created_by, created_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,'straight_line','draft',$12,$13,$14,$15,NOW(),NOW())`,
		assetID, tenantID, orgPtr, inv, name, catID, deptID,
		orderDate, cost, in.SalvageValue, in.UsefulLifeMonths, vendorID, orderNumber, poID, userID); err != nil {
		faErr(c, http.StatusInternalServerError, "SAVE_FAILED", "Aktivni saqlab bo'lmadi", "Не удалось сохранить актив")
		return
	}

	acc := h.lifecycleAccounts(tenantID)
	journalID, nextNum, prefix := faGetJournal(tx, tenantID)
	if _, e := h.faPostJournal(tx, tenantID, orgPtr, journalID,
		fmt.Sprintf("%s%06d", prefix, nextNum), inv+"-CAP", "Kapitalizatsiya (PO "+orderNumber+"): "+name, "asset_capitalization", assetID, time.Now(),
		[]faJELine{{Code: acc.Acquisition, Debit: cost, Desc: "Kapital qo'yilma (ombordan)"}, {Code: stockCode, Credit: cost, Desc: "Ombordan chiqarish"}}); e != nil {
		faErr(c, http.StatusBadRequest, "POSTING_FAILED", "Reklassifikatsiya: "+e.Error(), "Реклассификация: "+e.Error())
		return
	}
	nextNum++
	faBumpJournalNumber(tx, journalID, nextNum)
	if err := tx.Commit(); err != nil {
		faErr(c, http.StatusInternalServerError, "TX_FAILED", "Xatolik", "Ошибка")
		return
	}
	faOK(c, gin.H{"id": assetID, "inventory_number": inv, "status": "draft", "stock_account": stockCode,
		"message": "Aktiv yaratildi (ombordan kapitallashtirildi). Endi foydalanishga topshiring."})
}

// GetAssetEntries returns an asset's depreciation history (runs + disposal
// top-ups + the migrated opening entry).
// GET /api/v1/assets/:id/entries
func (h *Handler) GetAssetEntries(c *gin.Context) {
	tenantID, _ := middleware.GetTenantID(c)
	assetID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		faErr(c, http.StatusBadRequest, "INVALID_INPUT", "ID noto'g'ri", "Некорректный ID")
		return
	}
	type row struct {
		ID             string    `json:"id"`
		Period         string    `json:"period"`
		Amount         float64   `json:"amount"`
		Status         string    `json:"status"`
		DebitAccount   string    `json:"debit_account"`
		CreditAccount  string    `json:"credit_account"`
		RunID          *string   `json:"run_id"`
		JournalEntryID *string   `json:"journal_entry_id"`
		CreatedAt      time.Time `json:"created_at"`
	}
	out := []row{}
	rows, err := h.db.Query(`
		SELECT e.id, e.period, e.amount, e.status, e.debit_account, e.credit_account,
		       e.run_id::text, e.journal_entry_id::text, e.created_at
		FROM fa_depreciation_entries e
		JOIN fa_assets a ON a.id = e.asset_id
		WHERE e.asset_id=$1 AND e.tenant_id=$2 AND a.tenant_id=$2
		ORDER BY e.period, e.created_at`, assetID, tenantID)
	if err != nil {
		faErr(c, http.StatusInternalServerError, "QUERY_FAILED", "Xatolik", "Ошибка")
		return
	}
	defer rows.Close()
	for rows.Next() {
		var r row
		var runID, jeID sql.NullString
		if rows.Scan(&r.ID, &r.Period, &r.Amount, &r.Status, &r.DebitAccount, &r.CreditAccount, &runID, &jeID, &r.CreatedAt) == nil {
			if runID.Valid {
				r.RunID = &runID.String
			}
			if jeID.Valid {
				r.JournalEntryID = &jeID.String
			}
			out = append(out, r)
		}
	}
	faOK(c, out)
}

type recordMaintenanceInput struct {
	MaintenanceType     string  `json:"maintenance_type"` // regular_to|minor_repair|capital_repair|modernization
	ServiceDate         string  `json:"service_date"`
	Cost                float64 `json:"cost"`
	PaymentMethod       string  `json:"payment_method"` // cash|bank|credit
	LifeExtensionMonths int     `json:"life_extension_months"`
	PerformedBy         string  `json:"performed_by"`
	DocNumber           string  `json:"doc_number"`
	Description         string  `json:"description"`
	NextServiceDate     string  `json:"next_service_date"`
}

// RecordAssetMaintenance books maintenance on a v2 asset (457):
//
//	regular_to/minor_repair      Дт dept expense   Кт 6010|5010|5110
//	capital_repair/modernization Дт asset account  Кт 6010|5010|5110,
//	                             cost += amount, life += extension (prospective,
//	                             same semantics as /change-params)
//
// POST /api/v1/assets/:id/maintenance
func (h *Handler) RecordAssetMaintenance(c *gin.Context) {
	tenantID, _ := middleware.GetTenantID(c)
	userID, _ := middleware.GetUserID(c)
	orgID, _ := middleware.GetOrganizationID(c)
	var orgPtr *uuid.UUID
	if orgID != uuid.Nil {
		orgPtr = &orgID
	}
	assetID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		faErr(c, http.StatusBadRequest, "INVALID_INPUT", "ID noto'g'ri", "Некорректный ID")
		return
	}
	var in recordMaintenanceInput
	if err := c.ShouldBindJSON(&in); err != nil {
		faErr(c, http.StatusBadRequest, "INVALID_INPUT", "Ma'lumot noto'g'ri", "Некорректные данные")
		return
	}
	capitalizing := in.MaintenanceType == "capital_repair" || in.MaintenanceType == "modernization"
	switch in.MaintenanceType {
	case "regular_to", "minor_repair", "capital_repair", "modernization":
	default:
		faErr(c, http.StatusBadRequest, "INVALID_TYPE", "Xizmat turi noto'g'ri", "Некорректный тип обслуживания")
		return
	}
	if in.Cost < 0 || (in.Cost == 0 && capitalizing) {
		faErr(c, http.StatusBadRequest, "INVALID_COST", "Summa noto'g'ri", "Некорректная сумма")
		return
	}
	if in.LifeExtensionMonths < 0 || (in.LifeExtensionMonths > 0 && !capitalizing) {
		faErr(c, http.StatusBadRequest, "INVALID_LIFE", "Muddat uzayishi faqat kapital ta'mir/modernizatsiyada", "Продление срока — только при капремонте/модернизации")
		return
	}
	serviceDate, err := time.Parse("2006-01-02", in.ServiceDate)
	if err != nil {
		faErr(c, http.StatusBadRequest, "INVALID_DATE", "Xizmat sanasi noto'g'ri", "Некорректная дата")
		return
	}
	if msg := h.checkPeriodLock(tenantID, serviceDate); msg != "" {
		faErr(c, http.StatusBadRequest, "PERIOD_CLOSED", msg, msg)
		return
	}

	var name, status string
	var cost float64
	var life int
	if err := h.db.QueryRow(`SELECT name, status::text, cost, useful_life_months FROM fa_assets
		WHERE id=$1 AND tenant_id=$2 AND deleted_at IS NULL`, assetID, tenantID).
		Scan(&name, &status, &cost, &life); err != nil {
		faErr(c, http.StatusNotFound, "NOT_FOUND", "Aktiv topilmadi", "Актив не найден")
		return
	}
	if status == "disposed" || status == "draft" {
		faErr(c, http.StatusBadRequest, "INVALID_STATUS", "Faqat foydalanishdagi/konservatsiyadagi aktivga xizmat kiritiladi", "Обслуживание — только для действующих активов")
		return
	}
	eff, err := h.effectiveAccountsForAsset(tenantID, assetID)
	if err != nil || (capitalizing && eff.AssetCode == "") || (!capitalizing && eff.ExpenseCode == "") {
		faErr(c, http.StatusBadRequest, "MAPPING_MISSING", "Hisoblar sozlanmagan", "Счета не настроены")
		return
	}

	acc := h.lifecycleAccounts(tenantID)
	creditCode := acc.AP
	if in.PaymentMethod == "cash" {
		creditCode = acc.Cash
	} else if in.PaymentMethod == "bank" {
		creditCode = acc.Bank
	}
	debitCode := eff.ExpenseCode
	if capitalizing {
		debitCode = eff.AssetCode
	}

	typeLabel := map[string]string{
		"regular_to": "Texnik xizmat", "minor_repair": "Joriy ta'mirlash",
		"capital_repair": "Kapital ta'mirlash", "modernization": "Modernizatsiya",
	}[in.MaintenanceType]

	tx, err := h.db.Begin()
	if err != nil {
		faErr(c, http.StatusInternalServerError, "TX_FAILED", "Xatolik", "Ошибка")
		return
	}
	defer tx.Rollback()

	var jeID uuid.UUID
	if in.Cost > 0 {
		journalID, nextNum, prefix := faGetJournal(tx, tenantID)
		jeID, err = h.faPostJournal(tx, tenantID, orgPtr, journalID,
			fmt.Sprintf("%s%06d", prefix, nextNum), "MAINT", typeLabel+": "+name, "asset_maintenance", assetID, serviceDate,
			[]faJELine{{Code: debitCode, Debit: in.Cost, Desc: typeLabel}, {Code: creditCode, Credit: in.Cost, Desc: "To'lov/qarz"}})
		if err != nil {
			faErr(c, http.StatusBadRequest, "POSTING_FAILED", typeLabel+": "+err.Error(), typeLabel+": "+err.Error())
			return
		}
		faBumpJournalNumber(tx, journalID, nextNum+1)
	}

	newCost, newLife := cost, life
	if capitalizing {
		newCost = faRound(cost+in.Cost, 2)
		newLife = life + in.LifeExtensionMonths
		if _, err := tx.Exec(`UPDATE fa_assets SET cost=$1, useful_life_months=$2, updated_at=NOW() WHERE id=$3 AND tenant_id=$4`,
			newCost, newLife, assetID, tenantID); err != nil {
			faErr(c, http.StatusInternalServerError, "SAVE_FAILED", "Xatolik", "Ошибка")
			return
		}
	}

	var jePtr interface{}
	if jeID != uuid.Nil {
		jePtr = jeID
	}
	mID := uuid.New()
	if _, err := tx.Exec(`
		INSERT INTO fa_maintenance (id, tenant_id, asset_id, maintenance_type, service_date, cost, payment_method,
			life_extension_months, cost_before, cost_after, life_before, life_after,
			performed_by, doc_number, description, next_service_date, journal_entry_id, created_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)`,
		mID, tenantID, assetID, in.MaintenanceType, serviceDate, in.Cost, coalesceDefault(in.PaymentMethod, "credit"),
		in.LifeExtensionMonths, cost, newCost, life, newLife,
		nullIfEmpty(in.PerformedBy), nullIfEmpty(in.DocNumber), nullIfEmpty(in.Description),
		parseDatePtr(in.NextServiceDate), jePtr, userID); err != nil {
		faErr(c, http.StatusInternalServerError, "SAVE_FAILED", "Xizmat yozuvi saqlanmadi", "Запись не сохранена")
		return
	}
	if err := tx.Commit(); err != nil {
		faErr(c, http.StatusInternalServerError, "TX_FAILED", "Xatolik", "Ошибка")
		return
	}
	h.faAudit(tenantID, userID, "fa_asset", assetID, "maintenance",
		map[string]interface{}{"cost": cost, "useful_life_months": life},
		map[string]interface{}{"type": in.MaintenanceType, "amount": in.Cost, "cost": newCost, "useful_life_months": newLife})
	faOK(c, gin.H{"id": mID, "cost_after": newCost, "life_after": newLife})
}

// ListAssetMaintenance — GET /api/v1/assets/:id/maintenance
func (h *Handler) ListAssetMaintenance(c *gin.Context) {
	tenantID, _ := middleware.GetTenantID(c)
	assetID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		faErr(c, http.StatusBadRequest, "INVALID_INPUT", "ID noto'g'ri", "Некорректный ID")
		return
	}
	type row struct {
		ID              string     `json:"id"`
		Type            string     `json:"maintenance_type"`
		ServiceDate     time.Time  `json:"service_date"`
		Cost            float64    `json:"cost"`
		LifeExtension   int        `json:"life_extension_months"`
		CostAfter       *float64   `json:"cost_after"`
		LifeAfter       *int       `json:"life_after"`
		PerformedBy     string     `json:"performed_by"`
		Description     string     `json:"description"`
		NextServiceDate *time.Time `json:"next_service_date"`
		JournalEntryID  *string    `json:"journal_entry_id"`
	}
	out := []row{}
	rows, err := h.db.Query(`
		SELECT id, maintenance_type, service_date, cost, life_extension_months,
		       cost_after, life_after, COALESCE(performed_by,''), COALESCE(description,''),
		       next_service_date, journal_entry_id::text
		FROM fa_maintenance WHERE asset_id=$1 AND tenant_id=$2
		ORDER BY service_date DESC, created_at DESC`, assetID, tenantID)
	if err != nil {
		faErr(c, http.StatusInternalServerError, "QUERY_FAILED", "Xatolik", "Ошибка")
		return
	}
	defer rows.Close()
	for rows.Next() {
		var r row
		var costAfter sql.NullFloat64
		var lifeAfter sql.NullInt64
		var nextDate sql.NullTime
		var jeID sql.NullString
		if rows.Scan(&r.ID, &r.Type, &r.ServiceDate, &r.Cost, &r.LifeExtension,
			&costAfter, &lifeAfter, &r.PerformedBy, &r.Description, &nextDate, &jeID) == nil {
			if costAfter.Valid {
				r.CostAfter = &costAfter.Float64
			}
			if lifeAfter.Valid {
				v := int(lifeAfter.Int64)
				r.LifeAfter = &v
			}
			if nextDate.Valid {
				r.NextServiceDate = &nextDate.Time
			}
			if jeID.Valid {
				r.JournalEntryID = &jeID.String
			}
			out = append(out, r)
		}
	}
	faOK(c, out)
}

func coalesceDefault(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}

// faRegisterConstructionAsset creates the register row for a capitalized
// construction project — WITHOUT posting (the construction module already
// booked Дт fixed-assets / Кт 0810; audit finding #10: the GL was inflated
// while the register never learned about the building and it was never
// depreciated). The asset lands as in_service with the GL account it was
// actually posted to as an override, so /assets/reconcile stays truthful.
// Idempotent via doc_number = 'QUR-<projectID>'.
func (h *Handler) faRegisterConstructionAsset(tenantID uuid.UUID, orgPtr *uuid.UUID, projectID int64, cost float64, commissionDate time.Time, glAccountID uuid.UUID) {
	if cost <= 0 {
		return
	}
	docNumber := fmt.Sprintf("QUR-%d", projectID)
	var exists bool
	h.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM fa_assets WHERE tenant_id=$1 AND doc_number=$2 AND deleted_at IS NULL)`,
		tenantID, docNumber).Scan(&exists)
	if exists {
		return
	}

	var projectName string
	if h.db.QueryRow(`SELECT COALESCE(name,'') FROM construction_projects WHERE id=$1 AND tenant_id=$2`,
		projectID, tenantID).Scan(&projectName) != nil || projectName == "" {
		projectName = docNumber
	}

	var catID, deptID uuid.UUID
	var catAsset string
	var defaultLife sql.NullInt64
	if h.db.QueryRow(`SELECT id, asset_account, default_useful_life_months FROM fa_categories
		WHERE tenant_id=$1 AND code='buildings'`, tenantID).Scan(&catID, &catAsset, &defaultLife) != nil {
		h.log.Error("fa: buildings category missing, construction asset not registered", "project_id", projectID)
		return
	}
	if h.db.QueryRow(`SELECT id FROM fa_departments WHERE tenant_id=$1 AND code='production'`, tenantID).Scan(&deptID) != nil {
		if h.db.QueryRow(`SELECT id FROM fa_departments WHERE tenant_id=$1 ORDER BY code LIMIT 1`, tenantID).Scan(&deptID) != nil {
			h.log.Error("fa: no departments, construction asset not registered", "project_id", projectID)
			return
		}
	}
	life := 240
	if defaultLife.Valid && defaultLife.Int64 > 0 {
		life = int(defaultLife.Int64)
	}
	// Match the GL account construction actually debited; override only when it
	// differs from the category default.
	var override interface{}
	var glCode string
	if h.db.QueryRow(`SELECT code FROM accounts WHERE id=$1`, glAccountID).Scan(&glCode) == nil &&
		glCode != "" && glCode != catAsset {
		override = glCode
	}

	tx, err := h.db.Begin()
	if err != nil {
		return
	}
	defer tx.Rollback()
	inv, err := h.nextInventoryNumber(tx, tenantID)
	if err != nil {
		return
	}
	assetID := uuid.New()
	if _, err := tx.Exec(`
		INSERT INTO fa_assets (
			id, tenant_id, organization_id, inventory_number, name, category_id, department_id,
			purchase_date, commissioning_date, cost, salvage_value, useful_life_months, method, status,
			doc_number, construction_object_id, asset_account_override, notes, created_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$8,$9,0,$10,'straight_line','in_service',$11,$12,$13,$14,NOW(),NOW())`,
		assetID, tenantID, orgPtr, inv, projectName, catID, deptID,
		commissionDate, cost, life, docNumber, projectID, override,
		"Qurilish loyihasidan avtomatik kapitallashtirildi"); err != nil {
		h.log.Error("fa: construction asset insert failed", "project_id", projectID, "error", err)
		return
	}
	if err := tx.Commit(); err != nil {
		return
	}
	h.EmitWorkflowEvent(tenantID, "assets.commissioned", map[string]interface{}{
		"record_id": assetID.String(), "inventory_number": inv, "asset_name": projectName, "cost": cost,
	})
	h.log.Info("fa: construction project registered as asset", "project_id", projectID, "asset_id", assetID, "cost", cost)
}

// ListEmployeeAssets returns the non-disposed assets held by one employee —
// the moddiy javobgarlik list shown on the HR profile and in the dismissal
// banner (same shape as ListEmployeeTasks).
// GET /api/v1/employees/:id/assets
func (h *Handler) ListEmployeeAssets(c *gin.Context) {
	tenantID, _ := middleware.GetTenantID(c)
	empID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		faErr(c, http.StatusBadRequest, "INVALID_INPUT", "ID noto'g'ri", "Некорректный ID")
		return
	}
	type row struct {
		ID                string     `json:"id"`
		InventoryNumber   string     `json:"inventory_number"`
		Name              string     `json:"name"`
		Status            string     `json:"status"`
		BookValue         float64    `json:"book_value"`
		CommissioningDate *time.Time `json:"commissioning_date"`
	}
	out := []row{}
	rows, err := h.db.Query(`
		SELECT id, inventory_number, name, status::text, cost - accumulated_depreciation, commissioning_date
		FROM fa_assets
		WHERE tenant_id=$1 AND assigned_employee_id=$2 AND deleted_at IS NULL AND status <> 'disposed'
		ORDER BY inventory_number`, tenantID, empID)
	if err != nil {
		faErr(c, http.StatusInternalServerError, "QUERY_FAILED", "Xatolik", "Ошибка")
		return
	}
	defer rows.Close()
	for rows.Next() {
		var r row
		var comm sql.NullTime
		if rows.Scan(&r.ID, &r.InventoryNumber, &r.Name, &r.Status, &r.BookValue, &comm) == nil {
			if comm.Valid {
				r.CommissioningDate = &comm.Time
			}
			out = append(out, r)
		}
	}
	faOK(c, out)
}
