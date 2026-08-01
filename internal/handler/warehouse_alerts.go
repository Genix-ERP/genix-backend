package handler

import (
	"fmt"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// GetWarehouseAlerts returns smart alerts for the warehouse dashboard
func (h *Handler) GetWarehouseAlerts(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	var alerts []map[string]interface{}

	// 1. Low stock alerts (products below reorder point)
	lowStockRows, err := h.db.Query(`
		SELECT p.id, p.name, p.code, p.reorder_point,
		       COALESCE(SUM(i.quantity_available), 0) AS current_qty
		FROM products p
		LEFT JOIN inventory i ON i.product_id = p.id AND i.tenant_id = p.tenant_id
		WHERE p.tenant_id = $1
		  AND p.track_inventory = true
		  AND p.reorder_point > 0
		  AND p.deleted_at IS NULL
		GROUP BY p.id, p.name, p.code, p.reorder_point
		HAVING COALESCE(SUM(i.quantity_available), 0) <= p.reorder_point
		ORDER BY COALESCE(SUM(i.quantity_available), 0) ASC
		LIMIT 20
	`, tenantID)
	if err == nil {
		defer lowStockRows.Close()
		for lowStockRows.Next() {
			var id uuid.UUID
			var name, code string
			var reorderPoint, currentQty float64
			if err := lowStockRows.Scan(&id, &name, &code, &reorderPoint, &currentQty); err != nil {
				continue
			}
			severity := "warning"
			if currentQty == 0 {
				severity = "critical"
			}
			alerts = append(alerts, map[string]interface{}{
				"type":          "low_stock",
				"severity":      severity,
				"title":         "Low stock: " + name,
				"message":       code + " — " + formatFloat(currentQty) + " remaining (reorder point: " + formatFloat(reorderPoint) + ")",
				"product_id":    id,
				"current_qty":   currentQty,
				"reorder_point": reorderPoint,
			})
		}
	}

	// 2. Expiring lots (lots expiring within 30 days)
	expiringRows, err := h.db.Query(`
		SELECT l.id, l.lot_number, l.expiry_date, p.name AS product_name,
		       l.expiry_date - CURRENT_DATE AS days_left
		FROM inventory_lots l
		JOIN products p ON p.id = l.product_id AND p.tenant_id = l.tenant_id
		WHERE l.tenant_id = $1
		  AND l.expiry_date IS NOT NULL
		  AND l.remaining_quantity > 0
		  AND l.expiry_date <= CURRENT_DATE + INTERVAL '30 days'
		  AND l.expiry_date >= CURRENT_DATE
		ORDER BY l.expiry_date ASC
		LIMIT 20
	`, tenantID)
	if err == nil {
		defer expiringRows.Close()
		for expiringRows.Next() {
			var lotID uuid.UUID
			var lotNumber, productName string
			var expirationDate string
			var daysLeft int
			if err := expiringRows.Scan(&lotID, &lotNumber, &expirationDate, &productName, &daysLeft); err != nil {
				continue
			}
			severity := "warning"
			if daysLeft <= 7 {
				severity = "critical"
			}
			alerts = append(alerts, map[string]interface{}{
				"type":     "expiring_lot",
				"severity": severity,
				"title":    "Expiring lot: " + lotNumber,
				"message":  productName + " — expires in " + formatInt(daysLeft) + " days",
				"lot_id":   lotID,
			})
		}
	}

	// 3. Overdue operations (in_progress operations past scheduled date)
	overdueRows, err := h.db.Query(`
		SELECT so.id, so.name, so.direction, so.scheduled_date,
		       CURRENT_DATE - so.scheduled_date::date AS days_overdue
		FROM stock_operations so
		WHERE so.tenant_id = $1
		  AND so.state IN ('in_progress', 'waiting', 'awaiting_approval')
		  AND so.scheduled_date IS NOT NULL
		  AND so.scheduled_date < CURRENT_DATE
		  AND so.deleted_at IS NULL
		ORDER BY so.scheduled_date ASC
		LIMIT 20
	`, tenantID)
	if err == nil {
		defer overdueRows.Close()
		for overdueRows.Next() {
			var opID uuid.UUID
			var name, direction, scheduledDate string
			var daysOverdue int
			if err := overdueRows.Scan(&opID, &name, &direction, &scheduledDate, &daysOverdue); err != nil {
				continue
			}
			alerts = append(alerts, map[string]interface{}{
				"type":         "overdue_operation",
				"severity":     "warning",
				"title":        "Overdue: " + name,
				"message":      direction + " operation — " + formatInt(daysOverdue) + " days overdue",
				"operation_id": opID,
			})
		}
	}

	// 4. Timed-out steps (steps that exceeded max_duration_hours)
	timeoutRows, err := h.db.Query(`
		SELECT sl.id, sl.step_name, so.name AS operation_name, so.id AS operation_id,
		       sl.timeout_blocked
		FROM stock_operation_step_log sl
		JOIN stock_operations so ON so.id = sl.operation_id AND so.tenant_id = sl.tenant_id
		JOIN operation_type_steps ots ON ots.id = sl.step_id AND ots.tenant_id = sl.tenant_id
		WHERE sl.tenant_id = $1
		  AND sl.state = 'in_progress'
		  AND sl.timeout_notified = true
		  AND so.deleted_at IS NULL
		LIMIT 20
	`, tenantID)
	if err == nil {
		defer timeoutRows.Close()
		for timeoutRows.Next() {
			var slID, opID uuid.UUID
			var stepName, opName string
			var blocked bool
			if err := timeoutRows.Scan(&slID, &stepName, &opName, &opID, &blocked); err != nil {
				continue
			}
			severity := "warning"
			msg := "Step '" + stepName + "' in " + opName + " has exceeded time limit"
			if blocked {
				severity = "critical"
				msg += " (BLOCKED)"
			}
			alerts = append(alerts, map[string]interface{}{
				"type":         "step_timeout",
				"severity":     severity,
				"title":        "Step timeout: " + stepName,
				"message":      msg,
				"operation_id": opID,
			})
		}
	}

	// 5. Anomaly write-offs (write-offs with unusually high quantities in last 7 days)
	anomalyRows, err := h.db.Query(`
		SELECT so.id, so.name, so.write_off_reason,
		       COALESCE(SUM(sol.done_qty), 0) AS total_qty,
		       COUNT(sol.id) AS line_count
		FROM stock_operations so
		JOIN stock_operation_lines sol ON sol.operation_id = so.id AND sol.tenant_id = so.tenant_id
		WHERE so.tenant_id = $1
		  AND so.direction = 'write_off'
		  AND so.state = 'done'
		  AND so.done_at >= CURRENT_DATE - INTERVAL '7 days'
		  AND so.deleted_at IS NULL
		GROUP BY so.id, so.name, so.write_off_reason
		HAVING COALESCE(SUM(sol.done_qty), 0) > 100
		ORDER BY COALESCE(SUM(sol.done_qty), 0) DESC
		LIMIT 10
	`, tenantID)
	if err == nil {
		defer anomalyRows.Close()
		for anomalyRows.Next() {
			var opID uuid.UUID
			var name string
			var reason *string
			var totalQty float64
			var lineCount int
			if err := anomalyRows.Scan(&opID, &name, &reason, &totalQty, &lineCount); err != nil {
				continue
			}
			reasonStr := ""
			if reason != nil {
				reasonStr = " (" + *reason + ")"
			}
			alerts = append(alerts, map[string]interface{}{
				"type":         "anomaly_writeoff",
				"severity":     "info",
				"title":        "Large write-off: " + name,
				"message":      formatFloat(totalQty) + " units written off" + reasonStr + " — " + formatInt(lineCount) + " lines",
				"operation_id": opID,
			})
		}
	}

	if alerts == nil {
		alerts = []map[string]interface{}{}
	}
	response.Success(c, alerts)
}

func formatFloat(f float64) string {
	if f == float64(int(f)) {
		return fmt.Sprintf("%d", int(f))
	}
	return fmt.Sprintf("%.2f", f)
}

func formatInt(i int) string {
	return fmt.Sprintf("%d", i)
}
