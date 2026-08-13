package handler

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
)

// Zaxiralarni baholash, bosqich 4: qulflar (plan §2).
//
// The valuation method stops being editable once a category's goods have
// actually moved. Changing it afterwards would reinterpret history: the same
// layers, read by a different algorithm, produce a different cost of sale for
// invoices that are already posted and already declared.
//
// The trigger is a MOVEMENT, not the presence of products (§2.1). A category
// with goods attached but nothing yet received can still be switched — that is
// a real and common case during setup, and locking it would force people to
// delete and recreate categories to fix a first-day mistake.

// dbRowsQuerier is dbQuerier plus Query. Both *sql.DB and *sql.Tx satisfy it,
// so a lock check reads the same whether it runs standalone or inside the
// transaction that is about to write.
type dbRowsQuerier interface {
	QueryRow(query string, args ...interface{}) *sql.Row
	Query(query string, args ...interface{}) (*sql.Rows, error)
}

// MethodLock is why a category's method is or is not editable.
type MethodLock struct {
	Locked        bool   `json:"locked"`
	MovementCount int    `json:"movement_count"`
	Since         string `json:"since,omitempty"` // YYYY-MM-DD of the earliest movement
	Reason        string `json:"reason,omitempty"`
}

// evaluateMethodLock is the rule, separated from the query so it can be tested
// without a database and so every caller reaches the same verdict.
//
// The message is written for the person who hits it: it says how many movements
// and since when, because "locked" on its own gives them nothing to act on.
func evaluateMethodLock(movementCount int, earliest *time.Time) MethodLock {
	if movementCount <= 0 {
		return MethodLock{Locked: false}
	}
	lock := MethodLock{Locked: true, MovementCount: movementCount}
	if earliest != nil {
		lock.Since = earliest.Format("2006-01-02")
		lock.Reason = fmt.Sprintf("Usul qulflangan: kategoriya bo'yicha %d harakat, %s dan",
			movementCount, earliest.Format("02.01.2006"))
	} else {
		lock.Reason = fmt.Sprintf("Usul qulflangan: kategoriya bo'yicha %d harakat", movementCount)
	}
	return lock
}

// categoryMethodLock counts posted valuation layers across a category's
// products.
//
// Reversed layers are excluded: a receipt that was storno'd never really
// happened, and holding a category hostage to a cancelled document would be a
// lock nobody could clear.
func (h *Handler) categoryMethodLock(q dbQuerier, tenantID, categoryID uuid.UUID) (MethodLock, error) {
	var count int
	var earliest sql.NullTime
	err := q.QueryRow(`
		SELECT COUNT(*), MIN(svl.layer_date)
		FROM stock_valuation_layers svl
		JOIN products p ON p.id = svl.product_id
		WHERE svl.tenant_id = $1 AND p.category_id = $2 AND svl.is_reversed = false`,
		tenantID, categoryID).Scan(&count, &earliest)
	if err != nil {
		return MethodLock{}, err
	}
	var at *time.Time
	if earliest.Valid {
		at = &earliest.Time
	}
	return evaluateMethodLock(count, at), nil
}

// GetCategoryMethodLock godoc
// @Summary Kategoriya usuli qulflanganmi
// @Description Whether the category's valuation method can still be changed, and why not
// @Tags Inventory - Valuation
// @Produce json
// @Param id path string true "Category ID"
// @Success 200 {object} response.Response
// @Security BearerAuth
// @Router /product-categories/{id}/method-lock [get]
//
// The UI needs this to show the reason beside a disabled field. It is NOT the
// enforcement — §2.1 is explicit that the check has to be repeated as a server
// validation on write, because a disabled input protects nobody using the API,
// an import, or a bulk edit.
func (h *Handler) GetCategoryMethodLock(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	categoryID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid category ID")
		return
	}

	lock, err := h.categoryMethodLock(h.db, tenantID, categoryID)
	if err != nil {
		h.log.Error("Failed to evaluate method lock", "error", err)
		response.InternalError(c, "Failed to evaluate method lock")
		return
	}

	var current sql.NullString
	_ = h.db.QueryRow(`SELECT cost_method FROM product_categories WHERE id = $1 AND tenant_id = $2`,
		categoryID, tenantID).Scan(&current)

	effective := EffectiveCostMethod(current.String, h.readValuationSetting(tenantID).Method)

	response.Success(c, gin.H{
		"category_id":      categoryID,
		"cost_method":      current.String,
		"effective_method": string(effective),
		"inherited":        current.String == "",
		"lock":             lock,
	})
}

// guardCategoryMethodChange is the write-side enforcement.
//
// Returns a user-facing message when the change must be refused, empty when it
// may proceed. Setting the method to what it already is is always allowed —
// otherwise a client that PUTs the whole category object back would be unable
// to edit the name once the lock closed.
func (h *Handler) guardCategoryMethodChange(q dbQuerier, tenantID, categoryID uuid.UUID, newMethod string) (string, error) {
	var current sql.NullString
	err := q.QueryRow(`SELECT cost_method FROM product_categories WHERE id = $1 AND tenant_id = $2`,
		categoryID, tenantID).Scan(&current)
	if err != nil {
		return "", err
	}
	if normaliseMethod(current.String) == normaliseMethod(newMethod) {
		return "", nil
	}

	lock, err := h.categoryMethodLock(q, tenantID, categoryID)
	if err != nil {
		return "", err
	}
	if lock.Locked {
		return lock.Reason, nil
	}
	return "", nil
}

// normaliseMethod maps every spelling of a method to one value so a no-op
// change is recognised as one — "aveco" and "avco" are the same choice.
func normaliseMethod(raw string) string {
	if raw == "" {
		return ""
	}
	m, err := ParseCostMethod(raw)
	if err != nil {
		return raw
	}
	return string(m)
}

// guardProductCategoryChange closes the bypass in §2.3.
//
// Without it the lock is worth nothing: a product whose category is locked to
// FIFO can be moved to an AVCO category in two clicks, and its history is
// reinterpreted anyway. Moving a product that has movements is allowed only
// when the destination resolves to the SAME effective method.
//
// Returns a user-facing message when the move must be refused.
func (h *Handler) guardProductCategoryChange(q dbQuerier, tenantID, productID, newCategoryID uuid.UUID) (string, error) {
	var oldCategoryID uuid.NullUUID
	if err := q.QueryRow(`SELECT category_id FROM products WHERE id = $1 AND tenant_id = $2`,
		productID, tenantID).Scan(&oldCategoryID); err != nil {
		return "", err
	}
	if oldCategoryID.Valid && oldCategoryID.UUID == newCategoryID {
		return "", nil
	}

	// Only this product's own movements matter. A product with no history can
	// move anywhere — there is nothing to reinterpret.
	var moved int
	var earliest sql.NullTime
	if err := q.QueryRow(`
		SELECT COUNT(*), MIN(layer_date) FROM stock_valuation_layers
		WHERE tenant_id = $1 AND product_id = $2 AND is_reversed = false`,
		tenantID, productID).Scan(&moved, &earliest); err != nil {
		return "", err
	}
	if moved == 0 {
		return "", nil
	}

	company := h.readValuationSetting(tenantID).Method
	oldMethod := CostMethodAVCO
	if oldCategoryID.Valid {
		var m sql.NullString
		_ = q.QueryRow(`SELECT cost_method FROM product_categories WHERE id = $1`, oldCategoryID.UUID).Scan(&m)
		oldMethod = EffectiveCostMethod(m.String, company)
	} else {
		oldMethod = EffectiveCostMethod("", company)
	}
	var newRaw sql.NullString
	if err := q.QueryRow(`SELECT cost_method FROM product_categories WHERE id = $1 AND tenant_id = $2`,
		newCategoryID, tenantID).Scan(&newRaw); err != nil {
		return "", err
	}
	newMethod := EffectiveCostMethod(newRaw.String, company)

	if oldMethod == newMethod {
		return "", nil
	}
	return fmt.Sprintf(
		"Harakat tarixi bor tovarni boshqa baholash usulidagi kategoriyaga ko'chirib bo'lmaydi (%s → %s, %d harakat)",
		oldMethod, newMethod, moved), nil
}

// guardAVCOChronology is §2.4's second rule.
//
// Under AVCO the running average is rebuilt movement by movement, so a document
// inserted BEFORE the last movement would have had to change every average
// computed after it — and those costs are already posted. FIFO does not care
// (layers are consumed by date and a late insert simply takes its place) and
// standard does not care at all, so this is checked only for AVCO, exactly as
// the plan recommends for v1.
//
// The period lock is NOT re-implemented here: migration 483 installed a trigger
// that refuses any posting into a locked or closed fiscal period, and every
// stock movement posts. A second date check in Go would be a second definition
// that drifts.
func guardAVCOChronology(method CostMethod, docDate time.Time, lastMovement *time.Time) string {
	if method != CostMethodAVCO || lastMovement == nil {
		return ""
	}
	if docDate.Before(*lastMovement) {
		return fmt.Sprintf(
			"O'rtacha tortilgan usulida oxirgi harakatdan (%s) oldingi sana bilan hujjat kiritib bo'lmaydi",
			lastMovement.Format("02.01.2006"))
	}
	return ""
}

// ---------------------------------------------------------------- storno ---

// StornoPlan is what cancelling a document would do, decided before anything is
// written.
type StornoPlan struct {
	// Layers this document created, to be marked reversed.
	ReceiptLayers []string
	// Consumptions this document made, whose value returns to its layer.
	Consumptions []Consumption
	// Set when the cancellation must be refused, with the reason.
	Blocked string
}

// planStorno works out how to reverse a document's stock effect (§2.6).
//
// Documents are never deleted. A receipt's layer is marked reversed; an issue's
// consumption is given back to the layer it came from.
//
// The case that has to be refused: a receipt whose goods have since been
// ISSUED. Reversing it would leave those issues costed against a layer that no
// longer exists, so the dependent issues must be cancelled first. Silently
// allowing it is how a warehouse ends up with a negative layer and a cost of
// sale nobody can trace.
func (h *Handler) planStorno(q dbRowsQuerier, tenantID uuid.UUID, sourceType string, sourceID uuid.UUID) (StornoPlan, error) {
	var plan StornoPlan

	rows, err := q.Query(`
		SELECT id, quantity, remaining_qty
		FROM stock_valuation_layers
		WHERE tenant_id = $1 AND source_type = $2 AND source_doc_id = $3
		  AND is_reversed = false`,
		tenantID, sourceType, sourceID)
	if err != nil {
		return plan, err
	}
	for rows.Next() {
		var id string
		var qty, remaining float64
		if err := rows.Scan(&id, &qty, &remaining); err != nil {
			rows.Close()
			return plan, err
		}
		if remaining < qty {
			rows.Close()
			plan.Blocked = "Bu kirimning tovari allaqachon chiqim qilingan — avval bog'liq chiqim hujjatlarini bekor qiling"
			return plan, nil
		}
		plan.ReceiptLayers = append(plan.ReceiptLayers, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return plan, err
	}
	rows.Close()

	crows, err := q.Query(`
		SELECT layer_id, quantity, value
		FROM stock_valuation_consumptions
		WHERE tenant_id = $1 AND source_type = $2 AND source_doc_id = $3
		  AND is_reversed = false`,
		tenantID, sourceType, sourceID)
	if err != nil {
		return plan, err
	}
	defer crows.Close()
	for crows.Next() {
		var layerID string
		var qty, value float64
		if err := crows.Scan(&layerID, &qty, &value); err != nil {
			return plan, err
		}
		plan.Consumptions = append(plan.Consumptions, Consumption{
			LayerID: layerID, Qty: floatToRat(qty), Value: toTiyin(value),
		})
	}
	return plan, crows.Err()
}

// applyStorno writes what planStorno decided. Caller supplies the transaction;
// every failure is returned so the whole cancellation rolls back together.
func applyStorno(tx *sql.Tx, plan StornoPlan) error {
	if plan.Blocked != "" {
		return fmt.Errorf("%s", plan.Blocked)
	}
	for _, c := range plan.Consumptions {
		qty, _ := c.Qty.Float64()
		if _, err := tx.Exec(`
			UPDATE stock_valuation_layers
			SET remaining_qty = remaining_qty + $1, remaining_value = remaining_value + $2
			WHERE id = $3`, qty, fromTiyin(c.Value), c.LayerID); err != nil {
			return err
		}
	}
	if len(plan.Consumptions) > 0 {
		// Marked rather than deleted, so the audit trail still shows the issue
		// happened and was reversed.
		for _, c := range plan.Consumptions {
			if _, err := tx.Exec(`
				UPDATE stock_valuation_consumptions SET is_reversed = true
				WHERE layer_id = $1 AND is_reversed = false`, c.LayerID); err != nil {
				return err
			}
		}
	}
	for _, id := range plan.ReceiptLayers {
		if _, err := tx.Exec(`
			UPDATE stock_valuation_layers SET is_reversed = true, remaining_qty = 0, remaining_value = 0
			WHERE id = $1`, id); err != nil {
			return err
		}
	}
	return nil
}

// StornoStockDocument — §2.6 ni HTTP orqali ochadi.
//
// POST /inventory/valuation/storno  {"source_type": "...", "source_id": "..."}
//
// NIMA UCHUN ALOHIDA ENDPOINT
// Reja §2.6 stornoni birinchi darajali amal deb belgilaydi: "Hujjatni bekor
// qilish = storno, o'chirish emas". Mexanizm (planStorno + applyStorno)
// yozilgan edi, lekin StornoDocument ni hech kim chaqirmasdi — ya'ni amal
// mavjud, ammo yetib bo'lmaydigan edi.
//
// Nega hujjat bekor qilish yo'llariga ulanmadi: hozir bironta ham yo'l
// o'tkazilgan hujjatning zaxirasini stornosiz bekor qila olmaydi —
// tekshirildi, oltitasining hammasi (skrap, ombor amali, tovar qabuli,
// kompaniyalararo ko'chirish, dropshipping, landed cost) yo o'tkazilgandan
// keyin bekor qilishni RAD etadi, yoki qarama-qarshi harakat yozadi. Ya'ni
// bugun qatlamlar "osilib" qolmaydi. Ularning ichiga storno qo'shish esa ikki
// karra tuzatish bo'lardi: qarama-qarshi harakat allaqachon yangi qatlam
// yaratadi, storno esa eskisini tiklaydi.
//
// Shuning uchun storno aniq, qo'lda chaqiriladigan tuzatish amali sifatida
// ochiladi. U provodkalar konveyerga o'tgach (§6, 2-bosqich) kerak bo'ladi:
// o'shanda bekor qilish qarama-qarshi harakat emas, aynan storno bo'lishi
// kerak, aks holda FIFO da qaytgan qiymat asl qatlamga emas, bugungi narxdagi
// yangi qatlamga tushadi.
//
// planStorno iste'mol qilingan kirim qatlamini bekor qilishni RAD etadi
// (Blocked) — avval unga bog'liq chiqimlar stornolanishi kerak.
func (h *Handler) StornoStockDocument(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	var in struct {
		SourceType string `json:"source_type" binding:"required"`
		SourceID   string `json:"source_id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		response.BadRequest(c, "source_type va source_id majburiy")
		return
	}
	sourceID, err := uuid.Parse(in.SourceID)
	if err != nil {
		response.BadRequest(c, "source_id noto'g'ri")
		return
	}

	tx, err := h.db.Begin()
	if err != nil {
		response.InternalError(c, "Failed to start transaction")
		return
	}
	defer tx.Rollback()

	if err := h.StornoDocument(tx, tenantID, in.SourceType, sourceID); err != nil {
		// planStorno'ning "Blocked" sababi ham shu yerdan chiqadi — u
		// foydalanuvchiga tushunarli matn, ichki xato emas.
		response.BadRequest(c, err.Error())
		return
	}
	if err := tx.Commit(); err != nil {
		response.InternalError(c, "Failed to commit storno")
		return
	}

	response.Success(c, gin.H{
		"source_type": in.SourceType,
		"source_id":   sourceID,
		"stornoed":    true,
	})
}
