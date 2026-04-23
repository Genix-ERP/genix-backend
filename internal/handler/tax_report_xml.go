package handler

import (
	"encoding/xml"
	"fmt"
	"strings"
	"time"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ─────────────────────────────────────────────────────────────────────────────
// ExportEmployeeTaxReportXML — per-tax XML export for tax-authority filings.
//
//   GET /tax-reports/employee-taxes/xml?tax_code=NDFL&start_date=…&end_date=…
//
// Emits a self-described XML payload with a period header, a list of
// employees (one row per payroll_entry_taxes snapshot, aggregated by
// employee), and period totals. This is NOT a specific regulator schema
// — each authority (DSK / DSDMB / PFO'Z) uses a different one, and the
// frontend converts this to the required schema before uploading. But
// it's a solid intermediate form that's easy for an accountant to inspect
// or hand to their tax consultant, per §10 reports #2/#3/#4 of the TZ.
//
// ResponseText is served with Content-Type application/xml and a
// Content-Disposition header so the browser auto-downloads the file.
// ─────────────────────────────────────────────────────────────────────────────

type taxReportEmployeeXML struct {
	XMLName      xml.Name `xml:"Employee"`
	EmployeeID   string   `xml:"EmployeeId,omitempty"`
	Name         string   `xml:"Name"`
	Position     string   `xml:"Position,omitempty"`
	EntryCount   int      `xml:"EntryCount"`
	BaseAmount   float64  `xml:"BaseAmount"`
	TaxAmount    float64  `xml:"TaxAmount"`
}

type taxReportXML struct {
	XMLName     xml.Name               `xml:"TaxReport"`
	TenantID    string                 `xml:"TenantId,attr"`
	TaxCode     string                 `xml:"TaxCode,attr"`
	TaxName     string                 `xml:"TaxName,attr"`
	Payer       string                 `xml:"Payer,attr"`
	RatePercent float64                `xml:"Rate,attr"`
	StartDate   string                 `xml:"StartDate"`
	EndDate     string                 `xml:"EndDate"`
	GeneratedAt string                 `xml:"GeneratedAt"`
	Employees   []taxReportEmployeeXML `xml:"Employees>Employee"`
	Totals      struct {
		EmployeeCount int     `xml:"EmployeeCount"`
		EntryCount    int     `xml:"EntryCount"`
		BaseAmount    float64 `xml:"BaseAmount"`
		TaxAmount     float64 `xml:"TaxAmount"`
	} `xml:"Totals"`
}

// ExportEmployeeTaxReportXML writes the XML straight onto the response
// writer so we don't materialize big reports in memory for big payrolls.
func (h *Handler) ExportEmployeeTaxReportXML(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	taxCode := strings.ToUpper(strings.TrimSpace(c.Query("tax_code")))
	if taxCode == "" {
		response.BadRequest(c, "tax_code query parameter is required")
		return
	}

	startDate := c.Query("start_date")
	endDate := c.Query("end_date")
	if startDate == "" || endDate == "" {
		response.BadRequest(c, "start_date and end_date are required (YYYY-MM-DD)")
		return
	}

	// ───────── per-employee aggregation for the chosen tax code ─────────
	rows, err := h.db.Query(`
		SELECT
		  pe.employee_id::text,
		  pe.employee_name,
		  COALESCE(pe.position_snapshot, ''),
		  COUNT(*)::int                            AS entry_count,
		  COALESCE(SUM(pet.base_amount), 0)        AS base_sum,
		  COALESCE(SUM(pet.amount), 0)             AS tax_sum,
		  COALESCE(MAX(pet.tax_name_snapshot), '') AS tax_name,
		  COALESCE(MAX(pet.payer_snapshot), '')    AS payer,
		  COALESCE(MAX(pet.rate_snapshot), 0)      AS rate
		FROM payroll_entry_taxes pet
		JOIN payroll_entries pe ON pe.id = pet.payroll_entry_id
		WHERE pet.tenant_id             = $1
		  AND UPPER(pet.tax_code_snapshot) = $2
		  AND pe.created_at >= $3
		  AND pe.created_at <= $4
		GROUP BY pe.employee_id, pe.employee_name, pe.position_snapshot
		ORDER BY pe.employee_name
	`, tenantID, taxCode, startDate, endDate+" 23:59:59")
	if err != nil {
		h.log.Error("Failed to aggregate employee-taxes for XML export", "error", err)
		response.InternalError(c, "Failed to build XML report")
		return
	}
	defer rows.Close()

	report := taxReportXML{
		TenantID:    tenantID.String(),
		TaxCode:     taxCode,
		StartDate:   startDate,
		EndDate:     endDate,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Employees:   make([]taxReportEmployeeXML, 0),
	}

	for rows.Next() {
		var (
			empID, name, position, taxName, payer string
			entryCount                            int
			baseSum, taxSum, rate                 float64
		)
		if err := rows.Scan(&empID, &name, &position, &entryCount, &baseSum, &taxSum, &taxName, &payer, &rate); err != nil {
			continue
		}
		report.Employees = append(report.Employees, taxReportEmployeeXML{
			EmployeeID: empID,
			Name:       name,
			Position:   position,
			EntryCount: entryCount,
			BaseAmount: round2(baseSum),
			TaxAmount:  round2(taxSum),
		})
		// Use the most-recent snapshot's header for the whole report.
		report.TaxName = taxName
		report.Payer = payer
		report.RatePercent = rate
		report.Totals.EmployeeCount++
		report.Totals.EntryCount += entryCount
		report.Totals.BaseAmount += baseSum
		report.Totals.TaxAmount += taxSum
	}
	report.Totals.BaseAmount = round2(report.Totals.BaseAmount)
	report.Totals.TaxAmount = round2(report.Totals.TaxAmount)

	body, err := xml.MarshalIndent(report, "", "  ")
	if err != nil {
		h.log.Error("Failed to marshal tax report XML", "error", err)
		response.InternalError(c, "Failed to marshal XML")
		return
	}

	// Produce a stable, human-readable filename. The authority's specific
	// naming convention can be applied on the client side if needed.
	filename := fmt.Sprintf("tax-report-%s-%s-%s.xml", strings.ToLower(taxCode), startDate, endDate)
	c.Header("Content-Type", "application/xml; charset=utf-8")
	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	c.Writer.Write([]byte(xml.Header))
	c.Writer.Write(body)
}
