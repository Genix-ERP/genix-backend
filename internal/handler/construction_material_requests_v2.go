package handler

// ═══════════════════════════════════════════════════════════════════════════
// Material zayavkalari v2 (migration 470)
//
// Prorab → zayavka (YANGI) → omborchi inbox (KO'RIB CHIQILMOQDA) →
//   bor: chiqim (CHIQARILGAN) | yetmasa: xarid so'rovi (XARIDDA) |
//   aralash: QISMAN TA'MINLANGAN | asossiz: RAD ETILGAN →
// kirim kelganda avto-signal → chiqim → prorab «Qabul qildim» (YOPILGAN).
//
// Legacy JSONB-items oqimi (flow='legacy', mobil ilova) o'zgarmagan; bu fayl
// faqat flow='v2' yozuvlar bilan ishlaydi. Barcha stok harakati
// applyStockDelta orqali; chiqim JE = Dr 0810 (wip mapping) / Cr zaxira
// hisobi, S4 (sub-akt) konventsiyasi bilan bir xil: chart yechilmasa chiqim
// posting'siz o'tadi (log bilan) — sozlanmagan buxgalteriya obyektdagi
// ishni bloklamaydi.
// ═══════════════════════════════════════════════════════════════════════════

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
)

const (
	mrFlowV2 = "v2"

	mrStatusNew        = "new"
	mrStatusInReview   = "in_review"
	mrStatusInPurchase = "in_purchase"
	mrStatusPartial    = "partially_fulfilled"
	mrStatusIssued     = "issued"
	mrStatusClosed     = "closed"
	mrStatusRejected   = "rejected"
	mrStatusCancelled  = "cancelled"

	mrLinePending    = "pending"
	mrLineInPurchase = "in_purchase"
	mrLinePartial    = "partial"
	mrLineIssued     = "issued"
	mrLineRejected   = "rejected"

	mrQtyEps = 1e-6
)

// mrV2ActionableStatuses — omborchi amallari (chiqarish / xaridga yuborish)
// mumkin bo'lgan holatlar.
var mrV2ActionableStatuses = map[string]bool{
	mrStatusNew:        true,
	mrStatusInReview:   true,
	mrStatusInPurchase: true,
	mrStatusPartial:    true,
}

type mrV2Header struct {
	ID                int64
	TenantID          uuid.UUID
	ProjectID         int64
	OrganizationID    uuid.NullUUID
	RequestNumber     string
	Status            string
	Priority          string
	WarehouseID       uuid.NullUUID
	RequestedByUserID uuid.NullUUID
	RequiredDate      sql.NullTime
	Notes             sql.NullString
	RejectedReason    sql.NullString
}

type mrV2Item struct {
	ID            uuid.UUID
	ProductID     uuid.UUID
	ProductName   string
	Unit          string
	QtyRequested  float64
	QtyIssued     float64
	QtyInPurchase float64
	LineStatus    string
	StockSnapshot sql.NullFloat64
	Note          sql.NullString
}

// mrV2Querier is satisfied by both *database.DB and *sql.Tx.
type mrV2Querier interface {
	Query(query string, args ...interface{}) (*sql.Rows, error)
	QueryRow(query string, args ...interface{}) *sql.Row
}

// ── umumiy yordamchilar ─────────────────────────────────────────────────────

// mrV2IsPrivileged: JWT system-admin yoki users.role owner/site_admin —
// egalik tekshiruvlarini (bekor qilish, qabul qilish) chetlab o'ta oladi.
func (h *Handler) mrV2IsPrivileged(c *gin.Context, tenantID, userID uuid.UUID) bool {
	if claims, ok := middleware.GetClaims(c); ok && claims != nil && claims.IsSystemAdmin {
		return true
	}
	var role sql.NullString
	if err := h.db.QueryRow(`SELECT role FROM users WHERE id = $1 AND tenant_id = $2`, userID, tenantID).Scan(&role); err != nil {
		return false
	}
	return role.Valid && (role.String == "owner" || role.String == "site_admin")
}

func (h *Handler) mrV2Load(q dbQuerier, tenantID uuid.UUID, id int64) (*mrV2Header, error) {
	var m mrV2Header
	err := q.QueryRow(`
		SELECT id, tenant_id, project_id, organization_id, request_number,
		       COALESCE(status, ''), COALESCE(priority, 'normal'),
		       warehouse_id, requested_by_user_id, required_date, notes, rejected_reason
		FROM construction_material_requests
		WHERE id = $1 AND tenant_id = $2 AND flow = 'v2'
	`, id, tenantID).Scan(
		&m.ID, &m.TenantID, &m.ProjectID, &m.OrganizationID, &m.RequestNumber,
		&m.Status, &m.Priority, &m.WarehouseID, &m.RequestedByUserID,
		&m.RequiredDate, &m.Notes, &m.RejectedReason,
	)
	if err != nil {
		return nil, err
	}
	return &m, nil
}

func (h *Handler) mrV2LoadItems(q mrV2Querier, tenantID uuid.UUID, requestID int64) ([]mrV2Item, error) {
	rows, err := q.Query(`
		SELECT id, product_id, COALESCE(product_name, ''), COALESCE(unit, ''),
		       qty_requested, qty_issued, qty_in_purchase, line_status, stock_snapshot, note
		FROM construction_material_request_items
		WHERE request_id = $1 AND tenant_id = $2
		ORDER BY created_at, id
	`, requestID, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []mrV2Item
	for rows.Next() {
		var it mrV2Item
		if err := rows.Scan(&it.ID, &it.ProductID, &it.ProductName, &it.Unit,
			&it.QtyRequested, &it.QtyIssued, &it.QtyInPurchase, &it.LineStatus,
			&it.StockSnapshot, &it.Note); err != nil {
			return nil, err
		}
		items = append(items, it)
	}
	return items, rows.Err()
}

// mrV2LineStatus derives a line's status from its quantities.
func mrV2LineStatus(it mrV2Item) string {
	if it.LineStatus == mrLineRejected {
		return mrLineRejected
	}
	switch {
	case it.QtyIssued >= it.QtyRequested-mrQtyEps:
		return mrLineIssued
	case it.QtyIssued > mrQtyEps:
		return mrLinePartial
	case it.QtyInPurchase > mrQtyEps:
		return mrLineInPurchase
	default:
		return mrLinePending
	}
}

// mrV2AggregateStatus — zayavka statusi qator holatlaridan agregatsiya
// qilinadi (eng «orqada» qolgan qator belgilaydi). Terminal holatlar
// (closed/cancelled/rejected) bu yerga kelmaydi — ular aniq endpointlarda
// o'rnatiladi.
func mrV2AggregateStatus(items []mrV2Item, current string) string {
	var active []mrV2Item
	for _, it := range items {
		if mrV2LineStatus(it) != mrLineRejected {
			active = append(active, it)
		}
	}
	if len(active) == 0 {
		return mrStatusRejected
	}
	allIssued := true
	anyIssued := false
	anyPurchase := false
	for _, it := range active {
		st := mrV2LineStatus(it)
		if st != mrLineIssued {
			allIssued = false
		}
		if it.QtyIssued > mrQtyEps {
			anyIssued = true
		}
		if st == mrLineInPurchase {
			anyPurchase = true
		}
	}
	switch {
	case allIssued:
		return mrStatusIssued
	case anyIssued:
		return mrStatusPartial
	case anyPurchase:
		return mrStatusInPurchase
	default:
		if current == mrStatusInReview {
			return mrStatusInReview
		}
		return mrStatusNew
	}
}

// mrV2RecomputeStatus persists derived line statuses + the aggregate request
// status inside the caller's tx and returns the new request status.
func (h *Handler) mrV2RecomputeStatus(tx *sql.Tx, tenantID uuid.UUID, requestID int64, current string) (string, error) {
	items, err := h.mrV2LoadItems(tx, tenantID, requestID)
	if err != nil {
		return current, err
	}
	for _, it := range items {
		derived := mrV2LineStatus(it)
		if derived != it.LineStatus {
			if _, err := tx.Exec(`UPDATE construction_material_request_items SET line_status = $1, updated_at = NOW() WHERE id = $2 AND tenant_id = $3`,
				derived, it.ID, tenantID); err != nil {
				return current, err
			}
		}
	}
	newStatus := mrV2AggregateStatus(items, current)
	if newStatus != current {
		fulfilled := interface{}(nil)
		if newStatus == mrStatusIssued {
			fulfilled = time.Now().Format("2006-01-02")
		}
		if _, err := tx.Exec(`
			UPDATE construction_material_requests
			SET status = $1,
			    fulfilled_date = COALESCE($2::date, fulfilled_date),
			    updated_date = NOW()
			WHERE id = $3 AND tenant_id = $4
		`, newStatus, fulfilled, requestID, tenantID); err != nil {
			return current, err
		}
	}
	return newStatus, nil
}

// mrV2Activity — timeline yozuvi (construction_activity_log,
// related_model='material_request'). metadata ixtiyoriy.
func (h *Handler) mrV2Activity(tenantID uuid.UUID, projectID int64, userID uuid.UUID, requestID int64, actionType, description string, metadata map[string]interface{}) {
	var metaArg interface{}
	if len(metadata) > 0 {
		if b, err := json.Marshal(metadata); err == nil {
			metaArg = string(b)
		}
	}
	var userArg interface{}
	if userID != uuid.Nil {
		userArg = userID
	}
	if _, err := h.db.Exec(`
		INSERT INTO construction_activity_log (tenant_id, project_id, user_id, action_type, description, related_model, related_id, metadata, created_at)
		VALUES ($1, $2, $3, $4, $5, 'material_request', $6, COALESCE($7::jsonb, '{}'::jsonb), NOW())
	`, tenantID, projectID, userArg, actionType, description, requestID, metaArg); err != nil {
		h.log.Error("mrV2Activity failed", "error", err, "request_id", requestID, "action", actionType)
	}
}

// mrV2NotifyTargets — modul bo'yicha mas'ul foydalanuvchilar: moduli
// can_update bo'lgan xodimlar + faol owner/site_admin'lar. Fan-out 20 ta
// bilan cheklanadi.
func (h *Handler) mrV2NotifyTargets(tenantID uuid.UUID, moduleID string) []uuid.UUID {
	rows, err := h.db.Query(`
		SELECT DISTINCT u.id FROM users u
		JOIN employee_module_permissions emp
		  ON emp.employee_id = u.employee_id AND emp.tenant_id = u.tenant_id
		WHERE u.tenant_id = $1 AND u.is_active = true AND u.deleted_at IS NULL
		  AND emp.module_id = $2 AND emp.can_update = true
		UNION
		SELECT u.id FROM users u
		WHERE u.tenant_id = $1 AND u.is_active = true AND u.deleted_at IS NULL
		  AND u.role IN ('owner', 'site_admin')
		LIMIT 20
	`, tenantID, moduleID)
	if err != nil {
		h.log.Error("mrV2NotifyTargets failed", "error", err)
		return nil
	}
	defer rows.Close()
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	return ids
}

// mrV2Number — «MZ-YYYY-0001» (tenant+yil ko'lamida). Chaqiruvchi tx ichida
// ishlatadi — INSERT bilan bir tranzaksiyada raqam poygasi tor oynaga tushadi;
// unique bo'lmasa ham hujjat raqami sifatida yetarli (uy ichki hujjat).
func mrV2Number(tx *sql.Tx, tenantID uuid.UUID) string {
	year := time.Now().Format("2006")
	prefix := "MZ-" + year + "-"
	var next int
	_ = tx.QueryRow(`
		SELECT COALESCE(MAX(NULLIF(REGEXP_REPLACE(SUBSTRING(request_number FROM 9), '[^0-9]', '', 'g'), '')::int), 0) + 1
		FROM construction_material_requests
		WHERE tenant_id = $1 AND request_number LIKE $2
	`, tenantID, prefix+"%").Scan(&next)
	if next < 1 {
		next = 1
	}
	return fmt.Sprintf("%s%04d", prefix, next)
}

// mrV2ResolveWarehouse: zayavka ombori → loyiha ombori → default ombor.
func (h *Handler) mrV2ResolveWarehouse(m *mrV2Header) uuid.UUID {
	if m.WarehouseID.Valid && m.WarehouseID.UUID != uuid.Nil {
		return m.WarehouseID.UUID
	}
	var wh uuid.NullUUID
	_ = h.db.QueryRow(`SELECT warehouse_id FROM construction_projects WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL`,
		m.ProjectID, m.TenantID).Scan(&wh)
	if wh.Valid && wh.UUID != uuid.Nil {
		return wh.UUID
	}
	var def uuid.UUID
	if m.OrganizationID.Valid {
		_ = h.db.QueryRow(`SELECT id FROM warehouses WHERE tenant_id = $1 AND organization_id = $2 AND deleted_at IS NULL ORDER BY is_default DESC, created_at ASC LIMIT 1`,
			m.TenantID, m.OrganizationID.UUID).Scan(&def)
	}
	if def == uuid.Nil {
		_ = h.db.QueryRow(`SELECT id FROM warehouses WHERE tenant_id = $1 AND deleted_at IS NULL ORDER BY is_default DESC, created_at ASC LIMIT 1`,
			m.TenantID).Scan(&def)
	}
	return def
}

func (h *Handler) mrV2ProjectName(tenantID uuid.UUID, projectID int64) string {
	var name string
	_ = h.db.QueryRow(`SELECT COALESCE(name, '') FROM construction_projects WHERE id = $1 AND tenant_id = $2`, projectID, tenantID).Scan(&name)
	return name
}

// mrV2OnHand — mahsulotlar qoldig'i (berilgan omborda va jami).
func (h *Handler) mrV2OnHand(tenantID uuid.UUID, warehouseID uuid.UUID, productIDs []uuid.UUID) map[uuid.UUID][2]float64 {
	out := make(map[uuid.UUID][2]float64, len(productIDs))
	if len(productIDs) == 0 {
		return out
	}
	idStrs := make([]string, 0, len(productIDs))
	for _, id := range productIDs {
		idStrs = append(idStrs, id.String())
	}
	rows, err := h.db.Query(`
		SELECT product_id,
		       COALESCE(SUM(quantity_on_hand) FILTER (WHERE $2::uuid IS NOT NULL AND warehouse_id = $2::uuid), 0),
		       COALESCE(SUM(quantity_on_hand), 0)
		FROM inventory
		WHERE tenant_id = $1 AND product_id = ANY($3::uuid[])
		GROUP BY product_id
	`, tenantID, uuidArg(warehouseID), "{"+strings.Join(idStrs, ",")+"}")
	if err != nil {
		h.log.Error("mrV2OnHand failed", "error", err)
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var pid uuid.UUID
		var inWH, total float64
		if rows.Scan(&pid, &inWH, &total) == nil {
			out[pid] = [2]float64{inWH, total}
		}
	}
	return out
}

// ═══════════════════════════════════════════════════════════════════════════
// LIST / STATS / STOCK-CHECK / DETAIL
// ═══════════════════════════════════════════════════════════════════════════

// ListMaterialRequestsV2 godoc
// @Router /construction/material-requests-v2 [get]
func (h *Handler) ListMaterialRequestsV2(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	userID, _ := middleware.GetUserID(c)

	// cp.deleted_at IS NULL: o'chirilgan loyihaning zayavkalari ro'yxat va
	// inboxda «arvoh» bo'lib qolmasin (LEFT JOIN'da yetim satr ham o'tadi).
	where := []string{"mr.tenant_id = $1", "mr.flow = 'v2'", "cp.deleted_at IS NULL"}
	args := []interface{}{tenantID}
	arg := func(v interface{}) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}

	if status := c.Query("status"); status != "" {
		parts := strings.Split(status, ",")
		where = append(where, fmt.Sprintf("mr.status = ANY(%s::text[])", arg("{"+strings.Join(parts, ",")+"}")))
	}
	if c.Query("open") == "true" {
		where = append(where, "mr.status NOT IN ('closed', 'cancelled', 'rejected')")
	}
	if pid := c.Query("project_id"); pid != "" {
		if v, err := strconv.ParseInt(pid, 10, 64); err == nil {
			where = append(where, fmt.Sprintf("mr.project_id = %s", arg(v)))
		}
	}
	if pr := c.Query("priority"); pr != "" {
		where = append(where, fmt.Sprintf("mr.priority = %s", arg(pr)))
	}
	if c.Query("overdue") == "true" {
		where = append(where, "mr.required_date IS NOT NULL AND mr.required_date < CURRENT_DATE AND mr.status NOT IN ('closed', 'cancelled', 'rejected', 'issued')")
	}
	if c.Query("requested_by_me") == "true" && userID != uuid.Nil {
		where = append(where, fmt.Sprintf("mr.requested_by_user_id = %s", arg(userID)))
	}
	if q := strings.TrimSpace(c.Query("q")); q != "" {
		pat := "%" + strings.ToLower(q) + "%"
		p := arg(pat)
		where = append(where, fmt.Sprintf(`(LOWER(mr.request_number) LIKE %s OR LOWER(cp.name) LIKE %s OR EXISTS (
			SELECT 1 FROM construction_material_request_items qi
			WHERE qi.request_id = mr.id AND qi.tenant_id = mr.tenant_id AND LOWER(qi.product_name) LIKE %s))`, p, p, p))
	}

	whereSQL := strings.Join(where, " AND ")
	base := `
		FROM construction_material_requests mr
		LEFT JOIN construction_projects cp ON cp.id = mr.project_id
		LEFT JOIN warehouses w ON w.id = mr.warehouse_id
		LEFT JOIN users ru ON ru.id = mr.requested_by_user_id
		WHERE ` + whereSQL

	paginate, page, pageSize, offset := optPagination(c)
	query := `
		SELECT mr.id, mr.request_number, mr.project_id, COALESCE(cp.name, ''),
		       mr.status, mr.priority, mr.request_date, mr.required_date,
		       mr.warehouse_id, COALESCE(w.name, ''),
		       mr.requested_by_user_id,
		       COALESCE(NULLIF(TRIM(COALESCE(ru.first_name,'') || ' ' || COALESCE(ru.last_name,'')), ''), ru.email, ''),
		       mr.created_date, mr.closed_at, mr.rejected_reason, mr.notes,
		       (SELECT COUNT(*) FROM construction_material_request_items i WHERE i.request_id = mr.id AND i.tenant_id = mr.tenant_id) AS item_count,
		       (SELECT COALESCE(STRING_AGG(i.product_name || ' × ' || TRIM(TRAILING '.' FROM TRIM(TRAILING '0' FROM i.qty_requested::text)), ', ' ORDER BY i.created_at), '')
		          FROM construction_material_request_items i WHERE i.request_id = mr.id AND i.tenant_id = mr.tenant_id) AS items_summary,
		       (mr.required_date IS NOT NULL AND mr.required_date < CURRENT_DATE
		          AND mr.status NOT IN ('closed', 'cancelled', 'rejected', 'issued')) AS is_overdue,
		       GREATEST(0, EXTRACT(DAY FROM NOW() - mr.created_date))::int AS age_days
		` + base + `
		ORDER BY CASE WHEN mr.status IN ('closed', 'cancelled', 'rejected') THEN 1 ELSE 0 END,
		         CASE WHEN mr.priority = 'urgent' THEN 0 ELSE 1 END,
		         mr.required_date ASC NULLS LAST, mr.created_date DESC`
	if paginate {
		query += fmt.Sprintf(" LIMIT $%d OFFSET $%d", len(args)+1, len(args)+2)
		args = append(args, pageSize, offset)
	}

	rows, err := h.db.Query(query, args...)
	if err != nil {
		h.log.Error("ListMaterialRequestsV2 failed", "error", err)
		response.InternalError(c, "Failed to query material requests")
		return
	}
	defer rows.Close()

	out := []map[string]interface{}{}
	for rows.Next() {
		var id, projectID int64
		var reqNumber, projectName, status, priority, whName, requesterName, itemsSummary string
		var requestDate, createdDate time.Time
		var requiredDate, closedAt sql.NullTime
		var whID, requesterID uuid.NullUUID
		var rejectedReason, notes sql.NullString
		var itemCount, ageDays int
		var isOverdue bool
		if err := rows.Scan(&id, &reqNumber, &projectID, &projectName, &status, &priority,
			&requestDate, &requiredDate, &whID, &whName, &requesterID, &requesterName,
			&createdDate, &closedAt, &rejectedReason, &notes, &itemCount, &itemsSummary,
			&isOverdue, &ageDays); err != nil {
			h.log.Error("ListMaterialRequestsV2 scan failed", "error", err)
			continue
		}
		out = append(out, map[string]interface{}{
			"id":                   id,
			"request_number":       reqNumber,
			"project_id":           projectID,
			"project_name":         projectName,
			"status":               status,
			"priority":             priority,
			"request_date":         requestDate.Format("2006-01-02"),
			"required_date":        nullTimeValue(requiredDate),
			"warehouse_id":         nullUUIDValue(whID),
			"warehouse_name":       whName,
			"requested_by_user_id": nullUUIDValue(requesterID),
			"requester_name":       requesterName,
			"created_date":         createdDate,
			"closed_at":            nullTimeValue(closedAt),
			"rejected_reason":      nullStringValue(rejectedReason),
			"notes":                nullStringValue(notes),
			"item_count":           itemCount,
			"items_summary":        itemsSummary,
			"is_overdue":           isOverdue,
			"age_days":             ageDays,
		})
	}

	if !paginate {
		response.Success(c, out)
		return
	}
	var total int
	countQuery := "SELECT COUNT(*) " + base
	_ = h.db.QueryRow(countQuery, args[:len(args)-2]...).Scan(&total)
	response.Paginated(c, out, page, pageSize, total)
}

// GetMaterialRequestStatsV2 godoc
// @Router /construction/material-requests-v2/stats [get]
func (h *Handler) GetMaterialRequestStatsV2(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	// Ixtiyoriy loyiha ko'lami: loyiha detalidagi «Materiallar» tabi kartalari
	// shu loyihaning raqamlarini ko'rsatadi (aks holda tenant-agregat ro'yxat
	// bilan mos kelmay chalg'itadi). project_id=0 → tenant bo'ylab.
	var projectFilter int64
	if pid := c.Query("project_id"); pid != "" {
		projectFilter, _ = strconv.ParseInt(pid, 10, 64)
	}

	var openCount, inPurchaseCount, overdueCount, closedThisMonth, urgentOpen, inboxCount int
	var avgFulfillHours sql.NullFloat64
	err := h.db.QueryRow(`
		SELECT
			COUNT(*) FILTER (WHERE mr.status NOT IN ('closed', 'cancelled', 'rejected')),
			COUNT(*) FILTER (WHERE mr.status IN ('in_purchase', 'partially_fulfilled')),
			COUNT(*) FILTER (WHERE mr.required_date IS NOT NULL AND mr.required_date < CURRENT_DATE
				AND mr.status NOT IN ('closed', 'cancelled', 'rejected', 'issued')),
			COUNT(*) FILTER (WHERE mr.status = 'closed' AND mr.closed_at >= date_trunc('month', NOW())),
			COUNT(*) FILTER (WHERE mr.priority = 'urgent' AND mr.status NOT IN ('closed', 'cancelled', 'rejected')),
			COUNT(*) FILTER (WHERE mr.status IN ('new', 'in_review', 'in_purchase', 'partially_fulfilled')),
			AVG(EXTRACT(EPOCH FROM (mr.closed_at - mr.created_date)) / 3600.0)
				FILTER (WHERE mr.status = 'closed' AND mr.closed_at IS NOT NULL AND mr.closed_at >= NOW() - INTERVAL '90 days')
		FROM construction_material_requests mr
		JOIN construction_projects cp ON cp.id = mr.project_id AND cp.deleted_at IS NULL
		WHERE mr.tenant_id = $1 AND mr.flow = 'v2'
		  AND ($2::bigint = 0 OR mr.project_id = $2::bigint)
	`, tenantID, projectFilter).Scan(&openCount, &inPurchaseCount, &overdueCount, &closedThisMonth, &urgentOpen, &inboxCount, &avgFulfillHours)
	if err != nil {
		h.log.Error("GetMaterialRequestStatsV2 failed", "error", err)
		response.InternalError(c, "Failed to compute stats")
		return
	}

	// Xariddagi summa — ochiq zayavkalarga bog'langan, bekor qilinmagan xarid
	// so'rovlarining bahosi.
	var purchaseSum float64
	_ = h.db.QueryRow(`
		SELECT COALESCE(SUM(pr.total_amount), 0)
		FROM purchase_requisitions pr
		JOIN construction_material_requests mr ON mr.id = pr.material_request_id
		JOIN construction_projects cp ON cp.id = mr.project_id AND cp.deleted_at IS NULL
		WHERE pr.tenant_id = $1 AND pr.deleted_at IS NULL
		  AND pr.status NOT IN ('rejected', 'cancelled')
		  AND mr.flow = 'v2' AND mr.status NOT IN ('closed', 'cancelled', 'rejected')
		  AND ($2::bigint = 0 OR mr.project_id = $2::bigint)
	`, tenantID, projectFilter).Scan(&purchaseSum)

	avg := 0.0
	if avgFulfillHours.Valid {
		avg = avgFulfillHours.Float64
	}
	response.Success(c, map[string]interface{}{
		"open_count":            openCount,
		"in_purchase_count":     inPurchaseCount,
		"overdue_count":         overdueCount,
		"closed_this_month":     closedThisMonth,
		"urgent_open":           urgentOpen,
		"inbox_count":           inboxCount,
		"avg_fulfillment_hours": avg,
		"purchase_sum":          purchaseSum,
	})
}

// MaterialRequestStockCheck godoc — jonli qoldiq indikatori uchun.
// @Router /construction/material-requests-v2/stock-check [get]
func (h *Handler) MaterialRequestStockCheck(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	idsParam := strings.TrimSpace(c.Query("product_ids"))
	if idsParam == "" {
		response.BadRequest(c, "product_ids is required")
		return
	}
	var productIDs []uuid.UUID
	for _, s := range strings.Split(idsParam, ",") {
		if id, err := uuid.Parse(strings.TrimSpace(s)); err == nil {
			productIDs = append(productIDs, id)
		}
	}
	if len(productIDs) == 0 || len(productIDs) > 100 {
		response.BadRequest(c, "product_ids must contain 1..100 UUIDs")
		return
	}
	warehouseID := uuid.Nil
	if w := c.Query("warehouse_id"); w != "" {
		warehouseID, _ = uuid.Parse(w)
	} else if pid := c.Query("project_id"); pid != "" {
		// Loyihaga biriktirilgan ombor bo'yicha tekshirish
		if v, err := strconv.ParseInt(pid, 10, 64); err == nil {
			m := &mrV2Header{TenantID: tenantID, ProjectID: v}
			warehouseID = h.mrV2ResolveWarehouse(m)
		}
	}

	onHand := h.mrV2OnHand(tenantID, warehouseID, productIDs)
	out := make([]map[string]interface{}, 0, len(productIDs))
	for _, pid := range productIDs {
		vals := onHand[pid]
		out = append(out, map[string]interface{}{
			"product_id":    pid,
			"on_hand":       vals[0],
			"total_on_hand": vals[1],
		})
	}
	response.Success(c, map[string]interface{}{
		"warehouse_id": uuidArg(warehouseID),
		"items":        out,
	})
}

// GetMaterialRequestV2 godoc — detal: qatorlar, bog'liq hujjatlar, timeline.
// @Router /construction/material-requests-v2/{id} [get]
func (h *Handler) GetMaterialRequestV2(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid request ID")
		return
	}

	var (
		m                                  mrV2Header
		projectName, whName, requesterName string
		requestDate, createdDate           time.Time
		fulfilledDate, closedAt            sql.NullTime
		attachments                        json.RawMessage
	)
	err = h.db.QueryRow(`
		SELECT mr.id, mr.tenant_id, mr.project_id, mr.organization_id, mr.request_number,
		       COALESCE(mr.status, ''), COALESCE(mr.priority, 'normal'),
		       mr.warehouse_id, mr.requested_by_user_id, mr.required_date, mr.notes, mr.rejected_reason,
		       COALESCE(cp.name, ''), COALESCE(w.name, ''),
		       COALESCE(NULLIF(TRIM(COALESCE(ru.first_name,'') || ' ' || COALESCE(ru.last_name,'')), ''), ru.email, ''),
		       mr.request_date, mr.created_date, mr.fulfilled_date, mr.closed_at,
		       COALESCE(mr.attachments, '[]'::jsonb)
		FROM construction_material_requests mr
		LEFT JOIN construction_projects cp ON cp.id = mr.project_id
		LEFT JOIN warehouses w ON w.id = mr.warehouse_id
		LEFT JOIN users ru ON ru.id = mr.requested_by_user_id
		WHERE mr.id = $1 AND mr.tenant_id = $2 AND mr.flow = 'v2'
	`, id, tenantID).Scan(
		&m.ID, &m.TenantID, &m.ProjectID, &m.OrganizationID, &m.RequestNumber,
		&m.Status, &m.Priority, &m.WarehouseID, &m.RequestedByUserID,
		&m.RequiredDate, &m.Notes, &m.RejectedReason,
		&projectName, &whName, &requesterName,
		&requestDate, &createdDate, &fulfilledDate, &closedAt, &attachments,
	)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Material request not found")
		return
	}
	if err != nil {
		h.log.Error("GetMaterialRequestV2 failed", "error", err)
		response.InternalError(c, "Failed to load material request")
		return
	}

	items, err := h.mrV2LoadItems(h.db, tenantID, id)
	if err != nil {
		h.log.Error("GetMaterialRequestV2 items failed", "error", err)
		response.InternalError(c, "Failed to load request items")
		return
	}

	// Jonli qoldiq — detal ochilganda ham ko'rinadi.
	productIDs := make([]uuid.UUID, 0, len(items))
	for _, it := range items {
		productIDs = append(productIDs, it.ProductID)
	}
	onHand := h.mrV2OnHand(tenantID, h.mrV2ResolveWarehouse(&m), productIDs)

	itemsOut := make([]map[string]interface{}, 0, len(items))
	for _, it := range items {
		vals := onHand[it.ProductID]
		itemsOut = append(itemsOut, map[string]interface{}{
			"id":              it.ID,
			"product_id":      it.ProductID,
			"product_name":    it.ProductName,
			"unit":            it.Unit,
			"qty_requested":   it.QtyRequested,
			"qty_issued":      it.QtyIssued,
			"qty_in_purchase": it.QtyInPurchase,
			"line_status":     it.LineStatus,
			"stock_snapshot":  nullFloatValue(it.StockSnapshot),
			"on_hand":         vals[0],
			"total_on_hand":   vals[1],
			"note":            nullStringValue(it.Note),
		})
	}

	// Bog'liq chiqim hujjatlari
	issues := []map[string]interface{}{}
	if rows, err := h.db.Query(`
		SELECT so.id, so.name, so.state, so.done_at, so.journal_entry_id,
		       COALESCE((SELECT SUM(l.done_qty * l.unit_price) FROM stock_operation_lines l WHERE l.operation_id = so.id), 0)
		FROM stock_operations so
		WHERE so.tenant_id = $1 AND so.material_request_id = $2 AND so.deleted_at IS NULL
		ORDER BY so.created_at
	`, tenantID, id); err == nil {
		defer rows.Close()
		for rows.Next() {
			var opID uuid.UUID
			var name, state string
			var doneAt sql.NullTime
			var jeID uuid.NullUUID
			var total float64
			if rows.Scan(&opID, &name, &state, &doneAt, &jeID, &total) == nil {
				issues = append(issues, map[string]interface{}{
					"id":               opID,
					"name":             name,
					"state":            state,
					"done_at":          nullTimeValue(doneAt),
					"journal_entry_id": nullUUIDValue(jeID),
					"total_value":      total,
				})
			}
		}
	}

	// Bog'liq xarid so'rovlari (+ PO + kirim holati)
	purchases := []map[string]interface{}{}
	if rows, err := h.db.Query(`
		SELECT pr.id, pr.pr_number, pr.status, pr.total_amount, pr.converted_to_po,
		       COALESCE(po.order_number, ''), COALESCE(po.status, ''), po.expected_date
		FROM purchase_requisitions pr
		LEFT JOIN purchase_orders po ON po.id = pr.converted_to_po
		WHERE pr.tenant_id = $1 AND pr.material_request_id = $2 AND pr.deleted_at IS NULL
		ORDER BY pr.created_at
	`, tenantID, id); err == nil {
		defer rows.Close()
		for rows.Next() {
			var prID uuid.UUID
			var prNumber, prStatus, poNumber, poStatus string
			var totalAmount float64
			var poID uuid.NullUUID
			var poExpected sql.NullTime
			if rows.Scan(&prID, &prNumber, &prStatus, &totalAmount, &poID, &poNumber, &poStatus, &poExpected) == nil {
				purchases = append(purchases, map[string]interface{}{
					"id":                prID,
					"pr_number":         prNumber,
					"status":            prStatus,
					"total_amount":      totalAmount,
					"purchase_order_id": nullUUIDValue(poID),
					"po_number":         poNumber,
					"po_status":         poStatus,
					"po_expected_date":  nullTimeValue(poExpected),
				})
			}
		}
	}

	// Timeline
	timeline := []map[string]interface{}{}
	if rows, err := h.db.Query(`
		SELECT al.action_type, COALESCE(al.description, ''), al.created_at,
		       COALESCE(NULLIF(TRIM(COALESCE(u.first_name,'') || ' ' || COALESCE(u.last_name,'')), ''), u.email, ''),
		       COALESCE(al.metadata, '{}'::jsonb)
		FROM construction_activity_log al
		LEFT JOIN users u ON u.id = al.user_id
		WHERE al.tenant_id = $1 AND al.related_model = 'material_request' AND al.related_id = $2
		ORDER BY al.created_at ASC, al.id ASC
	`, tenantID, id); err == nil {
		defer rows.Close()
		for rows.Next() {
			var actionType, description, userName string
			var createdAt time.Time
			var meta json.RawMessage
			if rows.Scan(&actionType, &description, &createdAt, &userName, &meta) == nil {
				timeline = append(timeline, map[string]interface{}{
					"action_type": actionType,
					"description": description,
					"created_at":  createdAt,
					"user_name":   userName,
					"metadata":    meta,
				})
			}
		}
	}

	response.Success(c, map[string]interface{}{
		"id":                   m.ID,
		"request_number":       m.RequestNumber,
		"project_id":           m.ProjectID,
		"project_name":         projectName,
		"status":               m.Status,
		"priority":             m.Priority,
		"warehouse_id":         nullUUIDValue(m.WarehouseID),
		"warehouse_name":       whName,
		"requested_by_user_id": nullUUIDValue(m.RequestedByUserID),
		"requester_name":       requesterName,
		"request_date":         requestDate.Format("2006-01-02"),
		"required_date":        nullTimeValue(m.RequiredDate),
		"created_date":         createdDate,
		"fulfilled_date":       nullTimeValue(fulfilledDate),
		"closed_at":            nullTimeValue(closedAt),
		"notes":                nullStringValue(m.Notes),
		"rejected_reason":      nullStringValue(m.RejectedReason),
		"attachments":          attachments,
		"items":                itemsOut,
		"issues":               issues,
		"purchases":            purchases,
		"timeline":             timeline,
	})
}

// ═══════════════════════════════════════════════════════════════════════════
// CREATE / UPDATE / CANCEL / ACCEPT (prorab)
// ═══════════════════════════════════════════════════════════════════════════

type mrV2ItemInput struct {
	ProductID string  `json:"product_id"`
	Qty       float64 `json:"qty"`
	Unit      string  `json:"unit"`
	Note      string  `json:"note"`
}

// CreateMaterialRequestV2 godoc
// @Router /construction/material-requests-v2 [post]
func (h *Handler) CreateMaterialRequestV2(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	organizationID, _ := middleware.GetOrganizationID(c)
	userID, _ := middleware.GetUserID(c)

	var req struct {
		ProjectID    int64           `json:"project_id" binding:"required"`
		RequiredDate string          `json:"required_date"`
		Priority     string          `json:"priority"`
		WarehouseID  string          `json:"warehouse_id"`
		Notes        string          `json:"notes"`
		Items        []mrV2ItemInput `json:"items" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid input")
		return
	}
	if len(req.Items) == 0 {
		response.BadRequest(c, "At least one item is required")
		return
	}
	if req.Priority == "" {
		req.Priority = "normal"
	}
	if req.Priority != "normal" && req.Priority != "urgent" {
		response.BadRequest(c, "priority must be 'normal' or 'urgent'")
		return
	}

	// Loyiha mavjudligini tekshirish (+ org/warehouse defaultlari)
	var projOrgID, projWarehouseID uuid.NullUUID
	var projName string
	err := h.db.QueryRow(`
		SELECT organization_id, warehouse_id, COALESCE(name, '')
		FROM construction_projects
		WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
	`, req.ProjectID, tenantID).Scan(&projOrgID, &projWarehouseID, &projName)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Construction project not found")
		return
	}
	if err != nil {
		h.log.Error("CreateMaterialRequestV2 project lookup failed", "error", err)
		response.InternalError(c, "Failed to create material request")
		return
	}

	// Qatorlar: mahsulotlarni tekshirish + snapshot ma'lumotlari
	type lineData struct {
		ProductID uuid.UUID
		Name      string
		Unit      string
		Qty       float64
		Note      string
	}
	var lines []lineData
	for i, it := range req.Items {
		pid, err := uuid.Parse(it.ProductID)
		if err != nil || it.Qty <= 0 {
			response.BadRequest(c, fmt.Sprintf("Item %d: product_id/qty invalid", i+1))
			return
		}
		var name, unit string
		if err := h.db.QueryRow(`SELECT COALESCE(p.name, ''), COALESCE(u.name, '') FROM products p LEFT JOIN units_of_measure u ON u.id = p.unit_id WHERE p.id = $1 AND p.tenant_id = $2 AND p.deleted_at IS NULL`,
			pid, tenantID).Scan(&name, &unit); err != nil {
			response.BadRequest(c, fmt.Sprintf("Item %d: product not found", i+1))
			return
		}
		if it.Unit != "" {
			unit = it.Unit
		}
		lines = append(lines, lineData{ProductID: pid, Name: name, Unit: unit, Qty: it.Qty, Note: it.Note})
	}

	var requiredDate interface{}
	if req.RequiredDate != "" {
		t, err := time.Parse("2006-01-02", req.RequiredDate)
		if err != nil {
			response.BadRequest(c, "required_date must be YYYY-MM-DD")
			return
		}
		requiredDate = t
	}

	orgArg := interface{}(nil)
	if projOrgID.Valid {
		orgArg = projOrgID.UUID
	} else if organizationID != uuid.Nil {
		orgArg = organizationID
	}

	// Ombor: body → loyiha → default
	var warehouseArg interface{}
	if req.WarehouseID != "" {
		if wid, err := uuid.Parse(req.WarehouseID); err == nil {
			warehouseArg = wid
		}
	} else if projWarehouseID.Valid {
		warehouseArg = projWarehouseID.UUID
	}

	// Prorabning employee yozuvi (legacy requested_by FK employees'ga ishora)
	var empArg interface{}
	if userID != uuid.Nil {
		var empID uuid.UUID
		if err := h.db.QueryRow(`SELECT id FROM employees WHERE user_id = $1 AND tenant_id = $2 LIMIT 1`, userID, tenantID).Scan(&empID); err == nil {
			empArg = empID
		}
	}

	tx, err := h.db.Begin()
	if err != nil {
		response.InternalError(c, "Failed to create material request")
		return
	}
	defer tx.Rollback()

	requestNumber := mrV2Number(tx, tenantID)
	var requestID int64
	err = tx.QueryRow(`
		INSERT INTO construction_material_requests (
			tenant_id, project_id, organization_id, request_number, request_date, required_date,
			requested_by, requested_by_user_id, warehouse_id,
			items, notes, status, flow, priority, created_date, updated_date
		) VALUES ($1, $2, $3, $4, CURRENT_DATE, $5, $6, $7, $8, '[]'::jsonb, $9, 'new', 'v2', $10, NOW(), NOW())
		RETURNING id
	`, tenantID, req.ProjectID, orgArg, requestNumber, requiredDate,
		empArg, uuidArg(userID), warehouseArg, nullString(req.Notes), req.Priority,
	).Scan(&requestID)
	if err != nil {
		h.log.Error("CreateMaterialRequestV2 insert failed", "error", err)
		response.InternalError(c, "Failed to create material request")
		return
	}

	// Qoldiq snapshot — yaratish paytidagi holat
	whForSnapshot := uuid.Nil
	if w, ok := warehouseArg.(uuid.UUID); ok {
		whForSnapshot = w
	}
	productIDs := make([]uuid.UUID, 0, len(lines))
	for _, l := range lines {
		productIDs = append(productIDs, l.ProductID)
	}
	onHand := h.mrV2OnHand(tenantID, whForSnapshot, productIDs)

	for _, l := range lines {
		vals := onHand[l.ProductID]
		if _, err := tx.Exec(`
			INSERT INTO construction_material_request_items (
				tenant_id, request_id, product_id, product_name, unit,
				qty_requested, line_status, stock_snapshot, note, created_at, updated_at
			) VALUES ($1, $2, $3, $4, $5, $6, 'pending', $7, $8, NOW(), NOW())
		`, tenantID, requestID, l.ProductID, l.Name, l.Unit, l.Qty, vals[0], nullString(l.Note)); err != nil {
			h.log.Error("CreateMaterialRequestV2 item insert failed", "error", err)
			response.InternalError(c, "Failed to create request items")
			return
		}
	}

	if err := tx.Commit(); err != nil {
		h.log.Error("CreateMaterialRequestV2 commit failed", "error", err)
		response.InternalError(c, "Failed to create material request")
		return
	}

	h.mrV2Activity(tenantID, req.ProjectID, userID, requestID, "created",
		fmt.Sprintf("Zayavka yaratildi: %s (%d qator)", requestNumber, len(lines)), nil)

	// Omborchilarga bildirishnoma
	notifData := map[string]interface{}{"material_request_id": requestID, "request_number": requestNumber}
	for _, uid := range h.mrV2NotifyTargets(tenantID, "inventory") {
		if uid != userID {
			h.createTranslatedNotification(tenantID, uid, "material_request_created", notifData, requestNumber, projName)
		}
	}

	h.EmitWorkflowEvent(tenantID, "construction.material_request_created", map[string]interface{}{
		"record_id":      strconv.FormatInt(requestID, 10),
		"request_number": requestNumber,
		"project_name":   projName,
		"priority":       req.Priority,
		"item_count":     len(lines),
	})

	response.Created(c, map[string]interface{}{
		"id":             requestID,
		"request_number": requestNumber,
		"status":         mrStatusNew,
	})
}

// UpdateMaterialRequestV2 godoc — faqat YANGI holatda, egasi (yoki admin).
// @Router /construction/material-requests-v2/{id} [put]
func (h *Handler) UpdateMaterialRequestV2(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	userID, _ := middleware.GetUserID(c)
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid request ID")
		return
	}

	m, err := h.mrV2Load(h.db, tenantID, id)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Material request not found")
		return
	}
	if err != nil {
		response.InternalError(c, "Failed to load material request")
		return
	}
	if m.Status != mrStatusNew {
		response.ErrorWithDetails(c, 400, "MR_NOT_EDITABLE", "Faqat YANGI holatdagi zayavkani tahrirlash mumkin", nil)
		return
	}
	if !(m.RequestedByUserID.Valid && m.RequestedByUserID.UUID == userID) && !h.mrV2IsPrivileged(c, tenantID, userID) {
		response.Forbidden(c, "Only the requester can edit this request")
		return
	}

	var req struct {
		RequiredDate string          `json:"required_date"`
		Priority     string          `json:"priority"`
		Notes        string          `json:"notes"`
		Items        []mrV2ItemInput `json:"items"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid input")
		return
	}
	if req.Priority != "" && req.Priority != "normal" && req.Priority != "urgent" {
		response.BadRequest(c, "priority must be 'normal' or 'urgent'")
		return
	}

	tx, err := h.db.Begin()
	if err != nil {
		response.InternalError(c, "Failed to update material request")
		return
	}
	defer tx.Rollback()

	var requiredDate interface{}
	if req.RequiredDate != "" {
		t, err := time.Parse("2006-01-02", req.RequiredDate)
		if err != nil {
			response.BadRequest(c, "required_date must be YYYY-MM-DD")
			return
		}
		requiredDate = t
	}
	if _, err := tx.Exec(`
		UPDATE construction_material_requests
		SET required_date = COALESCE($1, required_date),
		    priority = COALESCE(NULLIF($2, ''), priority),
		    notes = COALESCE(NULLIF($3, ''), notes),
		    updated_date = NOW()
		WHERE id = $4 AND tenant_id = $5
	`, requiredDate, req.Priority, req.Notes, id, tenantID); err != nil {
		h.log.Error("UpdateMaterialRequestV2 failed", "error", err)
		response.InternalError(c, "Failed to update material request")
		return
	}

	if req.Items != nil {
		if len(req.Items) == 0 {
			response.BadRequest(c, "At least one item is required")
			return
		}
		if _, err := tx.Exec(`DELETE FROM construction_material_request_items WHERE request_id = $1 AND tenant_id = $2`, id, tenantID); err != nil {
			response.InternalError(c, "Failed to update request items")
			return
		}
		for i, it := range req.Items {
			pid, err := uuid.Parse(it.ProductID)
			if err != nil || it.Qty <= 0 {
				response.BadRequest(c, fmt.Sprintf("Item %d: product_id/qty invalid", i+1))
				return
			}
			var name, unit string
			if err := tx.QueryRow(`SELECT COALESCE(p.name, ''), COALESCE(u.name, '') FROM products p LEFT JOIN units_of_measure u ON u.id = p.unit_id WHERE p.id = $1 AND p.tenant_id = $2 AND p.deleted_at IS NULL`,
				pid, tenantID).Scan(&name, &unit); err != nil {
				response.BadRequest(c, fmt.Sprintf("Item %d: product not found", i+1))
				return
			}
			if it.Unit != "" {
				unit = it.Unit
			}
			if _, err := tx.Exec(`
				INSERT INTO construction_material_request_items (
					tenant_id, request_id, product_id, product_name, unit,
					qty_requested, line_status, note, created_at, updated_at
				) VALUES ($1, $2, $3, $4, $5, $6, 'pending', $7, NOW(), NOW())
			`, tenantID, id, pid, name, unit, it.Qty, nullString(it.Note)); err != nil {
				response.InternalError(c, "Failed to update request items")
				return
			}
		}
	}

	if err := tx.Commit(); err != nil {
		response.InternalError(c, "Failed to update material request")
		return
	}
	h.mrV2Activity(tenantID, m.ProjectID, userID, id, "updated", "Zayavka tahrirlandi", nil)
	response.Success(c, map[string]interface{}{"message": "Material request updated"})
}

// CancelMaterialRequestV2 godoc — prorab, faqat YANGI holatda.
// @Router /construction/material-requests-v2/{id}/cancel [post]
func (h *Handler) CancelMaterialRequestV2(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	userID, _ := middleware.GetUserID(c)
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid request ID")
		return
	}
	m, err := h.mrV2Load(h.db, tenantID, id)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Material request not found")
		return
	}
	if err != nil {
		response.InternalError(c, "Failed to load material request")
		return
	}
	if !(m.RequestedByUserID.Valid && m.RequestedByUserID.UUID == userID) && !h.mrV2IsPrivileged(c, tenantID, userID) {
		response.Forbidden(c, "Only the requester can cancel this request")
		return
	}
	// State machine: faqat new → cancelled. UPDATE WHERE ichida — poyga xavfsiz.
	res, err := h.db.Exec(`
		UPDATE construction_material_requests
		SET status = 'cancelled', updated_date = NOW()
		WHERE id = $1 AND tenant_id = $2 AND flow = 'v2' AND status = 'new'
	`, id, tenantID)
	if err != nil {
		response.InternalError(c, "Failed to cancel material request")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		response.ErrorWithDetails(c, 400, "MR_INVALID_TRANSITION", "Faqat YANGI holatdagi zayavkani bekor qilish mumkin", nil)
		return
	}
	h.mrV2Activity(tenantID, m.ProjectID, userID, id, "cancelled", "Zayavka bekor qilindi", nil)
	response.Success(c, map[string]interface{}{"status": mrStatusCancelled})
}

// AcceptMaterialRequestV2 godoc — prorab «Qabul qildim»: issued → closed.
// @Router /construction/material-requests-v2/{id}/accept [post]
func (h *Handler) AcceptMaterialRequestV2(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	userID, _ := middleware.GetUserID(c)
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid request ID")
		return
	}
	m, err := h.mrV2Load(h.db, tenantID, id)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Material request not found")
		return
	}
	if err != nil {
		response.InternalError(c, "Failed to load material request")
		return
	}
	if !(m.RequestedByUserID.Valid && m.RequestedByUserID.UUID == userID) && !h.mrV2IsPrivileged(c, tenantID, userID) {
		response.Forbidden(c, "Only the requester can accept this request")
		return
	}
	res, err := h.db.Exec(`
		UPDATE construction_material_requests
		SET status = 'closed', closed_at = NOW(), updated_date = NOW()
		WHERE id = $1 AND tenant_id = $2 AND flow = 'v2' AND status = 'issued'
	`, id, tenantID)
	if err != nil {
		response.InternalError(c, "Failed to accept material request")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		response.ErrorWithDetails(c, 400, "MR_INVALID_TRANSITION", "Faqat CHIQARILGAN holatdagi zayavkani qabul qilish mumkin", nil)
		return
	}
	h.mrV2Activity(tenantID, m.ProjectID, userID, id, "accepted", "Prorab materiallarni qabul qildi — zayavka yopildi", nil)
	response.Success(c, map[string]interface{}{"status": mrStatusClosed})
}

// ═══════════════════════════════════════════════════════════════════════════
// REVIEW / ISSUE / SEND-TO-PURCHASE / REJECT (omborchi)
// ═══════════════════════════════════════════════════════════════════════════

// ReviewMaterialRequestV2 godoc — omborchi ochdi: new → in_review (idempotent).
// @Router /construction/material-requests-v2/{id}/review [post]
func (h *Handler) ReviewMaterialRequestV2(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	userID, _ := middleware.GetUserID(c)
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid request ID")
		return
	}
	m, err := h.mrV2Load(h.db, tenantID, id)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Material request not found")
		return
	}
	if err != nil {
		response.InternalError(c, "Failed to load material request")
		return
	}

	res, err := h.db.Exec(`
		UPDATE construction_material_requests
		SET status = 'in_review', updated_date = NOW()
		WHERE id = $1 AND tenant_id = $2 AND flow = 'v2' AND status = 'new'
	`, id, tenantID)
	if err != nil {
		response.InternalError(c, "Failed to update material request")
		return
	}
	if n, _ := res.RowsAffected(); n > 0 {
		// Ko'rib chiqish paytidagi qoldiq snapshotini yangilash
		wh := h.mrV2ResolveWarehouse(m)
		items, _ := h.mrV2LoadItems(h.db, tenantID, id)
		productIDs := make([]uuid.UUID, 0, len(items))
		for _, it := range items {
			productIDs = append(productIDs, it.ProductID)
		}
		onHand := h.mrV2OnHand(tenantID, wh, productIDs)
		for _, it := range items {
			vals := onHand[it.ProductID]
			_, _ = h.db.Exec(`UPDATE construction_material_request_items SET stock_snapshot = $1, updated_at = NOW() WHERE id = $2 AND tenant_id = $3`,
				vals[0], it.ID, tenantID)
		}
		h.mrV2Activity(tenantID, m.ProjectID, userID, id, "reviewed", "Omborchi zayavkani ko'rib chiqishni boshladi", nil)
		m.Status = mrStatusInReview
	}
	response.Success(c, map[string]interface{}{"status": m.Status})
}

type mrV2LineAction struct {
	ItemID string  `json:"item_id"`
	Qty    float64 `json:"qty"`
}

// mrV2BuildPlan — line-amallar rejasi: body bo'sh bo'lsa default = barcha
// qoldiq (remaining) miqdorlar.
func mrV2BuildPlan(items []mrV2Item, lines []mrV2LineAction, remaining func(mrV2Item) float64) (map[uuid.UUID]float64, error) {
	plan := map[uuid.UUID]float64{}
	byID := map[uuid.UUID]mrV2Item{}
	for _, it := range items {
		byID[it.ID] = it
	}
	if len(lines) == 0 {
		for _, it := range items {
			if it.LineStatus == mrLineRejected {
				continue
			}
			if r := remaining(it); r > mrQtyEps {
				plan[it.ID] = r
			}
		}
		return plan, nil
	}
	for _, l := range lines {
		itemID, err := uuid.Parse(l.ItemID)
		if err != nil {
			return nil, fmt.Errorf("invalid item_id %q", l.ItemID)
		}
		it, ok := byID[itemID]
		if !ok {
			return nil, fmt.Errorf("item %s not found in request", l.ItemID)
		}
		if it.LineStatus == mrLineRejected {
			return nil, fmt.Errorf("item %s is rejected", l.ItemID)
		}
		r := remaining(it)
		qty := l.Qty
		if qty <= 0 {
			qty = r
		}
		if qty > r+mrQtyEps {
			return nil, fmt.Errorf("item %s: qty %.3f exceeds remaining %.3f", l.ItemID, qty, r)
		}
		if qty > mrQtyEps {
			plan[itemID] = qty
		}
	}
	return plan, nil
}

// IssueMaterialRequestV2 godoc — omborchi chiqarishi: chiqim hujjati (done) +
// applyStockDelta + JE (Dr 0810 / Cr zaxira) + loyiha xarajat qatorlari, BIR
// tranzaksiyada.
// @Router /construction/material-requests-v2/{id}/issue [post]
func (h *Handler) IssueMaterialRequestV2(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	organizationID, _ := middleware.GetOrganizationID(c)
	userID, _ := middleware.GetUserID(c)
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid request ID")
		return
	}

	var req struct {
		Lines []mrV2LineAction `json:"lines"`
		Note  string           `json:"note"`
	}
	_ = c.ShouldBindJSON(&req)

	m, err := h.mrV2Load(h.db, tenantID, id)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Material request not found")
		return
	}
	if err != nil {
		response.InternalError(c, "Failed to load material request")
		return
	}
	if !mrV2ActionableStatuses[m.Status] {
		response.ErrorWithDetails(c, 400, "MR_INVALID_TRANSITION",
			fmt.Sprintf("'%s' holatidagi zayavkadan chiqim qilib bo'lmaydi", m.Status), nil)
		return
	}

	items, err := h.mrV2LoadItems(h.db, tenantID, id)
	if err != nil {
		response.InternalError(c, "Failed to load request items")
		return
	}
	plan, err := mrV2BuildPlan(items, req.Lines, func(it mrV2Item) float64 {
		return it.QtyRequested - it.QtyIssued
	})
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	if len(plan) == 0 {
		response.ErrorWithDetails(c, 400, "MR_NOTHING_TO_ISSUE", "Chiqarish uchun qoldiq qator yo'q", nil)
		return
	}

	warehouseID := h.mrV2ResolveWarehouse(m)
	if warehouseID == uuid.Nil {
		response.ErrorWithDetails(c, 400, "MR_NO_WAREHOUSE", "Ombor topilmadi — loyihaga yoki tenantga ombor biriktiring", nil)
		return
	}

	// Do'stona 422: yetishmaydigan qatorlarning hammasini bir yo'la qaytarish.
	// (applyStockDelta baribir har qatorni tranzaksiyada qo'riqlaydi.)
	byID := map[uuid.UUID]mrV2Item{}
	productIDs := make([]uuid.UUID, 0, len(items))
	for _, it := range items {
		byID[it.ID] = it
		productIDs = append(productIDs, it.ProductID)
	}
	onHand := h.mrV2OnHand(tenantID, warehouseID, productIDs)
	var stockErrors []map[string]interface{}
	for itemID, qty := range plan {
		it := byID[itemID]
		if avail := onHand[it.ProductID][0]; qty > avail+mrQtyEps {
			stockErrors = append(stockErrors, map[string]interface{}{
				"item_id":      itemID,
				"product_id":   it.ProductID,
				"product_name": it.ProductName,
				"requested":    qty,
				"available":    avail,
			})
		}
	}
	if len(stockErrors) > 0 {
		c.JSON(422, gin.H{
			"success": false,
			"error": gin.H{
				"code":    "INSUFFICIENT_STOCK",
				"message": "Omborda yetarli qoldiq yo'q",
			},
			"errors": stockErrors,
		})
		return
	}

	// Chiqim operatsiya turi (ombor hujjati uchun)
	var opTypeID uuid.UUID
	if err := h.db.QueryRow(`
		SELECT id FROM warehouse_operation_types
		WHERE tenant_id = $1 AND operation_type = 'outgoing' AND is_active = true
		ORDER BY sequence LIMIT 1
	`, tenantID).Scan(&opTypeID); err != nil {
		response.ErrorWithDetails(c, 400, "MR_NO_DELIVERY_TYPE", "Chiqim operatsiya turi topilmadi — Ombor sozlamalarini tekshiring", nil)
		return
	}

	var orgPtr *uuid.UUID
	if m.OrganizationID.Valid {
		orgPtr = &m.OrganizationID.UUID
	} else if organizationID != uuid.Nil {
		orgPtr = &organizationID
	}

	now := time.Now()
	opID := uuid.New()
	opName := h.nextStockOperationName(tenantID, "delivery")

	tx, err := h.db.Begin()
	if err != nil {
		response.InternalError(c, "Failed to issue materials")
		return
	}
	defer tx.Rollback()

	// 1) Chiqim hujjati — darhol 'done': omborchi zayavkani tasdiqlashi =
	// chiqimni tasdiqlashi (bitta bosqich, 8-bo'lim 3-tamoyil).
	if _, err := tx.Exec(`
		INSERT INTO stock_operations (
			id, tenant_id, organization_id, name, operation_type_id, direction,
			date, source_document, source_type, material_request_id,
			state, current_step, total_steps, priority, note,
			done_at, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, 'delivery', $6, $7, 'material_request', $8,
		          'done', 1, 1, $9, $10, $6, $6, $6)
	`, opID, tenantID, orgPtr, opName, opTypeID, now, m.RequestNumber, id,
		m.Priority, nullString(fmt.Sprintf("Zayavka %s bo'yicha chiqim. %s", m.RequestNumber, req.Note))); err != nil {
		h.log.Error("IssueMaterialRequestV2 stock op insert failed", "error", err)
		response.InternalError(c, "Failed to create issue document")
		return
	}

	// 2) Har qator: applyStockDelta (manfiy) + hujjat qatori + item yangilash
	type issuedLine struct {
		Item  mrV2Item
		Qty   float64
		Cost  float64 // AVECO birlik tannarx (applyStockDelta qaytargan)
		Value float64
	}
	var issued []issuedLine
	var totalValue float64
	for itemID, qty := range plan {
		it := byID[itemID]
		newBal, valuedCost, err := h.applyStockDelta(tx, stockDeltaArgs{
			TenantID:    tenantID,
			OrgID:       orgPtr,
			ProductID:   it.ProductID,
			WarehouseID: warehouseID,
			Qty:         -qty,
			TxType:      "issue",
			RefType:     "stock_operation",
			RefID:       opID.String(),
			Reason:      "material_request",
			Notes:       fmt.Sprintf("Zayavka %s", m.RequestNumber),
			FromWH:      &warehouseID,
			CreatedBy:   userID,
			When:        now,
		})
		if err != nil {
			if err == errInsufficientStock {
				c.JSON(422, gin.H{
					"success": false,
					"error": gin.H{
						"code":    "INSUFFICIENT_STOCK",
						"message": fmt.Sprintf("Omborda yetarli qoldiq yo'q: %s", it.ProductName),
					},
				})
				return
			}
			h.log.Error("IssueMaterialRequestV2 applyStockDelta failed", "error", err)
			response.InternalError(c, "Failed to move stock")
			return
		}
		_ = newBal

		if _, err := tx.Exec(`
			INSERT INTO stock_operation_lines (
				id, tenant_id, operation_id, product_id,
				expected_qty, done_qty, uom, unit_price,
				quality_status, created_at, updated_at
			) VALUES (uuid_generate_v4(), $1, $2, $3, $4, $4, $5, $6, 'good', $7, $7)
		`, tenantID, opID, it.ProductID, qty, it.Unit, valuedCost, now); err != nil {
			h.log.Error("IssueMaterialRequestV2 op line insert failed", "error", err)
			response.InternalError(c, "Failed to create issue document lines")
			return
		}

		if _, err := tx.Exec(`
			UPDATE construction_material_request_items
			SET qty_issued = qty_issued + $1,
			    qty_in_purchase = GREATEST(0, qty_in_purchase - $1),
			    updated_at = NOW()
			WHERE id = $2 AND tenant_id = $3
		`, qty, itemID, tenantID); err != nil {
			response.InternalError(c, "Failed to update request items")
			return
		}

		value := qty * valuedCost
		totalValue += value
		issued = append(issued, issuedLine{Item: it, Qty: qty, Cost: valuedCost, Value: value})
	}

	// 3) JE: Dr 0810 (wip mapping) / Cr zaxira hisob(lar)i. S4 konventsiyasi:
	// chart yechilmasa — postingsiz davom (log), chiqim bloklanmaydi.
	var entryID uuid.UUID
	wipAcct := h.getConstructionMappedAccount(tenantID, orgPtr, "wip_0810", "tugallanmagan kapital", "0810")
	if wipAcct != uuid.Nil && totalValue > 0 {
		// Kredit hisoblar: kategoriya zaxira hisobi, fallback 1010
		creditTotals := map[uuid.UUID]float64{}
		creditOK := true
		for _, l := range issued {
			ca := getCategoryAccounts(tx, tenantID, orgPtr, l.Item.ProductID)
			acct := ca.StockValuationAccountID
			if acct == uuid.Nil {
				acct = findAccount(tx, tenantID, orgPtr, "xom ashyo", "1010")
			}
			if acct == uuid.Nil {
				creditOK = false
				break
			}
			creditTotals[acct] += l.Value
		}
		if creditOK {
			journalID := h.ensureConstructionJournal(tenantID, orgPtr)
			if journalID != uuid.Nil {
				var orgArg interface{}
				if orgPtr != nil {
					orgArg = *orgPtr
				}
				seed := h.getNextJournalNumber(tx, journalID)
				seq := nextEntryNumberSeq(tx, tenantID, orgArg, "MZC", seed)
				entryNumber := fmt.Sprintf("MZC%06d", seq)
				entryID = uuid.New()
				desc := fmt.Sprintf("Material chiqimi %s (%s)", m.RequestNumber, opName)
				if _, err := tx.Exec(`
					INSERT INTO journal_entries (
						id, tenant_id, organization_id, journal_id, entry_number,
						entry_date, reference, description, source_type, source_id,
						status, total_debit, total_credit, created_at, updated_at
					) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'material_request', $9, 'posted', $10, $10, $11, $11)
				`, entryID, tenantID, orgArg, journalID, entryNumber,
					now, m.RequestNumber, desc, opID, totalValue, now); err != nil {
					h.log.Error("IssueMaterialRequestV2 JE header failed", "error", err)
					response.InternalError(c, "Failed to post issue accounting")
					return
				}
				lineNo := 1
				writeJELine := func(acct uuid.UUID, debit, credit float64, lineDesc string) error {
					if _, err := tx.Exec(`
						INSERT INTO journal_entry_lines (id, journal_entry_id, account_id, description, debit_amount, credit_amount, line_number, warehouse_id, created_at)
						VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
					`, uuid.New(), entryID, acct, lineDesc, debit, credit, lineNo, warehouseID, now); err != nil {
						return err
					}
					lineNo++
					_, err := tx.Exec(`UPDATE accounts SET current_balance = current_balance + $1, updated_at = $2 WHERE id = $3`,
						debit-credit, now, acct)
					return err
				}
				if err := writeJELine(wipAcct, totalValue, 0, "Qurilishga material chiqimi"); err != nil {
					h.log.Error("IssueMaterialRequestV2 JE debit failed", "error", err)
					response.InternalError(c, "Failed to post issue accounting")
					return
				}
				for acct, val := range creditTotals {
					if err := writeJELine(acct, 0, val, "Ombordan chiqarilgan material"); err != nil {
						h.log.Error("IssueMaterialRequestV2 JE credit failed", "error", err)
						response.InternalError(c, "Failed to post issue accounting")
						return
					}
				}
				if _, err := tx.Exec(`UPDATE stock_operations SET accounting_posted = true, journal_entry_id = $1 WHERE id = $2`, entryID, opID); err != nil {
					response.InternalError(c, "Failed to post issue accounting")
					return
				}
			}
		} else {
			h.log.Warn("MR issue posted WITHOUT JE — stock account unresolved", "request_id", id, "tenant_id", tenantID)
		}
	} else if wipAcct == uuid.Nil {
		h.log.Warn("MR issue posted WITHOUT JE — 0810 unresolved in chart", "request_id", id, "tenant_id", tenantID)
	}

	// 4) Loyiha xarajat registri (CEL) + materiallar registri + sarf tarixi +
	// cost tracking — legacy bilan paritet, Byudjet/Materiallar tablari uchun.
	var materialsCatID sql.NullInt64
	_ = tx.QueryRow(`SELECT id FROM construction_cost_categories WHERE tenant_id = $1 AND code = 'materials' AND is_active = true LIMIT 1`, tenantID).Scan(&materialsCatID)
	var entryArg interface{}
	if entryID != uuid.Nil {
		entryArg = entryID
	}
	for _, l := range issued {
		if l.Value <= 0 {
			continue
		}
		if _, err := tx.Exec(`
			INSERT INTO construction_expense_lines (
				tenant_id, organization_id, project_id, cost_category_id,
				expense_date, description, product_id, quantity, uom, unit_price,
				amount, currency_code, debit_account_id, journal_entry_id,
				material_request_id, status, approved_at, created_by, created_at, updated_at
			) VALUES ($1, $2, $3, $4, CURRENT_DATE, $5, $6, $7, $8, $9, $10, 'UZS', $11, $12, $13, 'approved', NOW(), $14, NOW(), NOW())
		`, tenantID, orgPtr, m.ProjectID, materialsCatID,
			fmt.Sprintf("Zayavka %s: %s", m.RequestNumber, l.Item.ProductName),
			l.Item.ProductID, l.Qty, l.Item.Unit, l.Cost, l.Value,
			uuidArg(wipAcct), entryArg, id, uuidArg(userID)); err != nil {
			h.log.Error("IssueMaterialRequestV2 CEL insert failed", "error", err)
			response.InternalError(c, "Failed to record project cost")
			return
		}

		if _, err := tx.Exec(`
			INSERT INTO construction_project_materials
				(tenant_id, project_id, product_id, product_name, uom, approved_quantity, unit_cost, created_date, updated_date)
			VALUES ($1, $2, $3, $4, $5, $6, $7, NOW(), NOW())
			ON CONFLICT (tenant_id, project_id, product_id) DO UPDATE
				SET approved_quantity = construction_project_materials.approved_quantity + EXCLUDED.approved_quantity,
				    unit_cost = EXCLUDED.unit_cost,
				    updated_date = NOW()
		`, tenantID, m.ProjectID, l.Item.ProductID.String(), l.Item.ProductName, l.Item.Unit, l.Qty, l.Cost); err != nil {
			h.log.Error("IssueMaterialRequestV2 project materials upsert failed", "error", err)
			response.InternalError(c, "Failed to record project materials")
			return
		}

		if _, err := tx.Exec(`
			INSERT INTO construction_material_usage (
				tenant_id, project_id, product_id, product_name, uom,
				quantity_used, usage_date, notes, created_date, updated_date
			) VALUES ($1, $2, $3, $4, $5, $6, CURRENT_DATE, $7, NOW(), NOW())
		`, tenantID, m.ProjectID, l.Item.ProductID.String(), l.Item.ProductName, l.Item.Unit,
			l.Qty, fmt.Sprintf("Zayavka %s chiqimi", m.RequestNumber)); err != nil {
			h.log.Error("IssueMaterialRequestV2 material usage insert failed", "error", err)
			response.InternalError(c, "Failed to record material usage")
			return
		}
	}
	if totalValue > 0 {
		if _, err := tx.Exec(`
			INSERT INTO construction_cost_tracking (
				tenant_id, project_id, tracking_date, actual_cost, notes, created_date
			) VALUES ($1, $2, CURRENT_DATE, $3, $4, NOW())
		`, tenantID, m.ProjectID, totalValue, fmt.Sprintf("Zayavka %s chiqimi", m.RequestNumber)); err != nil {
			h.log.Error("IssueMaterialRequestV2 cost tracking insert failed", "error", err)
			response.InternalError(c, "Failed to record cost tracking")
			return
		}
	}

	// 5) Status agregatsiyasi
	newStatus, err := h.mrV2RecomputeStatus(tx, tenantID, id, m.Status)
	if err != nil {
		h.log.Error("IssueMaterialRequestV2 status recompute failed", "error", err)
		response.InternalError(c, "Failed to update request status")
		return
	}

	if err := tx.Commit(); err != nil {
		h.log.Error("IssueMaterialRequestV2 commit failed", "error", err)
		response.InternalError(c, "Failed to issue materials")
		return
	}

	// Post-commit: hodisalar + bildirishnomalar + timeline
	for _, l := range issued {
		h.emitInventoryAdjusted(tenantID, l.Item.ProductID, -l.Qty, onHand[l.Item.ProductID][0]-l.Qty)
	}
	lineNames := make([]string, 0, len(issued))
	for _, l := range issued {
		lineNames = append(lineNames, fmt.Sprintf("%s × %g", l.Item.ProductName, l.Qty))
	}
	h.mrV2Activity(tenantID, m.ProjectID, userID, id, "issued",
		fmt.Sprintf("Chiqim %s: %s", opName, strings.Join(lineNames, ", ")),
		map[string]interface{}{"stock_operation_id": opID, "journal_entry_id": entryArg, "total_value": totalValue})

	if m.RequestedByUserID.Valid && m.RequestedByUserID.UUID != userID {
		h.createTranslatedNotification(tenantID, m.RequestedByUserID.UUID, "material_request_issued",
			map[string]interface{}{"material_request_id": id, "request_number": m.RequestNumber},
			m.RequestNumber)
	}
	h.EmitWorkflowEvent(tenantID, "construction.material_request_issued", map[string]interface{}{
		"record_id":      strconv.FormatInt(id, 10),
		"request_number": m.RequestNumber,
		"project_name":   h.mrV2ProjectName(tenantID, m.ProjectID),
		"total_value":    totalValue,
		"status":         newStatus,
	})

	response.Success(c, map[string]interface{}{
		"stock_operation_id": opID,
		"journal_entry_id":   entryArg,
		"total_value":        totalValue,
		"status":             newStatus,
	})
}

// SendMaterialRequestToPurchase godoc — yetmagan qatorlarni xarid so'roviga
// aylantiradi (approved holatda — ta'minotchi darhol PO ochishi mumkin).
// @Router /construction/material-requests-v2/{id}/send-to-purchase [post]
func (h *Handler) SendMaterialRequestToPurchase(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	organizationID, _ := middleware.GetOrganizationID(c)
	userID, _ := middleware.GetUserID(c)
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid request ID")
		return
	}

	var req struct {
		Lines []mrV2LineAction `json:"lines"`
		Note  string           `json:"note"`
	}
	_ = c.ShouldBindJSON(&req)

	m, err := h.mrV2Load(h.db, tenantID, id)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Material request not found")
		return
	}
	if err != nil {
		response.InternalError(c, "Failed to load material request")
		return
	}
	if !mrV2ActionableStatuses[m.Status] {
		response.ErrorWithDetails(c, 400, "MR_INVALID_TRANSITION",
			fmt.Sprintf("'%s' holatidagi zayavkani xaridga yuborib bo'lmaydi", m.Status), nil)
		return
	}

	items, err := h.mrV2LoadItems(h.db, tenantID, id)
	if err != nil {
		response.InternalError(c, "Failed to load request items")
		return
	}
	plan, err := mrV2BuildPlan(items, req.Lines, func(it mrV2Item) float64 {
		return it.QtyRequested - it.QtyIssued - it.QtyInPurchase
	})
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	if len(plan) == 0 {
		response.ErrorWithDetails(c, 400, "MR_NOTHING_TO_PURCHASE", "Xaridga yuborish uchun qoldiq qator yo'q", nil)
		return
	}

	byID := map[uuid.UUID]mrV2Item{}
	for _, it := range items {
		byID[it.ID] = it
	}

	var orgArg interface{}
	if m.OrganizationID.Valid {
		orgArg = m.OrganizationID.UUID
	} else if organizationID != uuid.Nil {
		orgArg = organizationID
	}

	// Ta'minotchiga kontekst: manba, loyiha, muddat (6.5-bo'lim).
	projName := h.mrV2ProjectName(tenantID, m.ProjectID)
	purpose := fmt.Sprintf("%s · Loyiha: %s", m.RequestNumber, projName)
	if m.RequiredDate.Valid {
		purpose += " · Kerak: " + m.RequiredDate.Time.Format("2006-01-02")
	}

	var requesterName string
	_ = h.db.QueryRow(`SELECT COALESCE(NULLIF(TRIM(COALESCE(first_name,'') || ' ' || COALESCE(last_name,'')), ''), email, '') FROM users WHERE id = $1`, userID).Scan(&requesterName)

	now := time.Now()
	prID := uuid.New()
	prNumber := "PR-" + now.Format("200601") + "-" + uuid.New().String()[:6]

	tx, err := h.db.Begin()
	if err != nil {
		response.InternalError(c, "Failed to create purchase requisition")
		return
	}
	defer tx.Rollback()

	var requiredArg interface{}
	if m.RequiredDate.Valid {
		requiredArg = m.RequiredDate.Time
	}
	prPriority := "medium"
	if m.Priority == "urgent" {
		prPriority = "urgent"
	}

	var totalAmount float64
	type prLine struct {
		Item  mrV2Item
		Qty   float64
		Price float64
	}
	var prLines []prLine
	for itemID, qty := range plan {
		it := byID[itemID]
		var costPrice float64
		_ = tx.QueryRow(`SELECT COALESCE(cost_price, 0) FROM products WHERE id = $1`, it.ProductID).Scan(&costPrice)
		totalAmount += qty * costPrice
		prLines = append(prLines, prLine{Item: it, Qty: qty, Price: costPrice})
	}

	if _, err := tx.Exec(`
		INSERT INTO purchase_requisitions (
			id, tenant_id, organization_id, pr_number, requested_by, requested_by_id,
			department, request_date, required_date, status, priority, purpose, notes,
			total_amount, approved_by, approved_by_id, approved_at, material_request_id,
			created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, 'Qurilish', $7::date, $8, 'approved', $9, $10, $11,
		          $12, $5, $6, $7::timestamp, $13, $7::timestamp, $7::timestamp)
	`, prID, tenantID, orgArg, prNumber, requesterName, uuidArg(userID),
		now, requiredArg, prPriority, purpose, nullString(req.Note),
		totalAmount, id); err != nil {
		h.log.Error("SendMaterialRequestToPurchase PR insert failed", "error", err)
		response.InternalError(c, "Failed to create purchase requisition")
		return
	}

	for _, l := range prLines {
		var code sql.NullString
		_ = tx.QueryRow(`SELECT code FROM products WHERE id = $1`, l.Item.ProductID).Scan(&code)
		if _, err := tx.Exec(`
			INSERT INTO purchase_requisition_lines (
				id, requisition_id, product_id, product_name, product_code,
				quantity, unit, estimated_price, total_price, created_at
			) VALUES (uuid_generate_v4(), $1, $2, $3, $4, $5, $6, $7, $8, $9)
		`, prID, l.Item.ProductID, l.Item.ProductName, nullStringVal2(code),
			l.Qty, l.Item.Unit, l.Price, l.Qty*l.Price, now); err != nil {
			h.log.Error("SendMaterialRequestToPurchase PR line insert failed", "error", err)
			response.InternalError(c, "Failed to create purchase requisition lines")
			return
		}
		if _, err := tx.Exec(`
			UPDATE construction_material_request_items
			SET qty_in_purchase = qty_in_purchase + $1, updated_at = NOW()
			WHERE id = $2 AND tenant_id = $3
		`, l.Qty, l.Item.ID, tenantID); err != nil {
			response.InternalError(c, "Failed to update request items")
			return
		}
	}

	newStatus, err := h.mrV2RecomputeStatus(tx, tenantID, id, m.Status)
	if err != nil {
		response.InternalError(c, "Failed to update request status")
		return
	}

	if err := tx.Commit(); err != nil {
		h.log.Error("SendMaterialRequestToPurchase commit failed", "error", err)
		response.InternalError(c, "Failed to create purchase requisition")
		return
	}

	h.mrV2Activity(tenantID, m.ProjectID, userID, id, "sent_to_purchase",
		fmt.Sprintf("Xarid so'rovi yaratildi: %s (%d qator)", prNumber, len(prLines)),
		map[string]interface{}{"purchase_requisition_id": prID, "pr_number": prNumber})

	notifData := map[string]interface{}{"material_request_id": id, "request_number": m.RequestNumber, "pr_number": prNumber}
	for _, uid := range h.mrV2NotifyTargets(tenantID, "purchase") {
		if uid != userID {
			h.createTranslatedNotification(tenantID, uid, "material_request_purchase", notifData, m.RequestNumber, prNumber)
		}
	}
	if m.RequestedByUserID.Valid && m.RequestedByUserID.UUID != userID {
		h.createTranslatedNotification(tenantID, m.RequestedByUserID.UUID, "material_request_purchase", notifData, m.RequestNumber, prNumber)
	}

	response.Success(c, map[string]interface{}{
		"purchase_requisition_id": prID,
		"pr_number":               prNumber,
		"total_amount":            totalAmount,
		"status":                  newStatus,
	})
}

// RejectMaterialRequestV2 godoc — sabab majburiy; butun zayavka yoki
// tanlangan qatorlar.
// @Router /construction/material-requests-v2/{id}/reject [post]
func (h *Handler) RejectMaterialRequestV2(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	userID, _ := middleware.GetUserID(c)
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid request ID")
		return
	}

	var req struct {
		Reason  string   `json:"reason"`
		ItemIDs []string `json:"item_ids"`
	}
	_ = c.ShouldBindJSON(&req)
	if strings.TrimSpace(req.Reason) == "" {
		response.ErrorWithDetails(c, 400, "MR_REASON_REQUIRED", "Rad etish sababi majburiy", nil)
		return
	}

	m, err := h.mrV2Load(h.db, tenantID, id)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Material request not found")
		return
	}
	if err != nil {
		response.InternalError(c, "Failed to load material request")
		return
	}
	if m.Status != mrStatusNew && m.Status != mrStatusInReview {
		response.ErrorWithDetails(c, 400, "MR_INVALID_TRANSITION",
			"Faqat YANGI yoki KO'RIB CHIQILMOQDA holatidagi zayavkani rad etish mumkin", nil)
		return
	}

	items, err := h.mrV2LoadItems(h.db, tenantID, id)
	if err != nil {
		response.InternalError(c, "Failed to load request items")
		return
	}
	// Harakat boshlangan zayavkani rad etib bo'lmaydi
	for _, it := range items {
		if it.QtyIssued > mrQtyEps || it.QtyInPurchase > mrQtyEps {
			response.ErrorWithDetails(c, 400, "MR_HAS_MOVEMENTS",
				"Chiqim yoki xarid boshlangan zayavkani rad etib bo'lmaydi", nil)
			return
		}
	}

	tx, err := h.db.Begin()
	if err != nil {
		response.InternalError(c, "Failed to reject material request")
		return
	}
	defer tx.Rollback()

	if len(req.ItemIDs) == 0 {
		// Butun zayavka
		if _, err := tx.Exec(`UPDATE construction_material_request_items SET line_status = 'rejected', updated_at = NOW() WHERE request_id = $1 AND tenant_id = $2`, id, tenantID); err != nil {
			response.InternalError(c, "Failed to reject request items")
			return
		}
		if _, err := tx.Exec(`
			UPDATE construction_material_requests
			SET status = 'rejected', rejected_reason = $1, updated_date = NOW()
			WHERE id = $2 AND tenant_id = $3
		`, req.Reason, id, tenantID); err != nil {
			response.InternalError(c, "Failed to reject material request")
			return
		}
		if err := tx.Commit(); err != nil {
			response.InternalError(c, "Failed to reject material request")
			return
		}
		h.mrV2Activity(tenantID, m.ProjectID, userID, id, "rejected",
			fmt.Sprintf("Zayavka rad etildi: %s", req.Reason), nil)
		if m.RequestedByUserID.Valid && m.RequestedByUserID.UUID != userID {
			h.createTranslatedNotification(tenantID, m.RequestedByUserID.UUID, "material_request_rejected",
				map[string]interface{}{"material_request_id": id, "request_number": m.RequestNumber, "reason": req.Reason},
				m.RequestNumber, req.Reason)
		}
		h.EmitWorkflowEvent(tenantID, "construction.material_request_rejected", map[string]interface{}{
			"record_id":      strconv.FormatInt(id, 10),
			"request_number": m.RequestNumber,
			"reason":         req.Reason,
		})
		response.Success(c, map[string]interface{}{"status": mrStatusRejected})
		return
	}

	// Tanlangan qatorlar
	for _, s := range req.ItemIDs {
		itemID, err := uuid.Parse(s)
		if err != nil {
			response.BadRequest(c, fmt.Sprintf("invalid item_id %q", s))
			return
		}
		res, err := tx.Exec(`
			UPDATE construction_material_request_items
			SET line_status = 'rejected', updated_at = NOW()
			WHERE id = $1 AND request_id = $2 AND tenant_id = $3
			  AND qty_issued <= $4 AND qty_in_purchase <= $4
		`, itemID, id, tenantID, mrQtyEps)
		if err != nil {
			response.InternalError(c, "Failed to reject request items")
			return
		}
		if n, _ := res.RowsAffected(); n == 0 {
			response.BadRequest(c, fmt.Sprintf("item %s cannot be rejected", s))
			return
		}
	}
	newStatus, err := h.mrV2RecomputeStatus(tx, tenantID, id, m.Status)
	if err != nil {
		response.InternalError(c, "Failed to update request status")
		return
	}
	if newStatus == mrStatusRejected {
		if _, err := tx.Exec(`UPDATE construction_material_requests SET rejected_reason = $1, updated_date = NOW() WHERE id = $2 AND tenant_id = $3`,
			req.Reason, id, tenantID); err != nil {
			response.InternalError(c, "Failed to reject material request")
			return
		}
	}
	if err := tx.Commit(); err != nil {
		response.InternalError(c, "Failed to reject material request")
		return
	}
	h.mrV2Activity(tenantID, m.ProjectID, userID, id, "rejected",
		fmt.Sprintf("%d qator rad etildi: %s", len(req.ItemIDs), req.Reason), nil)
	if m.RequestedByUserID.Valid && m.RequestedByUserID.UUID != userID {
		h.createTranslatedNotification(tenantID, m.RequestedByUserID.UUID, "material_request_rejected",
			map[string]interface{}{"material_request_id": id, "request_number": m.RequestNumber, "reason": req.Reason},
			m.RequestNumber, req.Reason)
	}
	response.Success(c, map[string]interface{}{"status": newStatus})
}

// ═══════════════════════════════════════════════════════════════════════════
// Yopiq halqa: PO kirimi kelganda kutayotgan zayavkalarga signal
// ═══════════════════════════════════════════════════════════════════════════

// notifyMaterialRequestsForPOReceipt — PO bo'yicha kirim o'tganda shu PO'ga
// (PR orqali) bog'langan v2 zayavkalarga avto-signal: timeline yozuvi +
// omborchi va prorabga bildirishnoma. ReceivePurchaseOrder va
// CompleteGoodsReceipt commit'idan keyin chaqiriladi (fire-and-forget).
func (h *Handler) notifyMaterialRequestsForPOReceipt(tenantID, poID uuid.UUID, poNumber string, actingUserID uuid.UUID) {
	rows, err := h.db.Query(`
		SELECT DISTINCT mr.id, mr.request_number, mr.project_id, mr.requested_by_user_id, mr.status
		FROM purchase_requisitions pr
		JOIN construction_material_requests mr ON mr.id = pr.material_request_id
		WHERE pr.tenant_id = $1 AND pr.converted_to_po = $2 AND pr.deleted_at IS NULL
		  AND mr.flow = 'v2' AND mr.status NOT IN ('closed', 'cancelled', 'rejected')
	`, tenantID, poID)
	if err != nil {
		h.log.Error("notifyMaterialRequestsForPOReceipt query failed", "error", err)
		return
	}
	defer rows.Close()

	type waiting struct {
		ID            int64
		RequestNumber string
		ProjectID     int64
		RequestedBy   uuid.NullUUID
	}
	var list []waiting
	for rows.Next() {
		var w waiting
		var status string
		if rows.Scan(&w.ID, &w.RequestNumber, &w.ProjectID, &w.RequestedBy, &status) == nil {
			list = append(list, w)
		}
	}
	if len(list) == 0 {
		return
	}

	warehouseUsers := h.mrV2NotifyTargets(tenantID, "inventory")
	for _, w := range list {
		h.mrV2Activity(tenantID, w.ProjectID, actingUserID, w.ID, "material_arrived",
			fmt.Sprintf("Material omborga kirim qilindi (%s) — chiqarishga tayyor", poNumber),
			map[string]interface{}{"purchase_order_id": poID, "po_number": poNumber})
		data := map[string]interface{}{"material_request_id": w.ID, "request_number": w.RequestNumber, "po_number": poNumber}
		for _, uid := range warehouseUsers {
			h.createTranslatedNotification(tenantID, uid, "material_request_arrived", data, w.RequestNumber, poNumber)
		}
		if w.RequestedBy.Valid {
			h.createTranslatedNotification(tenantID, w.RequestedBy.UUID, "material_request_arrived", data, w.RequestNumber, poNumber)
		}
	}
}

// nullStringVal2 converts sql.NullString to interface{} for INSERT args.
func nullStringVal2(ns sql.NullString) interface{} {
	if ns.Valid {
		return ns.String
	}
	return nil
}

// nullFloatValue converts sql.NullFloat64 for JSON output.
func nullFloatValue(nf sql.NullFloat64) interface{} {
	if nf.Valid {
		return nf.Float64
	}
	return nil
}
