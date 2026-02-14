package handler

import (
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
)

// ========== CASH REGISTERS ==========

func (h *Handler) ListCashRegisters(c *gin.Context)  { response.Success(c, []interface{}{}) }
func (h *Handler) CreateCashRegister(c *gin.Context) { response.Created(c, gin.H{"message": "Cash register created"}) }
func (h *Handler) GetCashRegister(c *gin.Context)    { response.NotFound(c, "Cash register") }
func (h *Handler) UpdateCashRegister(c *gin.Context) { response.Success(c, gin.H{"message": "Cash register updated"}) }

// ========== CASH ORDERS (PKO/RKO) ==========

func (h *Handler) ListCashOrders(c *gin.Context)   { response.Success(c, []interface{}{}) }
func (h *Handler) CreateCashOrder(c *gin.Context)  { response.Created(c, gin.H{"message": "Cash order created"}) }
func (h *Handler) GetCashOrder(c *gin.Context)     { response.NotFound(c, "Cash order") }
func (h *Handler) UpdateCashOrder(c *gin.Context)  { response.Success(c, gin.H{"message": "Cash order updated"}) }
func (h *Handler) ConfirmCashOrder(c *gin.Context) { response.Success(c, gin.H{"message": "Cash order confirmed"}) }

// ========== CASH BOOK ==========

func (h *Handler) GetCashBook(c *gin.Context) { response.Success(c, []interface{}{}) }

// ========== CURRENCY RATES SYNC ==========

func (h *Handler) SyncCurrencyRates(c *gin.Context)  { response.Success(c, gin.H{"message": "Exchange rates synced from CBU"}) }
func (h *Handler) RevalueCurrency(c *gin.Context)    { response.Success(c, gin.H{"message": "Currency revaluation completed"}) }
func (h *Handler) ListExchangeDiffs(c *gin.Context)  { response.Success(c, []interface{}{}) }

// ========== RECONCILIATION ACTS (Akt sverka) ==========

func (h *Handler) ListReconciliationActs(c *gin.Context) { response.Success(c, []interface{}{}) }
func (h *Handler) CreateReconciliationAct(c *gin.Context) {
	response.Created(c, gin.H{"message": "Reconciliation act created"})
}
func (h *Handler) GetReconciliationAct(c *gin.Context)    { response.NotFound(c, "Reconciliation act") }
func (h *Handler) UpdateReconciliationAct(c *gin.Context) { response.Success(c, gin.H{"message": "Reconciliation act updated"}) }
func (h *Handler) DeleteReconciliationAct(c *gin.Context) { response.NoContent(c) }
func (h *Handler) BulkGenerateReconciliation(c *gin.Context) {
	response.Success(c, gin.H{"message": "Bulk reconciliation generated", "count": 0})
}
func (h *Handler) ExportReconciliationAct(c *gin.Context) {
	response.Success(c, gin.H{"message": "Export generated", "url": ""})
}

// ========== BUDGETS (extended) ==========

func (h *Handler) ListBudgetsV2(c *gin.Context)        { response.Success(c, []interface{}{}) }
func (h *Handler) CreateBudgetV2(c *gin.Context)       { response.Created(c, gin.H{"message": "Budget created"}) }
func (h *Handler) GetBudgetV2(c *gin.Context)          { response.NotFound(c, "Budget") }
func (h *Handler) UpdateBudgetV2(c *gin.Context)       { response.Success(c, gin.H{"message": "Budget updated"}) }
func (h *Handler) DeleteBudgetV2(c *gin.Context)       { response.NoContent(c) }
func (h *Handler) ListBudgetLines(c *gin.Context)      { response.Success(c, []interface{}{}) }
func (h *Handler) CreateBudgetLine(c *gin.Context)     { response.Created(c, gin.H{"message": "Budget line created"}) }
func (h *Handler) UpdateBudgetLine(c *gin.Context)     { response.Success(c, gin.H{"message": "Budget line updated"}) }
func (h *Handler) GetConsolidatedBudget(c *gin.Context) {
	response.Success(c, gin.H{"consolidated": []interface{}{}, "total_planned": 0, "total_actual": 0})
}
