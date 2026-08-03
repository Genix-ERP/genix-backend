package handler

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/genixerp/genix-backend/internal/domain/entity"
	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
)

// productionAudit writes one audit_logs row for a production-order lifecycle
// action (create/confirm/start/complete/cancel/delete) — the contractAudit
// pattern from contracts.go. Best-effort: failures are logged, never block
// the operation.
func (h *Handler) productionAudit(tenantID, userID, poID uuid.UUID, action string, oldValues, newValues map[string]interface{}) {
	oldJSON, _ := json.Marshal(oldValues)
	newJSON, _ := json.Marshal(newValues)
	if _, err := h.db.Exec(`
		INSERT INTO audit_logs (id, tenant_id, user_id, action, entity_type, entity_id, old_values, new_values, created_at)
		VALUES ($1, $2, $3, $4, 'production_order', $5, $6, $7, $8)
	`, uuid.New(), tenantID, nullableUUID(userID), action, poID, oldJSON, newJSON, time.Now()); err != nil {
		h.log.Error("Failed to write production audit log", "error", err, "po_id", poID)
	}
}

// =====================================================
// WORK CENTER HANDLERS
// =====================================================

// ListWorkCenters godoc
// @Summary List work centers
// @Description Get a paginated list of work centers with filtering options
// @Tags Manufacturing
// @Accept json
// @Produce json
// @Param status query string false "Filter by status"
// @Param is_available query boolean false "Filter by availability"
// @Param warehouse_id query string false "Filter by warehouse ID"
// @Param search query string false "Search by name or code"
// @Param page query int false "Page number" default(1)
// @Param limit query int false "Items per page" default(20)
// @Param sort_by query string false "Sort by field" default(name)
// @Param sort_order query string false "Sort order (asc/desc)" default(asc)
// calculateWorkCenterCosts computes per-hour cost components from input parameters.
func calculateWorkCenterCosts(assetValue, usefulLifeYears, workingHoursPerDay, powerKW, electricityRate, annualMaintenance, operatorMonthlySalary, overheadCost float64, laborRateType string) (depreciationPerHour, electricityPerHour, maintenancePerHour, laborPerHour, totalHourlyCost float64) {
	// 365 days/year — matches the migration-346 backfill formula (annualHours
	// = working_hours_per_day * 365). This function used 250, so a work
	// center edited through the API got a ~31% higher hourly rate than the
	// same record backfilled by 346 (audit §2.16, B4).
	annualWorkingHours := workingHoursPerDay * 365
	if annualWorkingHours <= 0 {
		annualWorkingHours = 2920
	}
	if assetValue > 0 && usefulLifeYears > 0 {
		depreciationPerHour = assetValue / usefulLifeYears / annualWorkingHours
	}
	electricityPerHour = powerKW * electricityRate
	if annualMaintenance > 0 {
		maintenancePerHour = annualMaintenance / annualWorkingHours
	}
	if operatorMonthlySalary > 0 {
		if laborRateType == "daily" {
			// Daily wage: divide by hours per day
			laborPerHour = operatorMonthlySalary / workingHoursPerDay
		} else {
			// Monthly wage: divide by 27 working days × hours per day
			monthlyWorkingHours := workingHoursPerDay * 27
			if monthlyWorkingHours <= 0 {
				monthlyWorkingHours = 176
			}
			laborPerHour = operatorMonthlySalary / monthlyWorkingHours
		}
	}
	totalHourlyCost = depreciationPerHour + electricityPerHour + maintenancePerHour + laborPerHour + overheadCost
	return
}

// computeMOMachineAndLaborCost is THE machine/labor costing formula for a
// production order (B4). Returns PER-UNIT costs. Previously three paths
// computed this differently — CompleteProductionOrder used the
// capacity+time split below while receiveFinishedGoods and
// createFinishedGoodsJournalEntry used a capacity-only formula — so the
// completion JE amount depended on which button finished the order. All
// three call sites now route through here.
//
// Split: capacity-based work centers contribute their per-hour components ÷
// capacity ÷ BOM output qty; time-based work centers contribute actual
// completed-WO hours × rate ÷ produced qty. Legacy work centers with no
// cost breakdown fall back to hourly_cost ÷ capacity.
func (h *Handler) computeMOMachineAndLaborCost(bomID uuid.UUID, bomOutputQty float64, poID uuid.UUID, producedQty float64) (machineCost, laborCost float64) {
	if bomOutputQty <= 0 {
		bomOutputQty = 1
	}

	// Machine cost — capacity-based work centers
	h.db.QueryRow(`
		SELECT COALESCE(SUM(
			(COALESCE(wc.depreciation_per_hour, 0) + COALESCE(wc.electricity_per_hour, 0) + COALESCE(wc.maintenance_per_hour, 0))
			/ GREATEST(COALESCE(wc.capacity_per_hour, 1), 1)
		), 0) / $1
		FROM bom_operations bo
		LEFT JOIN work_centers wc ON bo.work_center_id = wc.id
		WHERE bo.bom_id = $2 AND COALESCE(wc.cost_method, 'capacity') = 'capacity'
	`, bomOutputQty, bomID).Scan(&machineCost)

	// Machine cost — time-based work centers (actual hours)
	var timeMachineCost float64
	h.db.QueryRow(`
		SELECT COALESCE(SUM(
			COALESCE(wc.hourly_cost, 0) * COALESCE(wo.actual_duration_hours, 0)
		), 0) / GREATEST($1, 1)
		FROM work_orders wo
		LEFT JOIN work_centers wc ON wo.work_center_id = wc.id
		WHERE wo.production_order_id = $2 AND COALESCE(wc.cost_method, 'capacity') = 'time'
		AND wo.status IN ('completed', 'done')
	`, producedQty, poID).Scan(&timeMachineCost)
	machineCost += timeMachineCost

	// Labor cost — same capacity/time split
	h.db.QueryRow(`
		SELECT COALESCE(SUM(
			COALESCE(wc.labor_per_hour, 0)
			/ GREATEST(COALESCE(wc.capacity_per_hour, 1), 1)
		), 0) / $1
		FROM bom_operations bo
		LEFT JOIN work_centers wc ON bo.work_center_id = wc.id
		WHERE bo.bom_id = $2 AND COALESCE(wc.cost_method, 'capacity') = 'capacity'
	`, bomOutputQty, bomID).Scan(&laborCost)

	var timeLaborCost float64
	h.db.QueryRow(`
		SELECT COALESCE(SUM(
			COALESCE(wc.labor_per_hour, 0) * COALESCE(wo.actual_duration_hours, 0)
		), 0) / GREATEST($1, 1)
		FROM work_orders wo
		LEFT JOIN work_centers wc ON wo.work_center_id = wc.id
		WHERE wo.production_order_id = $2 AND COALESCE(wc.cost_method, 'capacity') = 'time'
		AND wo.status IN ('completed', 'done')
	`, producedQty, poID).Scan(&timeLaborCost)
	laborCost += timeLaborCost

	// Legacy fallback: no detailed cost fields anywhere → hourly_cost/capacity
	if machineCost == 0 && laborCost == 0 {
		h.db.QueryRow(`
			SELECT COALESCE(SUM(
				COALESCE(wc.hourly_cost, 0)
				/ GREATEST(COALESCE(wc.capacity_per_hour, 1), 1)
			), 0) / $1
			FROM bom_operations bo
			LEFT JOIN work_centers wc ON bo.work_center_id = wc.id
			WHERE bo.bom_id = $2
		`, bomOutputQty, bomID).Scan(&machineCost)
	}
	return machineCost, laborCost
}

// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /manufacturing/work-centers [get]
func (h *Handler) ListWorkCenters(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	var filter entity.WorkCenterFilter
	if err := c.ShouldBindQuery(&filter); err != nil {
		response.BadRequest(c, "Invalid query parameters")
		return
	}

	// Set defaults
	if filter.Page <= 0 {
		filter.Page = 1
	}
	if filter.Limit <= 0 || filter.Limit > 10000 {
		filter.Limit = 100
	}
	if filter.SortBy == "" {
		filter.SortBy = "name"
	}
	if filter.SortOrder == "" {
		filter.SortOrder = "asc"
	}

	// Build query
	baseQuery := `
		SELECT wc.id, wc.code, wc.name, wc.description, wc.warehouse_id, w.name as warehouse_name,
			   wc.department, wc.capacity_per_hour, wc.efficiency_factor, wc.oee_target,
			   wc.working_hours_per_day, wc.hourly_cost, wc.setup_cost, wc.overhead_cost,
			   COALESCE(wc.asset_value,0), COALESCE(wc.useful_life_years,10),
			   COALESCE(wc.power_kw,0), COALESCE(wc.electricity_rate,0),
			   COALESCE(wc.annual_maintenance,0), COALESCE(wc.operator_monthly_salary,0), COALESCE(wc.labor_rate_type,'monthly'), COALESCE(wc.cost_method,'capacity'), COALESCE(wc.require_operator, false),
			   COALESCE(wc.depreciation_per_hour,0), COALESCE(wc.electricity_per_hour,0),
			   COALESCE(wc.maintenance_per_hour,0), COALESCE(wc.labor_per_hour,0),
			   wc.currency, wc.status, wc.is_available, wc.next_maintenance_date,
			   wc.last_maintenance_date, wc.total_jobs_completed, wc.total_hours_worked,
			   wc.current_utilization, wc.notes, wc.created_at, wc.updated_at,
			   wc.asset_id, fa.name as asset_name
		FROM work_centers wc
		LEFT JOIN warehouses w ON wc.warehouse_id = w.id
		LEFT JOIN fa_assets fa ON fa.id = wc.asset_id
		WHERE wc.tenant_id = $1 AND wc.deleted_at IS NULL
	`

	countQuery := `SELECT COUNT(*) FROM work_centers wc WHERE wc.tenant_id = $1 AND wc.deleted_at IS NULL`
	args := []interface{}{tenantID}
	countArgs := []interface{}{tenantID}
	argCount := 1

	// Filter by organization
	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		argCount++
		baseQuery += fmt.Sprintf(" AND wc.organization_id = $%d", argCount)
		countQuery += fmt.Sprintf(" AND wc.organization_id = $%d", argCount)
		args = append(args, orgID)
		countArgs = append(countArgs, orgID)
	}

	// Apply filters
	if filter.Status != nil && *filter.Status != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND wc.status = $%d", argCount)
		countQuery += fmt.Sprintf(" AND wc.status = $%d", argCount)
		args = append(args, *filter.Status)
		countArgs = append(countArgs, *filter.Status)
	}

	if filter.IsAvailable != nil {
		argCount++
		baseQuery += fmt.Sprintf(" AND wc.is_available = $%d", argCount)
		countQuery += fmt.Sprintf(" AND wc.is_available = $%d", argCount)
		args = append(args, *filter.IsAvailable)
		countArgs = append(countArgs, *filter.IsAvailable)
	}

	if filter.WarehouseID != nil {
		argCount++
		baseQuery += fmt.Sprintf(" AND wc.warehouse_id = $%d", argCount)
		countQuery += fmt.Sprintf(" AND wc.warehouse_id = $%d", argCount)
		args = append(args, *filter.WarehouseID)
		countArgs = append(countArgs, *filter.WarehouseID)
	}

	if filter.Search != nil && *filter.Search != "" {
		argCount++
		searchPattern := "%" + *filter.Search + "%"
		baseQuery += fmt.Sprintf(" AND (wc.name ILIKE $%d OR wc.code ILIKE $%d)", argCount, argCount)
		countQuery += fmt.Sprintf(" AND (wc.name ILIKE $%d OR wc.code ILIKE $%d)", argCount, argCount)
		args = append(args, searchPattern)
		countArgs = append(countArgs, searchPattern)
	}

	// Get total count
	var total int
	err := h.db.QueryRow(countQuery, countArgs...).Scan(&total)
	if err != nil {
		h.log.Error("Failed to count work centers", "error", err)
		response.InternalError(c, "Failed to retrieve work centers")
		return
	}

	// Sorting
	validSortColumns := map[string]string{
		"name":        "wc.name",
		"code":        "wc.code",
		"status":      "wc.status",
		"utilization": "wc.current_utilization",
		"created_at":  "wc.created_at",
	}
	sortColumn := validSortColumns[filter.SortBy]
	if sortColumn == "" {
		sortColumn = "wc.name"
	}
	sortOrder := "ASC"
	if strings.ToLower(filter.SortOrder) == "desc" {
		sortOrder = "DESC"
	}
	baseQuery += fmt.Sprintf(" ORDER BY %s %s", sortColumn, sortOrder)

	// Pagination
	offset := (filter.Page - 1) * filter.Limit
	baseQuery += fmt.Sprintf(" LIMIT %d OFFSET %d", filter.Limit, offset)

	// Execute query
	rows, err := h.db.Query(baseQuery, args...)
	if err != nil {
		h.log.Error("Failed to list work centers", "error", err)
		response.InternalError(c, "Failed to retrieve work centers")
		return
	}
	defer rows.Close()

	workCenters := []entity.WorkCenterResponse{}
	for rows.Next() {
		var wc entity.WorkCenterResponse
		var warehouseName, wcAssetID, wcAssetName sql.NullString
		var nextMaint, lastMaint sql.NullTime

		err := rows.Scan(
			&wc.ID, &wc.Code, &wc.Name, &wc.Description, &wc.WarehouseID, &warehouseName,
			&wc.Department, &wc.CapacityPerHour, &wc.EfficiencyFactor, &wc.OEETarget,
			&wc.WorkingHoursPerDay, &wc.HourlyCost, &wc.SetupCost, &wc.OverheadCost,
			&wc.AssetValue, &wc.UsefulLifeYears,
			&wc.PowerKW, &wc.ElectricityRate,
			&wc.AnnualMaintenance, &wc.OperatorMonthlySalary, &wc.LaborRateType, &wc.CostMethod, &wc.RequireOperator,
			&wc.DepreciationPerHour, &wc.ElectricityPerHour,
			&wc.MaintenancePerHour, &wc.LaborPerHour,
			&wc.Currency, &wc.Status, &wc.IsAvailable, &nextMaint,
			&lastMaint, &wc.TotalJobsCompleted, &wc.TotalHoursWorked,
			&wc.CurrentUtilization, &wc.Notes, &wc.CreatedAt, &wc.UpdatedAt,
			&wcAssetID, &wcAssetName,
		)
		if err != nil {
			h.log.Error("Failed to scan work center", "error", err)
			continue
		}

		if warehouseName.Valid {
			wc.WarehouseName = &warehouseName.String
		}
		if wcAssetID.Valid {
			if aid, aErr := uuid.Parse(wcAssetID.String); aErr == nil {
				wc.AssetID = &aid
			}
		}
		if wcAssetName.Valid {
			wc.AssetName = &wcAssetName.String
		}
		if nextMaint.Valid {
			s := nextMaint.Time.Format("2006-01-02")
			wc.NextMaintenanceDate = &s
		}
		if lastMaint.Valid {
			s := lastMaint.Time.Format("2006-01-02")
			wc.LastMaintenanceDate = &s
		}

		workCenters = append(workCenters, wc)
	}

	pagination := entity.NewPagination(filter.Page, filter.Limit)
	pagination.Calculate(total)
	response.SuccessWithPagination(c, workCenters, pagination)
}

// GetWorkCenter godoc
// @Summary Get work center
// @Description Get a single work center by ID
// @Tags Manufacturing
// @Accept json
// @Produce json
// @Param id path string true "Work Center ID"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /manufacturing/work-centers/{id} [get]
func (h *Handler) GetWorkCenter(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid work center ID")
		return
	}

	query := `
		SELECT wc.id, wc.code, wc.name, wc.description, wc.warehouse_id, w.name as warehouse_name,
			   wc.department, wc.capacity_per_hour, wc.efficiency_factor, wc.oee_target,
			   wc.working_hours_per_day, wc.hourly_cost, wc.setup_cost, wc.overhead_cost,
			   COALESCE(wc.asset_value,0), COALESCE(wc.useful_life_years,10),
			   COALESCE(wc.power_kw,0), COALESCE(wc.electricity_rate,0),
			   COALESCE(wc.annual_maintenance,0), COALESCE(wc.operator_monthly_salary,0), COALESCE(wc.labor_rate_type,'monthly'), COALESCE(wc.cost_method,'capacity'), COALESCE(wc.require_operator, false),
			   COALESCE(wc.depreciation_per_hour,0), COALESCE(wc.electricity_per_hour,0),
			   COALESCE(wc.maintenance_per_hour,0), COALESCE(wc.labor_per_hour,0),
			   wc.currency, wc.status, wc.is_available, wc.next_maintenance_date,
			   wc.last_maintenance_date, wc.total_jobs_completed, wc.total_hours_worked,
			   wc.current_utilization, wc.notes, wc.created_at, wc.updated_at,
			   wc.asset_id, fa.name as asset_name
		FROM work_centers wc
		LEFT JOIN warehouses w ON wc.warehouse_id = w.id
		LEFT JOIN fa_assets fa ON fa.id = wc.asset_id
		WHERE wc.id = $1 AND wc.tenant_id = $2 AND wc.deleted_at IS NULL
	`

	var wc entity.WorkCenterResponse
	var warehouseName, wcAssetID, wcAssetName sql.NullString
	var nextMaint, lastMaint sql.NullTime

	err = h.db.QueryRow(query, id, tenantID).Scan(
		&wc.ID, &wc.Code, &wc.Name, &wc.Description, &wc.WarehouseID, &warehouseName,
		&wc.Department, &wc.CapacityPerHour, &wc.EfficiencyFactor, &wc.OEETarget,
		&wc.WorkingHoursPerDay, &wc.HourlyCost, &wc.SetupCost, &wc.OverheadCost,
		&wc.AssetValue, &wc.UsefulLifeYears,
		&wc.PowerKW, &wc.ElectricityRate,
		&wc.AnnualMaintenance, &wc.OperatorMonthlySalary, &wc.LaborRateType, &wc.CostMethod, &wc.RequireOperator,
		&wc.DepreciationPerHour, &wc.ElectricityPerHour,
		&wc.MaintenancePerHour, &wc.LaborPerHour,
		&wc.Currency, &wc.Status, &wc.IsAvailable, &nextMaint,
		&lastMaint, &wc.TotalJobsCompleted, &wc.TotalHoursWorked,
		&wc.CurrentUtilization, &wc.Notes, &wc.CreatedAt, &wc.UpdatedAt,
		&wcAssetID, &wcAssetName,
	)

	if err == sql.ErrNoRows {
		response.NotFound(c, "Work center not found")
		return
	}
	if err != nil {
		h.log.Error("Failed to get work center", "error", err)
		response.InternalError(c, "Failed to retrieve work center")
		return
	}

	if warehouseName.Valid {
		wc.WarehouseName = &warehouseName.String
	}
	if wcAssetID.Valid {
		if aid, aErr := uuid.Parse(wcAssetID.String); aErr == nil {
			wc.AssetID = &aid
		}
	}
	if wcAssetName.Valid {
		wc.AssetName = &wcAssetName.String
	}
	if nextMaint.Valid {
		s := nextMaint.Time.Format("2006-01-02")
		wc.NextMaintenanceDate = &s
	}
	if lastMaint.Valid {
		s := lastMaint.Time.Format("2006-01-02")
		wc.LastMaintenanceDate = &s
	}

	response.Success(c, wc)
}

// CreateWorkCenter godoc
// @Summary Create work center
// @Description Create a new work center
// @Tags Manufacturing
// @Accept json
// @Produce json
// @Param input body entity.WorkCenterInput true "Work center input"
// @Success 201 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /manufacturing/work-centers [post]
func (h *Handler) CreateWorkCenter(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	userID, _ := middleware.GetUserID(c)

	var input entity.WorkCenterInput
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	// Set defaults
	capacityPerHour := 1.0
	if input.CapacityPerHour != nil {
		capacityPerHour = *input.CapacityPerHour
	}
	efficiencyFactor := 100.0
	if input.EfficiencyFactor != nil {
		efficiencyFactor = *input.EfficiencyFactor
	}
	oeeTarget := 85.0
	if input.OEETarget != nil {
		oeeTarget = *input.OEETarget
	}
	workingHours := 8.0
	if input.WorkingHoursPerDay != nil {
		workingHours = *input.WorkingHoursPerDay
	}
	hourlyCost := 0.0
	if input.HourlyCost != nil {
		hourlyCost = *input.HourlyCost
	}
	setupCost := 0.0
	if input.SetupCost != nil {
		setupCost = *input.SetupCost
	}
	overheadCost := 0.0
	if input.OverheadCost != nil {
		overheadCost = *input.OverheadCost
	}
	assetValue := 0.0
	if input.AssetValue != nil {
		assetValue = *input.AssetValue
	}
	usefulLifeYears := 10.0
	if input.UsefulLifeYears != nil {
		usefulLifeYears = *input.UsefulLifeYears
	}
	powerKW := 0.0
	if input.PowerKW != nil {
		powerKW = *input.PowerKW
	}
	electricityRate := 0.0
	if input.ElectricityRate != nil {
		electricityRate = *input.ElectricityRate
	}
	annualMaintenance := 0.0
	if input.AnnualMaintenance != nil {
		annualMaintenance = *input.AnnualMaintenance
	}
	operatorMonthlySalary := 0.0
	if input.OperatorMonthlySalary != nil {
		operatorMonthlySalary = *input.OperatorMonthlySalary
	}

	laborRateType := "monthly"
	if input.LaborRateType != nil && *input.LaborRateType != "" {
		laborRateType = *input.LaborRateType
	}

	// Auto-calculate cost breakdown if any detailed input is provided
	var depreciationPerHour, electricityPerHour, maintenancePerHour, laborPerHour float64
	hasDetailedCosts := assetValue > 0 || powerKW > 0 || annualMaintenance > 0 || operatorMonthlySalary > 0
	if hasDetailedCosts {
		depreciationPerHour, electricityPerHour, maintenancePerHour, laborPerHour, hourlyCost =
			calculateWorkCenterCosts(assetValue, usefulLifeYears, workingHours, powerKW, electricityRate, annualMaintenance, operatorMonthlySalary, overheadCost, laborRateType)
	}

	currency := "USD"
	if input.Currency != nil {
		currency = *input.Currency
	}
	status := "active"
	if input.Status != nil {
		status = *input.Status
	}
	isAvailable := true
	if input.IsAvailable != nil {
		isAvailable = *input.IsAvailable
	}

	var nextMaintDate *time.Time
	if input.NextMaintenanceDate != nil {
		t, err := time.Parse("2006-01-02", *input.NextMaintenanceDate)
		if err == nil {
			nextMaintDate = &t
		}
	}

	now := time.Now()
	id := uuid.New()

	// Get organization ID from context
	orgID, _ := middleware.GetOrganizationID(c)
	var orgIDPtr *uuid.UUID
	if orgID != uuid.Nil {
		orgIDPtr = &orgID
	}

	query := `
		INSERT INTO work_centers (
			id, tenant_id, organization_id, code, name, description, warehouse_id, department,
			capacity_per_hour, efficiency_factor, oee_target, working_hours_per_day,
			hourly_cost, setup_cost, overhead_cost,
			asset_value, useful_life_years, power_kw, electricity_rate, annual_maintenance, operator_monthly_salary, labor_rate_type, cost_method, require_operator,
			depreciation_per_hour, electricity_per_hour, maintenance_per_hour, labor_per_hour,
			currency, status, is_available,
			next_maintenance_date, notes, asset_id, created_by, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15,
			$16, $17, $18, $19, $20, $21, $22, $23, $24, $25, $26, $27, $28, $29, $30, $31, $32, $33, $34, $35, $36, $37)
		RETURNING id
	`

	costMethod := "capacity"
	if input.CostMethod != nil && *input.CostMethod != "" {
		costMethod = *input.CostMethod
	}
	requireOperator := false
	if input.RequireOperator != nil {
		requireOperator = *input.RequireOperator
	}

	// fa_assets link (B7): uuid.Nil / absent → NULL.
	var wcAssetID interface{}
	if input.AssetID != nil && *input.AssetID != uuid.Nil {
		wcAssetID = *input.AssetID
	}

	err := h.db.QueryRow(query,
		id, tenantID, orgIDPtr, input.Code, input.Name, input.Description, input.WarehouseID,
		input.Department, capacityPerHour, efficiencyFactor, oeeTarget, workingHours,
		hourlyCost, setupCost, overheadCost,
		assetValue, usefulLifeYears, powerKW, electricityRate, annualMaintenance, operatorMonthlySalary, laborRateType, costMethod, requireOperator,
		depreciationPerHour, electricityPerHour, maintenancePerHour, laborPerHour,
		currency, status, isAvailable,
		nextMaintDate, input.Notes, wcAssetID, userID, now, now,
	).Scan(&id)

	if err != nil {
		h.log.Error("Failed to create work center", "error", err)
		if strings.Contains(err.Error(), "unique") {
			response.BadRequest(c, "Work center code already exists")
			return
		}
		response.InternalError(c, "Failed to create work center")
		return
	}

	resp := &entity.WorkCenterResponse{
		ID:                    id,
		Code:                  input.Code,
		Name:                  input.Name,
		Description:           input.Description,
		WarehouseID:           input.WarehouseID,
		Department:            input.Department,
		CapacityPerHour:       capacityPerHour,
		EfficiencyFactor:      efficiencyFactor,
		OEETarget:             oeeTarget,
		WorkingHoursPerDay:    workingHours,
		HourlyCost:            hourlyCost,
		SetupCost:             setupCost,
		OverheadCost:          overheadCost,
		AssetValue:            assetValue,
		UsefulLifeYears:       usefulLifeYears,
		PowerKW:               powerKW,
		ElectricityRate:       electricityRate,
		AnnualMaintenance:     annualMaintenance,
		OperatorMonthlySalary: operatorMonthlySalary,
		DepreciationPerHour:   depreciationPerHour,
		ElectricityPerHour:    electricityPerHour,
		MaintenancePerHour:    maintenancePerHour,
		LaborPerHour:          laborPerHour,
		Currency:              currency,
		Status:                status,
		IsAvailable:           isAvailable,
		Notes:                 input.Notes,
		CreatedAt:             now,
		UpdatedAt:             now,
	}

	if input.NextMaintenanceDate != nil {
		resp.NextMaintenanceDate = input.NextMaintenanceDate
	}
	if input.AssetID != nil && *input.AssetID != uuid.Nil {
		resp.AssetID = input.AssetID
	}

	response.Created(c, resp)
}

// UpdateWorkCenter godoc
// @Summary Update work center
// @Description Update an existing work center
// @Tags Manufacturing
// @Accept json
// @Produce json
// @Param id path string true "Work Center ID"
// @Param input body entity.WorkCenterInput true "Work center input"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /manufacturing/work-centers/{id} [put]
func (h *Handler) UpdateWorkCenter(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid work center ID")
		return
	}

	var input entity.WorkCenterInput
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	// Build dynamic update query
	updates := []string{}
	args := []interface{}{}
	argCount := 0

	if input.Code != "" {
		argCount++
		updates = append(updates, fmt.Sprintf("code = $%d", argCount))
		args = append(args, input.Code)
	}
	if input.Name != "" {
		argCount++
		updates = append(updates, fmt.Sprintf("name = $%d", argCount))
		args = append(args, input.Name)
	}
	if input.Description != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("description = $%d", argCount))
		args = append(args, *input.Description)
	}
	if input.WarehouseID != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("warehouse_id = $%d", argCount))
		args = append(args, *input.WarehouseID)
	}
	if input.AssetID != nil {
		// fa_assets link (B7): uuid.Nil clears it.
		argCount++
		updates = append(updates, fmt.Sprintf("asset_id = $%d", argCount))
		if *input.AssetID == uuid.Nil {
			args = append(args, nil)
		} else {
			args = append(args, *input.AssetID)
		}
	}
	if input.Department != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("department = $%d", argCount))
		args = append(args, *input.Department)
	}
	if input.CapacityPerHour != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("capacity_per_hour = $%d", argCount))
		args = append(args, *input.CapacityPerHour)
	}
	if input.EfficiencyFactor != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("efficiency_factor = $%d", argCount))
		args = append(args, *input.EfficiencyFactor)
	}
	if input.OEETarget != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("oee_target = $%d", argCount))
		args = append(args, *input.OEETarget)
	}
	if input.WorkingHoursPerDay != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("working_hours_per_day = $%d", argCount))
		args = append(args, *input.WorkingHoursPerDay)
	}
	// Whether any breakdown component is being changed — used to decide
	// whether the explicit hourly_cost input should be applied now or
	// deferred. When a breakdown field changes the detailed-compute block
	// below sets hourly_cost to the computed total, so skip the manual
	// assignment here to avoid assigning the same column twice.
	costFieldChangedEarly := input.AssetValue != nil || input.UsefulLifeYears != nil ||
		input.PowerKW != nil || input.ElectricityRate != nil ||
		input.AnnualMaintenance != nil || input.OperatorMonthlySalary != nil ||
		input.OverheadCost != nil || input.WorkingHoursPerDay != nil
	if input.HourlyCost != nil && !costFieldChangedEarly {
		argCount++
		updates = append(updates, fmt.Sprintf("hourly_cost = $%d", argCount))
		args = append(args, *input.HourlyCost)
	}
	if input.SetupCost != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("setup_cost = $%d", argCount))
		args = append(args, *input.SetupCost)
	}
	if input.OverheadCost != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("overhead_cost = $%d", argCount))
		args = append(args, *input.OverheadCost)
	}
	if input.AssetValue != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("asset_value = $%d", argCount))
		args = append(args, *input.AssetValue)
	}
	if input.UsefulLifeYears != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("useful_life_years = $%d", argCount))
		args = append(args, *input.UsefulLifeYears)
	}
	if input.PowerKW != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("power_kw = $%d", argCount))
		args = append(args, *input.PowerKW)
	}
	if input.ElectricityRate != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("electricity_rate = $%d", argCount))
		args = append(args, *input.ElectricityRate)
	}
	if input.AnnualMaintenance != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("annual_maintenance = $%d", argCount))
		args = append(args, *input.AnnualMaintenance)
	}
	if input.OperatorMonthlySalary != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("operator_monthly_salary = $%d", argCount))
		args = append(args, *input.OperatorMonthlySalary)
	}
	if input.LaborRateType != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("labor_rate_type = $%d", argCount))
		args = append(args, *input.LaborRateType)
	}
	if input.CostMethod != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("cost_method = $%d", argCount))
		args = append(args, *input.CostMethod)
	}
	if input.RequireOperator != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("require_operator = $%d", argCount))
		args = append(args, *input.RequireOperator)
	}

	// Recalculate cost breakdown if any cost-related field changed.
	// (costFieldChangedEarly was computed above to gate the explicit
	// hourly_cost assignment — reuse it here.)
	if costFieldChangedEarly {
		var cur struct {
			AssetValue, UsefulLifeYears, WorkingHoursPerDay float64
			PowerKW, ElectricityRate, AnnualMaintenance     float64
			OperatorMonthlySalary, OverheadCost             float64
			LaborRateType                                   string
		}
		h.db.QueryRow(`
			SELECT COALESCE(asset_value,0), COALESCE(useful_life_years,10), COALESCE(working_hours_per_day,8),
				COALESCE(power_kw,0), COALESCE(electricity_rate,0), COALESCE(annual_maintenance,0),
				COALESCE(operator_monthly_salary,0), COALESCE(overhead_cost,0), COALESCE(labor_rate_type,'monthly')
			FROM work_centers WHERE id = $1 AND tenant_id = $2
		`, id, tenantID).Scan(&cur.AssetValue, &cur.UsefulLifeYears, &cur.WorkingHoursPerDay,
			&cur.PowerKW, &cur.ElectricityRate, &cur.AnnualMaintenance,
			&cur.OperatorMonthlySalary, &cur.OverheadCost, &cur.LaborRateType)

		if input.AssetValue != nil {
			cur.AssetValue = *input.AssetValue
		}
		if input.UsefulLifeYears != nil {
			cur.UsefulLifeYears = *input.UsefulLifeYears
		}
		if input.WorkingHoursPerDay != nil {
			cur.WorkingHoursPerDay = *input.WorkingHoursPerDay
		}
		if input.PowerKW != nil {
			cur.PowerKW = *input.PowerKW
		}
		if input.ElectricityRate != nil {
			cur.ElectricityRate = *input.ElectricityRate
		}
		if input.AnnualMaintenance != nil {
			cur.AnnualMaintenance = *input.AnnualMaintenance
		}
		if input.OperatorMonthlySalary != nil {
			cur.OperatorMonthlySalary = *input.OperatorMonthlySalary
		}
		if input.OverheadCost != nil {
			cur.OverheadCost = *input.OverheadCost
		}
		if input.LaborRateType != nil {
			cur.LaborRateType = *input.LaborRateType
		}

		hasDetailed := cur.AssetValue > 0 || cur.PowerKW > 0 || cur.AnnualMaintenance > 0 || cur.OperatorMonthlySalary > 0
		if hasDetailed {
			dep, elec, maint, labor, total := calculateWorkCenterCosts(
				cur.AssetValue, cur.UsefulLifeYears, cur.WorkingHoursPerDay,
				cur.PowerKW, cur.ElectricityRate, cur.AnnualMaintenance,
				cur.OperatorMonthlySalary, cur.OverheadCost, cur.LaborRateType)
			argCount++
			updates = append(updates, fmt.Sprintf("depreciation_per_hour = $%d", argCount))
			args = append(args, dep)
			argCount++
			updates = append(updates, fmt.Sprintf("electricity_per_hour = $%d", argCount))
			args = append(args, elec)
			argCount++
			updates = append(updates, fmt.Sprintf("maintenance_per_hour = $%d", argCount))
			args = append(args, maint)
			argCount++
			updates = append(updates, fmt.Sprintf("labor_per_hour = $%d", argCount))
			args = append(args, labor)
			// Always overwrite hourly_cost with the computed total when the
			// breakdown components are provided. The breakdown is the source
			// of truth — matches create-handler behavior. Without this, a WC
			// edited to add breakdown inputs would keep hourly_cost = 0
			// because the frontend always sends hourly_cost in the payload.
			argCount++
			updates = append(updates, fmt.Sprintf("hourly_cost = $%d", argCount))
			args = append(args, total)
		}
	}

	if input.Currency != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("currency = $%d", argCount))
		args = append(args, *input.Currency)
	}
	if input.Status != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("status = $%d", argCount))
		args = append(args, *input.Status)
	}
	if input.IsAvailable != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("is_available = $%d", argCount))
		args = append(args, *input.IsAvailable)
	}
	if input.NextMaintenanceDate != nil {
		argCount++
		t, _ := time.Parse("2006-01-02", *input.NextMaintenanceDate)
		updates = append(updates, fmt.Sprintf("next_maintenance_date = $%d", argCount))
		args = append(args, t)
	}
	if input.Notes != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("notes = $%d", argCount))
		args = append(args, *input.Notes)
	}

	if len(updates) == 0 {
		response.BadRequest(c, "No fields to update")
		return
	}

	// Add updated_at
	argCount++
	updates = append(updates, fmt.Sprintf("updated_at = $%d", argCount))
	args = append(args, time.Now())

	// Add WHERE clause params
	argCount++
	args = append(args, id)
	argCount++
	args = append(args, tenantID)

	query := fmt.Sprintf(
		"UPDATE work_centers SET %s WHERE id = $%d AND tenant_id = $%d AND deleted_at IS NULL",
		strings.Join(updates, ", "), argCount-1, argCount,
	)

	result, err := h.db.Exec(query, args...)
	if err != nil {
		h.log.Error("Failed to update work center", "error", err)
		response.InternalError(c, "Failed to update work center")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Work center not found")
		return
	}

	// Fetch and return updated work center
	h.GetWorkCenter(c)
}

// DeleteWorkCenter godoc
// @Summary Delete work center
// @Description Soft delete a work center
// @Tags Manufacturing
// @Accept json
// @Produce json
// @Param id path string true "Work Center ID"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /manufacturing/work-centers/{id} [delete]
func (h *Handler) DeleteWorkCenter(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid work center ID")
		return
	}

	query := `UPDATE work_centers SET deleted_at = $1 WHERE id = $2 AND tenant_id = $3 AND deleted_at IS NULL`
	result, err := h.db.Exec(query, time.Now(), id, tenantID)
	if err != nil {
		h.log.Error("Failed to delete work center", "error", err)
		response.InternalError(c, "Failed to delete work center")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Work center not found")
		return
	}

	response.Success(c, map[string]interface{}{"message": "Work center deleted successfully"})
}

// =====================================================
// PRODUCTION ORDER HANDLERS
// =====================================================

// ListProductionOrders returns all production orders with filtering
// ListProductionOrders godoc
// @Summary List production orders
// @Description Get a paginated list of production/manufacturing orders
// @Tags Manufacturing
// @Accept json
// @Produce json
// @Param page query int false "Page number" default(1)
// @Param limit query int false "Items per page" default(20)
// @Param status query string false "Filter by status"
// @Success 200 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /manufacturing/production-orders [get]
func (h *Handler) ListProductionOrders(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	var filter entity.ProductionOrderFilter
	if err := c.ShouldBindQuery(&filter); err != nil {
		response.BadRequest(c, "Invalid query parameters")
		return
	}

	// Set defaults
	if filter.Page <= 0 {
		filter.Page = 1
	}
	if filter.Limit <= 0 || filter.Limit > 10000 {
		filter.Limit = 100
	}
	if filter.SortBy == "" {
		filter.SortBy = "created_at"
	}
	if filter.SortOrder == "" {
		filter.SortOrder = "desc"
	}

	baseQuery := `
		SELECT po.id, po.code, po.name, po.product_id, p.name as product_name, p.code as product_code,
			   po.bom_id, b.name as bom_name, po.quantity_planned, po.quantity_produced, po.quantity_scrapped,
			   po.uom, po.mold_count, po.shift, po.current_stage, po.package_count, po.good_quantity, po.reject_quantity,
			   po.scheduled_start, po.scheduled_end, po.actual_start, po.actual_end,
			   po.priority, po.status, po.progress_percent, po.source_type, po.warehouse_id,
			   w.name as warehouse_name, po.planned_cost, po.actual_cost, po.material_cost,
			   po.labor_cost, po.overhead_cost, po.currency, po.assigned_to, u.first_name || ' ' || u.last_name as assigned_to_name,
			   po.work_center_id, wc.name as work_center_name, po.requires_quality_check, po.quality_status,
			   po.notes, po.tags, po.created_by, po.confirmed_at, po.completed_at, po.created_at, po.updated_at,
			   po.manufacturing_category_id, mc.name as manufacturing_category_name,
			   cust.name as customer_name
		FROM production_orders po
		LEFT JOIN products p ON po.product_id = p.id
		LEFT JOIN product_boms b ON po.bom_id = b.id
		LEFT JOIN warehouses w ON po.warehouse_id = w.id
		LEFT JOIN users u ON po.assigned_to = u.id
		LEFT JOIN work_centers wc ON po.work_center_id = wc.id
		LEFT JOIN manufacturing_categories mc ON po.manufacturing_category_id = mc.id
		LEFT JOIN contacts cust ON po.customer_id = cust.id
		WHERE po.tenant_id = $1 AND po.deleted_at IS NULL
	`

	countQuery := `SELECT COUNT(*) FROM production_orders po WHERE po.tenant_id = $1 AND po.deleted_at IS NULL`
	args := []interface{}{tenantID}
	countArgs := []interface{}{tenantID}
	argCount := 1

	// Filter by organization
	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		argCount++
		baseQuery += fmt.Sprintf(" AND po.organization_id = $%d", argCount)
		countQuery += fmt.Sprintf(" AND po.organization_id = $%d", argCount)
		args = append(args, orgID)
		countArgs = append(countArgs, orgID)
	}

	// Apply filters
	if filter.Status != nil && *filter.Status != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND po.status = $%d", argCount)
		countQuery += fmt.Sprintf(" AND po.status = $%d", argCount)
		args = append(args, *filter.Status)
		countArgs = append(countArgs, *filter.Status)
	}

	if filter.ProductID != nil {
		argCount++
		baseQuery += fmt.Sprintf(" AND po.product_id = $%d", argCount)
		countQuery += fmt.Sprintf(" AND po.product_id = $%d", argCount)
		args = append(args, *filter.ProductID)
		countArgs = append(countArgs, *filter.ProductID)
	}

	if filter.WorkCenterID != nil {
		argCount++
		baseQuery += fmt.Sprintf(" AND po.work_center_id = $%d", argCount)
		countQuery += fmt.Sprintf(" AND po.work_center_id = $%d", argCount)
		args = append(args, *filter.WorkCenterID)
		countArgs = append(countArgs, *filter.WorkCenterID)
	}

	if filter.AssignedTo != nil {
		argCount++
		baseQuery += fmt.Sprintf(" AND po.assigned_to = $%d", argCount)
		countQuery += fmt.Sprintf(" AND po.assigned_to = $%d", argCount)
		args = append(args, *filter.AssignedTo)
		countArgs = append(countArgs, *filter.AssignedTo)
	}

	if filter.Priority != nil {
		argCount++
		baseQuery += fmt.Sprintf(" AND po.priority = $%d", argCount)
		countQuery += fmt.Sprintf(" AND po.priority = $%d", argCount)
		args = append(args, *filter.Priority)
		countArgs = append(countArgs, *filter.Priority)
	}

	if filter.CategoryID != nil {
		argCount++
		baseQuery += fmt.Sprintf(" AND po.manufacturing_category_id = $%d", argCount)
		countQuery += fmt.Sprintf(" AND po.manufacturing_category_id = $%d", argCount)
		args = append(args, *filter.CategoryID)
		countArgs = append(countArgs, *filter.CategoryID)
	}

	if filter.DateFrom != nil && *filter.DateFrom != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND po.scheduled_start >= $%d", argCount)
		countQuery += fmt.Sprintf(" AND po.scheduled_start >= $%d", argCount)
		args = append(args, *filter.DateFrom)
		countArgs = append(countArgs, *filter.DateFrom)
	}

	if filter.DateTo != nil && *filter.DateTo != "" {
		argCount++
		baseQuery += fmt.Sprintf(" AND po.scheduled_end <= $%d", argCount)
		countQuery += fmt.Sprintf(" AND po.scheduled_end <= $%d", argCount)
		args = append(args, *filter.DateTo)
		countArgs = append(countArgs, *filter.DateTo)
	}

	if filter.Search != nil && *filter.Search != "" {
		argCount++
		searchPattern := "%" + *filter.Search + "%"
		baseQuery += fmt.Sprintf(" AND (po.code ILIKE $%d OR po.name ILIKE $%d OR p.name ILIKE $%d)", argCount, argCount, argCount)
		countQuery += fmt.Sprintf(" AND (po.code ILIKE $%d OR po.name ILIKE $%d)", argCount, argCount)
		args = append(args, searchPattern)
		countArgs = append(countArgs, searchPattern)
	}

	// Get total count
	var total int
	err := h.db.QueryRow(countQuery, countArgs...).Scan(&total)
	if err != nil {
		h.log.Error("Failed to count production orders", "error", err)
		response.InternalError(c, "Failed to retrieve production orders")
		return
	}

	// Sorting
	validSortColumns := map[string]string{
		"code":            "po.code",
		"status":          "po.status",
		"priority":        "po.priority",
		"scheduled_start": "po.scheduled_start",
		"created_at":      "po.created_at",
	}
	sortColumn := validSortColumns[filter.SortBy]
	if sortColumn == "" {
		sortColumn = "po.created_at"
	}
	sortOrder := "ASC"
	if strings.ToLower(filter.SortOrder) == "desc" {
		sortOrder = "DESC"
	}
	baseQuery += fmt.Sprintf(" ORDER BY %s %s", sortColumn, sortOrder)

	// Pagination
	offset := (filter.Page - 1) * filter.Limit
	baseQuery += fmt.Sprintf(" LIMIT %d OFFSET %d", filter.Limit, offset)

	// Execute query
	rows, err := h.db.Query(baseQuery, args...)
	if err != nil {
		h.log.Error("Failed to list production orders", "error", err)
		response.InternalError(c, "Failed to retrieve production orders")
		return
	}
	defer rows.Close()

	orders := []entity.ProductionOrderResponse{}
	for rows.Next() {
		var po entity.ProductionOrderResponse
		var bomName, warehouseName, assignedToName, workCenterName, shift, categoryName, customerName sql.NullString
		var scheduledStart, scheduledEnd, actualStart, actualEnd, confirmedAt, completedAt sql.NullTime
		var tags []byte

		err := rows.Scan(
			&po.ID, &po.Code, &po.Name, &po.ProductID, &po.ProductName, &po.ProductCode,
			&po.BOMID, &bomName, &po.QuantityPlanned, &po.QuantityProduced, &po.QuantityScrapped,
			&po.UOM, &po.MoldCount, &shift, &po.CurrentStage, &po.PackageCount, &po.GoodQuantity, &po.RejectQuantity,
			&scheduledStart, &scheduledEnd, &actualStart, &actualEnd,
			&po.Priority, &po.Status, &po.ProgressPercent, &po.SourceType, &po.WarehouseID,
			&warehouseName, &po.PlannedCost, &po.ActualCost, &po.MaterialCost,
			&po.LaborCost, &po.OverheadCost, &po.Currency, &po.AssignedTo, &assignedToName,
			&po.WorkCenterID, &workCenterName, &po.RequiresQualityCheck, &po.QualityStatus,
			&po.Notes, &tags, &po.CreatedBy, &confirmedAt, &completedAt, &po.CreatedAt, &po.UpdatedAt,
			&po.ManufacturingCategoryID, &categoryName, &customerName,
		)
		if err != nil {
			h.log.Error("Failed to scan production order", "error", err)
			continue
		}

		if bomName.Valid {
			po.BOMName = &bomName.String
		}
		if warehouseName.Valid {
			po.WarehouseName = &warehouseName.String
		}
		if assignedToName.Valid {
			po.AssignedToName = &assignedToName.String
		}
		if workCenterName.Valid {
			po.WorkCenterName = &workCenterName.String
		}
		if shift.Valid {
			po.Shift = &shift.String
		}
		if categoryName.Valid {
			po.ManufacturingCategoryName = &categoryName.String
		}
		if customerName.Valid {
			po.CustomerName = &customerName.String
		}
		if scheduledStart.Valid {
			s := scheduledStart.Time.Format("2006-01-02")
			po.ScheduledStart = &s
		}
		if scheduledEnd.Valid {
			s := scheduledEnd.Time.Format("2006-01-02")
			po.ScheduledEnd = &s
		}
		if actualStart.Valid {
			s := actualStart.Time.Format(time.RFC3339)
			po.ActualStart = &s
		}
		if actualEnd.Valid {
			s := actualEnd.Time.Format(time.RFC3339)
			po.ActualEnd = &s
		}
		if confirmedAt.Valid {
			s := confirmedAt.Time.Format(time.RFC3339)
			po.ConfirmedAt = &s
		}
		if completedAt.Valid {
			s := completedAt.Time.Format(time.RFC3339)
			po.CompletedAt = &s
		}

		po.QuantityRemaining = po.QuantityPlanned - po.QuantityProduced - po.QuantityScrapped
		po.Tags = []string{}

		orders = append(orders, po)
	}

	pagination := entity.NewPagination(filter.Page, filter.Limit)
	pagination.Calculate(total)
	response.SuccessWithPagination(c, orders, pagination)
}

// GetProductionOrder godoc
// @Summary Get production order
// @Description Get a single production order by ID
// @Tags Manufacturing
// @Accept json
// @Produce json
// @Param id path string true "Production Order ID"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /manufacturing/production-orders/{id} [get]
func (h *Handler) GetProductionOrder(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid production order ID")
		return
	}

	query := `
		SELECT po.id, po.code, po.name, po.product_id, p.name as product_name, p.code as product_code,
			   po.bom_id, b.name as bom_name, po.quantity_planned, po.quantity_produced, po.quantity_scrapped,
			   po.uom, po.mold_count, po.shift, po.current_stage, po.package_count, po.good_quantity, po.reject_quantity,
			   po.scheduled_start, po.scheduled_end, po.actual_start, po.actual_end,
			   po.priority, po.status, po.progress_percent, po.source_type, po.warehouse_id,
			   w.name as warehouse_name, po.planned_cost, po.actual_cost, po.material_cost,
			   po.labor_cost, po.overhead_cost, po.currency, po.assigned_to, u.first_name || ' ' || u.last_name as assigned_to_name,
			   po.work_center_id, wc.name as work_center_name, po.requires_quality_check, po.quality_status,
			   po.notes, po.tags, po.created_by, cu.first_name || ' ' || cu.last_name as created_by_name,
			   po.confirmed_at, po.completed_at, po.created_at, po.updated_at,
			   po.manufacturing_category_id, mc.name as manufacturing_category_name,
			   po.has_split_output,
			   po.sales_order_id, so.order_number as sales_order_number,
			   po.shortfall_reason
		FROM production_orders po
		LEFT JOIN products p ON po.product_id = p.id
		LEFT JOIN product_boms b ON po.bom_id = b.id
		LEFT JOIN warehouses w ON po.warehouse_id = w.id
		LEFT JOIN users u ON po.assigned_to = u.id
		LEFT JOIN work_centers wc ON po.work_center_id = wc.id
		LEFT JOIN users cu ON po.created_by = cu.id
		LEFT JOIN manufacturing_categories mc ON po.manufacturing_category_id = mc.id
		LEFT JOIN sales_orders so ON po.sales_order_id = so.id
		WHERE po.id = $1 AND po.tenant_id = $2 AND po.deleted_at IS NULL
		  AND (po.organization_id IS NULL OR $3::uuid IS NULL OR po.organization_id = $3)
	`

	// Org scoping (audit §2.9): single-record reads were tenant-only, so a
	// user in Org A could read Org B's order by UUID.
	var orgArg interface{}
	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		orgArg = orgID
	}

	var po entity.ProductionOrderResponse
	var bomName, warehouseName, assignedToName, workCenterName, createdByName, shift, categoryName, salesOrderNumber sql.NullString
	var scheduledStart, scheduledEnd, actualStart, actualEnd, confirmedAt, completedAt sql.NullTime
	var tags []byte

	err = h.db.QueryRow(query, id, tenantID, orgArg).Scan(
		&po.ID, &po.Code, &po.Name, &po.ProductID, &po.ProductName, &po.ProductCode,
		&po.BOMID, &bomName, &po.QuantityPlanned, &po.QuantityProduced, &po.QuantityScrapped,
		&po.UOM, &po.MoldCount, &shift, &po.CurrentStage, &po.PackageCount, &po.GoodQuantity, &po.RejectQuantity,
		&scheduledStart, &scheduledEnd, &actualStart, &actualEnd,
		&po.Priority, &po.Status, &po.ProgressPercent, &po.SourceType, &po.WarehouseID,
		&warehouseName, &po.PlannedCost, &po.ActualCost, &po.MaterialCost,
		&po.LaborCost, &po.OverheadCost, &po.Currency, &po.AssignedTo, &assignedToName,
		&po.WorkCenterID, &workCenterName, &po.RequiresQualityCheck, &po.QualityStatus,
		&po.Notes, &tags, &po.CreatedBy, &createdByName,
		&confirmedAt, &completedAt, &po.CreatedAt, &po.UpdatedAt,
		&po.ManufacturingCategoryID, &categoryName,
		&po.HasSplitOutput,
		&po.SalesOrderID, &salesOrderNumber,
		&po.ShortfallReason,
	)

	if err == sql.ErrNoRows {
		response.NotFound(c, "Production order not found")
		return
	}
	if err != nil {
		h.log.Error("Failed to get production order", "error", err)
		response.InternalError(c, "Failed to retrieve production order")
		return
	}

	if bomName.Valid {
		po.BOMName = &bomName.String
	}
	if warehouseName.Valid {
		po.WarehouseName = &warehouseName.String
	}
	if assignedToName.Valid {
		po.AssignedToName = &assignedToName.String
	}
	if workCenterName.Valid {
		po.WorkCenterName = &workCenterName.String
	}
	if createdByName.Valid {
		po.CreatedByName = &createdByName.String
	}
	if shift.Valid {
		po.Shift = &shift.String
	}
	if categoryName.Valid {
		po.ManufacturingCategoryName = &categoryName.String
	}
	if salesOrderNumber.Valid {
		po.SalesOrderNumber = &salesOrderNumber.String
	}
	if scheduledStart.Valid {
		s := scheduledStart.Time.Format("2006-01-02")
		po.ScheduledStart = &s
	}
	if scheduledEnd.Valid {
		s := scheduledEnd.Time.Format("2006-01-02")
		po.ScheduledEnd = &s
	}
	if actualStart.Valid {
		s := actualStart.Time.Format(time.RFC3339)
		po.ActualStart = &s
	}
	if actualEnd.Valid {
		s := actualEnd.Time.Format(time.RFC3339)
		po.ActualEnd = &s
	}
	if confirmedAt.Valid {
		s := confirmedAt.Time.Format(time.RFC3339)
		po.ConfirmedAt = &s
	}
	if completedAt.Valid {
		s := completedAt.Time.Format(time.RFC3339)
		po.CompletedAt = &s
	}

	po.QuantityRemaining = po.QuantityPlanned - po.QuantityProduced - po.QuantityScrapped
	po.Tags = []string{}

	// If order is confirmed or beyond, fetch associated work orders
	if po.Status != "draft" {
		woQuery := `
			SELECT wo.id, wo.code, wo.name, wo.sequence, wo.work_center_id, wc.name as work_center_name,
				   wo.quantity_to_produce, wo.quantity_produced, wo.quantity_scrapped, wo.uom,
				   wo.planned_duration_hours, wo.actual_duration_hours, wo.setup_time_hours,
				   wo.planned_cost, wo.actual_cost, wo.labor_cost, wo.machine_cost,
				   wo.status, wo.progress_percent, wo.instructions, wo.notes, wo.created_at
			FROM work_orders wo
			LEFT JOIN work_centers wc ON wo.work_center_id = wc.id
			WHERE wo.production_order_id = $1 AND wo.tenant_id = $2 AND wo.deleted_at IS NULL
			ORDER BY wo.sequence ASC
		`
		woRows, err := h.db.Query(woQuery, id, tenantID)
		if err == nil {
			defer woRows.Close()
			for woRows.Next() {
				var wo entity.WorkOrder
				var wcID *uuid.UUID
				var wcName sql.NullString
				var instructions, notes sql.NullString

				err := woRows.Scan(
					&wo.ID, &wo.Code, &wo.Name, &wo.Sequence, &wcID, &wcName,
					&wo.QuantityToProduce, &wo.QuantityProduced, &wo.QuantityScrapped, &wo.UOM,
					&wo.PlannedDurationHrs, &wo.ActualDurationHrs, &wo.SetupTimeHrs,
					&wo.PlannedCost, &wo.ActualCost, &wo.LaborCost, &wo.MachineCost,
					&wo.Status, &wo.ProgressPercent, &instructions, &notes, &wo.CreatedAt,
				)
				if err != nil {
					h.log.Error("Failed to scan work order", "error", err)
					continue
				}
				wo.ProductionOrderID = id
				wo.TenantID = tenantID
				if wcID != nil {
					wo.WorkCenterID = wcID
				}
				if wcName.Valid {
					wo.WorkCenterName = &wcName.String
				}
				if instructions.Valid {
					wo.Instructions = &instructions.String
				}
				if notes.Valid {
					wo.Notes = &notes.String
				}
				po.WorkOrders = append(po.WorkOrders, wo)
			}
		}
	}

	// Fetch BOM operations if BOM exists
	if po.BOMID != nil {
		bomOpsQuery := `
			SELECT id, sequence, operation_name, work_center_id, setup_time_minutes,
				   run_time_minutes, labor_cost, overhead_cost, notes
			FROM bom_operations
			WHERE bom_id = $1
			ORDER BY sequence ASC
		`
		bomOpsRows, err := h.db.Query(bomOpsQuery, *po.BOMID)
		if err == nil {
			defer bomOpsRows.Close()
			po.BOMOperations = []map[string]interface{}{}
			for bomOpsRows.Next() {
				var opID uuid.UUID
				var sequence int
				var operationName string
				var workCenterID *uuid.UUID
				var setupTime, runTime, laborCost, overheadCost float64
				var notes sql.NullString

				err := bomOpsRows.Scan(&opID, &sequence, &operationName, &workCenterID,
					&setupTime, &runTime, &laborCost, &overheadCost, &notes)
				if err == nil {
					op := map[string]interface{}{
						"id":                 opID,
						"sequence":           sequence,
						"name":               operationName,
						"operation_name":     operationName,
						"work_center_id":     workCenterID,
						"setup_time_minutes": setupTime,
						"run_time_minutes":   runTime,
						"labor_cost":         laborCost,
						"overhead_cost":      overheadCost,
					}
					if notes.Valid {
						op["notes"] = notes.String
					}
					po.BOMOperations = append(po.BOMOperations, op)
				}
			}
		}
	}

	response.Success(c, po)
}

// CreateProductionOrder godoc
// @Summary Create production order
// @Description Create a new production order
// @Tags Manufacturing
// @Accept json
// @Produce json
// @Param input body entity.ProductionOrderInput true "Production order input"
// @Success 201 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /manufacturing/production-orders [post]
func (h *Handler) CreateProductionOrder(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	userID, _ := middleware.GetUserID(c)

	var input entity.ProductionOrderInput
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	// Generate code — use MAX to avoid conflicts with deleted orders or multi-org gaps
	now := time.Now()
	id := uuid.New()
	var maxCode sql.NullString
	h.db.QueryRow(`SELECT MAX(code) FROM production_orders WHERE tenant_id = $1 AND code ~ '^MO[0-9]+$'`, tenantID).Scan(&maxCode)
	nextNum := 1
	if maxCode.Valid && len(maxCode.String) > 2 {
		if n, err := strconv.Atoi(maxCode.String[2:]); err == nil {
			nextNum = n + 1
		}
	}
	code := fmt.Sprintf("MO%05d", nextNum)

	// Set defaults
	priority := 5
	if input.Priority != nil {
		priority = *input.Priority
	}
	requiresQC := false
	if input.RequiresQualityCheck != nil {
		requiresQC = *input.RequiresQualityCheck
	}

	var scheduledStart, scheduledEnd *time.Time
	if input.ScheduledStart != nil {
		t, err := time.Parse("2006-01-02", *input.ScheduledStart)
		if err == nil {
			scheduledStart = &t
		}
	}
	if input.ScheduledEnd != nil {
		t, err := time.Parse("2006-01-02", *input.ScheduledEnd)
		if err == nil {
			scheduledEnd = &t
		}
	}

	// Get organization ID from context
	orgID, _ := middleware.GetOrganizationID(c)
	var orgIDPtr *uuid.UUID
	if orgID != uuid.Nil {
		orgIDPtr = &orgID
	}

	// Manufacturing-specific fields
	moldCount := 0
	if input.MoldCount != nil {
		moldCount = *input.MoldCount
	}

	hasSplitOutput := false
	if input.HasSplitOutput != nil {
		hasSplitOutput = *input.HasSplitOutput
	}
	// Auto-inherit from BOM if not explicitly set in the request
	if !hasSplitOutput && input.BOMID != nil {
		var bomSplit bool
		if h.db.QueryRow(`SELECT COALESCE(has_split_output, false) FROM product_boms WHERE id = $1`, input.BOMID).Scan(&bomSplit) == nil && bomSplit {
			hasSplitOutput = true
		}
	}
	h.log.Info("CreateProductionOrder: split output", "input_value", input.HasSplitOutput, "resolved", hasSplitOutput)

	query := `
		INSERT INTO production_orders (
			id, tenant_id, organization_id, code, name, product_id, bom_id, quantity_planned, uom,
			mold_count, shift, current_stage,
			scheduled_start, scheduled_end, priority, status, source_type, source_id,
			sales_order_id, customer_id, warehouse_id, location_id, assigned_to,
			work_center_id, requires_quality_check, notes, tags, created_by, created_at, updated_at,
			manufacturing_category_id, has_split_output
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, 'draft', $12, $13, $14, 'draft', $15, $16, $17, $18, $19, $20, $21, $22, $23, $24, $25, $26, $27, $28, $29, $30)
		RETURNING id
	`

	tags := []byte("[]")
	if len(input.Tags) > 0 {
		tags = []byte(fmt.Sprintf(`["%s"]`, strings.Join(input.Tags, `","`)))
	}

	// Auto-fill warehouse: prefer caller-supplied; else BOM (validated against
	// the MO's organization); else any warehouse belonging to the MO's org.
	// Critical: never accept a cross-org warehouse, otherwise the produced
	// inventory ends up in another organization's stock (real bug observed:
	// EVROPLIT MO ended up writing to a warehouse owned by a different org
	// because the BOM's cached warehouse_id was from before the org-split).
	warehouseID := input.WarehouseID
	if warehouseID != nil && orgIDPtr != nil {
		// Validate caller-supplied warehouse belongs to this org
		var ok bool
		if h.db.QueryRow(
			`SELECT EXISTS(SELECT 1 FROM warehouses WHERE id = $1 AND organization_id = $2 AND deleted_at IS NULL)`,
			*warehouseID, *orgIDPtr,
		).Scan(&ok); !ok {
			h.log.Warn("CreateProductionOrder: caller-supplied warehouse not in org, ignoring",
				"warehouse_id", *warehouseID, "org_id", *orgIDPtr)
			warehouseID = nil
		}
	}
	if warehouseID == nil && input.BOMID != nil && orgIDPtr != nil {
		var bomWhID uuid.UUID
		if err := h.db.QueryRow(
			`SELECT b.warehouse_id
			 FROM product_boms b
			 JOIN warehouses w ON w.id = b.warehouse_id
			 WHERE b.id = $1 AND w.organization_id = $2 AND w.deleted_at IS NULL`,
			input.BOMID, *orgIDPtr,
		).Scan(&bomWhID); err == nil {
			warehouseID = &bomWhID
		} else {
			h.log.Warn("CreateProductionOrder: BOM warehouse not in MO org, skipping",
				"bom_id", input.BOMID, "org_id", *orgIDPtr)
		}
	}
	if warehouseID == nil && orgIDPtr != nil {
		// Last-resort fallback: pick any active warehouse for the org
		var fallbackWhID uuid.UUID
		if err := h.db.QueryRow(
			`SELECT id FROM warehouses
			 WHERE organization_id = $1 AND deleted_at IS NULL AND is_active = true
			 ORDER BY created_at LIMIT 1`,
			*orgIDPtr,
		).Scan(&fallbackWhID); err == nil {
			warehouseID = &fallbackWhID
			h.log.Info("CreateProductionOrder: using org default warehouse",
				"warehouse_id", fallbackWhID, "org_id", *orgIDPtr)
		}
	}

	err := h.db.QueryRow(query,
		id, tenantID, orgIDPtr, code, input.Name, input.ProductID, input.BOMID, input.QuantityPlanned, input.UOM,
		moldCount, input.Shift,
		scheduledStart, scheduledEnd, priority, input.SourceType, input.SourceID,
		input.SalesOrderID, input.CustomerID, warehouseID, input.LocationID, input.AssignedTo,
		input.WorkCenterID, requiresQC, input.Notes, tags, userID, now, now,
		input.ManufacturingCategoryID, hasSplitOutput,
	).Scan(&id)

	if err != nil {
		h.log.Error("Failed to create production order", "error", err,
			"product_id", input.ProductID, "bom_id", input.BOMID, "org_id", orgIDPtr,
			"warehouse_id", warehouseID, "has_split_output", hasSplitOutput)
		if strings.Contains(err.Error(), "unique") || strings.Contains(err.Error(), "duplicate") {
			response.Conflict(c, "Production order code already exists")
		} else {
			response.InternalError(c, "Failed to create production order: "+err.Error())
		}
		return
	}

	// Event + audit trail (integration map §5) — after the row is committed.
	var createdProductName string
	h.db.QueryRow(`SELECT COALESCE(name, '') FROM products WHERE id = $1`, input.ProductID).Scan(&createdProductName)
	h.EmitWorkflowEvent(tenantID, "production_order.created", map[string]interface{}{
		"record_id":        id.String(),
		"code":             code,
		"product_name":     createdProductName,
		"quantity_planned": input.QuantityPlanned,
	})
	h.productionAudit(tenantID, userID, id, "production_order.created", nil,
		map[string]interface{}{"status": "draft", "code": code})

	// Return created order
	c.Params = append(c.Params, gin.Param{Key: "id", Value: id.String()})
	h.GetProductionOrder(c)
}

// UpdateProductionOrder godoc
// @Summary Update production order
// @Description Update an existing production order
// @Tags Manufacturing
// @Accept json
// @Produce json
// @Param id path string true "Production Order ID"
// @Param input body entity.ProductionOrderInput true "Production order input"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /manufacturing/production-orders/{id} [put]
func (h *Handler) UpdateProductionOrder(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid production order ID")
		return
	}

	var input entity.ProductionOrderUpdateInput
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	// Build dynamic update query
	updates := []string{}
	args := []interface{}{}
	argCount := 0

	if input.Name != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("name = $%d", argCount))
		args = append(args, *input.Name)
	}
	if input.BOMID != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("bom_id = $%d", argCount))
		args = append(args, *input.BOMID)
	}
	if input.QuantityPlanned != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("quantity_planned = $%d", argCount))
		args = append(args, *input.QuantityPlanned)
	}
	if input.ScheduledStart != nil {
		argCount++
		t, _ := time.Parse("2006-01-02", *input.ScheduledStart)
		updates = append(updates, fmt.Sprintf("scheduled_start = $%d", argCount))
		args = append(args, t)
	}
	if input.ScheduledEnd != nil {
		argCount++
		t, _ := time.Parse("2006-01-02", *input.ScheduledEnd)
		updates = append(updates, fmt.Sprintf("scheduled_end = $%d", argCount))
		args = append(args, t)
	}
	if input.Priority != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("priority = $%d", argCount))
		args = append(args, *input.Priority)
	}
	if input.AssignedTo != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("assigned_to = $%d", argCount))
		args = append(args, *input.AssignedTo)
	}
	if input.WorkCenterID != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("work_center_id = $%d", argCount))
		args = append(args, *input.WorkCenterID)
	}
	if input.RequiresQualityCheck != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("requires_quality_check = $%d", argCount))
		args = append(args, *input.RequiresQualityCheck)
	}
	if input.Notes != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("notes = $%d", argCount))
		args = append(args, *input.Notes)
	}
	if len(input.Tags) > 0 {
		argCount++
		tags := []byte(fmt.Sprintf(`["%s"]`, strings.Join(input.Tags, `","`)))
		updates = append(updates, fmt.Sprintf("tags = $%d", argCount))
		args = append(args, tags)
	}

	// Manufacturing-specific fields
	if input.MoldCount != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("mold_count = $%d", argCount))
		args = append(args, *input.MoldCount)
	}
	if input.Shift != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("shift = $%d", argCount))
		args = append(args, *input.Shift)
	}
	if input.CurrentStage != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("current_stage = $%d", argCount))
		args = append(args, *input.CurrentStage)
	}
	if input.PackageCount != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("package_count = $%d", argCount))
		args = append(args, *input.PackageCount)
	}
	if input.GoodQuantity != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("good_quantity = $%d", argCount))
		args = append(args, *input.GoodQuantity)
	}
	if input.RejectQuantity != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("reject_quantity = $%d", argCount))
		args = append(args, *input.RejectQuantity)
	}
	if input.ManufacturingCategoryID != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("manufacturing_category_id = $%d", argCount))
		args = append(args, *input.ManufacturingCategoryID)
	}
	if input.HasSplitOutput != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("has_split_output = $%d", argCount))
		args = append(args, *input.HasSplitOutput)
	}
	if input.Status != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("status = $%d", argCount))
		args = append(args, *input.Status)
	}
	if input.ProgressPercent != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("progress_percent = $%d", argCount))
		args = append(args, *input.ProgressPercent)
	}

	if len(updates) == 0 {
		response.BadRequest(c, "No fields to update")
		return
	}

	// Add updated_at
	argCount++
	updates = append(updates, fmt.Sprintf("updated_at = $%d", argCount))
	args = append(args, time.Now())

	// Add WHERE clause params
	argCount++
	args = append(args, id)
	argCount++
	args = append(args, tenantID)

	// Org scoping (audit §2.9)
	var updOrgArg interface{}
	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		updOrgArg = orgID
	}
	argCount++
	args = append(args, updOrgArg)

	// Allow updates for draft, confirmed, in_progress and packaging status (for
	// manufacturing stage tracking + idempotent move-to-packaging before split output)
	query := fmt.Sprintf(
		"UPDATE production_orders SET %s WHERE id = $%d AND tenant_id = $%d AND deleted_at IS NULL AND status IN ('draft', 'confirmed', 'in_progress', 'packaging') AND (organization_id IS NULL OR $%d::uuid IS NULL OR organization_id = $%d)",
		strings.Join(updates, ", "), argCount-2, argCount-1, argCount, argCount,
	)

	result, err := h.db.Exec(query, args...)
	if err != nil {
		h.log.Error("Failed to update production order", "error", err)
		response.InternalError(c, "Failed to update production order")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Production order not found or cannot be updated")
		return
	}

	h.GetProductionOrder(c)
}

// DeleteProductionOrder godoc
// @Summary Delete production order
// @Description Soft delete a production order
// @Tags Manufacturing
// @Accept json
// @Produce json
// @Param id path string true "Production Order ID"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /manufacturing/production-orders/{id} [delete]
func (h *Handler) DeleteProductionOrder(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid production order ID")
		return
	}

	now := time.Now()
	userID, _ := middleware.GetUserID(c)
	var delOrgArg interface{}
	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		delOrgArg = orgID
	}

	// Fetch code + status BEFORE the soft-delete (audit trail + the legacy
	// description matcher below).
	var poCode, poStatus string
	h.db.QueryRow(`SELECT code, status FROM production_orders WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL`, id, tenantID).Scan(&poCode, &poStatus)

	// The JEs this MO posted. v2 matches by source_type/source_id (audit
	// §2.5 — the old description-LIKE soft-deleted ANY module's JE that
	// mentioned the code, MO10000 ⊂ MO100001). C3: entries that predate
	// source tagging entirely (source_type IS NULL) are matched by the code
	// as a WHOLE WORD (regex \m…\M — word boundaries, unlike the original
	// LIKE, so MO10000 no longer matches inside MO100001).
	const mfgJESourceTypes = `('production_start', 'production_complete', 'production_return',
		 'production_cancel', 'split_output_complete', 'split_material_consumption',
		 'production_order_consume')`
	const mfgJEMatchFmt = `((je.source_type IN ` + mfgJESourceTypes + ` AND je.source_id = $%[1]d)
		  OR (je.source_type IS NULL AND $%[2]d <> '' AND je.description ~ ('\m' || $%[2]d || '\M')))`

	// ── C2: the whole delete flow is ONE transaction — order soft-delete,
	// JE soft-delete, inventory reversal and account recompute land or fail
	// together. Any error rolls back and 500s (the old flow was five
	// independent unchecked statements; a mid-flight failure left the order
	// deleted with its stock/GL side effects intact).
	tx, txErr := h.db.Begin()
	if txErr != nil {
		h.log.Error("DeleteProductionOrder: failed to begin transaction", "error", txErr)
		response.InternalError(c, "Failed to delete production order")
		return
	}
	defer tx.Rollback()

	delFail := func(step string, stepErr error) {
		h.log.Error("DeleteProductionOrder: "+step, "error", stepErr, "po_id", id)
		response.InternalError(c, "Failed to delete production order")
	}

	// 1. Claim: soft-delete the order.
	result, err := tx.Exec(`
		UPDATE production_orders SET deleted_at = $1
		WHERE id = $2 AND tenant_id = $3 AND deleted_at IS NULL
		  AND status IN ('draft', 'cancelled', 'completed')
		  AND (organization_id IS NULL OR $4::uuid IS NULL OR organization_id = $4)
	`, now, id, tenantID, delOrgArg)
	if err != nil {
		delFail("failed to soft-delete order", err)
		return
	}
	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Production order not found or cannot be deleted")
		return
	}

	// 2. Cascade soft-delete all work orders so they disappear from Shop
	//    Floor Control.
	if _, err := tx.Exec(`UPDATE work_orders SET deleted_at = $1 WHERE production_order_id = $2 AND tenant_id = $3 AND deleted_at IS NULL`, now, id, tenantID); err != nil {
		delFail("failed to soft-delete work orders", err)
		return
	}

	// 3. Collect the accounts the doomed entries' lines touched, BEFORE
	//    soft-deleting them (recompute set for step 7).
	touchedAccounts := []string{}
	acctRows, err := tx.Query(`
		SELECT DISTINCT jel.account_id::text
		FROM journal_entries je
		JOIN journal_entry_lines jel ON jel.journal_entry_id = je.id
		WHERE je.tenant_id = $1 AND je.deleted_at IS NULL
		  AND `+fmt.Sprintf(mfgJEMatchFmt, 2, 3)+`
	`, tenantID, id, poCode)
	if err != nil {
		delFail("failed to collect touched accounts", err)
		return
	}
	for acctRows.Next() {
		var acctID string
		if acctRows.Scan(&acctID) == nil {
			touchedAccounts = append(touchedAccounts, acctID)
		}
	}
	acctRows.Close()

	// 4. Soft-delete this MO's journal entries (audit trail survives via
	//    deleted_at).
	if _, err := tx.Exec(`
		UPDATE journal_entries AS je
		SET deleted_at = $1, updated_at = $1
		WHERE je.tenant_id = $2 AND je.deleted_at IS NULL
		  AND `+fmt.Sprintf(mfgJEMatchFmt, 3, 4)+`
	`, now, tenantID, id, poCode); err != nil {
		delFail("failed to soft-delete journal entries", err)
		return
	}

	// 5. Reverse stock-out rows tied to this PO (legacy 'issue', v2
	//    'production_out', FG-transfer 'transfer_out'): add the quantity
	//    back to the inventory row, then soft-delete the ledger row so a
	//    retry can't double-undo. ABS() because legacy writers were split
	//    between signed and unsigned quantities.
	if _, err := tx.Exec(`
		WITH issues AS (
			SELECT id, inventory_id, ABS(quantity) AS qty
			FROM inventory_transactions
			WHERE tenant_id = $1
			  AND reference_type = 'production_order'
			  AND reference_id = $2
			  AND transaction_type IN ('issue', 'production_out', 'transfer_out')
			  AND deleted_at IS NULL
		), restore AS (
			UPDATE inventory inv
			SET quantity_on_hand = inv.quantity_on_hand + i.qty,
			    last_movement_date = $3,
			    updated_at = $3
			FROM issues i
			WHERE inv.id = i.inventory_id
			RETURNING inv.id
		)
		UPDATE inventory_transactions
		SET deleted_at = $3
		WHERE id IN (SELECT id FROM issues);
	`, tenantID, id, now); err != nil {
		delFail("failed to reverse stock issues", err)
		return
	}

	// 6. Reverse stock-in rows: FG receipts (legacy 'receipt', v2
	//    'production_in'), material returns ('return'), scrap receipts
	//    ('production_scrap') and FG-transfer arrivals ('transfer_in') —
	//    subtract what came in back out, then soft-delete the rows.
	if _, err := tx.Exec(`
		WITH receipts AS (
			SELECT id, inventory_id, ABS(quantity) AS qty
			FROM inventory_transactions
			WHERE tenant_id = $1
			  AND reference_type = 'production_order'
			  AND reference_id = $2
			  AND transaction_type IN ('receipt', 'production_in', 'return', 'production_scrap', 'transfer_in')
			  AND deleted_at IS NULL
		), restore AS (
			UPDATE inventory inv
			SET quantity_on_hand = GREATEST(inv.quantity_on_hand - r.qty, 0),
			    last_movement_date = $3,
			    updated_at = $3
			FROM receipts r
			WHERE inv.id = r.inventory_id
			RETURNING inv.id
		)
		UPDATE inventory_transactions
		SET deleted_at = $3
		WHERE id IN (SELECT id FROM receipts);
	`, tenantID, id, now); err != nil {
		delFail("failed to reverse stock receipts", err)
		return
	}

	// 7. Retire the finished-goods lot(s) created for this PO.
	if _, err := tx.Exec(`
		UPDATE inventory_lots
		SET status = 'consumed', remaining_quantity = 0, updated_at = $1
		WHERE tenant_id = $2 AND lot_number = 'MFG-' || LEFT($3::text, 8)
	`, now, tenantID, id); err != nil {
		delFail("failed to retire lots", err)
		return
	}

	// 8. Recompute ONLY the accounts that appeared on the deleted entries'
	//    lines. FILTER keeps the sum to posted, surviving entries; accounts
	//    whose every line is now soft-deleted correctly reset to 0.
	if len(touchedAccounts) > 0 {
		if _, err := tx.Exec(`
			UPDATE accounts a
			SET current_balance = CASE
				WHEN at.normal_balance = 'debit' THEN s.dt - s.kt
				ELSE                                  s.kt - s.dt
			END,
			    updated_at = NOW()
			FROM account_types at,
			     (SELECT acc.id AS account_id,
			             COALESCE(SUM(jel.debit_amount)  FILTER (WHERE je.status = 'posted' AND je.deleted_at IS NULL), 0) AS dt,
			             COALESCE(SUM(jel.credit_amount) FILTER (WHERE je.status = 'posted' AND je.deleted_at IS NULL), 0) AS kt
			      FROM accounts acc
			      LEFT JOIN journal_entry_lines jel ON jel.account_id = acc.id
			      LEFT JOIN journal_entries je ON je.id = jel.journal_entry_id
			      WHERE acc.tenant_id = $1 AND acc.id = ANY($2::uuid[])
			      GROUP BY acc.id) s
			WHERE a.account_type_id = at.id
			  AND s.account_id = a.id
			  AND a.tenant_id = $1;
		`, tenantID, pq.Array(touchedAccounts)); err != nil {
			delFail("targeted balance recompute failed", err)
			return
		}
	}

	if commitErr := tx.Commit(); commitErr != nil {
		h.log.Error("DeleteProductionOrder: commit failed", "error", commitErr, "po_id", id)
		response.InternalError(c, "Failed to delete production order")
		return
	}

	h.log.Info("DeleteProductionOrder: cascade cleanup complete",
		"po_id", id, "po_code", poCode, "accounts_recomputed", len(touchedAccounts))

	h.productionAudit(tenantID, userID, id, "production_order.deleted",
		map[string]interface{}{"status": poStatus, "code": poCode}, nil)

	response.Success(c, map[string]interface{}{"message": "Production order deleted successfully"})
}

// ConfirmProductionOrder godoc
// @Summary Confirm production order
// @Description Confirm a draft production order, calculate costs and generate work orders
// @Tags Manufacturing
// @Accept json
// @Produce json
// @Param id path string true "Production Order ID"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /manufacturing/production-orders/{id}/confirm [post]
func (h *Handler) ConfirmProductionOrder(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	userID, userIDExists := middleware.GetUserID(c)
	var createdByID interface{}
	if userIDExists && userID != uuid.Nil {
		createdByID = userID
	} else {
		createdByID = nil
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid production order ID")
		return
	}

	// Get active organization from request header (user's currently selected company)
	activeOrgID, _ := middleware.GetOrganizationID(c)

	// Start transaction
	tx, err := h.db.Begin()
	if err != nil {
		h.log.Error("Failed to start transaction", "error", err)
		response.InternalError(c, "Failed to confirm production order")
		return
	}
	defer tx.Rollback()

	// Get the production order details (including BOM ID and quantity)
	var bomID *uuid.UUID
	var quantityPlanned float64
	var workCenterID *uuid.UUID
	var uom string
	var productName string
	var orgID *uuid.UUID

	poQuery := `
		SELECT po.bom_id, po.quantity_planned, po.work_center_id, po.uom, po.organization_id, p.name as product_name
		FROM production_orders po
		LEFT JOIN products p ON p.id = po.product_id
		WHERE po.id = $1 AND po.tenant_id = $2 AND po.deleted_at IS NULL AND po.status = 'draft'
	`
	err = tx.QueryRow(poQuery, id, tenantID).Scan(&bomID, &quantityPlanned, &workCenterID, &uom, &orgID, &productName)
	if err != nil {
		if err == sql.ErrNoRows {
			response.NotFound(c, "Production order not found or not in draft status")
			return
		}
		h.log.Error("Failed to get production order", "error", err)
		response.InternalError(c, "Failed to confirm production order")
		return
	}

	// Prefer user's active organization for account lookups (the org they're currently viewing)
	if activeOrgID != uuid.Nil {
		orgID = &activeOrgID
	}

	var totalPlannedCost float64 = 0
	var totalLaborCost float64 = 0
	var totalOverheadCost float64 = 0

	// If BOM exists, calculate costs and generate work orders from BOM operations
	if bomID != nil {
		// Step 1: Read all BOM operations into a slice first
		// (lib/pq doesn't support executing queries while iterating rows on the same transaction)
		type bomOp struct {
			ID             uuid.UUID
			Sequence       int
			OperationName  string
			WorkCenterID   *uuid.UUID
			SetupTime      float64
			RunTime        float64
			LaborCost      float64
			OverheadCost   float64
			Notes          *string
			WCHourlyCost   float64
			WCSetupCost    float64
			WCOverheadCost float64
		}

		opsQuery := `
			SELECT
				bo.id, bo.sequence, bo.operation_name, bo.work_center_id,
				bo.setup_time_minutes, bo.run_time_minutes,
				bo.labor_cost, bo.overhead_cost, bo.notes,
				COALESCE(wc.hourly_cost, 0) as wc_hourly_cost,
				COALESCE(wc.setup_cost, 0) as wc_setup_cost,
				COALESCE(wc.overhead_cost, 0) as wc_overhead_cost
			FROM bom_operations bo
			LEFT JOIN work_centers wc ON wc.id = bo.work_center_id AND wc.deleted_at IS NULL
			WHERE bo.bom_id = $1
			ORDER BY bo.sequence ASC
		`
		rows, err := tx.Query(opsQuery, bomID)
		if err != nil {
			h.log.Error("Failed to get BOM operations", "error", err)
			response.InternalError(c, "Failed to confirm production order")
			return
		}

		var operations []bomOp
		for rows.Next() {
			var op bomOp
			err := rows.Scan(&op.ID, &op.Sequence, &op.OperationName, &op.WorkCenterID,
				&op.SetupTime, &op.RunTime,
				&op.LaborCost, &op.OverheadCost, &op.Notes,
				&op.WCHourlyCost, &op.WCSetupCost, &op.WCOverheadCost)
			if err != nil {
				h.log.Error("Failed to scan BOM operation", "error", err)
				continue
			}
			operations = append(operations, op)
		}
		rows.Close()

		if err := rows.Err(); err != nil {
			h.log.Error("Error iterating BOM operations", "error", err)
			response.InternalError(c, "Failed to confirm production order")
			return
		}

		// Step 2: Now create work orders from the collected operations.
		// We use a 1-based loop index in the work-order code instead of
		// op.Sequence — operators sometimes set the same sequence number
		// on parallel operations (e.g. two ops both at seq=30), which
		// used to collide on the (tenant_id, organization_id, code)
		// unique index inside this transaction and fail the whole confirm.
		now := time.Now()
		for woIdx, op := range operations {
			totalTimeMinutes := op.SetupTime + (op.RunTime * quantityPlanned)
			totalTimeHours := totalTimeMinutes / 60.0

			var opLaborCost float64
			var opOverheadCost float64

			if op.LaborCost > 0 {
				opLaborCost = op.LaborCost * quantityPlanned
			} else if op.WCHourlyCost > 0 {
				opLaborCost = totalTimeHours * op.WCHourlyCost
			}

			if op.OverheadCost > 0 {
				opOverheadCost = op.OverheadCost * quantityPlanned
			} else if op.WCOverheadCost > 0 {
				opOverheadCost = totalTimeHours * op.WCOverheadCost
			}

			machineCost := op.WCSetupCost + (totalTimeHours * op.WCHourlyCost)

			totalLaborCost += opLaborCost
			totalOverheadCost += opOverheadCost
			totalPlannedCost += opLaborCost + opOverheadCost + machineCost

			effectiveWorkCenterID := op.WorkCenterID
			if effectiveWorkCenterID == nil {
				effectiveWorkCenterID = workCenterID
			}

			woID := uuid.New()
			// 12-char UUID prefix (instead of 8) drops cross-MO collision risk
			// to effectively zero; woIdx+1 guarantees uniqueness within this MO
			// even when two BOM operations share a sequence number.
			woCode := fmt.Sprintf("WO-%s-%d", id.String()[:12], woIdx+1)
			woName := fmt.Sprintf("%s - %s", productName, op.OperationName)

			woQuery := `
				INSERT INTO work_orders (
					id, tenant_id, organization_id, production_order_id, code, name,
					sequence, operation_id, work_center_id,
					quantity_to_produce, uom,
					planned_duration_hours, setup_time_hours,
					planned_cost, labor_cost, machine_cost,
					status, instructions, notes,
					created_by, created_at, updated_at
				) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, 'pending', $17, $18, $19, $20, $21)
			`

			var instructions *string
			if op.Notes != nil && *op.Notes != "" {
				instructions = op.Notes
			}

			_, err = tx.Exec(woQuery,
				woID, tenantID, orgID, id, woCode, woName,
				op.Sequence, op.ID, effectiveWorkCenterID,
				quantityPlanned, uom,
				totalTimeHours, op.SetupTime/60.0,
				opLaborCost+machineCost, opLaborCost, machineCost,
				instructions, op.Notes,
				createdByID, now, now,
			)
			if err != nil {
				h.log.Error("Failed to create work order", "error", err, "operation", op.OperationName)
				response.InternalError(c, fmt.Sprintf("Failed to create work orders: %v", err))
				return
			}
		}
	}

	// If no work orders were created (BOM had no operations or no BOM), create a default one
	var confirmWOCount int
	tx.QueryRow(`SELECT COUNT(*) FROM work_orders WHERE production_order_id = $1 AND tenant_id = $2 AND deleted_at IS NULL`, id, tenantID).Scan(&confirmWOCount)
	if confirmWOCount == 0 {
		woID := uuid.New()
		woCode := fmt.Sprintf("WO-%s-1", id.String()[:12])
		woName := productName + " - Ishlab chiqarish"
		_, err = tx.Exec(`
			INSERT INTO work_orders (
				id, tenant_id, organization_id, production_order_id, code, name, sequence,
				quantity_to_produce, uom, status, created_by, created_at, updated_at
			) VALUES ($1, $2, $3, $4, $5, $6, 1, $7, $8, 'pending', $9, NOW(), NOW())
		`, woID, tenantID, orgID, id, woCode, woName, quantityPlanned, uom, createdByID)
		if err != nil {
			h.log.Error("Failed to create default work order", "error", err)
		}
	}

	// Update production order with calculated costs and confirm.
	// Org-scoped (audit §2.9) via the active organization header.
	var confirmOrgArg interface{}
	if activeOrgID != uuid.Nil {
		confirmOrgArg = activeOrgID
	}
	now := time.Now()
	updateQuery := `
		UPDATE production_orders
		SET status = 'confirmed',
			confirmed_by = $1,
			confirmed_at = $2,
			planned_cost = $3,
			labor_cost = $4,
			overhead_cost = $5,
			current_stage = 'draft',
			updated_at = $2
		WHERE id = $6 AND tenant_id = $7 AND deleted_at IS NULL AND status = 'draft'
		  AND (organization_id IS NULL OR $8::uuid IS NULL OR organization_id = $8)
	`

	result, err := tx.Exec(updateQuery, createdByID, now, totalPlannedCost, totalLaborCost, totalOverheadCost, id, tenantID, confirmOrgArg)
	if err != nil {
		h.log.Error("Failed to confirm production order", "error", err)
		response.InternalError(c, "Failed to confirm production order")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Production order not found or not in draft status")
		return
	}

	// Commit transaction
	if err := tx.Commit(); err != nil {
		h.log.Error("Failed to commit transaction", "error", err)
		response.InternalError(c, "Failed to confirm production order")
		return
	}

	// --- Create journal entry for planned cost: Dt 1320 WIP / Kt 1310 Raw Materials ---
	{
		var totalMaterialCost float64

		// 1. Try BOM-based material cost
		if bomID != nil {
			h.db.QueryRow(`
				SELECT COALESCE(SUM(bl.quantity * COALESCE(p.cost_price, 0) * $1 / GREATEST(COALESCE(pb.quantity, 1), 1) * (1 + COALESCE(bl.scrap_percent, 0) / 100.0)), 0)
				FROM bom_lines bl
				JOIN products p ON p.id = bl.component_id
				JOIN product_boms pb ON pb.id = bl.bom_id
				WHERE bl.bom_id = $2
			`, quantityPlanned, bomID).Scan(&totalMaterialCost)
		}

		// 2. Fallback: use product cost_price * quantity if BOM cost is 0
		if totalMaterialCost <= 0 {
			var productCost float64
			h.db.QueryRow(`
				SELECT COALESCE(cost_price, 0) FROM products WHERE id = (
					SELECT product_id FROM production_orders WHERE id = $1
				)
			`, id).Scan(&productCost)
			totalMaterialCost = productCost * quantityPlanned
		}

		// 3. Fallback: use product list_price * quantity (preferred over BOM list_price to avoid inflated costs from self-referencing BOMs)
		if totalMaterialCost <= 0 {
			var productListPrice float64
			h.db.QueryRow(`
				SELECT COALESCE(list_price, 0) FROM products WHERE id = (
					SELECT product_id FROM production_orders WHERE id = $1
				)
			`, id).Scan(&productListPrice)
			totalMaterialCost = productListPrice * quantityPlanned
		}

		// 4. Fallback: use planned_cost from confirmation if still 0
		if totalMaterialCost <= 0 && totalPlannedCost > 0 {
			totalMaterialCost = totalPlannedCost
		}

		h.log.Info("Confirm journal entry calculation",
			"order_id", id,
			"totalMaterialCost", totalMaterialCost,
			"totalPlannedCost", totalPlannedCost,
			"quantityPlanned", quantityPlanned)

		// PLANNED-COST JOURNAL: intentionally NOT posted at confirm time.
		//
		// Previously this block created a "confirmed - planned material cost"
		// journal entry (Dt 2010 WIP / Kt 1030 Raw Materials) using the BOM
		// estimate. The problem: the same Dt 2010 / Kt 1030 pair is posted
		// again at CompleteProductionOrder (line 4 of the WIP cost flow),
		// so every MO ended up double-charging 1030 and double-debiting 2010.
		//
		// Real-world impact (May 2026): 1030 Xom ashyo accumulated a
		// -21,258,612,507 so'm balance because every MO contributed two
		// credit lines instead of one. Cleanup SQL was run separately on
		// the existing data; this guard prevents the bug from recurring.
		//
		// Actual material consumption is journalled exactly once, by
		// StartProductionOrder (when materials are actually issued from
		// stock) or by CompleteProductionOrder (full WIP→Finished flow).
		// We still update production_orders.material_cost here because
		// other parts of the UI read that planned figure.
		if totalMaterialCost > 0 {
			h.db.Exec(`UPDATE production_orders SET material_cost = $1 WHERE id = $2`, totalMaterialCost, id)
			h.log.Info("Confirm: skipped planned-cost journal (would double-count on complete)", "order_id", id, "planned_material_cost", totalMaterialCost)
		} else {
			h.log.Warn("Total material cost is 0 for confirmed production order", "order_id", id)
		}
	}

	// Event + audit trail (integration map §5) — after commit.
	var confirmedCode string
	h.db.QueryRow(`SELECT code FROM production_orders WHERE id = $1`, id).Scan(&confirmedCode)
	h.EmitWorkflowEvent(tenantID, "production_order.confirmed", map[string]interface{}{
		"record_id":        id.String(),
		"code":             confirmedCode,
		"product_name":     productName,
		"quantity_planned": quantityPlanned,
		"planned_cost":     totalPlannedCost,
	})
	h.productionAudit(tenantID, userID, id, "production_order.confirmed",
		map[string]interface{}{"status": "draft"},
		map[string]interface{}{"status": "confirmed", "planned_cost": totalPlannedCost})

	h.GetProductionOrder(c)
}

// StartProductionOrder godoc
// @Summary Start production order
// @Description Start a confirmed production order
// @Tags Manufacturing
// @Accept json
// @Produce json
// @Param id path string true "Production Order ID"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /manufacturing/production-orders/{id}/start [post]
func (h *Handler) StartProductionOrder(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	userID, _ := middleware.GetUserID(c)

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid production order ID")
		return
	}

	now := time.Now()

	// Active organization from the request header — used for org scoping of
	// the status claim (audit §2.9) and for account lookups.
	startActiveOrgID, _ := middleware.GetOrganizationID(c)
	var orgArg interface{}
	if startActiveOrgID != uuid.Nil {
		orgArg = startActiveOrgID
	}

	// ── Pre-reads (no writes yet) ───────────────────────────────────────
	var productID uuid.UUID
	var bomID *uuid.UUID
	var warehouseID *uuid.UUID
	var organizationID *uuid.UUID
	var qtyPlanned float64
	var poCode, poStatus string
	err = h.db.QueryRow(`
		SELECT product_id, bom_id, warehouse_id, organization_id, quantity_planned, code, status
		FROM production_orders
		WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
		  AND (organization_id IS NULL OR $3::uuid IS NULL OR organization_id = $3)
	`, id, tenantID, orgArg).Scan(&productID, &bomID, &warehouseID, &organizationID, &qtyPlanned, &poCode, &poStatus)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Production order not found")
		return
	}
	if err != nil {
		h.log.Error("Failed to fetch production order for start", "error", err)
		response.InternalError(c, "Failed to start production order")
		return
	}

	// Prefer the user's active organization for account lookups
	if startActiveOrgID != uuid.Nil {
		organizationID = &startActiveOrgID
	}

	// First BOM operation stage name for current_stage
	firstStage := "in_progress"
	if bomID != nil {
		var firstSeq int
		if h.db.QueryRow(`
			SELECT sequence FROM bom_operations WHERE bom_id = $1 ORDER BY sequence ASC LIMIT 1
		`, bomID).Scan(&firstSeq) == nil {
			firstStage = fmt.Sprintf("op_%d", firstSeq)
		}
	}

	// Auto-assign warehouse if none set: BOM warehouse validated against the
	// org first, then tenant default. Reads only — the repair write is
	// deferred into the start tx AFTER the status claim (C1: an aborted
	// start must not rewrite the order).
	startWarehouseAutoAssigned := false
	if warehouseID == nil && bomID != nil && organizationID != nil {
		var bomWhID uuid.UUID
		if h.db.QueryRow(
			`SELECT b.warehouse_id FROM product_boms b
			 JOIN warehouses w ON w.id = b.warehouse_id
			 WHERE b.id = $1 AND w.organization_id = $2 AND w.deleted_at IS NULL`,
			bomID, *organizationID,
		).Scan(&bomWhID) == nil {
			warehouseID = &bomWhID
			startWarehouseAutoAssigned = true
		}
	}
	if warehouseID == nil {
		var firstWH uuid.UUID
		if h.db.QueryRow(`SELECT id FROM warehouses WHERE tenant_id = $1 AND deleted_at IS NULL ORDER BY created_at ASC LIMIT 1`, tenantID).Scan(&firstWH) == nil {
			warehouseID = &firstWH
			startWarehouseAutoAssigned = true
		}
	}

	// Materials-already-issued guard (prevents double-deduction on
	// pause/resume). Covers the legacy 'issue' rows and the v2
	// 'production_out' rows written via applyStockDelta.
	var existingConsumption int
	h.db.QueryRow(`
		SELECT COUNT(*) FROM inventory_transactions
		WHERE tenant_id = $1 AND reference_type = 'production_order' AND reference_id = $2
		  AND transaction_type IN ('issue', 'production_out') AND deleted_at IS NULL
	`, tenantID, id).Scan(&existingConsumption)

	// BOM components to issue
	type bomComponent struct {
		ComponentID    uuid.UUID
		Quantity       float64
		ScrapPercent   float64
		BOMOutputQty   float64
		TrackInventory bool
	}
	var components []bomComponent
	if bomID != nil && warehouseID != nil && existingConsumption == 0 {
		compRows, compErr := h.db.Query(`
			SELECT bl.component_id, bl.quantity, COALESCE(bl.scrap_percent, 0),
			       GREATEST(COALESCE(pb.quantity, 1), 0.0001),
			       COALESCE(p.track_inventory, true)
			FROM bom_lines bl
			JOIN product_boms pb ON pb.id = bl.bom_id
			LEFT JOIN products p ON p.id = bl.component_id
			WHERE bl.bom_id = $1
		`, bomID)
		if compErr != nil {
			h.log.Error("Failed to fetch BOM components", "error", compErr, "po_id", id)
			response.InternalError(c, "Failed to start production order")
			return
		}
		for compRows.Next() {
			var comp bomComponent
			if scanErr := compRows.Scan(&comp.ComponentID, &comp.Quantity, &comp.ScrapPercent, &comp.BOMOutputQty, &comp.TrackInventory); scanErr == nil {
				components = append(components, comp)
			}
		}
		compRows.Close()
	}

	// Start-JE dedupe guard (source_type match; legacy entries carry no
	// source_type, so the description LIKE for the old confirm-step JE stays).
	var existingStartJE int
	h.db.QueryRow(`
		SELECT COUNT(*) FROM journal_entries
		WHERE tenant_id = $1 AND status = 'posted' AND deleted_at IS NULL
		AND ((source_type = 'production_start' AND source_id = $2)
			OR (description LIKE '%confirmed - planned material cost%' AND description LIKE '%' || $3 || '%'))
	`, tenantID, id, poCode).Scan(&existingStartJE)

	// ── ONE transaction: status claim + all stock legs + the JE ─────────
	// Integration map §3/§4: stock and GL land or fail together; the
	// deferred migration-326 trigger validates the JE at COMMIT.
	tx, txErr := h.db.Begin()
	if txErr != nil {
		h.log.Error("Failed to begin start transaction", "error", txErr)
		response.InternalError(c, "Failed to start production order")
		return
	}
	defer tx.Rollback()

	claimRes, claimErr := tx.Exec(`
		UPDATE production_orders
		SET status = 'in_progress', actual_start = $1, current_stage = $2, updated_at = $1
		WHERE id = $3 AND tenant_id = $4 AND deleted_at IS NULL AND status IN ('confirmed', 'ready', 'paused')
		  AND (organization_id IS NULL OR $5::uuid IS NULL OR organization_id = $5)
	`, now, firstStage, id, tenantID, orgArg)
	if claimErr != nil {
		h.log.Error("Failed to start production order", "error", claimErr)
		response.InternalError(c, "Failed to start production order")
		return
	}
	if n, _ := claimRes.RowsAffected(); n == 0 {
		response.NotFound(c, "Production order not found or not in valid status")
		return
	}

	// C1: persist the auto-assigned warehouse only after the claim succeeded,
	// so an aborted start never rewrites the order.
	if startWarehouseAutoAssigned && warehouseID != nil {
		if _, wErr := tx.Exec(`UPDATE production_orders SET warehouse_id = $1 WHERE id = $2 AND tenant_id = $3 AND warehouse_id IS NULL`,
			*warehouseID, id, tenantID); wErr != nil {
			h.log.Error("StartProductionOrder: failed to persist auto-assigned warehouse", "error", wErr, "po_id", id)
			response.InternalError(c, "Failed to start production order")
			return
		}
	}

	// Material issue: every leg via applyStockDelta (production_out, signed
	// negative). Greedy multi-warehouse sourcing stays: pull from any
	// warehouse in the org with stock, biggest first; only the unmet
	// remainder is booked against the PO's warehouse (deliberately negative,
	// AllowNeg) so the shortfall is visible and reconcilable there.
	var totalMaterialCost float64
	for _, comp := range components {
		consumption := comp.Quantity * (qtyPlanned / comp.BOMOutputQty) * (1 + comp.ScrapPercent/100)
		if consumption <= 0 {
			continue
		}
		// Non-tracked components (water, gas, …) are infinite supply — no
		// stock rows touched; cost still flows via BOM calculations.
		if !comp.TrackInventory {
			continue
		}

		var compCost float64
		tx.QueryRow("SELECT COALESCE(cost_price, 0) FROM products WHERE id = $1", comp.ComponentID).Scan(&compCost)

		type src struct {
			whID     uuid.UUID
			unitCost float64
			onHand   float64
		}
		var sources []src
		srcQuery := `
			SELECT i.warehouse_id, COALESCE(i.unit_cost, 0), COALESCE(i.quantity_on_hand, 0)
			FROM inventory i
			JOIN warehouses w ON w.id = i.warehouse_id
			WHERE i.tenant_id = $1 AND i.product_id = $2
			  AND i.quantity_on_hand > 0
			  AND w.deleted_at IS NULL`
		srcArgs := []interface{}{tenantID, comp.ComponentID}
		if organizationID != nil {
			srcQuery += ` AND w.organization_id = $3`
			srcArgs = append(srcArgs, *organizationID)
		}
		srcQuery += ` ORDER BY i.quantity_on_hand DESC`
		if sRows, sErr := tx.Query(srcQuery, srcArgs...); sErr == nil {
			for sRows.Next() {
				var s src
				if scanErr := sRows.Scan(&s.whID, &s.unitCost, &s.onHand); scanErr == nil {
					sources = append(sources, s)
				}
			}
			sRows.Close()
		}

		remaining := consumption
		for _, s := range sources {
			if remaining <= 0 {
				break
			}
			take := s.onHand
			if take > remaining {
				take = remaining
			}
			unitCost := s.unitCost
			if unitCost == 0 {
				unitCost = compCost
			}
			_, valuedCost, dErr := h.applyStockDelta(tx, stockDeltaArgs{
				TenantID:    tenantID,
				OrgID:       organizationID,
				ProductID:   comp.ComponentID,
				WarehouseID: s.whID,
				Qty:         -take,
				UnitCost:    unitCost,
				TxType:      "production_out",
				RefType:     "production_order",
				RefID:       id.String(),
				Reason:      "material_consumption",
				Notes:       "Materials consumed at production start",
				CreatedBy:   userID,
				When:        now,
				AllowNeg:    false,
			})
			if dErr != nil {
				h.log.Error("StartProductionOrder: stock issue failed", "error", dErr, "component_id", comp.ComponentID)
				if dErr == errInsufficientStock {
					response.Error(c, http.StatusUnprocessableEntity, "INSUFFICIENT_STOCK",
						"Omborda material yetarli emas — ishlab chiqarishni boshlab bo'lmadi")
				} else {
					response.InternalError(c, "Failed to issue materials")
				}
				return
			}
			totalMaterialCost += take * valuedCost
			remaining -= take
		}

		// Shortfall: book the remainder against the PO's warehouse
		// (deliberately negative) so accounting matches reality.
		if remaining > 0.0001 && warehouseID != nil {
			_, valuedCost, dErr := h.applyStockDelta(tx, stockDeltaArgs{
				TenantID:    tenantID,
				OrgID:       organizationID,
				ProductID:   comp.ComponentID,
				WarehouseID: *warehouseID,
				Qty:         -remaining,
				UnitCost:    compCost,
				TxType:      "production_out",
				RefType:     "production_order",
				RefID:       id.String(),
				Reason:      "Materials shortfall — booked to PO warehouse",
				Notes:       "Materials shortfall — booked to PO warehouse",
				CreatedBy:   userID,
				When:        now,
				AllowNeg:    true,
			})
			if dErr != nil {
				h.log.Error("StartProductionOrder: shortfall booking failed", "error", dErr, "component_id", comp.ComponentID)
				response.InternalError(c, "Failed to issue materials")
				return
			}
			totalMaterialCost += remaining * valuedCost
		}
	}

	// JE: Dt 2010 WIP / Kt 1010 Raw Materials — INSIDE the same tx, so a GL
	// failure rolls back the stock (and vice versa).
	if totalMaterialCost > 0 && existingStartJE == 0 {
		wipAcct := findAccount(tx, tenantID, organizationID, "work in progress", "2010")
		// Raw-materials account is NAS code 1010 (Xom ashyo va materiallar);
		// 1030 is Yoqilg'i (Fuel) and was the wrong default — see migration 411.
		rawAcct := findAccount(tx, tenantID, organizationID, "xom ashyo", "1010")
		if rawAcct == uuid.Nil {
			rawAcct = findAccount(tx, tenantID, organizationID, "raw materials", "1010")
		}
		if rawAcct == uuid.Nil {
			rawAcct = findAccount(tx, tenantID, organizationID, "goods for resale", "2910")
		}

		var journalID uuid.UUID
		var nextNumber int
		tx.QueryRow(`
			SELECT id, next_number FROM journals
			WHERE tenant_id = $1 AND type = 'general' AND is_active = true
			ORDER BY created_at ASC LIMIT 1
		`, tenantID).Scan(&journalID, &nextNumber)

		if wipAcct == uuid.Nil || rawAcct == uuid.Nil || journalID == uuid.Nil {
			// Contract (integration map §4): never post a wrong entry and
			// never move stock without its GL leg — abort the whole start.
			response.Error(c, http.StatusUnprocessableEntity, "MISSING_ACCOUNTS",
				"Buxgalteriya sozlamalari to'liq emas: 2010 (Tugallanmagan ishlab chiqarish) / 1010 (Xom ashyo) schyoti yoki faol jurnal topilmadi. Ishlab chiqarish boshlanmadi.")
			return
		}

		var prodNameStarted string
		tx.QueryRow(`SELECT COALESCE(p.name, '') FROM production_orders po JOIN products p ON po.product_id = p.id WHERE po.id = $1`, id).Scan(&prodNameStarted)

		entryID := uuid.New()
		entryNumber := fmt.Sprintf("MFG%06d", nextEntryNumberSeq(tx, tenantID, organizationID, "MFG", nextNumber))
		description := fmt.Sprintf("Production Order %s started - materials consumed", poCode)
		if prodNameStarted != "" {
			description = fmt.Sprintf("Production Order %s started - %s - materials consumed", poCode, prodNameStarted)
		}

		if _, jeErr := tx.Exec(`
			INSERT INTO journal_entries (
				id, tenant_id, organization_id, journal_id, entry_number,
				entry_date, description, source_type, source_id, status, total_debit, total_credit,
				created_by, created_at, updated_at
			) VALUES ($1, $2, $3, $4, $5, $6::date, $7, 'production_start', $8, 'posted', $9, $9, $10, $11, $11)
		`, entryID, tenantID, organizationID, journalID, entryNumber,
			now, description, id.String(), totalMaterialCost, nullableUUID(userID), now); jeErr != nil {
			h.log.Error("StartProductionOrder: failed to insert start JE header", "error", jeErr, "po_id", id)
			response.InternalError(c, "Failed to post journal entry")
			return
		}
		if _, jeErr := tx.Exec(`
			INSERT INTO journal_entry_lines (id, journal_entry_id, account_id, description, debit_amount, credit_amount, line_number, created_at)
			VALUES ($1, $2, $3, $4, $5, 0, 1, $6)
		`, uuid.New(), entryID, wipAcct, "WIP: raw materials consumed", totalMaterialCost, now); jeErr != nil {
			h.log.Error("StartProductionOrder: failed to insert WIP debit line", "error", jeErr, "po_id", id)
			response.InternalError(c, "Failed to post journal entry")
			return
		}
		if _, jeErr := tx.Exec(`
			INSERT INTO journal_entry_lines (id, journal_entry_id, account_id, description, debit_amount, credit_amount, line_number, created_at)
			VALUES ($1, $2, $3, $4, 0, $5, 2, $6)
		`, uuid.New(), entryID, rawAcct, "Raw materials issued to production", totalMaterialCost, now); jeErr != nil {
			h.log.Error("StartProductionOrder: failed to insert raw materials credit line", "error", jeErr, "po_id", id)
			response.InternalError(c, "Failed to post journal entry")
			return
		}
		if _, jeErr := tx.Exec(`UPDATE accounts SET current_balance = current_balance + $1, updated_at = $2 WHERE id = $3`, totalMaterialCost, now, wipAcct); jeErr != nil {
			h.log.Error("StartProductionOrder: failed to bump WIP balance", "error", jeErr, "po_id", id)
			response.InternalError(c, "Failed to post journal entry")
			return
		}
		if _, jeErr := tx.Exec(`UPDATE accounts SET current_balance = current_balance - $1, updated_at = $2 WHERE id = $3`, totalMaterialCost, now, rawAcct); jeErr != nil {
			h.log.Error("StartProductionOrder: failed to bump raw materials balance", "error", jeErr, "po_id", id)
			response.InternalError(c, "Failed to post journal entry")
			return
		}
		if _, jeErr := tx.Exec(`UPDATE journals SET next_number = next_number + 1 WHERE id = $1`, journalID); jeErr != nil {
			h.log.Error("StartProductionOrder: failed to bump journal next_number", "error", jeErr, "po_id", id)
			response.InternalError(c, "Failed to post journal entry")
			return
		}
	} else if existingStartJE > 0 {
		h.log.Info("StartProductionOrder: skipping start JE — already exists (start or confirm step)", "order_id", id)
	}

	if commitErr := tx.Commit(); commitErr != nil {
		h.log.Error("StartProductionOrder: commit failed", "error", commitErr, "po_id", id)
		response.InternalError(c, "Failed to start production order")
		return
	}

	// ── Post-commit side effects (no stock / no GL) ─────────────────────

	// Create work orders from BOM if none exist (orders confirmed before the fix)
	var woCount int
	h.db.QueryRow(`SELECT COUNT(*) FROM work_orders WHERE production_order_id = $1 AND tenant_id = $2 AND deleted_at IS NULL`, id, tenantID).Scan(&woCount)
	if woCount == 0 {
		var poOrgID *uuid.UUID
		var poProductName string
		h.db.QueryRow(`
			SELECT po.organization_id, COALESCE(p.name, '')
			FROM production_orders po
			LEFT JOIN products p ON p.id = po.product_id
			WHERE po.id = $1 AND po.tenant_id = $2
		`, id, tenantID).Scan(&poOrgID, &poProductName)
		if bomID != nil {
			orgVal := uuid.Nil
			if poOrgID != nil {
				orgVal = *poOrgID
			}
			if woErr := h.CreateWorkOrdersFromBOM(id, *bomID, tenantID, orgVal, qtyPlanned, userID); woErr != nil {
				h.log.Error("Failed to create work orders on start", "error", woErr)
			}
		}
		// Re-check: if still none (BOM without operations), create a default WO
		h.db.QueryRow(`SELECT COUNT(*) FROM work_orders WHERE production_order_id = $1 AND tenant_id = $2 AND deleted_at IS NULL`, id, tenantID).Scan(&woCount)
		if woCount == 0 {
			qty := qtyPlanned
			if qty <= 0 {
				qty = 1
			}
			woName := "Ishlab chiqarish"
			if poProductName != "" {
				woName = poProductName + " - Ishlab chiqarish"
			}
			h.db.Exec(`
				INSERT INTO work_orders (
					id, tenant_id, organization_id, production_order_id, code, name, sequence,
					quantity_to_produce, uom, status, created_by, created_at, updated_at
				) VALUES ($1, $2, $3, $4, $5, $6, 1, $7, 'pcs', 'pending', $8, $9, $9)
			`, uuid.New(), tenantID, poOrgID, id, fmt.Sprintf("WO-%s-1", id.String()[:12]), woName, qty, userID, now)
		}
	}

	// Auto-start the first pending work order
	_, _ = h.db.Exec(`
		UPDATE work_orders
		SET status = 'in_progress', actual_start = $1, started_by = $2
		WHERE id = (
			SELECT id FROM work_orders
			WHERE production_order_id = $3 AND tenant_id = $4 AND deleted_at IS NULL
				AND status IN ('pending', 'ready')
			ORDER BY sequence ASC LIMIT 1
		)
	`, now, userID, id, tenantID)

	// Event + audit trail (integration map §5)
	var startedProductName string
	h.db.QueryRow(`SELECT COALESCE(name, '') FROM products WHERE id = $1`, productID).Scan(&startedProductName)
	whIDStr := ""
	if warehouseID != nil {
		whIDStr = warehouseID.String()
	}
	h.EmitWorkflowEvent(tenantID, "production_order.started", map[string]interface{}{
		"record_id":        id.String(),
		"code":             poCode,
		"product_name":     startedProductName,
		"quantity_planned": qtyPlanned,
		"warehouse_id":     whIDStr,
		"material_cost":    totalMaterialCost,
	})
	h.productionAudit(tenantID, userID, id, "production_order.started",
		map[string]interface{}{"status": poStatus},
		map[string]interface{}{"status": "in_progress", "material_cost": totalMaterialCost})

	h.GetProductionOrder(c)
}

// PauseProductionOrder godoc
// @Summary Pause production order
// @Description Pause an in-progress production order
// @Tags Manufacturing
// @Accept json
// @Produce json
// @Param id path string true "Production Order ID"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /manufacturing/production-orders/{id}/pause [post]
func (h *Handler) PauseProductionOrder(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid production order ID")
		return
	}

	now := time.Now()

	// Org scoping (audit §2.9)
	var pauseOrgArg interface{}
	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		pauseOrgArg = orgID
	}

	query := `
		UPDATE production_orders
		SET status = 'paused', updated_at = $1
		WHERE id = $2 AND tenant_id = $3 AND deleted_at IS NULL AND status = 'in_progress'
		  AND (organization_id IS NULL OR $4::uuid IS NULL OR organization_id = $4)
	`

	result, err := h.db.Exec(query, now, id, tenantID, pauseOrgArg)
	if err != nil {
		h.log.Error("Failed to pause production order", "error", err)
		response.InternalError(c, "Failed to pause production order")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Production order not found or not in progress")
		return
	}

	// --- Pause all in-progress work orders for this production order ---
	h.db.Exec(`
		UPDATE work_orders
		SET status = 'paused', updated_at = $1
		WHERE production_order_id = $2 AND tenant_id = $3
			AND deleted_at IS NULL AND status = 'in_progress'
	`, now, id, tenantID)

	h.GetProductionOrder(c)
}

// CompleteProductionOrder godoc
// @Summary Complete production order
// @Description Complete a production order and update inventory
// @Tags Manufacturing
// @Accept json
// @Produce json
// @Param id path string true "Production Order ID"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /manufacturing/production-orders/{id}/complete [post]
func (h *Handler) CompleteProductionOrder(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	userID, _ := middleware.GetUserID(c)

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid production order ID")
		return
	}

	// Optional: accept quantity produced and output details
	var input struct {
		QuantityProduced float64  `json:"quantity_produced"`
		QuantityScrapped float64  `json:"quantity_scrapped"`
		GoodQuantity     *float64 `json:"good_quantity"`
		RejectQuantity   *float64 `json:"reject_quantity"`
		PackageCount     *int     `json:"package_count"`
		ShortfallReason  *string  `json:"shortfall_reason"`
	}
	c.ShouldBindJSON(&input)

	now := time.Now()

	// Active organization from the request header — org scoping (audit §2.9)
	// + account lookups.
	completeActiveOrgID, _ := middleware.GetOrganizationID(c)
	var orgArg interface{}
	if completeActiveOrgID != uuid.Nil {
		orgArg = completeActiveOrgID
	}

	// ── Pre-reads ───────────────────────────────────────────────────────
	var productID uuid.UUID
	var bomID *uuid.UUID
	var warehouseID *uuid.UUID
	var organizationID *uuid.UUID
	var storedProduced, qtyPlanned, qtyScrapped float64
	var poStatus, poCode string
	err = h.db.QueryRow(`
		SELECT product_id, bom_id, warehouse_id, organization_id,
		       COALESCE(quantity_produced, 0), quantity_planned, COALESCE(quantity_scrapped, 0), status, code
		FROM production_orders
		WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
		  AND (organization_id IS NULL OR $3::uuid IS NULL OR organization_id = $3)
	`, id, tenantID, orgArg).Scan(&productID, &bomID, &warehouseID, &organizationID,
		&storedProduced, &qtyPlanned, &qtyScrapped, &poStatus, &poCode)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Production order not found")
		return
	}
	if err != nil {
		h.log.Error("Failed to fetch production order for completion", "error", err)
		response.InternalError(c, "Failed to complete production order")
		return
	}

	// Status precondition (audit §2.1): only in_progress/paused orders can
	// complete. A second complete is a hard 400 — re-running the completion
	// side effects is exactly the cumulative-drift bug this rebuild removes.
	if poStatus == "completed" {
		response.BadRequest(c, "Ishlab chiqarish buyurtmasi allaqachon yakunlangan")
		return
	}
	if poStatus != "in_progress" && poStatus != "paused" {
		response.BadRequest(c, fmt.Sprintf("Faqat jarayondagi yoki pauza qilingan buyurtmani yakunlash mumkin (joriy holat: %s)", poStatus))
		return
	}

	// Prefer user's active organization for account lookups
	if completeActiveOrgID != uuid.Nil {
		organizationID = &completeActiveOrgID
	}

	// Warehouse fallback chain: BOM warehouse (org-validated) → org default →
	// tenant default. Reads only — the repair write is deferred into the
	// completion tx AFTER the status claim (C1: an aborted completion must
	// not rewrite the order).
	warehouseAutoAssigned := false
	if warehouseID == nil && bomID != nil && organizationID != nil {
		var bomWhID uuid.UUID
		if h.db.QueryRow(
			`SELECT b.warehouse_id
			 FROM product_boms b
			 JOIN warehouses w ON w.id = b.warehouse_id
			 WHERE b.id = $1 AND w.organization_id = $2 AND w.deleted_at IS NULL`,
			bomID, *organizationID,
		).Scan(&bomWhID) == nil {
			warehouseID = &bomWhID
			warehouseAutoAssigned = true
		}
	}
	if warehouseID == nil && organizationID != nil {
		var orgWH uuid.UUID
		if h.db.QueryRow(
			`SELECT id FROM warehouses WHERE organization_id = $1 AND deleted_at IS NULL AND is_active = true ORDER BY created_at LIMIT 1`,
			*organizationID,
		).Scan(&orgWH) == nil {
			warehouseID = &orgWH
			warehouseAutoAssigned = true
		}
	}
	if warehouseID == nil {
		var firstWH uuid.UUID
		if h.db.QueryRow(`SELECT id FROM warehouses WHERE tenant_id = $1 AND deleted_at IS NULL ORDER BY created_at ASC LIMIT 1`, tenantID).Scan(&firstWH) == nil {
			warehouseID = &firstWH
			warehouseAutoAssigned = true
		}
	}

	producedQty := storedProduced
	if input.QuantityProduced > 0 {
		producedQty = input.QuantityProduced
	} else if input.GoodQuantity != nil && *input.GoodQuantity > 0 {
		producedQty = *input.GoodQuantity
	}
	if producedQty <= 0 {
		producedQty = qtyPlanned
	}

	// Calculate cost breakdown from BOM: material, machine, labor (per unit of BOM output)
	var materialCost, machineCost, laborCost, unitCost float64
	if bomID != nil {
		var bomOutputQty float64
		if h.db.QueryRow(`SELECT COALESCE(quantity, 1) FROM product_boms WHERE id = $1`, bomID).Scan(&bomOutputQty) == nil && bomOutputQty > 0 {
			// Material cost from BOM components
			h.db.QueryRow(`
				SELECT COALESCE(SUM(bl.quantity * COALESCE(p.cost_price, 0) * (1 + COALESCE(bl.scrap_percent, 0) / 100.0)), 0) / $1
				FROM bom_lines bl
				JOIN products p ON p.id = bl.component_id
				WHERE bl.bom_id = $2
			`, bomOutputQty, bomID).Scan(&materialCost)

			// Machine + labor via the ONE shared costing helper (B4) — same
			// formula as the work-order completion path.
			machineCost, laborCost = h.computeMOMachineAndLaborCost(*bomID, bomOutputQty, id, producedQty)

			// Add per-unit cost of extras added in shop floor. The BOM-derived
			// materialCost above only includes BOM components; any extra
			// materials the operator added via the work-order shop floor are
			// recorded in work_order_materials but excluded from BOM, so the
			// finished-goods value would otherwise miss them entirely.
			if producedQty > 0 {
				var extraMaterialCost float64
				h.db.QueryRow(`SELECT COALESCE(SUM(total_cost), 0) FROM work_order_materials WHERE production_order_id = $1 AND tenant_id = $2`, id, tenantID).Scan(&extraMaterialCost)
				if extraMaterialCost > 0 {
					materialCost += extraMaterialCost / producedQty
				}
			}
			unitCost = materialCost + machineCost + laborCost
		}
	}
	if unitCost <= 0 {
		h.db.QueryRow("SELECT COALESCE(cost_price, 0) FROM products WHERE id = $1 AND tenant_id = $2", productID, tenantID).Scan(&unitCost)
		materialCost = unitCost
	}
	// Fallback: use list_price if cost_price is 0
	if unitCost <= 0 {
		h.db.QueryRow("SELECT COALESCE(list_price, 0) FROM products WHERE id = $1 AND tenant_id = $2", productID, tenantID).Scan(&unitCost)
		materialCost = unitCost
	}
	// NOTE (C1): the product cost_price / product_organization_settings
	// updates happen INSIDE the completion tx after the status claim — an
	// aborted completion must not rewrite the product's standard cost.

	// ── Dedupe guards ───────────────────────────────────────────────────
	// FG receipt: only 'production_complete' rows count (a material_return
	// receipt must NOT suppress the FG receipt — audit §2.2). Covers the
	// legacy 'receipt' rows and the v2 'production_in' rows.
	var existingReceiptCount int
	h.db.QueryRow(`
		SELECT COUNT(*) FROM inventory_transactions
		WHERE tenant_id = $1 AND reference_type = 'production_order' AND reference_id = $2
		AND transaction_type IN ('receipt', 'production_in') AND reason = 'production_complete'
		AND deleted_at IS NULL
	`, tenantID, id).Scan(&existingReceiptCount)
	alreadyReceived := existingReceiptCount > 0
	if alreadyReceived {
		h.log.Info("Finished goods already added for production order, skipping inventory", "order_id", id)
	}

	// No warehouse resolvable and nothing received yet → the FG receipt is
	// impossible, and a Dt 2810 JE without the stock leg would recreate the
	// stock/GL drift this rebuild removes. 422, same policy as missing
	// accounts.
	if !alreadyReceived && warehouseID == nil {
		response.Error(c, http.StatusUnprocessableEntity, "NO_WAREHOUSE",
			"Ombor topilmadi — tayyor mahsulotni kirim qilib bo'lmaydi. Buyurtmaga ombor biriktiring va qayta urinib ko'ring.")
		return
	}

	totalMaterialCost := producedQty * materialCost
	totalMachineCost := producedQty * machineCost
	totalLaborCost := producedQty * laborCost
	totalCost := producedQty * unitCost

	// Completion JE: the work-order path (createFinishedGoodsJournalEntry)
	// writes the same source_type='production_complete' — skip if it exists.
	var existingCompleteJE int
	h.db.QueryRow(`
		SELECT COUNT(*) FROM journal_entries
		WHERE tenant_id = $1 AND source_type = 'production_complete' AND source_id = $2
	`, tenantID, id).Scan(&existingCompleteJE)

	needJE := totalCost > 0 && existingCompleteJE == 0

	// ── Account lookups (pre-tx). The degenerate Cr 9110/9120 fallback is
	// GONE: missing required accounts are a 422, not a wrong posting
	// (integration map §4). ──────────────────────────────────────────────
	var wipAcct, rawAcct, finishedAcct, machineAcct, salaryAcct, journalID uuid.UUID
	var nextNumber int
	materialAlreadyJournalized := false
	var productName string
	h.db.QueryRow(`SELECT COALESCE(name, '') FROM products WHERE id = $1`, productID).Scan(&productName)

	if needJE {
		wipAcct = findAccount(h.db, tenantID, organizationID, "work in progress", "2010")
		if wipAcct == uuid.Nil {
			wipAcct = findAccount(h.db, tenantID, organizationID, "tugallanmagan ishlab chiqarish", "2010")
		}

		// Raw-materials account is NAS code 1010 (Xom ashyo va materiallar);
		// 1030 is Yoqilg'i (Fuel) and was the wrong default — migration 411.
		// 2910 (goods for resale) is the trading-tenant fallback, same as the
		// start/return/cancel lookups (C5).
		rawAcct = findAccount(h.db, tenantID, organizationID, "xom ashyo", "1010")
		if rawAcct == uuid.Nil {
			rawAcct = findAccount(h.db, tenantID, organizationID, "raw materials", "1010")
		}
		if rawAcct == uuid.Nil {
			rawAcct = findAccount(h.db, tenantID, organizationID, "goods for resale", "2910")
		}

		finishedAcct = findAccount(h.db, tenantID, organizationID, "finished goods", "2810")
		if finishedAcct == uuid.Nil {
			finishedAcct = findAccount(h.db, tenantID, organizationID, "tayyor mahsulot", "2810")
		}
		if finishedAcct == uuid.Nil {
			finishedAcct = getInventoryAccountByType(h.db, tenantID, organizationID, productID)
		}

		machineAcct = findAccount(h.db, tenantID, organizationID, "accrued machine", "2590")
		salaryAcct = findAccount(h.db, tenantID, organizationID, "accrued salaries", "6710")
		if salaryAcct == uuid.Nil {
			salaryAcct = findAccount(h.db, tenantID, organizationID, "ish haqi", "6710")
		}

		h.db.QueryRow(`
			SELECT id, next_number FROM journals
			WHERE tenant_id = $1 AND type = 'general' AND is_active = true
			ORDER BY created_at ASC LIMIT 1
		`, tenantID).Scan(&journalID, &nextNumber)
		if journalID == uuid.Nil {
			h.db.QueryRow(`
				SELECT id, next_number FROM journals
				WHERE tenant_id = $1 AND is_active = true
				ORDER BY created_at ASC LIMIT 1
			`, tenantID).Scan(&journalID, &nextNumber)
		}

		if wipAcct == uuid.Nil || rawAcct == uuid.Nil || finishedAcct == uuid.Nil || journalID == uuid.Nil {
			response.Error(c, http.StatusUnprocessableEntity, "MISSING_ACCOUNTS",
				"Buxgalteriya sozlamalari to'liq emas: 2010 (Tugallanmagan ishlab chiqarish), 1010 (Xom ashyo), 2810 (Tayyor mahsulot) schyotlari yoki faol jurnal topilmadi. Buyurtma yakunlanmadi.")
			return
		}

		// Was the material-consumption JE already posted? Three writers can
		// have journalized it: StartProductionOrder ('production_start'),
		// consumeBOMComponents via StartWorkOrder ('production_order_consume')
		// and the legacy confirm/start entries matched by description.
		var materialJEExists int
		h.db.QueryRow(`
			SELECT COUNT(*) FROM journal_entries
			WHERE tenant_id = $1 AND ($2::uuid IS NULL OR organization_id = $2)
			AND ((source_type = 'production_start' AND source_id = $4)
				 OR (source_type = 'production_order_consume' AND source_id = $4)
				 OR description LIKE '%' || $3 || '%started - materials consumed%'
				 OR description LIKE '%' || $3 || '%confirmed - planned material cost%')
			AND status = 'posted' AND deleted_at IS NULL
		`, tenantID, orgArg, poCode, id).Scan(&materialJEExists)
		materialAlreadyJournalized = materialJEExists > 0
	}

	// ── ONE transaction: status claim + WO completion + FG receipt + JE ──
	tx, txErr := h.db.Begin()
	if txErr != nil {
		h.log.Error("Failed to begin completion transaction", "error", txErr)
		response.InternalError(c, "Failed to complete production order")
		return
	}
	defer tx.Rollback()

	// Atomic claim (the RowsAffected==0 abort is the primary double-post
	// protection — the JE COUNT guards above are advisory).
	// Persist the computed costs on the order row — the report endpoint reads
	// COALESCE(NULLIF(actual_cost,0), planned_cost); without this the column
	// stays at its DEFAULT 0 forever (QA finding, iteration 2).
	query := `
		UPDATE production_orders
		SET status = 'completed', actual_end = $1, completed_by = $2, completed_at = $1, updated_at = $1,
			progress_percent = 100, current_stage = 'done',
			actual_cost = $3, material_cost = $4, labor_cost = $5, overhead_cost = $6
	`
	args := []interface{}{now, userID, totalCost, totalMaterialCost, totalLaborCost, totalMachineCost}
	argCount := 6

	if input.QuantityProduced > 0 {
		argCount++
		query += fmt.Sprintf(", quantity_produced = $%d", argCount)
		args = append(args, input.QuantityProduced)
	}
	if input.QuantityScrapped > 0 {
		argCount++
		query += fmt.Sprintf(", quantity_scrapped = $%d", argCount)
		args = append(args, input.QuantityScrapped)
	}
	if input.GoodQuantity != nil {
		argCount++
		query += fmt.Sprintf(", good_quantity = $%d", argCount)
		args = append(args, *input.GoodQuantity)
		// Also set quantity_produced from good_quantity if not explicitly provided
		if input.QuantityProduced == 0 {
			argCount++
			query += fmt.Sprintf(", quantity_produced = $%d", argCount)
			args = append(args, *input.GoodQuantity)
		}
	}
	if input.RejectQuantity != nil {
		argCount++
		query += fmt.Sprintf(", reject_quantity = $%d", argCount)
		args = append(args, *input.RejectQuantity)
	}
	if input.PackageCount != nil {
		argCount++
		query += fmt.Sprintf(", package_count = $%d", argCount)
		args = append(args, *input.PackageCount)
	}
	if input.ShortfallReason != nil {
		argCount++
		query += fmt.Sprintf(", shortfall_reason = $%d", argCount)
		args = append(args, *input.ShortfallReason)
	}

	argCount++
	args = append(args, id)
	argCount++
	args = append(args, tenantID)
	argCount++
	args = append(args, orgArg)

	query += fmt.Sprintf(" WHERE id = $%d AND tenant_id = $%d AND deleted_at IS NULL AND status IN ('in_progress', 'paused') AND (organization_id IS NULL OR $%d::uuid IS NULL OR organization_id = $%d)",
		argCount-2, argCount-1, argCount, argCount)

	result, err := tx.Exec(query, args...)
	if err != nil {
		h.log.Error("Failed to complete production order", "error", err)
		response.InternalError(c, "Failed to complete production order")
		return
	}
	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		// Lost a race: someone else completed/cancelled between the pre-read
		// and the claim.
		response.BadRequest(c, "Buyurtma holati o'zgargan — allaqachon yakunlangan yoki bekor qilingan")
		return
	}

	// Complete all remaining work orders for this production order
	if _, woErr := tx.Exec(`
		UPDATE work_orders
		SET status = 'completed', actual_end = $1
		WHERE production_order_id = $2 AND tenant_id = $3
			AND deleted_at IS NULL AND status NOT IN ('completed', 'done', 'cancelled')
	`, now, id, tenantID); woErr != nil {
		h.log.Error("CompleteProductionOrder: failed to complete work orders", "error", woErr, "po_id", id)
		response.InternalError(c, "Failed to complete production order")
		return
	}

	// C1: deferred config-repair writes — only after the claim succeeded, so
	// an aborted completion never rewrites the order or the product master.
	if warehouseAutoAssigned && warehouseID != nil {
		if _, wErr := tx.Exec(`UPDATE production_orders SET warehouse_id = $1 WHERE id = $2 AND tenant_id = $3 AND warehouse_id IS NULL`,
			*warehouseID, id, tenantID); wErr != nil {
			h.log.Error("CompleteProductionOrder: failed to persist auto-assigned warehouse", "error", wErr, "po_id", id)
			response.InternalError(c, "Failed to complete production order")
			return
		}
	}
	if unitCost > 0 {
		if _, cErr := tx.Exec(`UPDATE products SET cost_price = $1, updated_at = $2 WHERE id = $3 AND tenant_id = $4`,
			unitCost, now, productID, tenantID); cErr != nil {
			h.log.Error("CompleteProductionOrder: failed to update product cost_price", "error", cErr, "po_id", id)
			response.InternalError(c, "Failed to complete production order")
			return
		}
		if organizationID != nil {
			if _, cErr := tx.Exec(`UPDATE product_organization_settings SET cost_price = $1, updated_at = $2 WHERE product_id = $3 AND organization_id = $4`,
				unitCost, now, productID, *organizationID); cErr != nil {
				h.log.Error("CompleteProductionOrder: failed to update org cost_price", "error", cErr, "po_id", id)
				response.InternalError(c, "Failed to complete production order")
				return
			}
		}
	}

	// FG receipt via applyStockDelta — same tx as the claim and the JE.
	// (warehouseID != nil is guaranteed by the 422 guard above.)
	if !alreadyReceived {
		if _, _, dErr := h.applyStockDelta(tx, stockDeltaArgs{
			TenantID:    tenantID,
			OrgID:       organizationID,
			ProductID:   productID,
			WarehouseID: *warehouseID,
			Qty:         producedQty,
			UnitCost:    unitCost,
			TxType:      "production_in",
			RefType:     "production_order",
			RefID:       id.String(),
			Reason:      "production_complete",
			Notes:       "Auto-generated from production order completion",
			CreatedBy:   userID,
			When:        now,
		}); dErr != nil {
			h.log.Error("CompleteProductionOrder: FG receipt failed", "error", dErr, "po_id", id)
			response.InternalError(c, "Failed to receive finished goods")
			return
		}
	}

	// ============================================
	// JOURNAL ENTRY: WIP-based manufacturing cost flow (same tx)
	// Line 1: Dt 2010 WIP        / Kt 1010 Raw Materials   = materialCost (skipped if posted at start)
	// Line 2: Dt 2010 WIP        / Kt 2590 Accrued Machine = machineCost
	// Line 3: Dt 2010 WIP        / Kt 6710 Accrued Salary  = laborCost
	// Line 4: Dt 2810 Finished   / Kt 2010 WIP             = totalCost
	// ============================================
	if needJE {
		// Actual entry total: sum of all debit amounts that will be posted.
		wipInflow := float64(0)
		if totalMaterialCost > 0 && !materialAlreadyJournalized {
			wipInflow += totalMaterialCost
		}
		if totalMachineCost > 0 && machineAcct != uuid.Nil {
			wipInflow += totalMachineCost
		}
		if totalLaborCost > 0 && salaryAcct != uuid.Nil {
			wipInflow += totalLaborCost
		}
		entryTotal := wipInflow + totalCost // WIP debits + Finished Goods debit

		entryID := uuid.New()
		entryNumber := fmt.Sprintf("MFG%06d", nextEntryNumberSeq(tx, tenantID, organizationID, "MFG", nextNumber))
		description := fmt.Sprintf("Production Order %s completed - %s (qty: %.2f)", poCode, productName, producedQty)

		jeFail := func(step string, jeErr error) {
			h.log.Error("CompleteProductionOrder: "+step, "error", jeErr, "po_id", id)
			response.InternalError(c, "Failed to post completion journal entry")
		}

		if _, jeErr := tx.Exec(`
			INSERT INTO journal_entries (
				id, tenant_id, organization_id, journal_id, entry_number,
				entry_date, description, source_type, source_id, status, total_debit, total_credit,
				created_at, updated_at
			) VALUES ($1, $2, $3, $4, $5, $6::date, $7, 'production_complete', $8, 'posted', $9, $9, $10, $10)
		`, entryID, tenantID, organizationID, journalID, entryNumber,
			now, description, id.String(), entryTotal, now); jeErr != nil {
			jeFail("failed to insert completion JE header", jeErr)
			return
		}

		lineNum := 1

		// Line 1: Dt WIP / Kt Raw Materials (skip if already journalized at start)
		if totalMaterialCost > 0 && !materialAlreadyJournalized {
			if _, jeErr := tx.Exec(`
				INSERT INTO journal_entry_lines (id, journal_entry_id, account_id, description, debit_amount, credit_amount, line_number, created_at)
				VALUES ($1, $2, $3, $4, $5, 0, $6, $7)
			`, uuid.New(), entryID, wipAcct, "WIP: raw materials consumed", totalMaterialCost, lineNum, now); jeErr != nil {
				jeFail("failed to insert WIP material debit line", jeErr)
				return
			}
			lineNum++
			if _, jeErr := tx.Exec(`
				INSERT INTO journal_entry_lines (id, journal_entry_id, account_id, description, debit_amount, credit_amount, line_number, created_at)
				VALUES ($1, $2, $3, $4, 0, $5, $6, $7)
			`, uuid.New(), entryID, rawAcct, "WIP: raw materials consumed", totalMaterialCost, lineNum, now); jeErr != nil {
				jeFail("failed to insert raw materials credit line", jeErr)
				return
			}
			lineNum++
			if _, jeErr := tx.Exec(`UPDATE accounts SET current_balance = current_balance + $1, updated_at = $2 WHERE id = $3`, totalMaterialCost, now, wipAcct); jeErr != nil {
				jeFail("failed to update WIP balance (material)", jeErr)
				return
			}
			if _, jeErr := tx.Exec(`UPDATE accounts SET current_balance = current_balance - $1, updated_at = $2 WHERE id = $3`, totalMaterialCost, now, rawAcct); jeErr != nil {
				jeFail("failed to update raw materials balance", jeErr)
				return
			}
		}

		// Line 2: Dt WIP / Kt Accrued Machine
		if totalMachineCost > 0 && machineAcct != uuid.Nil {
			if _, jeErr := tx.Exec(`
				INSERT INTO journal_entry_lines (id, journal_entry_id, account_id, description, debit_amount, credit_amount, line_number, created_at)
				VALUES ($1, $2, $3, $4, $5, 0, $6, $7)
			`, uuid.New(), entryID, wipAcct, "WIP: machine costs accrued", totalMachineCost, lineNum, now); jeErr != nil {
				jeFail("failed to insert WIP machine debit line", jeErr)
				return
			}
			lineNum++
			if _, jeErr := tx.Exec(`
				INSERT INTO journal_entry_lines (id, journal_entry_id, account_id, description, debit_amount, credit_amount, line_number, created_at)
				VALUES ($1, $2, $3, $4, 0, $5, $6, $7)
			`, uuid.New(), entryID, machineAcct, "WIP: machine costs accrued", totalMachineCost, lineNum, now); jeErr != nil {
				jeFail("failed to insert machine credit line", jeErr)
				return
			}
			lineNum++
			if _, jeErr := tx.Exec(`UPDATE accounts SET current_balance = current_balance + $1, updated_at = $2 WHERE id = $3`, totalMachineCost, now, wipAcct); jeErr != nil {
				jeFail("failed to update WIP balance (machine)", jeErr)
				return
			}
			if _, jeErr := tx.Exec(`UPDATE accounts SET current_balance = current_balance + $1, updated_at = $2 WHERE id = $3`, totalMachineCost, now, machineAcct); jeErr != nil {
				jeFail("failed to update machine balance", jeErr)
				return
			}
		}

		// Line 3: Dt WIP / Kt Accrued Salary
		if totalLaborCost > 0 && salaryAcct != uuid.Nil {
			if _, jeErr := tx.Exec(`
				INSERT INTO journal_entry_lines (id, journal_entry_id, account_id, description, debit_amount, credit_amount, line_number, created_at)
				VALUES ($1, $2, $3, $4, $5, 0, $6, $7)
			`, uuid.New(), entryID, wipAcct, "WIP: labor costs accrued", totalLaborCost, lineNum, now); jeErr != nil {
				jeFail("failed to insert WIP labor debit line", jeErr)
				return
			}
			lineNum++
			if _, jeErr := tx.Exec(`
				INSERT INTO journal_entry_lines (id, journal_entry_id, account_id, description, debit_amount, credit_amount, line_number, created_at)
				VALUES ($1, $2, $3, $4, 0, $5, $6, $7)
			`, uuid.New(), entryID, salaryAcct, "WIP: labor costs accrued", totalLaborCost, lineNum, now); jeErr != nil {
				jeFail("failed to insert salary credit line", jeErr)
				return
			}
			lineNum++
			if _, jeErr := tx.Exec(`UPDATE accounts SET current_balance = current_balance + $1, updated_at = $2 WHERE id = $3`, totalLaborCost, now, wipAcct); jeErr != nil {
				jeFail("failed to update WIP balance (labor)", jeErr)
				return
			}
			if _, jeErr := tx.Exec(`UPDATE accounts SET current_balance = current_balance + $1, updated_at = $2 WHERE id = $3`, totalLaborCost, now, salaryAcct); jeErr != nil {
				jeFail("failed to update salary balance", jeErr)
				return
			}
		}

		// Line 4: Dt Finished Goods / Kt WIP = totalCost
		if _, jeErr := tx.Exec(`
			INSERT INTO journal_entry_lines (id, journal_entry_id, account_id, description, debit_amount, credit_amount, line_number, created_at)
			VALUES ($1, $2, $3, $4, $5, 0, $6, $7)
		`, uuid.New(), entryID, finishedAcct, "Finished goods from production", totalCost, lineNum, now); jeErr != nil {
			jeFail("failed to insert finished goods debit line", jeErr)
			return
		}
		lineNum++
		if _, jeErr := tx.Exec(`
			INSERT INTO journal_entry_lines (id, journal_entry_id, account_id, description, debit_amount, credit_amount, line_number, created_at)
			VALUES ($1, $2, $3, $4, 0, $5, $6, $7)
		`, uuid.New(), entryID, wipAcct, "Finished goods from production", totalCost, lineNum, now); jeErr != nil {
			jeFail("failed to insert WIP transfer credit line", jeErr)
			return
		}
		if _, jeErr := tx.Exec(`UPDATE accounts SET current_balance = current_balance + $1, updated_at = $2 WHERE id = $3`, totalCost, now, finishedAcct); jeErr != nil {
			jeFail("failed to update finished goods balance", jeErr)
			return
		}
		if _, jeErr := tx.Exec(`UPDATE accounts SET current_balance = current_balance - $1, updated_at = $2 WHERE id = $3`, totalCost, now, wipAcct); jeErr != nil {
			jeFail("failed to update WIP balance (transfer)", jeErr)
			return
		}

		if _, jeErr := tx.Exec(`UPDATE journals SET next_number = next_number + 1, updated_at = $1 WHERE id = $2`, now, journalID); jeErr != nil {
			jeFail("failed to bump journal next_number", jeErr)
			return
		}

		h.log.Info("CompleteProductionOrder: completion JE prepared",
			"entry_id", entryID, "entry_total", entryTotal, "total_cost", totalCost,
			"material_cost", totalMaterialCost, "machine_cost", totalMachineCost, "labor_cost", totalLaborCost,
			"material_already_journalized", materialAlreadyJournalized)
	} else if existingCompleteJE > 0 {
		h.log.Info("CompleteProductionOrder: SKIPPED journal entry - completion JE already exists", "po_id", id)
	} else {
		h.log.Warn("CompleteProductionOrder: SKIPPED journal entry - totalCost is zero",
			"unitCost", unitCost, "producedQty", producedQty, "productID", productID)
	}

	// Shortfall material return INSIDE the same tx — a post-commit failure
	// used to be unrepairable, since a second complete is now a hard 400.
	finalScrapped := qtyScrapped
	if input.QuantityScrapped > 0 {
		finalScrapped = input.QuantityScrapped
	}
	if rErr := h.returnUnusedComponentsTx(tx, id, tenantID, bomID, warehouseID, organizationID, userID,
		qtyPlanned, producedQty, finalScrapped, now); rErr != nil {
		h.log.Error("CompleteProductionOrder: shortfall return failed", "error", rErr, "po_id", id)
		response.InternalError(c, "Failed to return unused materials")
		return
	}

	if commitErr := tx.Commit(); commitErr != nil {
		h.log.Error("CompleteProductionOrder: commit failed", "error", commitErr, "po_id", id)
		response.InternalError(c, "Failed to complete production order")
		return
	}

	// ── Post-commit side effects ────────────────────────────────────────

	// Lot insert stays OUTSIDE the transaction — in PostgreSQL any error
	// inside a tx poisons it and makes COMMIT fail, so a lot-insert failure
	// used to silently roll back the whole inventory update. Failures are
	// logged loudly instead.
	if !alreadyReceived && warehouseID != nil {
		lotNumber := fmt.Sprintf("MFG-%s", id.String()[:8])
		if _, lotErr := h.db.Exec(`
			INSERT INTO inventory_lots (
				id, tenant_id, product_id, warehouse_id, lot_number,
				received_date, initial_quantity, remaining_quantity,
				unit_cost, status, created_at, updated_at
			) VALUES ($1, $2, $3, $4, $5, $6::date, $7, $7, $8, 'available', $9, $9)
		`, uuid.New(), tenantID, productID, warehouseID, lotNumber,
			now, producedQty, unitCost, now); lotErr != nil {
			h.log.Error("CompleteProductionOrder: lot insert failed (non-fatal)", "error", lotErr, "po_id", id)
		}
	}

	// Event + audit trail (integration map §5)
	h.EmitWorkflowEvent(tenantID, "production_order.completed", map[string]interface{}{
		"record_id":         id.String(),
		"code":              poCode,
		"product_name":      productName,
		"quantity_produced": producedQty,
		"actual_cost":       totalCost,
	})
	h.productionAudit(tenantID, userID, id, "production_order.completed",
		map[string]interface{}{"status": poStatus},
		map[string]interface{}{"status": "completed", "quantity_produced": producedQty, "actual_cost": totalCost})

	h.GetProductionOrder(c)
}

// returnUnusedComponentsTx returns unconsumed BOM components to inventory
// when production finishes with a shortfall (produced + scrapped < planned).
// Materials were consumed in full at StartProductionOrder; this reverses the
// unused portion so inventory stays accurate.
//
// v2 (audit §2.1): idempotent — a receipt-guard on reason='material_return'
// stops the cumulative re-credit the old complete-twice flow caused; stock
// legs go through applyStockDelta and the Dt 1010 / Kt 2010 reversal JE is
// posted in the SAME transaction, tagged source_type='production_return' +
// source_id=PO with a COUNT guard on that pair.
//
// Runs entirely on the caller's tx: CompleteProductionOrder passes its own
// transaction so the shortfall return commits (or fails) atomically with the
// completion itself. A returned error means the caller must roll back;
// skip conditions (nothing to return, already returned, accounts missing)
// are nil so the caller's primary work proceeds.
func (h *Handler) returnUnusedComponentsTx(
	tx *sql.Tx,
	poID, tenantID uuid.UUID, bomID *uuid.UUID, warehouseID *uuid.UUID,
	organizationID *uuid.UUID, userID uuid.UUID,
	qtyPlanned, qtyProduced, qtyScrapped float64, now time.Time,
) error {
	if bomID == nil || warehouseID == nil || qtyPlanned <= 0 {
		return nil
	}
	shortfall := qtyPlanned - qtyProduced - qtyScrapped
	if shortfall <= 0 {
		return nil
	}

	// Idempotency guard: a prior completion attempt already returned the
	// shortfall — never re-credit stock. Covers the legacy 'receipt' rows
	// and the v2 'return' rows.
	var alreadyReturned int
	tx.QueryRow(`
		SELECT COUNT(*) FROM inventory_transactions
		WHERE tenant_id = $1 AND reference_type = 'production_order' AND reference_id = $2
		  AND transaction_type IN ('receipt', 'return') AND reason = 'material_return'
		  AND deleted_at IS NULL
	`, tenantID, poID).Scan(&alreadyReturned)
	if alreadyReturned > 0 {
		h.log.Info("returnUnusedComponents: material return already recorded, skipping", "po_id", poID)
		return nil
	}

	returnRatio := shortfall / qtyPlanned

	type bomComponent struct {
		ComponentID    uuid.UUID
		Quantity       float64
		ScrapPercent   float64
		BOMOutputQty   float64
		TrackInventory bool
	}

	rows, err := tx.Query(`
		SELECT bl.component_id, bl.quantity, COALESCE(bl.scrap_percent, 0),
		       GREATEST(COALESCE(pb.quantity, 1), 0.0001),
		       COALESCE(p.track_inventory, true)
		FROM bom_lines bl
		JOIN product_boms pb ON pb.id = bl.bom_id
		LEFT JOIN products p ON p.id = bl.component_id
		WHERE bl.bom_id = $1
	`, bomID)
	if err != nil {
		return fmt.Errorf("fetch BOM components: %w", err)
	}
	var components []bomComponent
	for rows.Next() {
		var comp bomComponent
		if scanErr := rows.Scan(&comp.ComponentID, &comp.Quantity, &comp.ScrapPercent, &comp.BOMOutputQty, &comp.TrackInventory); scanErr == nil {
			components = append(components, comp)
		}
	}
	rows.Close()

	if len(components) == 0 {
		return nil
	}

	// JE guard on the (source_type, source_id) pair. The old MRT entry
	// carried NO source tag at all, so nothing could dedupe it.
	var existingReturnJE int
	tx.QueryRow(`
		SELECT COUNT(*) FROM journal_entries
		WHERE tenant_id = $1 AND source_type = 'production_return' AND source_id = $2
	`, tenantID, poID).Scan(&existingReturnJE)

	// Resolve accounts/journal BEFORE moving any stock: if the JE cannot be
	// posted, the stock must not move either (stock and GL land together).
	rawAcct := findAccount(tx, tenantID, organizationID, "xom ashyo", "1010")
	if rawAcct == uuid.Nil {
		rawAcct = findAccount(tx, tenantID, organizationID, "raw materials", "1010")
	}
	if rawAcct == uuid.Nil {
		rawAcct = findAccount(tx, tenantID, organizationID, "goods for resale", "2910")
	}
	wipAcct := findAccount(tx, tenantID, organizationID, "work in progress", "2010")
	if wipAcct == uuid.Nil {
		wipAcct = findAccount(tx, tenantID, organizationID, "tugallanmagan ishlab chiqarish", "2010")
	}
	var journalID uuid.UUID
	var nextNumber int
	tx.QueryRow(`
		SELECT id, next_number FROM journals
		WHERE tenant_id = $1 AND type = 'general' AND is_active = true
		ORDER BY created_at ASC LIMIT 1
	`, tenantID).Scan(&journalID, &nextNumber)
	if rawAcct == uuid.Nil || wipAcct == uuid.Nil || journalID == uuid.Nil {
		h.log.Error("returnUnusedComponents: GL accounts/journal missing — material return NOT performed",
			"po_id", poID, "raw_acct", rawAcct, "wip_acct", wipAcct, "journal_id", journalID)
		return nil
	}

	var totalReturnCost float64
	for _, comp := range components {
		// Non-tracked components were never deducted — nothing to return.
		if !comp.TrackInventory {
			continue
		}
		consumed := comp.Quantity * (qtyPlanned / comp.BOMOutputQty) * (1 + comp.ScrapPercent/100)
		returnQty := consumed * returnRatio
		if returnQty <= 0 {
			continue
		}

		var compCost float64
		tx.QueryRow("SELECT COALESCE(cost_price, 0) FROM products WHERE id = $1", comp.ComponentID).Scan(&compCost)

		_, valuedCost, dErr := h.applyStockDelta(tx, stockDeltaArgs{
			TenantID:    tenantID,
			OrgID:       organizationID,
			ProductID:   comp.ComponentID,
			WarehouseID: *warehouseID,
			Qty:         returnQty,
			UnitCost:    compCost,
			TxType:      "return",
			RefType:     "production_order",
			RefID:       poID.String(),
			Reason:      "material_return",
			Notes:       "Unused materials returned due to production shortfall",
			CreatedBy:   userID,
			When:        now,
		})
		if dErr != nil {
			return fmt.Errorf("stock return for component %s: %w", comp.ComponentID, dErr)
		}
		totalReturnCost += returnQty * valuedCost
	}

	// JE: Dt 1010 Raw Materials / Kt 2010 WIP — in the SAME tx as the stock.
	if totalReturnCost > 0 && existingReturnJE == 0 {
		var poNumber string
		tx.QueryRow(`SELECT code FROM production_orders WHERE id = $1`, poID).Scan(&poNumber)

		entryID := uuid.New()
		entryNumber := fmt.Sprintf("MFG%06d", nextEntryNumberSeq(tx, tenantID, organizationID, "MFG", nextNumber))
		description := fmt.Sprintf("Material return - %s shortfall %.2f (produced %.2f / planned %.2f)",
			poNumber, shortfall, qtyProduced, qtyPlanned)

		if _, jeErr := tx.Exec(`
			INSERT INTO journal_entries (id, tenant_id, organization_id, journal_id, entry_number,
				entry_date, description, source_type, source_id, status, total_debit, total_credit,
				created_by, created_at, updated_at)
			VALUES ($1,$2,$3,$4,$5,$6::date,$7,'production_return',$8,'posted',$9,$9,$10,$11,$11)
		`, entryID, tenantID, organizationID, journalID, entryNumber,
			now, description, poID.String(), totalReturnCost, nullableUUID(userID), now); jeErr != nil {
			return fmt.Errorf("insert return JE header: %w", jeErr)
		}

		// Dt Raw Materials (return materials to inventory account)
		if _, jeErr := tx.Exec(`
			INSERT INTO journal_entry_lines (id, journal_entry_id, account_id, description,
				debit_amount, credit_amount, line_number, created_at)
			VALUES ($1,$2,$3,$4,$5,0,1,$6)
		`, uuid.New(), entryID, rawAcct, "Unused materials returned", totalReturnCost, now); jeErr != nil {
			return fmt.Errorf("insert raw materials debit line: %w", jeErr)
		}

		// Kt WIP (reduce WIP by returned amount)
		if _, jeErr := tx.Exec(`
			INSERT INTO journal_entry_lines (id, journal_entry_id, account_id, description,
				debit_amount, credit_amount, line_number, created_at)
			VALUES ($1,$2,$3,$4,0,$5,2,$6)
		`, uuid.New(), entryID, wipAcct, "WIP reduced for returned materials", totalReturnCost, now); jeErr != nil {
			return fmt.Errorf("insert WIP credit line: %w", jeErr)
		}

		if _, jeErr := tx.Exec(`UPDATE accounts SET current_balance = current_balance + $1, updated_at = $2 WHERE id = $3`, totalReturnCost, now, rawAcct); jeErr != nil {
			return fmt.Errorf("update raw materials balance: %w", jeErr)
		}
		if _, jeErr := tx.Exec(`UPDATE accounts SET current_balance = current_balance - $1, updated_at = $2 WHERE id = $3`, totalReturnCost, now, wipAcct); jeErr != nil {
			return fmt.Errorf("update WIP balance: %w", jeErr)
		}
		if _, jeErr := tx.Exec(`UPDATE journals SET next_number = next_number + 1 WHERE id = $1`, journalID); jeErr != nil {
			return fmt.Errorf("bump journal next_number: %w", jeErr)
		}
	}

	h.log.Info("returnUnusedComponents: materials returned",
		"po_id", poID, "shortfall", shortfall, "returnRatio", returnRatio,
		"components", len(components), "totalReturnCost", totalReturnCost)
	return nil
}

// returnUnusedComponents is the own-transaction wrapper around
// returnUnusedComponentsTx for callers not already inside a transaction
// (CompleteWorkOrder's finish path in work_orders.go). CompleteProductionOrder
// calls the Tx variant directly, inside its completion transaction.
func (h *Handler) returnUnusedComponents(
	poID, tenantID uuid.UUID, bomID *uuid.UUID, warehouseID *uuid.UUID,
	organizationID *uuid.UUID, userID uuid.UUID,
	qtyPlanned, qtyProduced, qtyScrapped float64, now time.Time,
) {
	tx, txErr := h.db.Begin()
	if txErr != nil {
		h.log.Error("returnUnusedComponents: failed to start transaction", "error", txErr, "po_id", poID)
		return
	}
	defer tx.Rollback()
	if err := h.returnUnusedComponentsTx(tx, poID, tenantID, bomID, warehouseID, organizationID, userID,
		qtyPlanned, qtyProduced, qtyScrapped, now); err != nil {
		h.log.Error("returnUnusedComponents: rolled back", "error", err, "po_id", poID)
		return
	}
	if commitErr := tx.Commit(); commitErr != nil {
		h.log.Error("returnUnusedComponents: commit failed", "error", commitErr, "po_id", poID)
	}
}

// CancelProductionOrder godoc
// @Summary Cancel production order
// @Description Cancel a production order
// @Tags Manufacturing
// @Accept json
// @Produce json
// @Param id path string true "Production Order ID"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /manufacturing/production-orders/{id}/cancel [post]
func (h *Handler) CancelProductionOrder(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	userID, _ := middleware.GetUserID(c)

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid production order ID")
		return
	}

	// Optional cancellation reason (event/audit payload)
	var input struct {
		Reason string `json:"reason"`
	}
	c.ShouldBindJSON(&input)

	now := time.Now()

	cancelActiveOrgID, _ := middleware.GetOrganizationID(c)
	var orgArg interface{}
	if cancelActiveOrgID != uuid.Nil {
		orgArg = cancelActiveOrgID
	}

	// ── Pre-reads ───────────────────────────────────────────────────────
	var poCode, poStatus, productName string
	var organizationID *uuid.UUID
	err = h.db.QueryRow(`
		SELECT po.code, po.status, po.organization_id, COALESCE(p.name, '')
		FROM production_orders po
		LEFT JOIN products p ON p.id = po.product_id
		WHERE po.id = $1 AND po.tenant_id = $2 AND po.deleted_at IS NULL
		  AND (po.organization_id IS NULL OR $3::uuid IS NULL OR po.organization_id = $3)
	`, id, tenantID, orgArg).Scan(&poCode, &poStatus, &organizationID, &productName)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Production order not found or cannot be cancelled")
		return
	}
	if err != nil {
		h.log.Error("Failed to fetch production order for cancel", "error", err)
		response.InternalError(c, "Failed to cancel production order")
		return
	}
	if cancelActiveOrgID != uuid.Nil {
		organizationID = &cancelActiveOrgID
	}

	// What was issued at start (audit §2.3 — cancel used to leave phantom
	// WIP forever). Legacy 'issue' + v2 'production_out' rows, joined to
	// inventory because pre-447 ledger rows are not self-contained. ABS()
	// because legacy writers were split between signed and unsigned
	// quantities. This handler runs before completion, so nothing was
	// consumed into FG yet — the full issued quantity comes back.
	type issuedLeg struct {
		ProductID   uuid.UUID
		WarehouseID uuid.UUID
		Qty         float64
		Cost        float64
	}
	var legs []issuedLeg
	if legRows, legErr := h.db.Query(`
		SELECT inv.product_id, inv.warehouse_id,
		       SUM(ABS(it.quantity)) AS qty,
		       SUM(ABS(it.quantity) * COALESCE(it.unit_cost, 0)) AS cost
		FROM inventory_transactions it
		JOIN inventory inv ON inv.id = it.inventory_id
		WHERE it.tenant_id = $1 AND it.reference_type = 'production_order' AND it.reference_id = $2
		  AND it.transaction_type IN ('issue', 'production_out')
		  AND it.deleted_at IS NULL
		GROUP BY inv.product_id, inv.warehouse_id
	`, tenantID, id); legErr == nil {
		for legRows.Next() {
			var l issuedLeg
			if legRows.Scan(&l.ProductID, &l.WarehouseID, &l.Qty, &l.Cost) == nil && l.Qty > 0 {
				legs = append(legs, l)
			}
		}
		legRows.Close()
	} else {
		h.log.Error("CancelProductionOrder: failed to read issued materials", "error", legErr, "po_id", id)
	}

	// Reversal guards on the (source_type, source_id) pair — advisory; the
	// status-flip claim below is the primary protection.
	var existingCancelJE int
	h.db.QueryRow(`
		SELECT COUNT(*) FROM journal_entries
		WHERE tenant_id = $1 AND source_type = 'production_cancel' AND source_id = $2
	`, tenantID, id).Scan(&existingCancelJE)
	var existingCancelReturn int
	h.db.QueryRow(`
		SELECT COUNT(*) FROM inventory_transactions
		WHERE tenant_id = $1 AND reference_type = 'production_order' AND reference_id = $2
		  AND transaction_type = 'return' AND reason = 'production_cancel' AND deleted_at IS NULL
	`, tenantID, id).Scan(&existingCancelReturn)

	doReversal := len(legs) > 0 && existingCancelReturn == 0

	// Resolve accounts/journal BEFORE moving stock: if the reversing JE
	// cannot be posted, the stock must not move either. The cancel itself
	// (status flip) still goes through — matching v1 behavior, but loudly.
	var rawAcct, wipAcct, journalID uuid.UUID
	var nextNumber int
	if doReversal {
		rawAcct = findAccount(h.db, tenantID, organizationID, "xom ashyo", "1010")
		if rawAcct == uuid.Nil {
			rawAcct = findAccount(h.db, tenantID, organizationID, "raw materials", "1010")
		}
		if rawAcct == uuid.Nil {
			rawAcct = findAccount(h.db, tenantID, organizationID, "goods for resale", "2910")
		}
		wipAcct = findAccount(h.db, tenantID, organizationID, "work in progress", "2010")
		if wipAcct == uuid.Nil {
			wipAcct = findAccount(h.db, tenantID, organizationID, "tugallanmagan ishlab chiqarish", "2010")
		}
		h.db.QueryRow(`
			SELECT id, next_number FROM journals
			WHERE tenant_id = $1 AND type = 'general' AND is_active = true
			ORDER BY created_at ASC LIMIT 1
		`, tenantID).Scan(&journalID, &nextNumber)
		if rawAcct == uuid.Nil || wipAcct == uuid.Nil || journalID == uuid.Nil {
			h.log.Error("CancelProductionOrder: GL accounts/journal missing — cancelling WITHOUT the material reversal",
				"po_id", id, "raw_acct", rawAcct, "wip_acct", wipAcct, "journal_id", journalID)
			doReversal = false
		}
	}

	// ── ONE transaction: status claim + stock reversal + reversing JE ───
	tx, txErr := h.db.Begin()
	if txErr != nil {
		h.log.Error("Failed to begin cancel transaction", "error", txErr)
		response.InternalError(c, "Failed to cancel production order")
		return
	}
	defer tx.Rollback()

	claimRes, claimErr := tx.Exec(`
		UPDATE production_orders
		SET status = 'cancelled', updated_at = $1
		WHERE id = $2 AND tenant_id = $3 AND deleted_at IS NULL
		  AND status NOT IN ('completed', 'closed', 'cancelled')
		  AND (organization_id IS NULL OR $4::uuid IS NULL OR organization_id = $4)
	`, now, id, tenantID, orgArg)
	if claimErr != nil {
		h.log.Error("Failed to cancel production order", "error", claimErr)
		response.InternalError(c, "Failed to cancel production order")
		return
	}
	if n, _ := claimRes.RowsAffected(); n == 0 {
		response.NotFound(c, "Production order not found or cannot be cancelled")
		return
	}

	// Cancel all remaining work orders for this production order
	tx.Exec(`
		UPDATE work_orders
		SET status = 'cancelled', updated_at = $1
		WHERE production_order_id = $2 AND tenant_id = $3
			AND deleted_at IS NULL AND status NOT IN ('completed', 'done', 'cancelled')
	`, now, id, tenantID)

	var totalReversalCost float64
	if doReversal {
		for _, leg := range legs {
			unitCost := 0.0
			if leg.Qty > 0 {
				unitCost = leg.Cost / leg.Qty
			}
			_, valuedCost, dErr := h.applyStockDelta(tx, stockDeltaArgs{
				TenantID:    tenantID,
				OrgID:       organizationID,
				ProductID:   leg.ProductID,
				WarehouseID: leg.WarehouseID,
				Qty:         leg.Qty,
				UnitCost:    unitCost,
				TxType:      "return",
				RefType:     "production_order",
				RefID:       id.String(),
				Reason:      "production_cancel",
				Notes:       "Issued materials returned on cancellation",
				CreatedBy:   userID,
				When:        now,
			})
			if dErr != nil {
				h.log.Error("CancelProductionOrder: stock reversal failed", "error", dErr,
					"po_id", id, "product_id", leg.ProductID)
				response.InternalError(c, "Failed to return issued materials")
				return
			}
			totalReversalCost += leg.Qty * valuedCost
		}

		// Reversing JE: Dt 1010 Raw Materials / Kt 2010 WIP — the mirror of
		// the production_start entry, in the SAME tx as the stock legs.
		if totalReversalCost > 0 && existingCancelJE == 0 {
			entryID := uuid.New()
			entryNumber := fmt.Sprintf("MFG%06d", nextEntryNumberSeq(tx, tenantID, organizationID, "MFG", nextNumber))
			description := fmt.Sprintf("Production Order %s cancelled - issued materials returned", poCode)

			if _, jeErr := tx.Exec(`
				INSERT INTO journal_entries (
					id, tenant_id, organization_id, journal_id, entry_number,
					entry_date, description, source_type, source_id, status, total_debit, total_credit,
					created_by, created_at, updated_at
				) VALUES ($1, $2, $3, $4, $5, $6::date, $7, 'production_cancel', $8, 'posted', $9, $9, $10, $11, $11)
			`, entryID, tenantID, organizationID, journalID, entryNumber,
				now, description, id.String(), totalReversalCost, nullableUUID(userID), now); jeErr != nil {
				h.log.Error("CancelProductionOrder: failed to insert reversal JE header", "error", jeErr, "po_id", id)
				response.InternalError(c, "Failed to post reversal journal entry")
				return
			}
			if _, jeErr := tx.Exec(`
				INSERT INTO journal_entry_lines (id, journal_entry_id, account_id, description, debit_amount, credit_amount, line_number, created_at)
				VALUES ($1, $2, $3, $4, $5, 0, 1, $6)
			`, uuid.New(), entryID, rawAcct, "Issued materials returned on cancellation", totalReversalCost, now); jeErr != nil {
				h.log.Error("CancelProductionOrder: failed to insert raw materials debit line", "error", jeErr, "po_id", id)
				response.InternalError(c, "Failed to post reversal journal entry")
				return
			}
			if _, jeErr := tx.Exec(`
				INSERT INTO journal_entry_lines (id, journal_entry_id, account_id, description, debit_amount, credit_amount, line_number, created_at)
				VALUES ($1, $2, $3, $4, 0, $5, 2, $6)
			`, uuid.New(), entryID, wipAcct, "WIP reversed on cancellation", totalReversalCost, now); jeErr != nil {
				h.log.Error("CancelProductionOrder: failed to insert WIP credit line", "error", jeErr, "po_id", id)
				response.InternalError(c, "Failed to post reversal journal entry")
				return
			}
			if _, jeErr := tx.Exec(`UPDATE accounts SET current_balance = current_balance + $1, updated_at = $2 WHERE id = $3`, totalReversalCost, now, rawAcct); jeErr != nil {
				h.log.Error("CancelProductionOrder: failed to update raw materials balance", "error", jeErr, "po_id", id)
				response.InternalError(c, "Failed to post reversal journal entry")
				return
			}
			if _, jeErr := tx.Exec(`UPDATE accounts SET current_balance = current_balance - $1, updated_at = $2 WHERE id = $3`, totalReversalCost, now, wipAcct); jeErr != nil {
				h.log.Error("CancelProductionOrder: failed to update WIP balance", "error", jeErr, "po_id", id)
				response.InternalError(c, "Failed to post reversal journal entry")
				return
			}
			if _, jeErr := tx.Exec(`UPDATE journals SET next_number = next_number + 1 WHERE id = $1`, journalID); jeErr != nil {
				h.log.Error("CancelProductionOrder: failed to bump journal next_number", "error", jeErr, "po_id", id)
				response.InternalError(c, "Failed to post reversal journal entry")
				return
			}
		}
	}

	if commitErr := tx.Commit(); commitErr != nil {
		h.log.Error("CancelProductionOrder: commit failed", "error", commitErr, "po_id", id)
		response.InternalError(c, "Failed to cancel production order")
		return
	}

	// Event + audit trail (integration map §5)
	h.EmitWorkflowEvent(tenantID, "production_order.cancelled", map[string]interface{}{
		"record_id":     id.String(),
		"code":          poCode,
		"product_name":  productName,
		"reason":        input.Reason,
		"reversed_cost": totalReversalCost,
	})
	h.productionAudit(tenantID, userID, id, "production_order.cancelled",
		map[string]interface{}{"status": poStatus},
		map[string]interface{}{"status": "cancelled", "reason": input.Reason, "reversed_cost": totalReversalCost})

	h.GetProductionOrder(c)
}

// RecordProduction godoc
// @Summary Record production
// @Description Record production output for an order
// @Tags Manufacturing
// @Accept json
// @Produce json
// @Param id path string true "Production Order ID"
// @Param input body entity.ProductionOutputInput true "Production record input"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /manufacturing/production-orders/{id}/record [post]
func (h *Handler) RecordProduction(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid production order ID")
		return
	}

	var input struct {
		QuantityProduced float64 `json:"quantity_produced" binding:"required,gt=0"`
		QuantityScrapped float64 `json:"quantity_scrapped"`
		Notes            string  `json:"notes"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	now := time.Now()

	// Org scoping (audit §2.9)
	var orgArg interface{}
	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		orgArg = orgID
	}

	// Get current quantities and planned
	var quantityPlanned, currentProduced, currentScrapped float64
	checkQuery := `
		SELECT quantity_planned, quantity_produced, quantity_scrapped
		FROM production_orders
		WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL AND status = 'in_progress'
		  AND (organization_id IS NULL OR $3::uuid IS NULL OR organization_id = $3)
	`
	err = h.db.QueryRow(checkQuery, id, tenantID, orgArg).Scan(&quantityPlanned, &currentProduced, &currentScrapped)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Production order not found or not in progress")
		return
	}
	if err != nil {
		h.log.Error("Failed to get production order", "error", err)
		response.InternalError(c, "Failed to record production")
		return
	}

	// Upper bound (audit §2.12): recorded output cannot exceed the plan
	// (0.01% float tolerance). Previously a 100-unit order accepted 10 000.
	if currentProduced+input.QuantityProduced > quantityPlanned*1.0001 {
		response.BadRequest(c, fmt.Sprintf(
			"Ishlab chiqarilgan miqdor rejadan oshib ketadi: %.2f + %.2f > %.2f (reja). Avval buyurtma rejasini oshiring.",
			currentProduced, input.QuantityProduced, quantityPlanned))
		return
	}

	newProduced := currentProduced + input.QuantityProduced
	newScrapped := currentScrapped + input.QuantityScrapped
	progressPercent := (newProduced / quantityPlanned) * 100
	if progressPercent > 100 {
		progressPercent = 100
	}

	updateQuery := `
		UPDATE production_orders
		SET quantity_produced = $1, quantity_scrapped = $2, progress_percent = $3, updated_at = $4
		WHERE id = $5 AND tenant_id = $6
		  AND (organization_id IS NULL OR $7::uuid IS NULL OR organization_id = $7)
	`

	_, err = h.db.Exec(updateQuery, newProduced, newScrapped, progressPercent, now, id, tenantID, orgArg)
	if err != nil {
		h.log.Error("Failed to update production quantities", "error", err)
		response.InternalError(c, "Failed to record production")
		return
	}

	h.GetProductionOrder(c)
}

// GetProductionSchedule godoc
// @Summary Get production schedule
// @Description Get scheduled production orders for calendar view
// @Tags Manufacturing
// @Accept json
// @Produce json
// @Param date_from query string false "Start date (YYYY-MM-DD)"
// @Param date_to query string false "End date (YYYY-MM-DD)"
// @Success 200 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 500 {object} response.Response
// @Security BearerAuth
// @Router /manufacturing/schedule [get]
func (h *Handler) GetProductionSchedule(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	dateFrom := c.Query("date_from")
	dateTo := c.Query("date_to")

	if dateFrom == "" {
		dateFrom = time.Now().AddDate(0, 0, -7).Format("2006-01-02")
	}
	if dateTo == "" {
		dateTo = time.Now().AddDate(0, 0, 30).Format("2006-01-02")
	}

	query := `
		SELECT po.id, po.code, p.name as product_name, wc.name as work_center_name,
			   po.quantity_planned, po.status, po.scheduled_start, po.scheduled_end,
			   po.priority, po.progress_percent
		FROM production_orders po
		LEFT JOIN products p ON po.product_id = p.id
		LEFT JOIN work_centers wc ON po.work_center_id = wc.id
		WHERE po.tenant_id = $1 AND po.deleted_at IS NULL
		AND po.status NOT IN ('cancelled', 'closed')
		AND (
			(po.scheduled_start >= $2 AND po.scheduled_start <= $3)
			OR (po.scheduled_end >= $2 AND po.scheduled_end <= $3)
			OR (po.scheduled_start <= $2 AND po.scheduled_end >= $3)
		)
	`
	schedArgs := []interface{}{tenantID, dateFrom, dateTo}
	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		query += " AND po.organization_id = $4"
		schedArgs = append(schedArgs, orgID)
	}
	query += " ORDER BY po.scheduled_start ASC, po.priority ASC"

	rows, err := h.db.Query(query, schedArgs...)
	if err != nil {
		h.log.Error("Failed to get production schedule", "error", err)
		response.InternalError(c, "Failed to retrieve production schedule")
		return
	}
	defer rows.Close()

	schedule := []entity.ProductionScheduleItem{}
	for rows.Next() {
		var item entity.ProductionScheduleItem
		var wcName sql.NullString
		var scheduledStart, scheduledEnd sql.NullTime

		err := rows.Scan(
			&item.ID, &item.Code, &item.ProductName, &wcName,
			&item.QuantityPlanned, &item.Status, &scheduledStart, &scheduledEnd,
			&item.Priority, &item.ProgressPercent,
		)
		if err != nil {
			continue
		}

		if wcName.Valid {
			item.WorkCenterName = &wcName.String
		}
		if scheduledStart.Valid {
			s := scheduledStart.Time.Format("2006-01-02")
			item.ScheduledStart = &s
		}
		if scheduledEnd.Valid {
			s := scheduledEnd.Time.Format("2006-01-02")
			item.ScheduledEnd = &s
		}

		schedule = append(schedule, item)
	}

	response.Success(c, schedule)
}

// GetManufacturingStats moved to manufacturing_stats.go (Ishlab chiqarish v2
// rebuild) — one period-aware call in the purchase_stats.go shape.

// NOTE: Work Order handlers (ListWorkOrders, GetWorkOrder, CreateWorkOrder, StartWorkOrder,
// CompleteWorkOrder, RecordWorkOrderTime, PauseWorkOrder) are defined in work_orders.go

// =====================================================
// EQUIPMENT HANDLERS
// =====================================================

func (h *Handler) ListEquipment(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	search := c.Query("search")
	status := c.Query("status")
	workCenterID := c.Query("work_center_id")

	query := `
		SELECT e.id, e.code, e.name, e.description, e.equipment_type, e.category,
			   e.work_center_id, wc.name as work_center_name,
			   e.manufacturer, e.model, e.serial_number,
			   e.purchase_date, e.warranty_expiry, e.status,
			   e.last_maintenance_date, e.next_maintenance_date, e.maintenance_interval_days,
			   e.purchase_cost, e.current_value, e.hourly_rate, e.notes,
			   e.created_at, e.updated_at,
			   e.asset_id, fa.name as asset_name
		FROM manufacturing_equipment e
		LEFT JOIN work_centers wc ON wc.id = e.work_center_id AND wc.deleted_at IS NULL
		LEFT JOIN fa_assets fa ON fa.id = e.asset_id
		WHERE e.tenant_id = $1 AND e.deleted_at IS NULL
	`
	args := []interface{}{tenantID}
	argCount := 1

	if search != "" {
		argCount++
		query += fmt.Sprintf(" AND (e.name ILIKE $%d OR e.code ILIKE $%d OR e.serial_number ILIKE $%d)", argCount, argCount, argCount)
		args = append(args, "%"+search+"%")
	}
	if status != "" {
		argCount++
		query += fmt.Sprintf(" AND e.status = $%d", argCount)
		args = append(args, status)
	}
	if workCenterID != "" {
		argCount++
		query += fmt.Sprintf(" AND e.work_center_id = $%d", argCount)
		args = append(args, workCenterID)
	}
	query += " ORDER BY e.name ASC"

	rows, err := h.db.Query(query, args...)
	if err != nil {
		h.log.Error("Failed to list equipment", "error", err)
		response.InternalError(c, "Failed to list equipment")
		return
	}
	defer rows.Close()

	type EquipmentItem struct {
		ID                      uuid.UUID  `json:"id"`
		Code                    string     `json:"code"`
		Name                    string     `json:"name"`
		Description             *string    `json:"description,omitempty"`
		EquipmentType           string     `json:"equipment_type"`
		Category                *string    `json:"category,omitempty"`
		WorkCenterID            *uuid.UUID `json:"work_center_id,omitempty"`
		WorkCenterName          *string    `json:"work_center_name,omitempty"`
		Manufacturer            *string    `json:"manufacturer,omitempty"`
		Model                   *string    `json:"model,omitempty"`
		SerialNumber            *string    `json:"serial_number,omitempty"`
		PurchaseDate            *string    `json:"purchase_date,omitempty"`
		WarrantyExpiry          *string    `json:"warranty_expiry,omitempty"`
		Status                  string     `json:"status"`
		LastMaintenanceDate     *string    `json:"last_maintenance_date,omitempty"`
		NextMaintenanceDate     *string    `json:"next_maintenance_date,omitempty"`
		MaintenanceIntervalDays *int       `json:"maintenance_interval_days,omitempty"`
		PurchaseCost            float64    `json:"purchase_cost"`
		CurrentValue            float64    `json:"current_value"`
		HourlyRate              float64    `json:"hourly_rate"`
		Notes                   *string    `json:"notes,omitempty"`
		AssetID                 *uuid.UUID `json:"asset_id,omitempty"`
		AssetName               *string    `json:"asset_name,omitempty"`
		CreatedAt               time.Time  `json:"created_at"`
		UpdatedAt               time.Time  `json:"updated_at"`
	}

	equipment := []EquipmentItem{}
	for rows.Next() {
		var e EquipmentItem
		var description, category, manufacturer, model, serialNumber, notes, workCenterName sql.NullString
		var workCenterID, eqAssetID, eqAssetName sql.NullString
		var purchaseDate, warrantyExpiry, lastMaint, nextMaint sql.NullTime
		var maintInterval sql.NullInt64

		err := rows.Scan(
			&e.ID, &e.Code, &e.Name, &description, &e.EquipmentType, &category,
			&workCenterID, &workCenterName,
			&manufacturer, &model, &serialNumber,
			&purchaseDate, &warrantyExpiry, &e.Status,
			&lastMaint, &nextMaint, &maintInterval,
			&e.PurchaseCost, &e.CurrentValue, &e.HourlyRate, &notes,
			&e.CreatedAt, &e.UpdatedAt,
			&eqAssetID, &eqAssetName,
		)
		if err != nil {
			h.log.Error("Failed to scan equipment", "error", err)
			continue
		}
		if description.Valid {
			e.Description = &description.String
		}
		if category.Valid {
			e.Category = &category.String
		}
		if workCenterID.Valid {
			id, _ := uuid.Parse(workCenterID.String)
			e.WorkCenterID = &id
		}
		if workCenterName.Valid {
			e.WorkCenterName = &workCenterName.String
		}
		if eqAssetID.Valid {
			if aid, aErr := uuid.Parse(eqAssetID.String); aErr == nil {
				e.AssetID = &aid
			}
		}
		if eqAssetName.Valid {
			e.AssetName = &eqAssetName.String
		}
		if manufacturer.Valid {
			e.Manufacturer = &manufacturer.String
		}
		if model.Valid {
			e.Model = &model.String
		}
		if serialNumber.Valid {
			e.SerialNumber = &serialNumber.String
		}
		if notes.Valid {
			e.Notes = &notes.String
		}
		if purchaseDate.Valid {
			s := purchaseDate.Time.Format("2006-01-02")
			e.PurchaseDate = &s
		}
		if warrantyExpiry.Valid {
			s := warrantyExpiry.Time.Format("2006-01-02")
			e.WarrantyExpiry = &s
		}
		if lastMaint.Valid {
			s := lastMaint.Time.Format("2006-01-02")
			e.LastMaintenanceDate = &s
		}
		if nextMaint.Valid {
			s := nextMaint.Time.Format("2006-01-02")
			e.NextMaintenanceDate = &s
		}
		if maintInterval.Valid {
			v := int(maintInterval.Int64)
			e.MaintenanceIntervalDays = &v
		}
		equipment = append(equipment, e)
	}

	response.Success(c, equipment)
}

func (h *Handler) CreateEquipment(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	userID, _ := middleware.GetUserID(c)

	var input struct {
		Code                    string  `json:"code"`
		Name                    string  `json:"name" binding:"required"`
		Description             *string `json:"description"`
		EquipmentType           string  `json:"equipment_type"`
		Category                *string `json:"category"`
		WorkCenterID            *string `json:"work_center_id"`
		Manufacturer            *string `json:"manufacturer"`
		Model                   *string `json:"model"`
		SerialNumber            *string `json:"serial_number"`
		PurchaseDate            *string `json:"purchase_date"`
		WarrantyExpiry          *string `json:"warranty_expiry"`
		Status                  string  `json:"status"`
		MaintenanceIntervalDays *int    `json:"maintenance_interval_days"`
		PurchaseCost            float64 `json:"purchase_cost"`
		CurrentValue            float64 `json:"current_value"`
		HourlyRate              float64 `json:"hourly_rate"`
		Notes                   *string `json:"notes"`
		AssetID                 *string `json:"asset_id"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	if input.Code == "" {
		input.Code = fmt.Sprintf("EQ-%d", time.Now().UnixMilli())
	}
	if input.EquipmentType == "" {
		input.EquipmentType = "machine"
	}
	if input.Status == "" {
		input.Status = "operational"
	}

	var workCenterID *uuid.UUID
	if input.WorkCenterID != nil && *input.WorkCenterID != "" {
		id, err := uuid.Parse(*input.WorkCenterID)
		if err == nil {
			workCenterID = &id
		}
	}

	// fa_assets link (B7)
	var eqAssetID *uuid.UUID
	if input.AssetID != nil && *input.AssetID != "" {
		if aid, aErr := uuid.Parse(*input.AssetID); aErr == nil {
			eqAssetID = &aid
		}
	}

	var purchaseDate, warrantyExpiry interface{}
	if input.PurchaseDate != nil && *input.PurchaseDate != "" {
		purchaseDate = *input.PurchaseDate
	}
	if input.WarrantyExpiry != nil && *input.WarrantyExpiry != "" {
		warrantyExpiry = *input.WarrantyExpiry
	}

	id := uuid.New()
	now := time.Now()

	_, err := h.db.Exec(`
		INSERT INTO manufacturing_equipment (
			id, tenant_id, code, name, description, equipment_type, category,
			work_center_id, manufacturer, model, serial_number,
			purchase_date, warranty_expiry, status,
			maintenance_interval_days, purchase_cost, current_value, hourly_rate,
			notes, asset_id, created_by, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23)
	`, id, tenantID, input.Code, input.Name, input.Description, input.EquipmentType, input.Category,
		workCenterID, input.Manufacturer, input.Model, input.SerialNumber,
		purchaseDate, warrantyExpiry, input.Status,
		input.MaintenanceIntervalDays, input.PurchaseCost, input.CurrentValue, input.HourlyRate,
		input.Notes, eqAssetID, userID, now, now)
	if err != nil {
		h.log.Error("Failed to create equipment", "error", err)
		if strings.Contains(err.Error(), "unique") {
			response.BadRequest(c, "Equipment code already exists")
			return
		}
		response.InternalError(c, "Failed to create equipment")
		return
	}

	response.Created(c, gin.H{"id": id, "code": input.Code, "name": input.Name, "status": input.Status})
}

func (h *Handler) UpdateEquipment(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	equipID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid equipment ID")
		return
	}

	var input struct {
		Name                    *string  `json:"name"`
		Description             *string  `json:"description"`
		EquipmentType           *string  `json:"equipment_type"`
		Category                *string  `json:"category"`
		WorkCenterID            *string  `json:"work_center_id"`
		Manufacturer            *string  `json:"manufacturer"`
		Model                   *string  `json:"model"`
		SerialNumber            *string  `json:"serial_number"`
		PurchaseDate            *string  `json:"purchase_date"`
		WarrantyExpiry          *string  `json:"warranty_expiry"`
		Status                  *string  `json:"status"`
		MaintenanceIntervalDays *int     `json:"maintenance_interval_days"`
		PurchaseCost            *float64 `json:"purchase_cost"`
		CurrentValue            *float64 `json:"current_value"`
		HourlyRate              *float64 `json:"hourly_rate"`
		Notes                   *string  `json:"notes"`
		AssetID                 *string  `json:"asset_id"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	updates := []string{}
	args := []interface{}{}
	argCount := 0

	if input.Name != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("name = $%d", argCount))
		args = append(args, *input.Name)
	}
	if input.Description != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("description = $%d", argCount))
		args = append(args, *input.Description)
	}
	if input.EquipmentType != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("equipment_type = $%d", argCount))
		args = append(args, *input.EquipmentType)
	}
	if input.Category != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("category = $%d", argCount))
		args = append(args, *input.Category)
	}
	if input.WorkCenterID != nil {
		if *input.WorkCenterID == "" {
			argCount++
			updates = append(updates, fmt.Sprintf("work_center_id = $%d", argCount))
			args = append(args, nil)
		} else if id, err := uuid.Parse(*input.WorkCenterID); err == nil {
			argCount++
			updates = append(updates, fmt.Sprintf("work_center_id = $%d", argCount))
			args = append(args, id)
		}
	}
	if input.AssetID != nil {
		// fa_assets link (B7): empty string clears it.
		if *input.AssetID == "" {
			argCount++
			updates = append(updates, fmt.Sprintf("asset_id = $%d", argCount))
			args = append(args, nil)
		} else if aid, aErr := uuid.Parse(*input.AssetID); aErr == nil {
			argCount++
			updates = append(updates, fmt.Sprintf("asset_id = $%d", argCount))
			args = append(args, aid)
		}
	}
	if input.Manufacturer != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("manufacturer = $%d", argCount))
		args = append(args, *input.Manufacturer)
	}
	if input.Model != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("model = $%d", argCount))
		args = append(args, *input.Model)
	}
	if input.SerialNumber != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("serial_number = $%d", argCount))
		args = append(args, *input.SerialNumber)
	}
	if input.Status != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("status = $%d", argCount))
		args = append(args, *input.Status)
	}
	if input.PurchaseCost != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("purchase_cost = $%d", argCount))
		args = append(args, *input.PurchaseCost)
	}
	if input.CurrentValue != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("current_value = $%d", argCount))
		args = append(args, *input.CurrentValue)
	}
	if input.HourlyRate != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("hourly_rate = $%d", argCount))
		args = append(args, *input.HourlyRate)
	}
	if input.MaintenanceIntervalDays != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("maintenance_interval_days = $%d", argCount))
		args = append(args, *input.MaintenanceIntervalDays)
	}
	if input.Notes != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("notes = $%d", argCount))
		args = append(args, *input.Notes)
	}

	if len(updates) == 0 {
		response.BadRequest(c, "No fields to update")
		return
	}

	argCount++
	updates = append(updates, fmt.Sprintf("updated_at = $%d", argCount))
	args = append(args, time.Now())
	argCount++
	args = append(args, equipID)
	argCount++
	args = append(args, tenantID)

	query := fmt.Sprintf("UPDATE manufacturing_equipment SET %s WHERE id = $%d AND tenant_id = $%d AND deleted_at IS NULL",
		strings.Join(updates, ", "), argCount-1, argCount)
	result, err := h.db.Exec(query, args...)
	if err != nil {
		h.log.Error("Failed to update equipment", "error", err)
		response.InternalError(c, "Failed to update equipment")
		return
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		response.NotFound(c, "Equipment not found")
		return
	}
	response.Success(c, gin.H{"message": "Equipment updated successfully"})
}

func (h *Handler) DeleteEquipment(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	equipID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid equipment ID")
		return
	}

	result, err := h.db.Exec(
		"UPDATE manufacturing_equipment SET deleted_at = $1 WHERE id = $2 AND tenant_id = $3 AND deleted_at IS NULL",
		time.Now(), equipID, tenantID,
	)
	if err != nil {
		response.InternalError(c, "Failed to delete equipment")
		return
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		response.NotFound(c, "Equipment not found")
		return
	}
	response.Success(c, gin.H{"message": "Equipment deleted"})
}

func (h *Handler) ListMaintenanceTasks(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	equipID := c.Param("id")

	rows, err := h.db.Query(`
		SELECT m.id, m.equipment_id, m.work_center_id, m.maintenance_type,
			   m.scheduled_date, m.actual_date, m.duration_hours,
			   m.description, m.work_performed, m.status,
			   m.labor_cost, m.parts_cost, m.total_cost, m.notes,
			   m.created_at, m.updated_at
		FROM equipment_maintenance m
		WHERE m.tenant_id = $1 AND m.equipment_id = $2
		ORDER BY m.scheduled_date DESC
	`, tenantID, equipID)
	if err != nil {
		h.log.Error("Failed to list maintenance tasks", "error", err)
		response.InternalError(c, "Failed to list maintenance tasks")
		return
	}
	defer rows.Close()

	type MaintenanceTask struct {
		ID              uuid.UUID  `json:"id"`
		EquipmentID     uuid.UUID  `json:"equipment_id"`
		WorkCenterID    *uuid.UUID `json:"work_center_id,omitempty"`
		MaintenanceType string     `json:"maintenance_type"`
		ScheduledDate   *string    `json:"scheduled_date,omitempty"`
		ActualDate      *string    `json:"actual_date,omitempty"`
		DurationHours   float64    `json:"duration_hours"`
		Description     *string    `json:"description,omitempty"`
		WorkPerformed   *string    `json:"work_performed,omitempty"`
		Status          string     `json:"status"`
		LaborCost       float64    `json:"labor_cost"`
		PartsCost       float64    `json:"parts_cost"`
		TotalCost       float64    `json:"total_cost"`
		Notes           *string    `json:"notes,omitempty"`
		CreatedAt       time.Time  `json:"created_at"`
		UpdatedAt       time.Time  `json:"updated_at"`
	}

	tasks := []MaintenanceTask{}
	for rows.Next() {
		var t MaintenanceTask
		var workCenterID sql.NullString
		var scheduledDate, actualDate sql.NullTime
		var description, workPerformed, notes sql.NullString

		err := rows.Scan(
			&t.ID, &t.EquipmentID, &workCenterID, &t.MaintenanceType,
			&scheduledDate, &actualDate, &t.DurationHours,
			&description, &workPerformed, &t.Status,
			&t.LaborCost, &t.PartsCost, &t.TotalCost, &notes,
			&t.CreatedAt, &t.UpdatedAt,
		)
		if err != nil {
			h.log.Error("Failed to scan maintenance task", "error", err)
			continue
		}
		if workCenterID.Valid {
			id, _ := uuid.Parse(workCenterID.String)
			t.WorkCenterID = &id
		}
		if scheduledDate.Valid {
			s := scheduledDate.Time.Format("2006-01-02")
			t.ScheduledDate = &s
		}
		if actualDate.Valid {
			s := actualDate.Time.Format("2006-01-02")
			t.ActualDate = &s
		}
		if description.Valid {
			t.Description = &description.String
		}
		if workPerformed.Valid {
			t.WorkPerformed = &workPerformed.String
		}
		if notes.Valid {
			t.Notes = &notes.String
		}
		tasks = append(tasks, t)
	}

	response.Success(c, tasks)
}

func (h *Handler) CreateMaintenanceTask(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	equipIDStr := c.Param("id")
	equipID, err := uuid.Parse(equipIDStr)
	if err != nil {
		response.BadRequest(c, "Invalid equipment ID")
		return
	}

	var input struct {
		MaintenanceType string  `json:"maintenance_type" binding:"required"`
		ScheduledDate   string  `json:"scheduled_date" binding:"required"`
		Description     *string `json:"description"`
		DurationHours   float64 `json:"duration_hours"`
		LaborCost       float64 `json:"labor_cost"`
		PartsCost       float64 `json:"parts_cost"`
		Notes           *string `json:"notes"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	id := uuid.New()
	now := time.Now()
	totalCost := input.LaborCost + input.PartsCost

	_, err = h.db.Exec(`
		INSERT INTO equipment_maintenance (
			id, tenant_id, equipment_id, maintenance_type,
			scheduled_date, duration_hours, description,
			status, labor_cost, parts_cost, total_cost, notes,
			created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, 'scheduled', $8, $9, $10, $11, $12, $12)
	`, id, tenantID, equipID, input.MaintenanceType,
		input.ScheduledDate, input.DurationHours, input.Description,
		input.LaborCost, input.PartsCost, totalCost, input.Notes, now)
	if err != nil {
		h.log.Error("Failed to create maintenance task", "error", err)
		response.InternalError(c, "Failed to create maintenance task")
		return
	}

	// Update next_maintenance_date on the equipment
	h.db.Exec("UPDATE manufacturing_equipment SET next_maintenance_date = $1, updated_at = $2 WHERE id = $3 AND tenant_id = $4",
		input.ScheduledDate, now, equipID, tenantID)

	response.Created(c, gin.H{"id": id, "status": "scheduled"})
}

func (h *Handler) CompleteMaintenanceTask(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	equipID := c.Param("id")
	taskID, err := uuid.Parse(c.Param("task_id"))
	if err != nil {
		response.BadRequest(c, "Invalid task ID")
		return
	}

	var input struct {
		WorkPerformed string  `json:"work_performed"`
		DurationHours float64 `json:"duration_hours"`
		LaborCost     float64 `json:"labor_cost"`
		PartsCost     float64 `json:"parts_cost"`
		Notes         *string `json:"notes"`
	}
	c.ShouldBindJSON(&input)

	now := time.Now()
	totalCost := input.LaborCost + input.PartsCost
	today := now.Format("2006-01-02")

	result, err := h.db.Exec(`
		UPDATE equipment_maintenance
		SET status = 'completed', actual_date = $1, work_performed = $2,
			duration_hours = $3, labor_cost = $4, parts_cost = $5, total_cost = $6,
			notes = $7, updated_at = $8
		WHERE id = $9 AND tenant_id = $10 AND equipment_id = $11
	`, today, input.WorkPerformed, input.DurationHours,
		input.LaborCost, input.PartsCost, totalCost, input.Notes, now,
		taskID, tenantID, equipID)
	if err != nil {
		h.log.Error("Failed to complete maintenance task", "error", err)
		response.InternalError(c, "Failed to complete maintenance task")
		return
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		response.NotFound(c, "Maintenance task not found")
		return
	}

	// Update last_maintenance_date on equipment
	h.db.Exec("UPDATE manufacturing_equipment SET last_maintenance_date = $1, updated_at = $2 WHERE id = $3 AND tenant_id = $4",
		today, now, equipID, tenantID)

	response.Success(c, gin.H{"message": "Maintenance task completed"})
}

func (h *Handler) UpdateMaintenanceTask(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	equipID := c.Param("id")
	taskID, err := uuid.Parse(c.Param("task_id"))
	if err != nil {
		response.BadRequest(c, "Invalid task ID")
		return
	}

	var input struct {
		MaintenanceType *string  `json:"maintenance_type"`
		ScheduledDate   *string  `json:"scheduled_date"`
		DurationHours   *float64 `json:"duration_hours"`
		Description     *string  `json:"description"`
		Notes           *string  `json:"notes"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	updates := []string{}
	args := []interface{}{}
	argCount := 0

	if input.MaintenanceType != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("maintenance_type = $%d", argCount))
		args = append(args, *input.MaintenanceType)
	}
	if input.ScheduledDate != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("scheduled_date = $%d", argCount))
		args = append(args, *input.ScheduledDate)
	}
	if input.DurationHours != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("duration_hours = $%d", argCount))
		args = append(args, *input.DurationHours)
	}
	if input.Description != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("description = $%d", argCount))
		args = append(args, *input.Description)
	}
	if input.Notes != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("notes = $%d", argCount))
		args = append(args, *input.Notes)
	}

	if len(updates) == 0 {
		response.BadRequest(c, "No fields to update")
		return
	}

	argCount++
	updates = append(updates, fmt.Sprintf("updated_at = $%d", argCount))
	args = append(args, time.Now())
	argCount++
	args = append(args, taskID)
	argCount++
	args = append(args, tenantID)
	argCount++
	args = append(args, equipID)

	query := fmt.Sprintf(
		"UPDATE equipment_maintenance SET %s WHERE id = $%d AND tenant_id = $%d AND equipment_id = $%d",
		strings.Join(updates, ", "), argCount-2, argCount-1, argCount,
	)
	result, err := h.db.Exec(query, args...)
	if err != nil {
		h.log.Error("Failed to update maintenance task", "error", err)
		response.InternalError(c, "Failed to update maintenance task")
		return
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		response.NotFound(c, "Maintenance task not found")
		return
	}
	response.Success(c, gin.H{"message": "Maintenance task updated"})
}

// =====================================================
// MANUFACTURING CATEGORY HANDLERS
// =====================================================

func (h *Handler) ListManufacturingCategories(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	baseQuery := `
		SELECT id, name, description, color, is_active, sort_order, created_at, updated_at
		FROM manufacturing_categories
		WHERE tenant_id = $1 AND deleted_at IS NULL
	`
	args := []interface{}{tenantID}
	argCount := 1

	if orgID, orgOk := middleware.GetOrganizationID(c); orgOk && orgID != uuid.Nil {
		argCount++
		baseQuery += fmt.Sprintf(" AND organization_id = $%d", argCount)
		args = append(args, orgID)
	}

	baseQuery += " ORDER BY sort_order ASC, name ASC"

	rows, err := h.db.Query(baseQuery, args...)
	if err != nil {
		h.log.Error("Failed to list manufacturing categories", "error", err)
		response.InternalError(c, "Failed to retrieve manufacturing categories")
		return
	}
	defer rows.Close()

	type CategoryResponse struct {
		ID          uuid.UUID `json:"id"`
		Name        string    `json:"name"`
		Description *string   `json:"description"`
		Color       *string   `json:"color"`
		IsActive    bool      `json:"is_active"`
		SortOrder   int       `json:"sort_order"`
		CreatedAt   time.Time `json:"created_at"`
		UpdatedAt   time.Time `json:"updated_at"`
	}

	categories := []CategoryResponse{}
	for rows.Next() {
		var cat CategoryResponse
		if err := rows.Scan(&cat.ID, &cat.Name, &cat.Description, &cat.Color, &cat.IsActive, &cat.SortOrder, &cat.CreatedAt, &cat.UpdatedAt); err != nil {
			h.log.Error("Failed to scan manufacturing category", "error", err)
			continue
		}
		categories = append(categories, cat)
	}

	response.Success(c, categories)
}

func (h *Handler) CreateManufacturingCategory(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	var input struct {
		Name        string  `json:"name" binding:"required"`
		Description *string `json:"description"`
		Color       *string `json:"color"`
		SortOrder   *int    `json:"sort_order"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	id := uuid.New()
	now := time.Now()
	sortOrder := 0
	if input.SortOrder != nil {
		sortOrder = *input.SortOrder
	}

	orgID, _ := middleware.GetOrganizationID(c)
	var orgIDPtr *uuid.UUID
	if orgID != uuid.Nil {
		orgIDPtr = &orgID
	}

	query := `
		INSERT INTO manufacturing_categories (id, tenant_id, organization_id, name, description, color, is_active, sort_order, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, true, $7, $8, $9)
		RETURNING id
	`
	err := h.db.QueryRow(query, id, tenantID, orgIDPtr, input.Name, input.Description, input.Color, sortOrder, now, now).Scan(&id)
	if err != nil {
		h.log.Error("Failed to create manufacturing category", "error", err)
		if strings.Contains(err.Error(), "unique") {
			response.BadRequest(c, "Category name already exists")
			return
		}
		response.InternalError(c, "Failed to create manufacturing category")
		return
	}

	response.Created(c, gin.H{
		"id":          id,
		"name":        input.Name,
		"description": input.Description,
		"color":       input.Color,
		"is_active":   true,
		"sort_order":  sortOrder,
		"created_at":  now,
		"updated_at":  now,
	})
}

func (h *Handler) UpdateManufacturingCategory(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid category ID")
		return
	}

	var input struct {
		Name        *string `json:"name"`
		Description *string `json:"description"`
		Color       *string `json:"color"`
		IsActive    *bool   `json:"is_active"`
		SortOrder   *int    `json:"sort_order"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		h.log.Error("Invalid input", "error", err)
		response.BadRequest(c, "Invalid input")
		return
	}

	updates := []string{}
	args := []interface{}{}
	argCount := 0

	if input.Name != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("name = $%d", argCount))
		args = append(args, *input.Name)
	}
	if input.Description != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("description = $%d", argCount))
		args = append(args, *input.Description)
	}
	if input.Color != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("color = $%d", argCount))
		args = append(args, *input.Color)
	}
	if input.IsActive != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("is_active = $%d", argCount))
		args = append(args, *input.IsActive)
	}
	if input.SortOrder != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("sort_order = $%d", argCount))
		args = append(args, *input.SortOrder)
	}

	if len(updates) == 0 {
		response.BadRequest(c, "No fields to update")
		return
	}

	argCount++
	updates = append(updates, fmt.Sprintf("updated_at = $%d", argCount))
	args = append(args, time.Now())

	argCount++
	args = append(args, id)
	argCount++
	args = append(args, tenantID)

	query := fmt.Sprintf("UPDATE manufacturing_categories SET %s WHERE id = $%d AND tenant_id = $%d AND deleted_at IS NULL",
		strings.Join(updates, ", "), argCount-1, argCount)

	result, err := h.db.Exec(query, args...)
	if err != nil {
		h.log.Error("Failed to update manufacturing category", "error", err)
		if strings.Contains(err.Error(), "unique") {
			response.BadRequest(c, "Category name already exists")
			return
		}
		response.InternalError(c, "Failed to update manufacturing category")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Manufacturing category not found")
		return
	}

	response.Success(c, gin.H{"message": "Manufacturing category updated"})
}

func (h *Handler) DeleteManufacturingCategory(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(c, "Invalid category ID")
		return
	}

	query := `UPDATE manufacturing_categories SET deleted_at = $1 WHERE id = $2 AND tenant_id = $3 AND deleted_at IS NULL`
	result, err := h.db.Exec(query, time.Now(), id, tenantID)
	if err != nil {
		h.log.Error("Failed to delete manufacturing category", "error", err)
		response.InternalError(c, "Failed to delete manufacturing category")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Manufacturing category not found")
		return
	}

	response.Success(c, gin.H{"message": "Manufacturing category deleted"})
}

// =====================================================
// WORK CENTER EMPLOYEES
// =====================================================

func (h *Handler) ListWorkCenterEmployees(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	wcID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid work center ID")
		return
	}

	query := `
		SELECT wce.id, wce.employee_id,
			CONCAT(e.first_name, ' ', e.last_name) as employee_name,
			e.employee_number, e.job_title, wce.role, wce.created_at
		FROM work_center_employees wce
		JOIN employees e ON e.id = wce.employee_id
		WHERE wce.tenant_id = $1 AND wce.work_center_id = $2
		ORDER BY e.first_name, e.last_name`

	rows, err := h.db.Query(query, tenantID, wcID)
	if err != nil {
		response.InternalError(c, "Failed to fetch work center employees")
		return
	}
	defer rows.Close()

	var employees []entity.WorkCenterEmployeeResponse
	for rows.Next() {
		var emp entity.WorkCenterEmployeeResponse
		if err := rows.Scan(&emp.ID, &emp.EmployeeID, &emp.EmployeeName,
			&emp.EmployeeNumber, &emp.JobTitle, &emp.Role, &emp.CreatedAt); err != nil {
			response.InternalError(c, "Failed to scan employee")
			return
		}
		employees = append(employees, emp)
	}

	if employees == nil {
		employees = []entity.WorkCenterEmployeeResponse{}
	}

	response.Success(c, gin.H{"employees": employees})
}

func (h *Handler) AssignWorkCenterEmployees(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	wcID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid work center ID")
		return
	}

	var input entity.WorkCenterEmployeeInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input: "+err.Error())
		return
	}

	if len(input.EmployeeIDs) == 0 {
		response.BadRequest(c, "At least one employee ID is required")
		return
	}

	if input.Role == "" {
		input.Role = "operator"
	}

	// Verify work center exists
	var exists bool
	err = h.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM work_centers WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL)`,
		wcID, tenantID).Scan(&exists)
	if err != nil || !exists {
		response.NotFound(c, "Work center not found")
		return
	}

	// Insert employees (ignore duplicates)
	query := `INSERT INTO work_center_employees (tenant_id, work_center_id, employee_id, role)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (tenant_id, work_center_id, employee_id) DO UPDATE SET role = $4`

	for _, empID := range input.EmployeeIDs {
		_, err := h.db.Exec(query, tenantID, wcID, empID, input.Role)
		if err != nil {
			response.InternalError(c, "Failed to assign employee: "+err.Error())
			return
		}
	}

	response.Success(c, gin.H{"message": fmt.Sprintf("%d employee(s) assigned", len(input.EmployeeIDs))})
}

func (h *Handler) RemoveWorkCenterEmployee(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	wcID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid work center ID")
		return
	}

	empID, err := uuid.Parse(c.Param("employee_id"))
	if err != nil {
		response.BadRequest(c, "Invalid employee ID")
		return
	}

	result, err := h.db.Exec(`DELETE FROM work_center_employees WHERE tenant_id = $1 AND work_center_id = $2 AND employee_id = $3`,
		tenantID, wcID, empID)
	if err != nil {
		response.InternalError(c, "Failed to remove employee")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		response.NotFound(c, "Employee assignment not found")
		return
	}

	response.Success(c, gin.H{"message": "Employee removed from work center"})
}

// ─── Cost Calculations ────────────────────────────────────────────────────────

type costCalcLine struct {
	ID          string  `json:"id,omitempty"`
	ProductID   string  `json:"product_id,omitempty"`
	ProductName string  `json:"product_name"`
	Quantity    float64 `json:"quantity"`
	Unit        string  `json:"unit"`
	UnitCost    float64 `json:"unit_cost"`
	TotalCost   float64 `json:"total_cost"`
}

type costCalcResponse struct {
	ID              string         `json:"id"`
	Name            string         `json:"name"`
	ProductName     string         `json:"product_name"`
	Quantity        float64        `json:"quantity"`
	ProfitPercent   float64        `json:"profit_percent"`
	MaterialCost    float64        `json:"material_cost"`
	TotalCost       float64        `json:"total_cost"`
	TotalWithProfit float64        `json:"total_with_profit"`
	Notes           string         `json:"notes"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
	Lines           []costCalcLine `json:"lines"`
}

func (h *Handler) ListCostCalculations(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok {
		response.Unauthorized(c, "Invalid tenant")
		return
	}

	rows, err := h.db.Query(`
		SELECT id, name, product_name, quantity, profit_percent,
		       material_cost, total_cost, total_with_profit,
		       COALESCE(notes,''), created_at, updated_at
		FROM cost_calculations
		WHERE tenant_id = $1 AND deleted_at IS NULL
		ORDER BY created_at DESC
	`, tenantID)
	if err != nil {
		response.InternalError(c, "Failed to fetch calculations")
		return
	}
	defer rows.Close()

	var items []costCalcResponse
	for rows.Next() {
		var item costCalcResponse
		var id uuid.UUID
		if err := rows.Scan(&id, &item.Name, &item.ProductName, &item.Quantity,
			&item.ProfitPercent, &item.MaterialCost, &item.TotalCost, &item.TotalWithProfit,
			&item.Notes, &item.CreatedAt, &item.UpdatedAt); err != nil {
			continue
		}
		item.ID = id.String()
		item.Lines = []costCalcLine{}
		items = append(items, item)
	}
	if items == nil {
		items = []costCalcResponse{}
	}
	response.Success(c, items)
}

func (h *Handler) CreateCostCalculation(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok {
		response.Unauthorized(c, "Invalid tenant")
		return
	}
	userID, _ := middleware.GetUserID(c)
	organizationID, _ := middleware.GetOrganizationID(c)

	var req struct {
		Name          string         `json:"name" binding:"required"`
		ProductName   string         `json:"product_name" binding:"required"`
		Quantity      float64        `json:"quantity"`
		ProfitPercent float64        `json:"profit_percent"`
		Notes         string         `json:"notes"`
		Lines         []costCalcLine `json:"lines"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	if req.Quantity == 0 {
		req.Quantity = 1
	}
	if req.ProfitPercent == 0 {
		req.ProfitPercent = 45
	}

	// Calculate totals
	var materialCost float64
	for i := range req.Lines {
		req.Lines[i].TotalCost = req.Lines[i].Quantity * req.Lines[i].UnitCost
		materialCost += req.Lines[i].TotalCost
	}
	totalCost := materialCost
	totalWithProfit := totalCost * (1 + req.ProfitPercent/100)

	now := time.Now()
	id := uuid.New()

	var orgVal interface{}
	if organizationID != uuid.Nil {
		orgVal = organizationID
	}

	_, err := h.db.Exec(`
		INSERT INTO cost_calculations
		  (id, tenant_id, organization_id, name, product_name, quantity, profit_percent,
		   material_cost, total_cost, total_with_profit, notes, created_by, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$13)
	`, id, tenantID, orgVal, req.Name, req.ProductName, req.Quantity, req.ProfitPercent,
		materialCost, totalCost, totalWithProfit, req.Notes, userID, now)
	if err != nil {
		h.log.Error("Failed to create cost calculation", "error", err)
		response.InternalError(c, "Failed to create calculation")
		return
	}

	for _, line := range req.Lines {
		lineID := uuid.New()
		var productID interface{}
		if line.ProductID != "" {
			if pid, err := uuid.Parse(line.ProductID); err == nil {
				productID = pid
			}
		}
		if line.Unit == "" {
			line.Unit = "pcs"
		}
		h.db.Exec(`
			INSERT INTO cost_calculation_lines
			  (id, calculation_id, tenant_id, product_id, product_name, quantity, unit, unit_cost, total_cost)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		`, lineID, id, tenantID, productID, line.ProductName, line.Quantity, line.Unit, line.UnitCost, line.TotalCost)
	}

	response.Created(c, gin.H{"id": id.String()})
}

func (h *Handler) GetCostCalculation(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok {
		response.Unauthorized(c, "Invalid tenant")
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid ID")
		return
	}

	var item costCalcResponse
	var itemID uuid.UUID
	err = h.db.QueryRow(`
		SELECT id, name, product_name, quantity, profit_percent,
		       material_cost, total_cost, total_with_profit,
		       COALESCE(notes,''), created_at, updated_at
		FROM cost_calculations
		WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
	`, id, tenantID).Scan(&itemID, &item.Name, &item.ProductName, &item.Quantity,
		&item.ProfitPercent, &item.MaterialCost, &item.TotalCost, &item.TotalWithProfit,
		&item.Notes, &item.CreatedAt, &item.UpdatedAt)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Calculation not found")
		return
	}
	if err != nil {
		response.InternalError(c, "Failed to fetch calculation")
		return
	}
	item.ID = itemID.String()

	lrows, err := h.db.Query(`
		SELECT id, COALESCE(product_id::text,''), product_name, quantity, unit, unit_cost, total_cost
		FROM cost_calculation_lines WHERE calculation_id = $1 ORDER BY created_at
	`, id)
	if err == nil {
		defer lrows.Close()
		for lrows.Next() {
			var l costCalcLine
			lrows.Scan(&l.ID, &l.ProductID, &l.ProductName, &l.Quantity, &l.Unit, &l.UnitCost, &l.TotalCost)
			item.Lines = append(item.Lines, l)
		}
	}
	if item.Lines == nil {
		item.Lines = []costCalcLine{}
	}
	response.Success(c, item)
}

func (h *Handler) UpdateCostCalculation(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok {
		response.Unauthorized(c, "Invalid tenant")
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid ID")
		return
	}

	var req struct {
		Name          string         `json:"name"`
		ProductName   string         `json:"product_name"`
		Quantity      float64        `json:"quantity"`
		ProfitPercent float64        `json:"profit_percent"`
		Notes         string         `json:"notes"`
		Lines         []costCalcLine `json:"lines"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	var materialCost float64
	for i := range req.Lines {
		req.Lines[i].TotalCost = req.Lines[i].Quantity * req.Lines[i].UnitCost
		materialCost += req.Lines[i].TotalCost
	}
	totalCost := materialCost
	totalWithProfit := totalCost * (1 + req.ProfitPercent/100)
	now := time.Now()

	_, err = h.db.Exec(`
		UPDATE cost_calculations SET
		  name=$1, product_name=$2, quantity=$3, profit_percent=$4,
		  material_cost=$5, total_cost=$6, total_with_profit=$7, notes=$8, updated_at=$9
		WHERE id=$10 AND tenant_id=$11 AND deleted_at IS NULL
	`, req.Name, req.ProductName, req.Quantity, req.ProfitPercent,
		materialCost, totalCost, totalWithProfit, req.Notes, now, id, tenantID)
	if err != nil {
		response.InternalError(c, "Failed to update calculation")
		return
	}

	// Replace lines
	h.db.Exec("DELETE FROM cost_calculation_lines WHERE calculation_id=$1", id)
	for _, line := range req.Lines {
		lineID := uuid.New()
		var productID interface{}
		if line.ProductID != "" {
			if pid, err2 := uuid.Parse(line.ProductID); err2 == nil {
				productID = pid
			}
		}
		if line.Unit == "" {
			line.Unit = "pcs"
		}
		h.db.Exec(`
			INSERT INTO cost_calculation_lines
			  (id, calculation_id, tenant_id, product_id, product_name, quantity, unit, unit_cost, total_cost)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		`, lineID, id, tenantID, productID, line.ProductName, line.Quantity, line.Unit, line.UnitCost, line.TotalCost)
	}

	response.Success(c, gin.H{"message": "Updated"})
}

func (h *Handler) DeleteCostCalculation(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok {
		response.Unauthorized(c, "Invalid tenant")
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid ID")
		return
	}
	h.db.Exec(`UPDATE cost_calculations SET deleted_at=NOW() WHERE id=$1 AND tenant_id=$2`, id, tenantID)
	response.Success(c, gin.H{"message": "Deleted"})
}
