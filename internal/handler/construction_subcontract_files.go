package handler

// Subcontract file attachments — GET/POST /construction/subcontracts/:id/files
// and DELETE /construction/subcontracts/:id/files/:fileId.
//
// The SubcontractorsTab UI has been calling these routes since it shipped,
// but they never existed (phantom-endpoint audit finding). Storage reuses
// the project_files register with subcontract_id set (migration 463) — the
// upload payload the tab sends is exactly the project-file shape, and the
// binary itself lives in uploaded_files via POST /files/upload like every
// other attachment.

import (
	"strconv"
	"time"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// resolveSubcontract returns the subcontract's project id, enforcing tenant
// scope. uuid.Nil tenant or a foreign subcontract yields ok=false.
func (h *Handler) resolveSubcontract(tenantID uuid.UUID, subID int64) (int64, bool) {
	var projectID int64
	err := h.db.QueryRow(
		`SELECT project_id FROM construction_subcontract WHERE id = $1 AND tenant_id = $2`,
		subID, tenantID,
	).Scan(&projectID)
	return projectID, err == nil
}

// ListSubcontractFiles — GET /construction/subcontracts/:id/files
func (h *Handler) ListSubcontractFiles(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	subID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid subcontract ID")
		return
	}
	if _, found := h.resolveSubcontract(tenantID, subID); !found {
		response.NotFound(c, "Subcontract not found")
		return
	}

	rows, err := h.db.Query(`
		SELECT id, project_id, file_id, file_url, filename, file_size, mime_type, description, created_at, created_by
		FROM project_files
		WHERE tenant_id = $1 AND subcontract_id = $2
		ORDER BY created_at DESC
	`, tenantID, subID)
	if err != nil {
		h.log.Error("Failed to list subcontract files", "error", err)
		response.InternalServerError(c, "Failed to list files")
		return
	}
	defer rows.Close()

	type subFile struct {
		ID          int    `json:"id"`
		ProjectID   int    `json:"project_id"`
		FileID      string `json:"file_id"`
		FileURL     string `json:"file_url"`
		Filename    string `json:"filename"`
		FileSize    int64  `json:"file_size"`
		MimeType    string `json:"mime_type"`
		Description string `json:"description"`
		CreatedAt   string `json:"created_at"`
		CreatedBy   string `json:"created_by"`
	}
	files := []subFile{}
	for rows.Next() {
		var f subFile
		var createdAt time.Time
		if err := rows.Scan(&f.ID, &f.ProjectID, &f.FileID, &f.FileURL, &f.Filename, &f.FileSize, &f.MimeType, &f.Description, &createdAt, &f.CreatedBy); err != nil {
			continue
		}
		f.CreatedAt = createdAt.Format(time.RFC3339)
		files = append(files, f)
	}
	response.Success(c, files)
}

// CreateSubcontractFile — POST /construction/subcontracts/:id/files
func (h *Handler) CreateSubcontractFile(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	subID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid subcontract ID")
		return
	}
	projectID, found := h.resolveSubcontract(tenantID, subID)
	if !found {
		response.NotFound(c, "Subcontract not found")
		return
	}

	var input struct {
		FileID      string `json:"file_id" binding:"required"`
		FileURL     string `json:"file_url" binding:"required"`
		Filename    string `json:"filename" binding:"required"`
		FileSize    int64  `json:"file_size"`
		MimeType    string `json:"mime_type"`
		Description string `json:"description"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input")
		return
	}

	createdBy := ""
	if email, exists := c.Get("user_email"); exists {
		createdBy, _ = email.(string)
	}

	var id int
	err = h.db.QueryRow(`
		INSERT INTO project_files (tenant_id, project_id, subcontract_id, file_id, file_url, filename, file_size, mime_type, description, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		RETURNING id
	`, tenantID, projectID, subID, input.FileID, input.FileURL, input.Filename, input.FileSize, input.MimeType, input.Description, createdBy).Scan(&id)
	if err != nil {
		h.log.Error("Failed to create subcontract file", "error", err)
		response.InternalServerError(c, "Failed to save file")
		return
	}
	response.Success(c, gin.H{"id": id})
}

// DeleteSubcontractFile — DELETE /construction/subcontracts/:id/files/:fileId
func (h *Handler) DeleteSubcontractFile(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	subID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid subcontract ID")
		return
	}
	fileID, err := strconv.Atoi(c.Param("fileId"))
	if err != nil {
		response.BadRequest(c, "Invalid file ID")
		return
	}

	result, err := h.db.Exec(`
		DELETE FROM project_files WHERE id = $1 AND tenant_id = $2 AND subcontract_id = $3
	`, fileID, tenantID, subID)
	if err != nil {
		h.log.Error("Failed to delete subcontract file", "error", err)
		response.InternalServerError(c, "Failed to delete file")
		return
	}
	if n, _ := result.RowsAffected(); n == 0 {
		response.NotFound(c, "File not found")
		return
	}
	response.Success(c, gin.H{"message": "File deleted"})
}
