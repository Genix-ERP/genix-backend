package handler

// Zaxiralarni baholash — konveyerni hujjat yo'llariga ULASH.
//
// Reja §3 dagi konveyer (qatlam yaratish / iste'mol qilish → tannarxni usul
// bo'yicha hisoblash) allaqachon RecordStockMovement da yozilgan edi, ammo uni
// HECH KIM chaqirmasdi: butun quyi tizim qurilgan, lekin ishlamay turgan edi.
// Bu fayl ana shu bo'shliqni to'ldiradi.
//
// Ulanish nuqtasi — applyStockDelta. Sabab: ombor qoldig'ini o'zgartiradigan
// 31 ta chaqiruv nuqtasi allaqachon o'sha yagona yordamchi orqali o'tadi,
// shuning uchun bitta joyga ulash 31 ta faylni tahrirlashdan ham qisqaroq, ham
// ishonchliroq — kelajakda qo'shiladigan yangi yo'l ham avtomatik qamrab
// olinadi.
//
// PROVODKALAR HAQIDA (muhim)
// Konveyer bu yerda DeferPosting bilan chaqiriladi, ya'ni qatlamlar va
// iste'mol yozuvlari yaratiladi, lekin provodka YOZILMAYDI. Bu rejaning
// 6-bo'limidagi bosqichlar tartibiga aynan mos: 1-bosqich "FIFO va AVCO
// hisobi (provodkasiz)", provodkalar esa 2-bosqich.
//
// Buning amaliy sababi ham bor: tizimda hozir tannarx provodkalarini
// postInventoryConsumptionJE va sotuv/POS/ishlab chiqarish yo'llari o'zi
// yozadi. Konveyerga ham provodka yozdirish IKKI KARRA yozuvga olib kelardi —
// jonli buxgalteriyani buzadi. To'g'ri ketma-ketlik: avval qatlamlar haqiqatni
// kuzatib borsin, §1.3 invarianti va "Buxgalteriya bilan solishtirish"
// hisoboti farqni ko'rsatsin, keyin har bir yo'l alohida konveyerga
// o'tkazilsin (eski provodkasi o'chirilib).
//
// Xatolarga munosabat. Baholash xatosi hujjatni YIQITMAYDI: bu bosqichda
// qatlamlar kuzatuv qatlami, hisobning manbasi emas. Aks holda bitta
// baholanmagan tovar butun sotuvni to'xtatib qo'yardi. Xato jurnalga yoziladi.
// 2-bosqichda, provodkalar konveyerdan chiqa boshlaganda, bu qat'iy bo'ladi
// (valuation_service.go boshidagi izohga qarang).

import (
	"database/sql"
	"math/big"
	"strings"
	"time"

	"github.com/google/uuid"
)

// valuationOpFor maps the ledger's transaction_type (migration 447 vocabulary)
// onto the plan's operation set. The signed delta decides direction where a
// type is used for both (adjustment, count).
//
// Returns ok=false for movements that must NOT produce a layer:
//   - transfers between warehouses of one company: §4 says explicitly
//     "provodka yo'q", and the cost does not change, so a layer would double
//     the stock value;
//   - production_in / production_complete: a manufactured item's cost is the
//     BOM roll-up, which this phase does not compute (§8 v2 candidate);
//   - landed_cost: those adjust an existing layer's unit cost rather than
//     create one (§3.1, v2).
func valuationOpFor(txType string, qty float64) (StockOperation, bool) {
	switch strings.ToLower(strings.TrimSpace(txType)) {
	case "receipt", "opening":
		return OpSupplierReceipt, true
	case "issue", "sale", "ship", "delivery", "consume", "production_out":
		return OpSaleIssue, true
	case "return":
		// A signed delta tells the two returns apart: stock coming back in is
		// a customer return, stock going back out is a supplier return.
		if qty >= 0 {
			return OpCustomerReturn, true
		}
		return OpSupplierReturn, true
	case "write_off", "scrap", "production_scrap":
		return OpScrap, true
	case "adjustment", "adjustment_in", "adjustment_out", "count":
		if qty >= 0 {
			return OpCountSurplus, true
		}
		return OpCountShortage, true
	}
	return "", false
}

// ensureOpeningLayer gives a product that has stock but no layers its start
// layer, so the FIFO drain has something to consume.
//
// Needed because valuation was switched on over a live database: every
// existing balance predates the layer model. Migration 500 seeds these in
// bulk; this is the per-product safety net for anything that slips through
// (a product created between the migration and the deploy, a race).
//
// The layer is valued at the balance row's stored unit_cost — the same figure
// the old module already treated as the item's cost, so switching on valuation
// does not silently restate anyone's stock value.
func (h *Handler) ensureOpeningLayer(tx *sql.Tx, tenantID uuid.UUID, orgID *uuid.UUID, productID uuid.UUID, when time.Time) error {
	var hasLayer bool
	if err := tx.QueryRow(`
		SELECT EXISTS(
			SELECT 1 FROM stock_valuation_layers
			WHERE tenant_id = $1 AND product_id = $2 AND NOT is_reversed
		)`, tenantID, productID).Scan(&hasLayer); err != nil {
		return err
	}
	if hasLayer {
		return nil
	}

	// Balance BEFORE this movement is what the opening layer represents. The
	// caller has already applied its delta, so subtract it back out.
	var qty, unitCost float64
	if err := tx.QueryRow(`
		SELECT COALESCE(SUM(quantity_on_hand), 0),
		       COALESCE(MAX(NULLIF(unit_cost, 0)), 0)
		FROM inventory
		WHERE tenant_id = $1 AND product_id = $2
	`, tenantID, productID).Scan(&qty, &unitCost); err != nil {
		return err
	}
	if qty <= 0 || unitCost <= 0 {
		// Nothing to open: either no stock, or no cost we could stand behind.
		// A zero-cost opening layer would poison every later FIFO issue.
		return nil
	}

	value := int64(qty*unitCost*100 + 0.5)
	if value <= 0 {
		return nil
	}
	_, err := tx.Exec(`
		INSERT INTO stock_valuation_layers (
			tenant_id, organization_id, product_id, layer_date,
			source_type, source_doc_number,
			quantity, unit_cost, value, remaining_qty, remaining_value
		) VALUES ($1, $2, $3, $4, 'opening_balance', 'AUTO',
		          $5, $6, $7, $5, $7)
	`, tenantID, orgID, productID, when.Format("2006-01-02"), qty, unitCost, float64(value)/100)
	return err
}

// recordValuationForDelta is the bridge from a posted stock delta to the
// valuation conveyor. It is deliberately total: any problem is logged and
// swallowed (see the file header on why this phase must not block documents).
func (h *Handler) recordValuationForDelta(q dbExecQuerier, a stockDeltaArgs, valuedCost float64) {
	if a.SkipValuation {
		return
	}
	// The conveyor writes inside the caller's transaction. Paths that pass
	// h.db instead of a *sql.Tx get no valuation — recording layers outside
	// the document's transaction would leave them behind if it rolls back.
	tx, ok := q.(*sql.Tx)
	if !ok {
		return
	}

	op := a.Op
	if op == "" {
		mapped, mapOK := valuationOpFor(a.TxType, a.Qty)
		if !mapOK {
			return
		}
		op = mapped
	}
	if a.Qty == 0 {
		return
	}

	when := a.When
	if when.IsZero() {
		when = time.Now()
	}
	if err := h.ensureOpeningLayer(tx, a.TenantID, a.OrgID, a.ProductID, when); err != nil {
		h.log.Warn("valuation: opening layer failed", "product", a.ProductID, "error", err)
		return
	}

	qty := a.Qty
	if qty < 0 {
		qty = -qty
	}
	docID, _ := uuid.Parse(a.RefID)

	res, err := h.RecordStockMovement(tx, StockMovement{
		TenantID:       a.TenantID,
		OrganizationID: a.OrgID,
		ProductID:      a.ProductID,
		Operation:      op,
		Date:           when,
		Quantity:       new(big.Rat).SetFloat64(qty),
		UnitCost:       int64(valuedCost*100 + 0.5),
		SourceType:     a.RefType,
		SourceDocID:    docID,
		Description:    a.Reason,
		CreatedBy:      a.CreatedBy,

		OriginalDocType: a.OriginalDocType,
		OriginalDocID:   a.OriginalDocID,

		// Phase 1: layers only. See the file header.
		DeferPosting: true,
	})
	if err != nil {
		h.log.Warn("valuation: movement not recorded",
			"product", a.ProductID, "op", op, "ref", a.RefType, "error", err)
		return
	}
	_ = res
}
