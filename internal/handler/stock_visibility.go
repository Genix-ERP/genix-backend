package handler

import (
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/genixerp/genix-backend/internal/middleware"
)

// Stock that is written but cannot be read is worse than stock never written:
// the document says "Bajarildi 200 Dona", the ledger moves, quantity_on_hand
// moves — and the product page still shows Qoldiq 0, with nothing anywhere to
// explain the gap.
//
// Reads gate on the WAREHOUSE's organization, not on inventory.organization_id
// (which is frequently NULL, as the comment above the filter itself admits):
//
//	ListInventory   AND w.organization_id = $orgID   internal/handler/inventory.go
//	ListWarehouses  AND w.organization_id = $orgID   internal/handler/warehouses.go
//	Products.jsx    accessibleWarehouseIds           the same rule a third time, client-side
//
// Three copies of one rule, and the write path enforced none of them. So a
// receipt into a warehouse belonging to another organization — or to none —
// banked goods into a void, silently, with a green success toast.
//
// checkStockVisible asks the question the read path will ask later, against the
// organization the caller is working in right now. It returns the reason the
// goods would be invisible, or "" when they would be visible.
func (h *Handler) checkStockVisible(c *gin.Context, tenantID uuid.UUID, warehouses ...uuid.UUID) string {
	reqOrg, ok := middleware.GetOrganizationID(c)
	if !ok || reqOrg == uuid.Nil {
		// No organization scope on the request means ListInventory applies no
		// organization filter either — every warehouse is readable, so there is
		// genuinely nothing to warn about. Single-organization tenants live
		// here, and must not be broken by a guard aimed at multi-org ones.
		return ""
	}

	for _, wh := range warehouses {
		if wh == uuid.Nil {
			continue
		}
		var whName string
		var whOrg *uuid.UUID
		var ownerName *string
		if err := h.db.QueryRow(`
			SELECT w.name, w.organization_id, o.name
			FROM warehouses w
			LEFT JOIN organizations o ON o.id = w.organization_id
			WHERE w.id = $1 AND w.tenant_id = $2
		`, wh, tenantID).Scan(&whName, &whOrg, &ownerName); err != nil {
			// A warehouse that does not resolve is the movement's problem to
			// report, not this check's — staying quiet keeps one failure from
			// being described by two different messages.
			continue
		}

		if whOrg == nil {
			return "\"" + whName + "\" ombori hech qaysi kompaniyaga biriktirilmagan — " +
				"unga kirgan tovar hech bir kompaniyada ko'rinmaydi. " +
				"Ombor sozlamalarida kompaniyani tanlang."
		}
		if *whOrg != reqOrg {
			owner := "boshqa kompaniya"
			if ownerName != nil {
				owner = *ownerName
			}
			return "\"" + whName + "\" ombori \"" + owner + "\" kompaniyasiga tegishli, " +
				"siz esa boshqa kompaniyada ishlayapsiz — tovar bu yerda ko'rinmaydi. " +
				"\"" + owner + "\" kompaniyasiga o'ting yoki boshqa ombor tanlang."
		}
	}
	return ""
}

// resolveOwningOrganization decides which organization a new warehouse belongs
// to when the request did not say.
//
// NULL is not a neutral default here. Reads filter `w.organization_id = $orgID`
// and NULL satisfies no such comparison, so a warehouse stored without an
// organization can accept stock but can never show it. The defect surfaces
// weeks later and far from its cause: a receipt reading "Bajarildi 200 Dona"
// above a product reading "Qoldiq 0".
//
// Returns (Nil, "") when the tenant has no organizations at all — there the
// read applies no organization filter either, so NULL is genuinely safe.
// Returns a message only when the tenant has several and the request named
// none, because that is the single case where a guess could file a warehouse,
// and everything ever received into it, under the wrong company.
func (h *Handler) resolveOwningOrganization(tenantID uuid.UUID) (uuid.UUID, string) {
	rows, err := h.db.Query(
		`SELECT id FROM organizations WHERE tenant_id = $1 ORDER BY created_at`, tenantID)
	if err != nil {
		// The database is already unhappy; let the caller's own INSERT report it
		// rather than turning a transient error into a confusing validation
		// message about organizations.
		return uuid.Nil, ""
	}
	defer rows.Close()

	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}

	switch len(ids) {
	case 0:
		return uuid.Nil, ""
	case 1:
		return ids[0], ""
	default:
		return uuid.Nil, "Ombor qaysi kompaniyaga tegishli ekani ko'rsatilmadi. " +
			"Yuqoridan kompaniyani tanlang va omborni qaytadan yarating — " +
			"kompaniyasiz ombordagi tovar hech qayerda ko'rinmaydi."
	}
}

// rollbackStep puts an advanced step back to 'ready'.
//
// Every refusal after the step has been advanced must call this, otherwise the
// operation is left half-advanced: not done, but not re-runnable either, which
// is how a receipt becomes permanently stuck at "Bajarildi" with no stock.
func (h *Handler) rollbackStep(operationID uuid.UUID, step int, tenantID uuid.UUID) {
	if _, execErr := h.db.Exec(`
		UPDATE stock_operation_step_log
		SET state='ready', completed_at=NULL, completed_by=NULL
		WHERE operation_id=$1 AND step_sequence=$2 AND tenant_id=$3
	`, operationID, step, tenantID); execErr != nil {
		h.log.Error("write failed (was silently discarded)", "stmt", "UPDATE stock_operation_step_log", "error", execErr)
	}
}
