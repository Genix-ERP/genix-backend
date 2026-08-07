package handler

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ============================================================================
// Fixed Assets v1 — asset lifecycle + lifecycle postings (§4, §5, §9).
// ============================================================================

// Lifecycle posting accounts were hardcoded here (0820/6010/5010/5110/4410/9210)
// until migration 453 moved them into fa_settings — see lifecycleAccounts().

type createAssetInput struct {
	Name                 string  `json:"name"`
	CategoryID           string  `json:"category_id"`
	DepartmentID         string  `json:"department_id"`
	SerialNumber         string  `json:"serial_number"`
	Location             string  `json:"location"`
	PurchaseDate         string  `json:"purchase_date"` // YYYY-MM-DD
	Cost                 float64 `json:"cost"`
	SalvageValue         float64 `json:"salvage_value"`
	UsefulLifeMonths     int     `json:"useful_life_months"`
	Method               string  `json:"method"`
	VatAmount            float64 `json:"vat_amount"`
	SupplierID           string  `json:"supplier_id"`
	PaymentMethod        string  `json:"payment_method"` // 'cash' | 'credit'
	DocNumber            string  `json:"doc_number"`
	DocDate              string  `json:"doc_date"`
	CommissionNow        *bool   `json:"commission_now"`     // default true (§2.3 "darhol")
	CommissioningDate    string  `json:"commissioning_date"` // honored for commission_now too
	AssignedEmployeeID   string  `json:"assigned_employee_id"`
	ConstructionObjectID string  `json:"construction_object_id"`
	Notes                string  `json:"notes"`
}

// CreateAsset creates an asset (draft), posts the purchase (+VAT +payment), and
// — unless commission_now=false — immediately commissions it (§2.3, §5).
// POST /api/v1/assets
func (h *Handler) CreateAsset(c *gin.Context) {
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

	var in createAssetInput
	if err := c.ShouldBindJSON(&in); err != nil {
		faErr(c, http.StatusBadRequest, "INVALID_INPUT", "Ma'lumot noto'g'ri", "Некорректные данные")
		return
	}
	if strings.TrimSpace(in.Name) == "" || in.CategoryID == "" || in.DepartmentID == "" {
		faErr(c, http.StatusBadRequest, "MISSING_FIELD", "Nomi, kategoriya va bo'lim majburiy", "Название, категория и подразделение обязательны")
		return
	}
	if in.Cost <= 0 {
		faErr(c, http.StatusBadRequest, "INVALID_COST", "Qiymat 0 dan katta bo'lishi kerak", "Стоимость должна быть больше 0")
		return
	}
	if in.SalvageValue < 0 || in.SalvageValue >= in.Cost {
		faErr(c, http.StatusBadRequest, "INVALID_SALVAGE", "Likvidatsiya qiymati noto'g'ri", "Некорректная ликвидационная стоимость")
		return
	}
	if in.UsefulLifeMonths <= 0 {
		faErr(c, http.StatusBadRequest, "INVALID_LIFE", "Foydali xizmat muddati noto'g'ri", "Некорректный срок службы")
		return
	}
	catID, err := uuid.Parse(in.CategoryID)
	if err != nil {
		faErr(c, http.StatusBadRequest, "INVALID_INPUT", "Kategoriya noto'g'ri", "Некорректная категория")
		return
	}
	deptID, err := uuid.Parse(in.DepartmentID)
	if err != nil {
		faErr(c, http.StatusBadRequest, "INVALID_INPUT", "Bo'lim noto'g'ri", "Некорректное подразделение")
		return
	}
	purchaseDate, err := time.Parse("2006-01-02", in.PurchaseDate)
	if err != nil {
		faErr(c, http.StatusBadRequest, "INVALID_DATE", "Xarid sanasi noto'g'ri", "Некорректная дата покупки")
		return
	}

	// §2.2 — mapping must be configured for the chosen category/department.
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
	if strings.TrimSpace(catAsset) == "" || (catDepreciable && !catDepr.Valid) || strings.TrimSpace(deptExpense) == "" {
		faErr(c, http.StatusBadRequest, "MAPPING_MISSING", "Kategoriya/bo'lim uchun hisoblar sozlanmagan", "Счета для категории/подразделения не настроены")
		return
	}

	method := in.Method
	if method == "" {
		method = "straight_line"
	}
	commissionNow := true
	if in.CommissionNow != nil {
		commissionNow = *in.CommissionNow
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

	var supplierPtr interface{}
	if sid, err := uuid.Parse(in.SupplierID); err == nil {
		supplierPtr = sid
	}
	var employeePtr, objectPtr interface{}
	if eid, err := uuid.Parse(in.AssignedEmployeeID); err == nil {
		employeePtr = eid
	}
	// construction_projects uses BIGSERIAL ids.
	if oid, err := strconv.ParseInt(in.ConstructionObjectID, 10, 64); err == nil && oid > 0 {
		objectPtr = oid
	}

	assetID := uuid.New()
	_, err = tx.Exec(`
		INSERT INTO fa_assets (
			id, tenant_id, organization_id, inventory_number, name, category_id, department_id,
			serial_number, location, purchase_date, cost, salvage_value, useful_life_months, method,
			status, supplier_id, payment_method, doc_number, doc_date,
			assigned_employee_id, construction_object_id, notes, created_by, created_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,'draft',$15,$16,$17,$18,$19,$20,$21,$22,NOW(),NOW())`,
		assetID, tenantID, orgPtr, inv, strings.TrimSpace(in.Name), catID, deptID,
		nullIfEmpty(in.SerialNumber), nullIfEmpty(in.Location), purchaseDate, in.Cost, in.SalvageValue,
		in.UsefulLifeMonths, method, supplierPtr, nullIfEmpty(in.PaymentMethod), nullIfEmpty(in.DocNumber),
		parseDatePtr(in.DocDate), employeePtr, objectPtr, nullIfEmpty(in.Notes), userID)
	if err != nil {
		faErr(c, http.StatusInternalServerError, "SAVE_FAILED", "Aktivni saqlab bo'lmadi", "Не удалось сохранить актив")
		return
	}

	acc := h.lifecycleAccounts(tenantID)
	journalID, nextNum, prefix := faGetJournal(tx, tenantID)

	// Purchase: Дт acquisition (0820) Кт AP (6010), cost ex-VAT. Always booked.
	if _, e := h.faPostJournal(tx, tenantID, orgPtr, journalID,
		fmt.Sprintf("%s%06d", prefix, nextNum), inv+"-BUY", "Aktiv xaridi: "+in.Name, "asset_purchase", assetID, purchaseDate,
		[]faJELine{{Code: acc.Acquisition, Debit: in.Cost, Desc: "Kapital qo'yilma"}, {Code: acc.AP, Credit: in.Cost, Desc: "Ta'minotchiga qarz"}}); e != nil {
		faErr(c, http.StatusBadRequest, "POSTING_FAILED", "Xarid provodkasi: "+e.Error(), "Проводка покупки: "+e.Error())
		return
	}
	nextNum++

	// Optional VAT: Дт 4410 Кт 6010.
	if in.VatAmount > 0 {
		if _, e := h.faPostJournal(tx, tenantID, orgPtr, journalID,
			fmt.Sprintf("%s%06d", prefix, nextNum), inv+"-VAT", "QQS: "+in.Name, "asset_vat", assetID, purchaseDate,
			[]faJELine{{Code: acc.VATInput, Debit: in.VatAmount, Desc: "QQS hisobga olish"}, {Code: acc.AP, Credit: in.VatAmount, Desc: "Ta'minotchiga qarz (QQS)"}}); e != nil {
			faErr(c, http.StatusBadRequest, "POSTING_FAILED", "QQS provodkasi: "+e.Error(), "Проводка НДС: "+e.Error())
			return
		}
		nextNum++
	}

	// Payment (cash): Дт 6010 Кт 5010/5110. Credit purchase leaves debt on 6010.
	if in.PaymentMethod == "cash" || in.PaymentMethod == "bank" {
		payCredit := acc.Cash
		if in.PaymentMethod == "bank" {
			payCredit = acc.Bank
		}
		if _, e := h.faPostJournal(tx, tenantID, orgPtr, journalID,
			fmt.Sprintf("%s%06d", prefix, nextNum), inv+"-PAY", "To'lov: "+in.Name, "asset_payment", assetID, purchaseDate,
			[]faJELine{{Code: acc.AP, Debit: in.Cost + in.VatAmount, Desc: "Qarz yopilishi"}, {Code: payCredit, Credit: in.Cost + in.VatAmount, Desc: "To'lov"}}); e != nil {
			faErr(c, http.StatusBadRequest, "POSTING_FAILED", "To'lov provodkasi: "+e.Error(), "Проводка оплаты: "+e.Error())
			return
		}
		nextNum++
	}

	// Immediate commission (§2.3): Дт asset_account Кт acquisition, in_service.
	// The commissioning date defaults to the purchase date but an explicit
	// commissioning_date wins (audit: the form previously ignored it).
	commissioned := false
	if commissionNow {
		commDate := purchaseDate
		if d, e := time.Parse("2006-01-02", in.CommissioningDate); e == nil {
			commDate = d
		}
		if e := h.faCommissionInTx(tx, tenantID, orgPtr, assetID, in.Name, catAsset, acc.Acquisition, in.Cost, commDate, journalID, &nextNum, prefix); e != nil {
			faErr(c, http.StatusBadRequest, "POSTING_FAILED", "Ishga tushirish: "+e.Error(), "Ввод в эксплуатацию: "+e.Error())
			return
		}
		commissioned = true
	}

	faBumpJournalNumber(tx, journalID, nextNum)
	if err := tx.Commit(); err != nil {
		faErr(c, http.StatusInternalServerError, "TX_FAILED", "Xatolik", "Ошибка")
		return
	}

	if commissioned {
		h.EmitWorkflowEvent(tenantID, "assets.commissioned", map[string]interface{}{
			"record_id": assetID.String(), "inventory_number": inv, "asset_name": in.Name, "cost": in.Cost,
		})
	}
	faOK(c, gin.H{"id": assetID, "inventory_number": inv, "status": statusOf(commissioned)})
}

func statusOf(commissioned bool) string {
	if commissioned {
		return "in_service"
	}
	return "draft"
}

// faCommissionInTx books the commission entry and flips status to in_service.
func (h *Handler) faCommissionInTx(tx *sql.Tx, tenantID uuid.UUID, orgPtr *uuid.UUID, assetID uuid.UUID, name, assetAccount, acquisitionAccount string, cost float64, commDate time.Time, journalID uuid.UUID, nextNum *int, prefix string) error {
	if _, e := h.faPostJournal(tx, tenantID, orgPtr, journalID,
		fmt.Sprintf("%s%06d", prefix, *nextNum), "COMM", "Foydalanishga topshirish: "+name, "asset_commission", assetID, commDate,
		[]faJELine{{Code: assetAccount, Debit: cost, Desc: "Asosiy vosita"}, {Code: acquisitionAccount, Credit: cost, Desc: "Kapital qo'yilma yopilishi"}}); e != nil {
		return e
	}
	*nextNum++
	_, err := tx.Exec(`UPDATE fa_assets SET status='in_service', commissioning_date=$1, updated_at=NOW() WHERE id=$2 AND tenant_id=$3`, commDate, assetID, tenantID)
	return err
}

type commissionInput struct {
	CommissioningDate string `json:"commissioning_date"`
}

// CommissionAsset moves a draft asset to in_service on a chosen date (§4).
// POST /api/v1/assets/:id/commission
func (h *Handler) CommissionAsset(c *gin.Context) {
	tenantID, _ := middleware.GetTenantID(c)
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
	var in commissionInput
	_ = c.ShouldBindJSON(&in)

	var name, status string
	var cost float64
	err = h.db.QueryRow(`SELECT name, status::text, cost FROM fa_assets WHERE id=$1 AND tenant_id=$2 AND deleted_at IS NULL`,
		assetID, tenantID).Scan(&name, &status, &cost)
	if err != nil {
		faErr(c, http.StatusNotFound, "NOT_FOUND", "Aktiv topilmadi", "Актив не найден")
		return
	}
	if status != "draft" {
		faErr(c, http.StatusBadRequest, "INVALID_STATUS", "Faqat qoralama aktivni ishga tushirish mumkin", "Ввести можно только черновик")
		return
	}
	eff, err := h.effectiveAccountsForAsset(tenantID, assetID)
	if err != nil || eff.AssetCode == "" {
		faErr(c, http.StatusBadRequest, "MAPPING_MISSING", "Aktiv hisobi sozlanmagan", "Счёт актива не настроен")
		return
	}
	effAsset := eff.AssetCode
	acc := h.lifecycleAccounts(tenantID)

	commDate := time.Now()
	if in.CommissioningDate != "" {
		if d, e := time.Parse("2006-01-02", in.CommissioningDate); e == nil {
			commDate = d
		}
	}

	tx, err := h.db.Begin()
	if err != nil {
		faErr(c, http.StatusInternalServerError, "TX_FAILED", "Xatolik", "Ошибка")
		return
	}
	defer tx.Rollback()
	journalID, nextNum, prefix := faGetJournal(tx, tenantID)
	if e := h.faCommissionInTx(tx, tenantID, orgPtr, assetID, name, effAsset, acc.Acquisition, cost, commDate, journalID, &nextNum, prefix); e != nil {
		faErr(c, http.StatusBadRequest, "POSTING_FAILED", "Ishga tushirish: "+e.Error(), "Ввод: "+e.Error())
		return
	}
	faBumpJournalNumber(tx, journalID, nextNum)
	if err := tx.Commit(); err != nil {
		faErr(c, http.StatusInternalServerError, "TX_FAILED", "Xatolik", "Ошибка")
		return
	}
	var inv string
	h.db.QueryRow(`SELECT inventory_number FROM fa_assets WHERE id=$1 AND tenant_id=$2`, assetID, tenantID).Scan(&inv)
	h.EmitWorkflowEvent(tenantID, "assets.commissioned", map[string]interface{}{
		"record_id": assetID.String(), "inventory_number": inv, "asset_name": name, "cost": cost,
	})
	faOK(c, gin.H{"id": assetID, "status": "in_service", "commissioning_date": commDate.Format("2006-01-02")})
}

// ConserveAsset / ReactivateAsset toggle the conserved status (§4).
// POST /api/v1/assets/:id/conserve  |  /reactivate
func (h *Handler) ConserveAsset(c *gin.Context)   { h.faSetStatus(c, "in_service", "conserved") }
func (h *Handler) ReactivateAsset(c *gin.Context) { h.faSetStatus(c, "conserved", "in_service") }

func (h *Handler) faSetStatus(c *gin.Context, from, to string) {
	tenantID, _ := middleware.GetTenantID(c)
	assetID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		faErr(c, http.StatusBadRequest, "INVALID_INPUT", "ID noto'g'ri", "Некорректный ID")
		return
	}
	res, err := h.db.Exec(`UPDATE fa_assets SET status=$1::fa_asset_status, updated_at=NOW() WHERE id=$2 AND tenant_id=$3 AND status=$4::fa_asset_status AND deleted_at IS NULL`,
		to, assetID, tenantID, from)
	if err != nil {
		faErr(c, http.StatusInternalServerError, "SAVE_FAILED", "Xatolik", "Ошибка")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		faErr(c, http.StatusBadRequest, "INVALID_STATUS", "Holatni o'zgartirib bo'lmadi", "Не удалось изменить статус")
		return
	}
	faOK(c, gin.H{"id": assetID, "status": to})
}

type disposeInput struct {
	DisposalDate string  `json:"disposal_date"`
	DisposalType string  `json:"disposal_type"` // 'sale' | 'writeoff' (default)
	SalePrice    float64 `json:"sale_price"`    // required when disposal_type='sale'
	Reason       string  `json:"reason"`
}

// DisposeAsset tops up depreciation for the disposal month (§6.3) then retires
// the asset through the disposal transit account (9210 by default):
//
//	Дт disposal            Кт asset_account       (cost)
//	Дт depreciation_acct   Кт disposal            (accumulated)
//	sale only: Дт receivable Кт disposal          (sale price)
//	result:    Дт loss / Кт gain                  (closes the transit to zero)
//
// The transit account always nets to zero, so the gain/loss lands on the
// tenant-configured result accounts. Terminal (§4).
// POST /api/v1/assets/:id/dispose
func (h *Handler) DisposeAsset(c *gin.Context) {
	tenantID, _ := middleware.GetTenantID(c)
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
	var in disposeInput
	_ = c.ShouldBindJSON(&in)
	disposalDate := time.Now()
	if in.DisposalDate != "" {
		if d, e := time.Parse("2006-01-02", in.DisposalDate); e == nil {
			disposalDate = d
		}
	}
	isSale := in.DisposalType == "sale"
	if isSale && in.SalePrice <= 0 {
		faErr(c, http.StatusBadRequest, "INVALID_SALE_PRICE", "Sotish narxi 0 dan katta bo'lishi kerak", "Цена продажи должна быть больше 0")
		return
	}
	period := disposalDate.Format("2006-01")
	if msg := h.checkPeriodLock(tenantID, disposalDate); msg != "" {
		faErr(c, http.StatusBadRequest, "PERIOD_CLOSED", msg, msg)
		return
	}

	var name, inv, status string
	var cost, salvage, accumulated float64
	var lifeMonths int
	err = h.db.QueryRow(`SELECT name, inventory_number, status::text, cost, salvage_value, useful_life_months, accumulated_depreciation
		FROM fa_assets WHERE id=$1 AND tenant_id=$2 AND deleted_at IS NULL`, assetID, tenantID).
		Scan(&name, &inv, &status, &cost, &salvage, &lifeMonths, &accumulated)
	if err != nil {
		faErr(c, http.StatusNotFound, "NOT_FOUND", "Aktiv topilmadi", "Актив не найден")
		return
	}
	if status == "disposed" {
		faErr(c, http.StatusBadRequest, "INVALID_STATUS", "Aktiv allaqachon chiqarilgan", "Актив уже выбыл")
		return
	}
	if status == "draft" {
		faErr(c, http.StatusBadRequest, "INVALID_STATUS", "Ishga tushmagan aktivni chiqarib bo'lmaydi", "Нельзя выбыть не введённый актив")
		return
	}
	eff, err := h.effectiveAccountsForAsset(tenantID, assetID)
	if err != nil {
		faErr(c, http.StatusInternalServerError, "MAPPING_MISSING", "Hisoblar topilmadi", "Счета не найдены")
		return
	}
	acc := h.lifecycleAccounts(tenantID)

	tx, err := h.db.Begin()
	if err != nil {
		faErr(c, http.StatusInternalServerError, "TX_FAILED", "Xatolik", "Ошибка")
		return
	}
	defer tx.Rollback()
	journalID, nextNum, prefix := faGetJournal(tx, tenantID)

	// §6.3.1 — top up the disposal month if not already accrued and depreciable.
	rounding := h.faRounding(tenantID)
	if eff.Depreciable && eff.DepreciationCode != "" {
		var exists bool
		tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM fa_depreciation_entries WHERE tenant_id=$1 AND asset_id=$2 AND period=$3 AND status='active')`,
			tenantID, assetID, period).Scan(&exists)
		if !exists {
			var priorPeriods int
			tx.QueryRow(`SELECT COUNT(*) FROM fa_depreciation_entries WHERE asset_id=$1 AND tenant_id=$2 AND status='active'`, assetID, tenantID).Scan(&priorPeriods)
			amt := faAccrualAmount(cost, salvage, accumulated, lifeMonths, priorPeriods, rounding)
			if amt > 0 {
				jeID, e := h.faPostJournal(tx, tenantID, orgPtr, journalID,
					fmt.Sprintf("%s%06d", prefix, nextNum), name+"-DEPDISP", "Chiqarish oyi amortizatsiyasi "+period, "depreciation", assetID, faLastDayOf(period),
					[]faJELine{{Code: eff.ExpenseCode, Debit: amt, Desc: "Amortizatsiya"}, {Code: eff.DepreciationCode, Credit: amt, Desc: "Yig'ilgan amortizatsiya"}})
				if e != nil {
					faErr(c, http.StatusBadRequest, "POSTING_FAILED", "Amortizatsiya: "+e.Error(), "Амортизация: "+e.Error())
					return
				}
				nextNum++
				if _, e := tx.Exec(`INSERT INTO fa_depreciation_entries (tenant_id, run_id, asset_id, period, amount, debit_account, credit_account, status, journal_entry_id)
					VALUES ($1,NULL,$2,$3,$4,$5,$6,'active',$7)`, tenantID, assetID, period, amt, eff.ExpenseCode, eff.DepreciationCode, jeID); e != nil {
					faErr(c, http.StatusInternalServerError, "SAVE_FAILED", "Yozuv saqlanmadi", "Запись не сохранена")
					return
				}
				accumulated += amt
				tx.Exec(`UPDATE fa_assets SET accumulated_depreciation=accumulated_depreciation+$1 WHERE id=$2`, amt, assetID)
			}
		}
	}

	// §5 disposal postings through the transit account, closed to gain/loss so
	// it always nets to zero. book value = cost − accumulated;
	// result = sale price − book value (write-off: result = −book value).
	bookValue := faRound(cost-accumulated, 2)
	lines := []faJELine{{Code: acc.Disposal, Debit: cost, Desc: "Chiqarish (tannarx)"}, {Code: eff.AssetCode, Credit: cost, Desc: "Asosiy vosita hisobdan chiqarish"}}
	if accumulated > 0 && eff.DepreciationCode != "" {
		lines = append(lines, faJELine{Code: eff.DepreciationCode, Debit: accumulated, Desc: "Yig'ilgan amortizatsiya yopilishi"}, faJELine{Code: acc.Disposal, Credit: accumulated, Desc: "Chiqarish"})
	}
	result := -bookValue
	if isSale {
		lines = append(lines, faJELine{Code: acc.DisposalReceivable, Debit: in.SalePrice, Desc: "Sotish tushumi (debitor)"}, faJELine{Code: acc.Disposal, Credit: in.SalePrice, Desc: "Sotish"})
		result = faRound(in.SalePrice-bookValue, 2)
	}
	if result > 0 {
		lines = append(lines, faJELine{Code: acc.Disposal, Debit: result, Desc: "Chiqarish yakuni"}, faJELine{Code: acc.DisposalGain, Credit: result, Desc: "Chiqarishdan foyda"})
	} else if result < 0 {
		lines = append(lines, faJELine{Code: acc.DisposalLoss, Debit: -result, Desc: "Chiqarishdan zarar"}, faJELine{Code: acc.Disposal, Credit: -result, Desc: "Chiqarish yakuni"})
	}
	if _, e := h.faPostJournal(tx, tenantID, orgPtr, journalID,
		fmt.Sprintf("%s%06d", prefix, nextNum), inv+"-DISP", "Aktiv chiqarilishi: "+name, "asset_disposal", assetID, disposalDate, lines); e != nil {
		faErr(c, http.StatusBadRequest, "POSTING_FAILED", "Chiqarish: "+e.Error(), "Выбытие: "+e.Error())
		return
	}
	nextNum++

	var saleAmt interface{}
	if isSale {
		saleAmt = in.SalePrice
	}
	if _, err := tx.Exec(`UPDATE fa_assets SET status='disposed', disposal_date=$1, disposal_amount=$2, disposal_reason=$3, updated_at=NOW() WHERE id=$4 AND tenant_id=$5`,
		disposalDate, saleAmt, nullIfEmpty(in.Reason), assetID, tenantID); err != nil {
		faErr(c, http.StatusInternalServerError, "SAVE_FAILED", "Xatolik", "Ошибка")
		return
	}
	faBumpJournalNumber(tx, journalID, nextNum)
	if err := tx.Commit(); err != nil {
		faErr(c, http.StatusInternalServerError, "TX_FAILED", "Xatolik", "Ошибка")
		return
	}
	h.EmitWorkflowEvent(tenantID, "assets.disposed", map[string]interface{}{
		"record_id": assetID.String(), "inventory_number": inv, "asset_name": name,
		"disposal_type": map[bool]string{true: "sale", false: "writeoff"}[isSale],
		"book_value":    bookValue, "sale_price": in.SalePrice, "gain_loss": result, "reason": in.Reason,
	})
	faOK(c, gin.H{"id": assetID, "status": "disposed", "book_value": bookValue, "gain_loss": result})
}

type changeParamsInput struct {
	Cost             *float64 `json:"cost"`
	SalvageValue     *float64 `json:"salvage_value"`
	UsefulLifeMonths *int     `json:"useful_life_months"`
	Method           *string  `json:"method"`
	EffectiveFrom    string   `json:"effective_from"`
}

// ChangeAssetParams updates cost/salvage/life/method prospectively (§4). Future
// runs recompute from the new values + current accumulated; past entries stand.
// POST /api/v1/assets/:id/change-params
func (h *Handler) ChangeAssetParams(c *gin.Context) {
	tenantID, _ := middleware.GetTenantID(c)
	userID, _ := middleware.GetUserID(c)
	assetID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		faErr(c, http.StatusBadRequest, "INVALID_INPUT", "ID noto'g'ri", "Некорректный ID")
		return
	}
	var in changeParamsInput
	if err := c.ShouldBindJSON(&in); err != nil {
		faErr(c, http.StatusBadRequest, "INVALID_INPUT", "Ma'lumot noto'g'ri", "Некорректные данные")
		return
	}

	var oldCost, oldSalvage float64
	var oldLife int
	var oldMethod string
	if err := h.db.QueryRow(`SELECT cost, salvage_value, useful_life_months, method FROM fa_assets WHERE id=$1 AND tenant_id=$2 AND deleted_at IS NULL`,
		assetID, tenantID).Scan(&oldCost, &oldSalvage, &oldLife, &oldMethod); err != nil {
		faErr(c, http.StatusNotFound, "NOT_FOUND", "Aktiv topilmadi", "Актив не найден")
		return
	}
	newCost, newSalvage, newLife, newMethod := oldCost, oldSalvage, oldLife, oldMethod
	if in.Cost != nil {
		newCost = *in.Cost
	}
	if in.SalvageValue != nil {
		newSalvage = *in.SalvageValue
	}
	if in.UsefulLifeMonths != nil {
		newLife = *in.UsefulLifeMonths
	}
	if in.Method != nil && *in.Method != "" {
		newMethod = *in.Method
	}
	if newCost <= 0 || newSalvage < 0 || newSalvage >= newCost || newLife <= 0 {
		faErr(c, http.StatusBadRequest, "INVALID_INPUT", "Qiymatlar noto'g'ri", "Некорректные значения")
		return
	}
	if _, err := h.db.Exec(`UPDATE fa_assets SET cost=$1, salvage_value=$2, useful_life_months=$3, method=$4, updated_at=NOW() WHERE id=$5 AND tenant_id=$6`,
		newCost, newSalvage, newLife, newMethod, assetID, tenantID); err != nil {
		faErr(c, http.StatusInternalServerError, "SAVE_FAILED", "Xatolik", "Ошибка")
		return
	}
	h.faAudit(tenantID, userID, "fa_asset", assetID, "change_params",
		map[string]interface{}{"cost": oldCost, "salvage_value": oldSalvage, "useful_life_months": oldLife, "method": oldMethod},
		map[string]interface{}{"cost": newCost, "salvage_value": newSalvage, "useful_life_months": newLife, "method": newMethod, "effective_from": in.EffectiveFrom})
	faOK(c, gin.H{"id": assetID, "message": "Parametrlar yangilandi"})
}

type patchAccountsInput struct {
	AssetAccount        *string `json:"asset_account"`
	DepreciationAccount *string `json:"depreciation_account"`
	ExpenseAccount      *string `json:"expense_account"`
	Comment             string  `json:"comment"`
}

// PatchAssetAccounts overrides an asset's GL accounts (§2.6). Requires
// accounting.override_accounts, a comment, and writes an audit record. Only
// depreciation/expense overrides here; changing the asset account of a
// commissioned object requires a transfer document (§2.6) — rejected here.
// PATCH /api/v1/assets/:id/accounts
func (h *Handler) PatchAssetAccounts(c *gin.Context) {
	tenantID, _ := middleware.GetTenantID(c)
	userID, _ := middleware.GetUserID(c)
	assetID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		faErr(c, http.StatusBadRequest, "INVALID_INPUT", "ID noto'g'ri", "Некорректный ID")
		return
	}
	var in patchAccountsInput
	if err := c.ShouldBindJSON(&in); err != nil {
		faErr(c, http.StatusBadRequest, "INVALID_INPUT", "Ma'lumot noto'g'ri", "Некорректные данные")
		return
	}
	if strings.TrimSpace(in.Comment) == "" {
		faErr(c, http.StatusBadRequest, "COMMENT_REQUIRED", "Izoh majburiy", "Комментарий обязателен")
		return
	}

	var status string
	var oldAsset, oldDepr, oldExp sql.NullString
	if err := h.db.QueryRow(`SELECT status::text, asset_account_override, depreciation_account_override, expense_account_override
		FROM fa_assets WHERE id=$1 AND tenant_id=$2 AND deleted_at IS NULL`, assetID, tenantID).
		Scan(&status, &oldAsset, &oldDepr, &oldExp); err != nil {
		faErr(c, http.StatusNotFound, "NOT_FOUND", "Aktiv topilmadi", "Актив не найден")
		return
	}

	sets := []string{}
	args := []interface{}{}
	arg := 1
	if in.AssetAccount != nil {
		if status != "draft" {
			faErr(c, http.StatusBadRequest, "TRANSFER_REQUIRED", "Ishga tushirilgan aktiv aktiv hisobini faqat ko'chirish hujjati bilan o'zgartiradi", "Счёт актива после ввода меняется только документом перевода")
			return
		}
		if code, uz, ru := h.validateAccountCode(tenantID, *in.AssetAccount, faKindAsset); code != "" {
			faErr(c, http.StatusBadRequest, code, uz, ru)
			return
		}
		sets = append(sets, fmt.Sprintf("asset_account_override=$%d", arg))
		args = append(args, *in.AssetAccount)
		arg++
	}
	if in.DepreciationAccount != nil {
		if code, uz, ru := h.validateAccountCode(tenantID, *in.DepreciationAccount, faKindDepreciation); code != "" {
			faErr(c, http.StatusBadRequest, code, uz, ru)
			return
		}
		sets = append(sets, fmt.Sprintf("depreciation_account_override=$%d", arg))
		args = append(args, *in.DepreciationAccount)
		arg++
	}
	if in.ExpenseAccount != nil {
		if code, uz, ru := h.validateAccountCode(tenantID, *in.ExpenseAccount, faKindExpense); code != "" {
			faErr(c, http.StatusBadRequest, code, uz, ru)
			return
		}
		sets = append(sets, fmt.Sprintf("expense_account_override=$%d", arg))
		args = append(args, *in.ExpenseAccount)
		arg++
	}
	if len(sets) == 0 {
		faErr(c, http.StatusBadRequest, "NOTHING_TO_CHANGE", "O'zgartirish yo'q", "Нечего менять")
		return
	}
	args = append(args, assetID, tenantID)
	q := fmt.Sprintf("UPDATE fa_assets SET %s, updated_at=NOW() WHERE id=$%d AND tenant_id=$%d", strings.Join(sets, ", "), arg, arg+1)
	if _, err := h.db.Exec(q, args...); err != nil {
		faErr(c, http.StatusInternalServerError, "SAVE_FAILED", "Xatolik", "Ошибка")
		return
	}
	h.faAudit(tenantID, userID, "fa_asset", assetID, "override_accounts",
		map[string]interface{}{"asset": oldAsset.String, "depreciation": oldDepr.String, "expense": oldExp.String},
		map[string]interface{}{"asset_account": in.AssetAccount, "depreciation_account": in.DepreciationAccount, "expense_account": in.ExpenseAccount, "comment": in.Comment})
	faOK(c, gin.H{"id": assetID, "message": "Hisoblar o'zgartirildi"})
}

// GetAssetSchedule projects future monthly accruals to end of life (§9).
// GET /api/v1/assets/:id/schedule
func (h *Handler) GetAssetSchedule(c *gin.Context) {
	tenantID, _ := middleware.GetTenantID(c)
	assetID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		faErr(c, http.StatusBadRequest, "INVALID_INPUT", "ID noto'g'ri", "Некорректный ID")
		return
	}
	var cost, salvage, accumulated float64
	var lifeMonths int
	var commissioning sql.NullTime
	if err := h.db.QueryRow(`SELECT cost, salvage_value, useful_life_months, accumulated_depreciation, commissioning_date
		FROM fa_assets WHERE id=$1 AND tenant_id=$2 AND deleted_at IS NULL`, assetID, tenantID).
		Scan(&cost, &salvage, &lifeMonths, &accumulated, &commissioning); err != nil {
		faErr(c, http.StatusNotFound, "NOT_FOUND", "Aktiv topilmadi", "Актив не найден")
		return
	}
	rounding := h.faRounding(tenantID)
	type row struct {
		Period      string  `json:"period"`
		Amount      float64 `json:"amount"`
		Accumulated float64 `json:"accumulated"`
		BookValue   float64 `json:"book_value"`
	}
	sched := []row{}
	acc := accumulated
	var prior0 int
	h.db.QueryRow(`SELECT COUNT(*) FROM fa_depreciation_entries WHERE asset_id=$1 AND tenant_id=$2 AND status='active'`, assetID, tenantID).Scan(&prior0)
	// Start from the month after commissioning (or now if not commissioned).
	start := time.Now()
	if commissioning.Valid {
		start = commissioning.Time
	}
	p := time.Date(start.Year(), start.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, 1, 0)
	for i := 0; i < lifeMonths+2; i++ {
		amt := faAccrualAmount(cost, salvage, acc, lifeMonths, prior0+i, rounding)
		if amt <= 0 {
			break
		}
		acc += amt
		sched = append(sched, row{Period: p.Format("2006-01"), Amount: amt, Accumulated: faRound(acc, 2), BookValue: faRound(cost-acc, 2)})
		p = p.AddDate(0, 1, 0)
	}
	faOK(c, sched)
}

// ListAssets / GetAsset — read views over the register (§2.1).
func (h *Handler) ListAssets(c *gin.Context) {
	tenantID, _ := middleware.GetTenantID(c)

	// Paging + filters. Previously this read zero query params and had no LIMIT,
	// so the whole asset register shipped on every open and the client could
	// neither page nor count (faOK carries no meta). Search and the status
	// filter were both done in Dart over whatever had been downloaded.
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	if page < 1 {
		page = 1
	}
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	if pageSize < 1 {
		pageSize = 20
	}
	if pageSize > 100 { // clamp to the cap, never fall back to the default
		pageSize = 100
	}

	where := " WHERE a.tenant_id=$1 AND a.deleted_at IS NULL"
	args := []interface{}{tenantID}
	n := 1
	if q := strings.TrimSpace(c.Query("q")); q != "" {
		n++
		where += fmt.Sprintf(` AND (a.name ILIKE $%d OR a.inventory_number ILIKE $%d
			OR COALESCE(a.serial_number,'') ILIKE $%d OR c.name_uz ILIKE $%d)`, n, n, n, n)
		args = append(args, "%"+q+"%")
	}
	if st := strings.TrimSpace(c.Query("status")); st != "" && strings.ToLower(st) != "all" {
		n++
		where += fmt.Sprintf(" AND a.status::text = $%d", n)
		args = append(args, st)
	}
	if cat := strings.TrimSpace(c.Query("category_id")); cat != "" {
		if cid, e := uuid.Parse(cat); e == nil {
			n++
			where += fmt.Sprintf(" AND a.category_id = $%d", n)
			args = append(args, cid)
		}
	}

	// The COUNT must repeat both INNER JOINs — an asset whose category or
	// department row was deleted is absent from the page, so counting off
	// fa_assets alone would promise a page that never materialises.
	const joins = `
		FROM fa_assets a
		JOIN fa_categories c ON c.id = a.category_id
		JOIN fa_departments d ON d.id = a.department_id
		LEFT JOIN employees e ON e.id = a.assigned_employee_id
		LEFT JOIN construction_projects cp ON cp.id = a.construction_object_id`

	var total int
	if err := h.db.QueryRow(`SELECT COUNT(*)`+joins+where, args...).Scan(&total); err != nil {
		faErr(c, http.StatusInternalServerError, "QUERY_FAILED", "Xatolik", "Ошибка")
		return
	}

	rows, err := h.db.Query(fmt.Sprintf(`
		SELECT a.id, a.inventory_number, a.name, a.status::text, a.cost, a.salvage_value, a.useful_life_months,
		       a.accumulated_depreciation, (a.cost - a.accumulated_depreciation) AS book_value,
		       a.category_id, c.name_uz, d.name_uz, COALESCE(a.serial_number,''), a.purchase_date, a.commissioning_date,
		       COALESCE(e.first_name || ' ' || e.last_name, ''), COALESCE(cp.name, '')`+joins+where+`
		ORDER BY a.inventory_number LIMIT %d OFFSET %d`, pageSize, (page-1)*pageSize), args...)
	if err != nil {
		faErr(c, http.StatusInternalServerError, "QUERY_FAILED", "Xatolik", "Ошибка")
		return
	}
	defer rows.Close()
	type asset struct {
		ID                string     `json:"id"`
		InventoryNumber   string     `json:"inventory_number"`
		Name              string     `json:"name"`
		Status            string     `json:"status"`
		Cost              float64    `json:"cost"`
		SalvageValue      float64    `json:"salvage_value"`
		UsefulLifeMonths  int        `json:"useful_life_months"`
		Accumulated       float64    `json:"accumulated_depreciation"`
		BookValue         float64    `json:"book_value"`
		CategoryID        string     `json:"category_id"`
		CategoryName      string     `json:"category_name"`
		DepartmentName    string     `json:"department_name"`
		SerialNumber      string     `json:"serial_number"`
		PurchaseDate      time.Time  `json:"purchase_date"`
		CommissioningDate *time.Time `json:"commissioning_date"`
		AssignedEmpName   string     `json:"assigned_employee_name"`
		ObjectName        string     `json:"construction_object_name"`
	}
	out := []asset{}
	for rows.Next() {
		var a asset
		var comm sql.NullTime
		if rows.Scan(&a.ID, &a.InventoryNumber, &a.Name, &a.Status, &a.Cost, &a.SalvageValue, &a.UsefulLifeMonths,
			&a.Accumulated, &a.BookValue, &a.CategoryID, &a.CategoryName, &a.DepartmentName, &a.SerialNumber,
			&a.PurchaseDate, &comm, &a.AssignedEmpName, &a.ObjectName) == nil {
			if comm.Valid {
				a.CommissioningDate = &comm.Time
			}
			out = append(out, a)
		}
	}
	// response.Paginated instead of faOK: the success body is byte-identical
	// ({success, data}) and this is the only way the client gets a meta to page
	// with. faOK stays for singletons and mutations across the fa_* module.
	response.Paginated(c, out, page, pageSize, total)
}

type updateAssetInfoInput struct {
	Name                 *string `json:"name"`
	SerialNumber         *string `json:"serial_number"`
	Location             *string `json:"location"`
	Notes                *string `json:"notes"`
	AssignedEmployeeID   *string `json:"assigned_employee_id"`   // "" clears
	ConstructionObjectID *string `json:"construction_object_id"` // "" clears
	DepartmentID         *string `json:"department_id"`
}

// UpdateAssetInfo patches the operational (non-financial) fields of an asset.
// Financial parameters go through /change-params, GL accounts through
// /accounts — this endpoint deliberately cannot touch money.
// PATCH /api/v1/assets/:id
func (h *Handler) UpdateAssetInfo(c *gin.Context) {
	tenantID, _ := middleware.GetTenantID(c)
	userID, _ := middleware.GetUserID(c)
	assetID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		faErr(c, http.StatusBadRequest, "INVALID_INPUT", "ID noto'g'ri", "Некорректный ID")
		return
	}
	var in updateAssetInfoInput
	if err := c.ShouldBindJSON(&in); err != nil {
		faErr(c, http.StatusBadRequest, "INVALID_INPUT", "Ma'lumot noto'g'ri", "Некорректные данные")
		return
	}
	var exists bool
	h.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM fa_assets WHERE id=$1 AND tenant_id=$2 AND deleted_at IS NULL)`, assetID, tenantID).Scan(&exists)
	if !exists {
		faErr(c, http.StatusNotFound, "NOT_FOUND", "Aktiv topilmadi", "Актив не найден")
		return
	}

	sets := []string{}
	args := []interface{}{}
	arg := 1
	addSet := func(col string, v interface{}) {
		sets = append(sets, fmt.Sprintf("%s=$%d", col, arg))
		args = append(args, v)
		arg++
	}
	if in.Name != nil && strings.TrimSpace(*in.Name) != "" {
		addSet("name", strings.TrimSpace(*in.Name))
	}
	if in.SerialNumber != nil {
		addSet("serial_number", nullIfEmpty(*in.SerialNumber))
	}
	if in.Location != nil {
		addSet("location", nullIfEmpty(*in.Location))
	}
	if in.Notes != nil {
		addSet("notes", nullIfEmpty(*in.Notes))
	}
	if in.AssignedEmployeeID != nil {
		if *in.AssignedEmployeeID == "" {
			addSet("assigned_employee_id", nil)
		} else if eid, e := uuid.Parse(*in.AssignedEmployeeID); e == nil {
			var ok bool
			h.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM employees WHERE id=$1 AND tenant_id=$2 AND deleted_at IS NULL)`, eid, tenantID).Scan(&ok)
			if !ok {
				faErr(c, http.StatusBadRequest, "INVALID_INPUT", "Xodim topilmadi", "Сотрудник не найден")
				return
			}
			addSet("assigned_employee_id", eid)
		}
	}
	if in.ConstructionObjectID != nil {
		if *in.ConstructionObjectID == "" {
			addSet("construction_object_id", nil)
		} else if oid, e := strconv.ParseInt(*in.ConstructionObjectID, 10, 64); e == nil && oid > 0 {
			var ok bool
			h.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM construction_projects WHERE id=$1 AND tenant_id=$2)`, oid, tenantID).Scan(&ok)
			if !ok {
				faErr(c, http.StatusBadRequest, "INVALID_INPUT", "Obyekt topilmadi", "Объект не найден")
				return
			}
			addSet("construction_object_id", oid)
		}
	}
	if in.DepartmentID != nil {
		if did, e := uuid.Parse(*in.DepartmentID); e == nil {
			var ok bool
			h.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM fa_departments WHERE id=$1 AND tenant_id=$2)`, did, tenantID).Scan(&ok)
			if !ok {
				faErr(c, http.StatusBadRequest, "INVALID_INPUT", "Bo'lim topilmadi", "Подразделение не найдено")
				return
			}
			addSet("department_id", did)
		}
	}
	if len(sets) == 0 {
		faErr(c, http.StatusBadRequest, "NOTHING_TO_CHANGE", "O'zgartirish yo'q", "Нечего менять")
		return
	}
	args = append(args, assetID, tenantID)
	q := fmt.Sprintf("UPDATE fa_assets SET %s, updated_at=NOW() WHERE id=$%d AND tenant_id=$%d", strings.Join(sets, ", "), arg, arg+1)
	if _, err := h.db.Exec(q, args...); err != nil {
		faErr(c, http.StatusInternalServerError, "SAVE_FAILED", "Xatolik", "Ошибка")
		return
	}
	h.faAudit(tenantID, userID, "fa_asset", assetID, "update_info", nil,
		map[string]interface{}{"name": in.Name, "assigned_employee_id": in.AssignedEmployeeID, "construction_object_id": in.ConstructionObjectID, "department_id": in.DepartmentID})
	faOK(c, gin.H{"id": assetID, "message": "Yangilandi"})
}

func (h *Handler) GetAsset(c *gin.Context) {
	tenantID, _ := middleware.GetTenantID(c)
	assetID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		faErr(c, http.StatusBadRequest, "INVALID_INPUT", "ID noto'g'ri", "Некорректный ID")
		return
	}
	var out struct {
		ID                string     `json:"id"`
		InventoryNumber   string     `json:"inventory_number"`
		Name              string     `json:"name"`
		Status            string     `json:"status"`
		Cost              float64    `json:"cost"`
		Salvage           float64    `json:"salvage_value"`
		Life              int        `json:"useful_life_months"`
		Method            string     `json:"method"`
		Accumulated       float64    `json:"accumulated_depreciation"`
		BookValue         float64    `json:"book_value"`
		CategoryID        string     `json:"category_id"`
		CategoryName      string     `json:"category_name"`
		DepartmentID      string     `json:"department_id"`
		DepartmentName    string     `json:"department_name"`
		SerialNumber      string     `json:"serial_number"`
		Location          string     `json:"location"`
		Notes             string     `json:"notes"`
		PurchaseDate      *time.Time `json:"purchase_date"`
		CommissioningDate *time.Time `json:"commissioning_date"`
		DisposalDate      *time.Time `json:"disposal_date"`
		DisposalAmount    *float64   `json:"disposal_amount"`
		DisposalReason    string     `json:"disposal_reason"`
		SupplierID        *string    `json:"supplier_id"`
		SupplierName      string     `json:"supplier_name"`
		DocNumber         string     `json:"doc_number"`
		AssignedEmpID     *string    `json:"assigned_employee_id"`
		AssignedEmpName   string     `json:"assigned_employee_name"`
		ObjectID          *string    `json:"construction_object_id"`
		ObjectName        string     `json:"construction_object_name"`
		PurchaseOrderID   *string    `json:"purchase_order_id"`
		EffAsset          string     `json:"effective_asset_account"`
		EffDepr           string     `json:"effective_depreciation_account"`
		EffExpense        string     `json:"effective_expense_account"`
	}
	var purchaseDate time.Time
	var comm, disp sql.NullTime
	var dispAmt sql.NullFloat64
	var serial, location, notes, dispReason, supName, empName, objName, docNum sql.NullString
	var supID, empID, objID, poID sql.NullString
	err = h.db.QueryRow(`
		SELECT a.id, a.inventory_number, a.name, a.status::text, a.cost, a.salvage_value, a.useful_life_months,
		       a.method, a.accumulated_depreciation, a.category_id, c.name_uz, a.department_id, d.name_uz,
		       a.serial_number, a.location, a.notes, a.purchase_date, a.commissioning_date,
		       a.disposal_date, a.disposal_amount, a.disposal_reason,
		       a.supplier_id::text, COALESCE(ct.name, ''), a.doc_number,
		       a.assigned_employee_id::text, COALESCE(e.first_name || ' ' || e.last_name, ''),
		       a.construction_object_id::text, COALESCE(cp.name, ''), a.purchase_order_id::text
		FROM fa_assets a
		JOIN fa_categories c ON c.id = a.category_id
		JOIN fa_departments d ON d.id = a.department_id
		LEFT JOIN contacts ct ON ct.id = a.supplier_id
		LEFT JOIN employees e ON e.id = a.assigned_employee_id
		LEFT JOIN construction_projects cp ON cp.id = a.construction_object_id
		WHERE a.id=$1 AND a.tenant_id=$2 AND a.deleted_at IS NULL`, assetID, tenantID).
		Scan(&out.ID, &out.InventoryNumber, &out.Name, &out.Status, &out.Cost, &out.Salvage, &out.Life,
			&out.Method, &out.Accumulated, &out.CategoryID, &out.CategoryName, &out.DepartmentID, &out.DepartmentName,
			&serial, &location, &notes, &purchaseDate, &comm, &disp, &dispAmt, &dispReason,
			&supID, &supName, &docNum, &empID, &empName, &objID, &objName, &poID)
	if err != nil {
		faErr(c, http.StatusNotFound, "NOT_FOUND", "Aktiv topilmadi", "Актив не найден")
		return
	}
	out.BookValue = out.Cost - out.Accumulated
	out.SerialNumber, out.Location, out.Notes = serial.String, location.String, notes.String
	out.DisposalReason, out.SupplierName, out.DocNumber = dispReason.String, supName.String, docNum.String
	out.AssignedEmpName, out.ObjectName = empName.String, objName.String
	out.PurchaseDate = &purchaseDate
	if comm.Valid {
		out.CommissioningDate = &comm.Time
	}
	if disp.Valid {
		out.DisposalDate = &disp.Time
	}
	if dispAmt.Valid {
		out.DisposalAmount = &dispAmt.Float64
	}
	for src, dst := range map[*sql.NullString]**string{&supID: &out.SupplierID, &empID: &out.AssignedEmpID, &objID: &out.ObjectID, &poID: &out.PurchaseOrderID} {
		if src.Valid && src.String != "" {
			s := src.String
			*dst = &s
		}
	}
	eff, _ := h.effectiveAccountsForAsset(tenantID, assetID)
	out.EffAsset, out.EffDepr, out.EffExpense = eff.AssetCode, eff.DepreciationCode, eff.ExpenseCode
	faOK(c, out)
}

// faAudit writes an audit_logs row (best-effort).
func (h *Handler) faAudit(tenantID, userID uuid.UUID, entityType string, entityID uuid.UUID, action string, oldV, newV map[string]interface{}) {
	ob, _ := json.Marshal(oldV)
	nb, _ := json.Marshal(newV)
	var uid interface{}
	if userID != uuid.Nil {
		uid = userID
	}
	h.db.Exec(`INSERT INTO audit_logs (tenant_id, user_id, action, entity_type, entity_id, old_values, new_values, created_at)
		VALUES ($1,$2,$3,$4,$5,$6::jsonb,$7::jsonb,NOW())`, tenantID, uid, action, entityType, entityID, string(ob), string(nb))
}

func parseDatePtr(s string) interface{} {
	if d, err := time.Parse("2006-01-02", s); err == nil {
		return d
	}
	return nil
}
