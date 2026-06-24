package handler

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/genixerp/genix-backend/internal/domain/entity"
	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ---------- Milestone substages ----------

// ListMilestoneSubstages returns the substages for a milestone.
func (h *Handler) ListMilestoneSubstages(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Invalid tenant")
		return
	}

	milestoneID, err := uuid.Parse(c.Param("milestoneId"))
	if err != nil {
		response.BadRequest(c, "Invalid milestone ID")
		return
	}

	rows, err := h.db.Query(`
		SELECT id, tenant_id, project_id, milestone_id, title, COALESCE(description, ''), status, due_date, position, created_at, updated_at
		FROM project_milestone_substages
		WHERE milestone_id = $1 AND tenant_id = $2
		ORDER BY position ASC, created_at ASC`, milestoneID, tenantID)
	if err != nil {
		h.log.Error("Failed to fetch substages", "error", err)
		response.InternalError(c, "Failed to fetch substages")
		return
	}
	defer rows.Close()

	substages := []entity.ProjectMilestoneSubstage{}
	for rows.Next() {
		var s entity.ProjectMilestoneSubstage
		var dueDate sql.NullTime
		if err := rows.Scan(&s.ID, &s.TenantID, &s.ProjectID, &s.MilestoneID, &s.Title, &s.Description, &s.Status, &dueDate, &s.Position, &s.CreatedAt, &s.UpdatedAt); err != nil {
			continue
		}
		if dueDate.Valid {
			s.DueDate = dueDate.Time.Format("2006-01-02")
		}
		substages = append(substages, s)
	}

	response.Success(c, substages)
}

// CreateMilestoneSubstage adds a substage to a milestone.
func (h *Handler) CreateMilestoneSubstage(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Invalid tenant")
		return
	}

	projectID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid project ID")
		return
	}
	milestoneID, err := uuid.Parse(c.Param("milestoneId"))
	if err != nil {
		response.BadRequest(c, "Invalid milestone ID")
		return
	}

	var input entity.CreateSubstageInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input")
		return
	}

	status := input.Status
	if status == "" {
		status = "pending"
	}

	var dueDate *time.Time
	if input.DueDate != "" {
		if t, err := time.Parse("2006-01-02", input.DueDate); err == nil {
			dueDate = &t
		}
	}

	var nextPos int
	h.db.QueryRow(`SELECT COALESCE(MAX(position), -1) + 1 FROM project_milestone_substages WHERE milestone_id = $1`, milestoneID).Scan(&nextPos)

	id := uuid.New()
	now := time.Now()
	_, err = h.db.Exec(`
		INSERT INTO project_milestone_substages (id, tenant_id, project_id, milestone_id, title, description, status, due_date, position, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $10)`,
		id, tenantID, projectID, milestoneID, input.Title, input.Description, status, dueDate, nextPos, now)
	if err != nil {
		h.log.Error("Failed to create substage", "error", err)
		response.InternalError(c, "Failed to create substage")
		return
	}

	resp := entity.ProjectMilestoneSubstage{
		ID: id, TenantID: tenantID, ProjectID: projectID, MilestoneID: milestoneID,
		Title: input.Title, Description: input.Description, Status: status,
		Position: nextPos, CreatedAt: now, UpdatedAt: now,
	}
	if dueDate != nil {
		resp.DueDate = dueDate.Format("2006-01-02")
	}
	response.Created(c, resp)
}

// UpdateMilestoneSubstage updates a substage.
func (h *Handler) UpdateMilestoneSubstage(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Invalid tenant")
		return
	}

	substageID, err := uuid.Parse(c.Param("substageId"))
	if err != nil {
		response.BadRequest(c, "Invalid substage ID")
		return
	}

	var input entity.UpdateSubstageInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input")
		return
	}

	updates := []string{}
	args := []interface{}{}
	argCount := 0
	if input.Title != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("title = $%d", argCount))
		args = append(args, *input.Title)
	}
	if input.Description != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("description = $%d", argCount))
		args = append(args, *input.Description)
	}
	if input.Status != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("status = $%d", argCount))
		args = append(args, *input.Status)
	}
	if input.DueDate != nil {
		argCount++
		updates = append(updates, fmt.Sprintf("due_date = $%d", argCount))
		if *input.DueDate != "" {
			if t, err := time.Parse("2006-01-02", *input.DueDate); err == nil {
				args = append(args, t)
			} else {
				args = append(args, nil)
			}
		} else {
			args = append(args, nil)
		}
	}
	if len(updates) == 0 {
		response.BadRequest(c, "No fields to update")
		return
	}

	argCount++
	updates = append(updates, fmt.Sprintf("updated_at = $%d", argCount))
	args = append(args, time.Now())

	subPlaceholder := argCount + 1
	tenantPlaceholder := argCount + 2
	args = append(args, substageID, tenantID)

	query := fmt.Sprintf("UPDATE project_milestone_substages SET %s WHERE id = $%d AND tenant_id = $%d",
		strings.Join(updates, ", "), subPlaceholder, tenantPlaceholder)

	result, err := h.db.Exec(query, args...)
	if err != nil {
		h.log.Error("Failed to update substage", "error", err)
		response.InternalError(c, "Failed to update substage")
		return
	}
	if n, _ := result.RowsAffected(); n == 0 {
		response.NotFound(c, "Substage")
		return
	}

	response.Success(c, gin.H{"message": "Substage updated"})
}

// DeleteMilestoneSubstage removes a substage.
func (h *Handler) DeleteMilestoneSubstage(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Invalid tenant")
		return
	}

	substageID, err := uuid.Parse(c.Param("substageId"))
	if err != nil {
		response.BadRequest(c, "Invalid substage ID")
		return
	}

	result, err := h.db.Exec(`DELETE FROM project_milestone_substages WHERE id = $1 AND tenant_id = $2`, substageID, tenantID)
	if err != nil {
		response.InternalError(c, "Failed to delete substage")
		return
	}
	if n, _ := result.RowsAffected(); n == 0 {
		response.NotFound(c, "Substage")
		return
	}

	response.Success(c, gin.H{"message": "Substage deleted"})
}

// ---------- Milestone attachments (reuses the shared attachments table) ----------

type milestoneAttachment struct {
	ID           uuid.UUID `json:"id"`
	FileName     string    `json:"file_name"`
	OriginalName string    `json:"original_name"`
	MimeType     string    `json:"mime_type"`
	FileSize     int64     `json:"file_size"`
	URL          string    `json:"url"`
	Description  string    `json:"description"`
	CreatedAt    time.Time `json:"created_at"`
}

// ListMilestoneAttachments lists files attached to a milestone.
func (h *Handler) ListMilestoneAttachments(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	milestoneID, err := uuid.Parse(c.Param("milestoneId"))
	if err != nil {
		response.BadRequest(c, "Invalid milestone ID")
		return
	}

	rows, err := h.db.Query(`
		SELECT id, file_name, original_name, mime_type, file_size, storage_path,
		       COALESCE(metadata->>'description', '') as description, created_at
		FROM attachments
		WHERE tenant_id = $1 AND entity_type = 'milestone' AND entity_id = $2
		ORDER BY created_at DESC`, tenantID, milestoneID)
	if err != nil {
		response.InternalError(c, "Failed to list attachments")
		return
	}
	defer rows.Close()

	attachments := []milestoneAttachment{}
	for rows.Next() {
		var a milestoneAttachment
		var storagePath string
		if err := rows.Scan(&a.ID, &a.FileName, &a.OriginalName, &a.MimeType, &a.FileSize, &storagePath, &a.Description, &a.CreatedAt); err != nil {
			continue
		}
		a.URL = "/api/v1/files/" + a.FileName
		attachments = append(attachments, a)
	}

	response.Success(c, attachments)
}

// UploadMilestoneAttachment uploads a file and links it to a milestone.
func (h *Handler) UploadMilestoneAttachment(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	userID, _ := middleware.GetUserID(c)

	milestoneID, err := uuid.Parse(c.Param("milestoneId"))
	if err != nil {
		response.BadRequest(c, "Invalid milestone ID")
		return
	}

	file, header, err := c.Request.FormFile("file")
	if err != nil {
		response.BadRequest(c, "No file provided")
		return
	}
	defer file.Close()

	if header.Size > h.config.Storage.MaxFileSize {
		response.BadRequest(c, fmt.Sprintf("File size exceeds maximum of %d bytes", h.config.Storage.MaxFileSize))
		return
	}

	buffer := make([]byte, 512)
	file.Read(buffer)
	mimeType := http.DetectContentType(buffer)
	file.Seek(0, 0)

	randomBytes := make([]byte, 16)
	rand.Read(randomBytes)
	fileID := hex.EncodeToString(randomBytes)
	ext := filepath.Ext(header.Filename)
	storedName := fileID + ext

	now := time.Now()
	dirPath := filepath.Join(h.config.Storage.LocalPath, "uploads", now.Format("2006"), now.Format("01"))
	os.MkdirAll(dirPath, 0755)
	filePath := filepath.Join(dirPath, storedName)

	dst, err := os.Create(filePath)
	if err != nil {
		response.InternalError(c, "Failed to save file")
		return
	}
	defer dst.Close()

	if _, err = io.Copy(dst, file); err != nil {
		response.InternalError(c, "Failed to write file")
		return
	}

	metaPath := filePath + ".meta"
	metaContent := fmt.Sprintf("%s\n%s\n%d\n%s\n%d", header.Filename, mimeType, header.Size, filePath, now.Unix())
	os.WriteFile(metaPath, []byte(metaContent), 0644)

	description := c.PostForm("description")
	metadata := fmt.Sprintf(`{"description": "%s"}`, strings.ReplaceAll(description, `"`, `'`))

	attachID := uuid.New()
	_, err = h.db.Exec(`
		INSERT INTO attachments (id, tenant_id, uploaded_by, entity_type, entity_id,
			file_name, original_name, mime_type, file_size, storage_path, metadata)
		VALUES ($1, $2, $3, 'milestone', $4, $5, $6, $7, $8, $9, $10::jsonb)
	`, attachID, tenantID, userID, milestoneID, storedName, header.Filename, mimeType, header.Size, filePath, metadata)
	if err != nil {
		h.log.Error("Failed to save attachment record", "error", err)
		response.InternalError(c, "Failed to save attachment")
		return
	}

	response.Created(c, milestoneAttachment{
		ID: attachID, FileName: storedName, OriginalName: header.Filename,
		MimeType: mimeType, FileSize: header.Size, URL: "/api/v1/files/" + storedName,
		Description: description, CreatedAt: now,
	})
}

// DeleteMilestoneAttachment removes a milestone attachment.
func (h *Handler) DeleteMilestoneAttachment(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}

	attachID, err := uuid.Parse(c.Param("attachmentId"))
	if err != nil {
		response.BadRequest(c, "Invalid attachment ID")
		return
	}

	var storagePath string
	h.db.QueryRow(`SELECT storage_path FROM attachments WHERE id = $1 AND tenant_id = $2`, attachID, tenantID).Scan(&storagePath)

	_, err = h.db.Exec(`DELETE FROM attachments WHERE id = $1 AND tenant_id = $2`, attachID, tenantID)
	if err != nil {
		response.InternalError(c, "Failed to delete attachment")
		return
	}

	if storagePath != "" {
		os.Remove(storagePath)
		os.Remove(storagePath + ".meta")
	}

	response.Success(c, gin.H{"message": "Attachment deleted"})
}
