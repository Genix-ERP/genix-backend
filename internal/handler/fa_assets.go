package handler

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ============================================================================
// Fixed Assets v1 — asset lifecycle + lifecycle postings (§4, §5, §9).
// ============================================================================

// Fixed posting accounts from the §5 templates (not type-dependent).
const (
	faAcctAcquisition = "0820" // capital investment: purchase lands here, cleared on commission
	faAcctAP          = "6010" // accounts payable (supplier debt)
	faAcctCash        = "5010"
	faAcctBank        = "5110"
	faAcctVATInput    = "4410"
	faAcctDisposal    = "9210" // disposal of fixed assets
)

type createAssetInput struct {
	Name              string  `json:"name"`
	CategoryID        string  `json:"category_id"`
	DepartmentID      string  `json:"department_id"`
	SerialNumber      string  `json:"serial_number"`
	Location          string  `json:"location"`
	PurchaseDate      string  `json:"purchase_date"` // YYYY-MM-DD
	Cost              float64 `json:"cost"`
	SalvageValue      float64 `json:"salvage_value"`
	UsefulLifeMonths  int     `json:"useful_life_months"`
	Method            string  `json:"method"`
	VatAmount         float64 `json:"vat_amount"`
	SupplierID        string  `json:"supplier_id"`
	PaymentMethod     string  `json:"payment_method"` // 'cash' | 'credit'
	DocNumber         string  `json:"doc_number"`
	DocDate           string  `json:"doc_date"`
	CommissionNow     *bool   `json:"commission_now"`     // default true (§2.3 "darhol")
	CommissioningDate string  `json:"commissioning_date"` // used when CommissionNow is false
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

	assetID := uuid.New()
	_, err = tx.Exec(`
		INSERT INTO fa_assets (
			id, tenant_id, organization_id, inventory_number, name, category_id, department_id,
			serial_number, location, purchase_date, cost, salvage_value, useful_life_months, method,
			status, supplier_id, payment_method, doc_number, doc_date, created_by, created_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,'draft',$15,$16,$17,$18,$19,NOW(),NOW())`,
		assetID, tenantID, orgPtr, inv, strings.TrimSpace(in.Name), catID, deptID,
		nullIfEmpty(in.SerialNumber), nullIfEmpty(in.Location), purchaseDate, in.Cost, in.SalvageValue,
		in.UsefulLifeMonths, method, supplierPtr, nullIfEmpty(in.PaymentMethod), nullIfEmpty(in.DocNumber),
		parseDatePtr(in.DocDate), userID)
	if err != nil {
		faErr(c, http.StatusInternalServerError, "SAVE_FAILED", "Aktivni saqlab bo'lmadi", "Не удалось сохранить актив")
		return
	}

	journalID, nextNum, prefix := faGetJournal(tx, tenantID)

	// Purchase: Дт 0820 Кт 6010 (cost ex-VAT). Always booked, even for cash.
	if _, e := h.faPostJournal(tx, tenantID, orgPtr, journalID,
		fmt.Sprintf("%s%06d", prefix, nextNum), inv+"-BUY", "Aktiv xaridi: "+in.Name, "asset_purchase", assetID, purchaseDate,
		[]faJELine{{Code: faAcctAcquisition, Debit: in.Cost, Desc: "Kapital qo'yilma"}, {Code: faAcctAP, Credit: in.Cost, Desc: "Ta'minotchiga qarz"}}); e != nil {
		faErr(c, http.StatusBadRequest, "POSTING_FAILED", "Xarid provodkasi: "+e.Error(), "Проводка покупки: "+e.Error())
		return
	}
	nextNum++

	// Optional VAT: Дт 4410 Кт 6010.
	if in.VatAmount > 0 {
		if _, e := h.faPostJournal(tx, tenantID, orgPtr, journalID,
			fmt.Sprintf("%s%06d", prefix, nextNum), inv+"-VAT", "QQS: "+in.Name, "asset_vat", assetID, purchaseDate,
			[]faJELine{{Code: faAcctVATInput, Debit: in.VatAmount, Desc: "QQS hisobga olish"}, {Code: faAcctAP, Credit: in.VatAmount, Desc: "Ta'minotchiga qarz (QQS)"}}); e != nil {
			faErr(c, http.StatusBadRequest, "POSTING_FAILED", "QQS provodkasi: "+e.Error(), "Проводка НДС: "+e.Error())
			return
		}
		nextNum++
	}

	// Payment (cash): Дт 6010 Кт 5010/5110. Credit purchase leaves debt on 6010.
	if in.PaymentMethod == "cash" || in.PaymentMethod == "bank" {
		payCredit := faAcctCash
		if in.PaymentMethod == "bank" {
			payCredit = faAcctBank
		}
		if _, e := h.faPostJournal(tx, tenantID, orgPtr, journalID,
			fmt.Sprintf("%s%06d", prefix, nextNum), inv+"-PAY", "To'lov: "+in.Name, "asset_payment", assetID, purchaseDate,
			[]faJELine{{Code: faAcctAP, Debit: in.Cost + in.VatAmount, Desc: "Qarz yopilishi"}, {Code: payCredit, Credit: in.Cost + in.VatAmount, Desc: "To'lov"}}); e != nil {
			faErr(c, http.StatusBadRequest, "POSTING_FAILED", "To'lov provodkasi: "+e.Error(), "Проводка оплаты: "+e.Error())
			return
		}
		nextNum++
	}

	// Immediate commission (§2.3): Дт asset_account Кт 0820, status -> in_service.
	commissioned := false
	if commissionNow {
		if e := h.faCommissionInTx(tx, tenantID, orgPtr, assetID, in.Name, catAsset, in.Cost, purchaseDate, journalID, &nextNum, prefix); e != nil {
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

	faOK(c, gin.H{"id": assetID, "inventory_number": inv, "status": statusOf(commissioned)})
}

func statusOf(commissioned bool) string {
	if commissioned {
		return "in_service"
	}
	return "draft"
}

// faCommissionInTx books the commission entry and flips status to in_service.
func (h *Handler) faCommissionInTx(tx *sql.Tx, tenantID uuid.UUID, orgPtr *uuid.UUID, assetID uuid.UUID, name, assetAccount string, cost float64, commDate time.Time, journalID uuid.UUID, nextNum *int, prefix string) error {
	if _, e := h.faPostJournal(tx, tenantID, orgPtr, journalID,
		fmt.Sprintf("%s%06d", prefix, *nextNum), "COMM", "Foydalanishga topshirish: "+name, "asset_commission", assetID, commDate,
		[]faJELine{{Code: assetAccount, Debit: cost, Desc: "Asosiy vosita"}, {Code: faAcctAcquisition, Credit: cost, Desc: "Kapital qo'yilma yopilishi"}}); e != nil {
		return e
	}
	*nextNum++
	_, err := tx.Exec(`UPDATE fa_assets SET status='in_service', commissioning_date=$1, updated_at=NOW() WHERE id=$2`, commDate, assetID)
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
	eff, err := h.effectiveAccountsForAsset(assetID)
	if err != nil || eff.AssetCode == "" {
		faErr(c, http.StatusBadRequest, "MAPPING_MISSING", "Aktiv hisobi sozlanmagan", "Счёт актива не настроен")
		return
	}
	effAsset := eff.AssetCode

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
	if e := h.faCommissionInTx(tx, tenantID, orgPtr, assetID, name, effAsset, cost, commDate, journalID, &nextNum, prefix); e != nil {
		faErr(c, http.StatusBadRequest, "POSTING_FAILED", "Ishga tushirish: "+e.Error(), "Ввод: "+e.Error())
		return
	}
	faBumpJournalNumber(tx, journalID, nextNum)
	if err := tx.Commit(); err != nil {
		faErr(c, http.StatusInternalServerError, "TX_FAILED", "Xatolik", "Ошибка")
		return
	}
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
	DisposalDate string `json:"disposal_date"`
	Reason       string `json:"reason"`
}

// DisposeAsset tops up depreciation for the disposal month (§6.3) then writes the
// asset off: Дт 9210 Кт asset_account (cost); Дт depreciation_account Кт 9210
// (accumulated). Terminal (§4).
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
	period := disposalDate.Format("2006-01")
	if msg := h.checkPeriodLock(tenantID, disposalDate); msg != "" {
		faErr(c, http.StatusBadRequest, "PERIOD_CLOSED", msg, msg)
		return
	}

	var name, status string
	var cost, salvage, accumulated float64
	var lifeMonths int
	err = h.db.QueryRow(`SELECT name, status::text, cost, salvage_value, useful_life_months, accumulated_depreciation
		FROM fa_assets WHERE id=$1 AND tenant_id=$2 AND deleted_at IS NULL`, assetID, tenantID).
		Scan(&name, &status, &cost, &salvage, &lifeMonths, &accumulated)
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
	eff, err := h.effectiveAccountsForAsset(assetID)
	if err != nil {
		faErr(c, http.StatusInternalServerError, "MAPPING_MISSING", "Hisoblar topilmadi", "Счета не найдены")
		return
	}

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
			tx.QueryRow(`SELECT COUNT(*) FROM fa_depreciation_entries WHERE asset_id=$1 AND status='active'`, assetID).Scan(&priorPeriods)
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

	// §5 disposal postings. Дт 9210 Кт asset (cost); Дт depreciation Кт 9210 (accum).
	lines := []faJELine{{Code: faAcctDisposal, Debit: cost, Desc: "Chiqarish (balans qiymati)"}, {Code: eff.AssetCode, Credit: cost, Desc: "Asosiy vosita hisobdan chiqarish"}}
	if accumulated > 0 && eff.DepreciationCode != "" {
		lines = append(lines, faJELine{Code: eff.DepreciationCode, Debit: accumulated, Desc: "Yig'ilgan amortizatsiya yopilishi"}, faJELine{Code: faAcctDisposal, Credit: accumulated, Desc: "Chiqarish"})
	}
	if _, e := h.faPostJournal(tx, tenantID, orgPtr, journalID,
		fmt.Sprintf("%s%06d", prefix, nextNum), name+"-DISP", "Aktiv chiqarilishi: "+name, "asset_disposal", assetID, disposalDate, lines); e != nil {
		faErr(c, http.StatusBadRequest, "POSTING_FAILED", "Chiqarish: "+e.Error(), "Выбытие: "+e.Error())
		return
	}
	nextNum++

	if _, err := tx.Exec(`UPDATE fa_assets SET status='disposed', disposal_date=$1, notes=COALESCE(notes,'')||$2, updated_at=NOW() WHERE id=$3`,
		disposalDate, "\nChiqarish sababi: "+in.Reason, assetID); err != nil {
		faErr(c, http.StatusInternalServerError, "SAVE_FAILED", "Xatolik", "Ошибка")
		return
	}
	faBumpJournalNumber(tx, journalID, nextNum)
	if err := tx.Commit(); err != nil {
		faErr(c, http.StatusInternalServerError, "TX_FAILED", "Xatolik", "Ошибка")
		return
	}
	faOK(c, gin.H{"id": assetID, "status": "disposed"})
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
	h.db.QueryRow(`SELECT COUNT(*) FROM fa_depreciation_entries WHERE asset_id=$1 AND status='active'`, assetID).Scan(&prior0)
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
		sched = append(sched, row{Period: p.Format("2006-01"), Amount: amt, Accumulated: acc, BookValue: cost - acc})
		p = p.AddDate(0, 1, 0)
	}
	faOK(c, sched)
}

// ListAssets / GetAsset — read views over the register (§2.1).
func (h *Handler) ListAssets(c *gin.Context) {
	tenantID, _ := middleware.GetTenantID(c)
	rows, err := h.db.Query(`
		SELECT a.id, a.inventory_number, a.name, a.status::text, a.cost, a.salvage_value, a.useful_life_months,
		       a.accumulated_depreciation, (a.cost - a.accumulated_depreciation) AS book_value,
		       c.name_uz, d.name_uz, COALESCE(a.serial_number,''), a.purchase_date, a.commissioning_date
		FROM fa_assets a
		JOIN fa_categories c ON c.id = a.category_id
		JOIN fa_departments d ON d.id = a.department_id
		WHERE a.tenant_id=$1 AND a.deleted_at IS NULL
		ORDER BY a.inventory_number`, tenantID)
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
		CategoryName      string     `json:"category_name"`
		DepartmentName    string     `json:"department_name"`
		SerialNumber      string     `json:"serial_number"`
		PurchaseDate      time.Time  `json:"purchase_date"`
		CommissioningDate *time.Time `json:"commissioning_date"`
	}
	out := []asset{}
	for rows.Next() {
		var a asset
		var comm sql.NullTime
		if rows.Scan(&a.ID, &a.InventoryNumber, &a.Name, &a.Status, &a.Cost, &a.SalvageValue, &a.UsefulLifeMonths,
			&a.Accumulated, &a.BookValue, &a.CategoryName, &a.DepartmentName, &a.SerialNumber, &a.PurchaseDate, &comm) == nil {
			if comm.Valid {
				a.CommissioningDate = &comm.Time
			}
			out = append(out, a)
		}
	}
	faOK(c, out)
}

func (h *Handler) GetAsset(c *gin.Context) {
	tenantID, _ := middleware.GetTenantID(c)
	assetID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		faErr(c, http.StatusBadRequest, "INVALID_INPUT", "ID noto'g'ri", "Некорректный ID")
		return
	}
	eff, _ := h.effectiveAccountsForAsset(assetID)
	var out struct {
		ID              string  `json:"id"`
		InventoryNumber string  `json:"inventory_number"`
		Name            string  `json:"name"`
		Status          string  `json:"status"`
		Cost            float64 `json:"cost"`
		Salvage         float64 `json:"salvage_value"`
		Life            int     `json:"useful_life_months"`
		Accumulated     float64 `json:"accumulated_depreciation"`
		CategoryID      string  `json:"category_id"`
		DepartmentID    string  `json:"department_id"`
		EffAsset        string  `json:"effective_asset_account"`
		EffDepr         string  `json:"effective_depreciation_account"`
		EffExpense      string  `json:"effective_expense_account"`
	}
	err = h.db.QueryRow(`SELECT id, inventory_number, name, status::text, cost, salvage_value, useful_life_months,
		accumulated_depreciation, category_id, department_id
		FROM fa_assets WHERE id=$1 AND tenant_id=$2 AND deleted_at IS NULL`, assetID, tenantID).
		Scan(&out.ID, &out.InventoryNumber, &out.Name, &out.Status, &out.Cost, &out.Salvage, &out.Life,
			&out.Accumulated, &out.CategoryID, &out.DepartmentID)
	if err != nil {
		faErr(c, http.StatusNotFound, "NOT_FOUND", "Aktiv topilmadi", "Актив не найден")
		return
	}
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
