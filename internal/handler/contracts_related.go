package handler

// Contract child resources: amendments (ilova / qo'shimcha kelishuv),
// versioned files, polymorphic links, invoice rollup, linked tasks and
// the activity feed. Core CRUD lives in contracts.go.

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/genixerp/genix-backend/internal/domain/entity"
	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// requireContract loads the contract id from the path and verifies it
// belongs to the tenant. Returns uuid.Nil after writing the response on
// failure.
func (h *Handler) requireContract(c *gin.Context, tenantID uuid.UUID) uuid.UUID {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid contract ID")
		return uuid.Nil
	}
	var exists bool
	if err := h.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM procurement_contracts WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL)`,
		id, tenantID).Scan(&exists); err != nil || !exists {
		response.NotFound(c, "Contract")
		return uuid.Nil
	}
	return id
}

// ── Amendments ──────────────────────────────────────────────────────────

// ListContractAmendments — GET /contracts/:id/amendments
func (h *Handler) ListContractAmendments(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	contractID := h.requireContract(c, tenantID)
	if contractID == uuid.Nil {
		return
	}

	rows, err := h.db.Query(`
		SELECT id, contract_id, number, date, amount_delta, description, file_id, file_name, created_by, created_at
		FROM contract_amendments
		WHERE contract_id = $1 AND tenant_id = $2 AND deleted_at IS NULL
		ORDER BY date, created_at
	`, contractID, tenantID)
	if err != nil {
		h.log.Error("Failed to list amendments", "error", err)
		response.InternalError(c, "Failed to list amendments")
		return
	}
	defer rows.Close()

	items := []entity.ContractAmendment{}
	for rows.Next() {
		var (
			a           entity.ContractAmendment
			amountDelta sql.NullFloat64
			description sql.NullString
			fileID      sql.NullString
			fileName    sql.NullString
			createdBy   sql.NullString
		)
		if err := rows.Scan(&a.ID, &a.ContractID, &a.Number, &a.Date, &amountDelta, &description, &fileID, &fileName, &createdBy, &a.CreatedAt); err != nil {
			h.log.Error("Failed to scan amendment", "error", err)
			response.InternalError(c, "Failed to list amendments")
			return
		}
		if amountDelta.Valid {
			a.AmountDelta = &amountDelta.Float64
		}
		if description.Valid {
			a.Description = &description.String
		}
		if fileID.Valid {
			a.FileID = &fileID.String
		}
		if fileName.Valid {
			a.FileName = &fileName.String
		}
		if createdBy.Valid {
			if id, err := uuid.Parse(createdBy.String); err == nil {
				a.CreatedBy = &id
			}
		}
		items = append(items, a)
	}
	response.Success(c, items)
}

// CreateContractAmendment — POST /contracts/:id/amendments (multipart;
// fields: number, date, amount_delta?, description?, file?)
func (h *Handler) CreateContractAmendment(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	userID, _ := middleware.GetUserID(c)
	contractID := h.requireContract(c, tenantID)
	if contractID == uuid.Nil {
		return
	}

	number := strings.TrimSpace(c.PostForm("number"))
	if number == "" {
		response.BadRequest(c, "Amendment number is required")
		return
	}
	date, err := time.Parse("2006-01-02", c.PostForm("date"))
	if err != nil {
		response.BadRequest(c, "Invalid amendment date")
		return
	}
	var amountDelta *float64
	if raw := strings.TrimSpace(c.PostForm("amount_delta")); raw != "" {
		v, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			response.BadRequest(c, "Invalid amount_delta")
			return
		}
		amountDelta = &v
	}
	var description *string
	if d := c.PostForm("description"); d != "" {
		description = &d
	}

	var fileID, fileName *string
	if file, header, err := c.Request.FormFile("file"); err == nil {
		defer file.Close()
		storedID, _, _, uploadErr := h.persistContractFileBytes(c, file, header.Filename, header.Size, tenantID, userID)
		if uploadErr != nil {
			return // response already written
		}
		fileID, fileName = &storedID, &header.Filename
	}

	id := uuid.New()
	if _, err := h.db.Exec(`
		INSERT INTO contract_amendments (id, tenant_id, contract_id, number, date, amount_delta, description, file_id, file_name, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`, id, tenantID, contractID, number, date, amountDelta, description, fileID, fileName, nullableUUID(userID)); err != nil {
		if strings.Contains(err.Error(), "duplicate key") {
			response.Conflict(c, "An amendment with this number already exists")
			return
		}
		h.log.Error("Failed to create amendment", "error", err)
		response.InternalError(c, "Failed to create amendment")
		return
	}

	h.contractAudit(tenantID, userID, contractID, "amendment_added", nil, map[string]interface{}{
		"number":       number,
		"date":         date.Format("2006-01-02"),
		"amount_delta": amountDelta,
	})

	response.Created(c, gin.H{"id": id, "number": number})
}

// UpdateContractAmendment — PUT /contracts/:id/amendments/:amendmentId (JSON)
func (h *Handler) UpdateContractAmendment(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	userID, _ := middleware.GetUserID(c)
	contractID := h.requireContract(c, tenantID)
	if contractID == uuid.Nil {
		return
	}
	amendmentID, err := uuid.Parse(c.Param("amendmentId"))
	if err != nil {
		response.BadRequest(c, "Invalid amendment ID")
		return
	}

	var input struct {
		Number      *string  `json:"number,omitempty"`
		Date        *string  `json:"date,omitempty"`
		AmountDelta *float64 `json:"amount_delta,omitempty"`
		Description *string  `json:"description,omitempty"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input")
		return
	}

	updates := []string{}
	args := []interface{}{}
	arg := func(field string, v interface{}) {
		args = append(args, v)
		updates = append(updates, fmt.Sprintf("%s = $%d", field, len(args)))
	}
	if input.Number != nil && strings.TrimSpace(*input.Number) != "" {
		arg("number", strings.TrimSpace(*input.Number))
	}
	if input.Date != nil && *input.Date != "" {
		d, err := time.Parse("2006-01-02", *input.Date)
		if err != nil {
			response.BadRequest(c, "Invalid date")
			return
		}
		arg("date", d)
	}
	if input.AmountDelta != nil {
		arg("amount_delta", *input.AmountDelta)
	}
	if input.Description != nil {
		arg("description", *input.Description)
	}
	if len(updates) == 0 {
		response.Success(c, gin.H{"message": "Nothing to update"})
		return
	}

	args = append(args, time.Now())
	updates = append(updates, fmt.Sprintf("updated_at = $%d", len(args)))
	args = append(args, amendmentID, contractID, tenantID)

	res, err := h.db.Exec(fmt.Sprintf(`
		UPDATE contract_amendments SET %s
		WHERE id = $%d AND contract_id = $%d AND tenant_id = $%d AND deleted_at IS NULL
	`, strings.Join(updates, ", "), len(args)-2, len(args)-1, len(args)), args...)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate key") {
			response.Conflict(c, "An amendment with this number already exists")
			return
		}
		h.log.Error("Failed to update amendment", "error", err)
		response.InternalError(c, "Failed to update amendment")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		response.NotFound(c, "Amendment")
		return
	}

	h.contractAudit(tenantID, userID, contractID, "amendment_updated", nil, map[string]interface{}{"amendment_id": amendmentID})
	response.Success(c, gin.H{"message": "Amendment updated"})
}

// DeleteContractAmendment — DELETE /contracts/:id/amendments/:amendmentId
func (h *Handler) DeleteContractAmendment(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	userID, _ := middleware.GetUserID(c)
	contractID := h.requireContract(c, tenantID)
	if contractID == uuid.Nil {
		return
	}
	amendmentID, err := uuid.Parse(c.Param("amendmentId"))
	if err != nil {
		response.BadRequest(c, "Invalid amendment ID")
		return
	}

	res, err := h.db.Exec(`
		UPDATE contract_amendments SET deleted_at = $1, updated_at = $1
		WHERE id = $2 AND contract_id = $3 AND tenant_id = $4 AND deleted_at IS NULL
	`, time.Now(), amendmentID, contractID, tenantID)
	if err != nil {
		h.log.Error("Failed to delete amendment", "error", err)
		response.InternalError(c, "Failed to delete amendment")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		response.NotFound(c, "Amendment")
		return
	}

	h.contractAudit(tenantID, userID, contractID, "amendment_deleted", nil, map[string]interface{}{"amendment_id": amendmentID})
	response.Success(c, gin.H{"message": "Amendment deleted"})
}

// ── Files (versioned, never overwritten) ────────────────────────────────

// ListContractFiles — GET /contracts/:id/files
func (h *Handler) ListContractFiles(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	contractID := h.requireContract(c, tenantID)
	if contractID == uuid.Nil {
		return
	}

	rows, err := h.db.Query(`
		SELECT id, contract_id, version, file_id, original_name, file_size, mime_type,
		       (ai_summary IS NOT NULL), uploaded_by, uploaded_at
		FROM contract_files
		WHERE contract_id = $1 AND tenant_id = $2 AND deleted_at IS NULL
		ORDER BY version DESC
	`, contractID, tenantID)
	if err != nil {
		h.log.Error("Failed to list contract files", "error", err)
		response.InternalError(c, "Failed to list files")
		return
	}
	defer rows.Close()

	items := []entity.ContractFile{}
	for rows.Next() {
		var (
			f          entity.ContractFile
			mimeType   sql.NullString
			uploadedBy sql.NullString
		)
		if err := rows.Scan(&f.ID, &f.ContractID, &f.Version, &f.FileID, &f.OriginalName, &f.FileSize, &mimeType, &f.HasAISummary, &uploadedBy, &f.UploadedAt); err != nil {
			h.log.Error("Failed to scan contract file", "error", err)
			response.InternalError(c, "Failed to list files")
			return
		}
		if mimeType.Valid {
			f.MimeType = &mimeType.String
		}
		if uploadedBy.Valid {
			if id, err := uuid.Parse(uploadedBy.String); err == nil {
				f.UploadedBy = &id
			}
		}
		items = append(items, f)
	}
	response.Success(c, items)
}

// UploadContractFile — POST /contracts/:id/files (multipart "file").
// Each upload becomes a new version; old versions are kept.
func (h *Handler) UploadContractFile(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	userID, _ := middleware.GetUserID(c)
	contractID := h.requireContract(c, tenantID)
	if contractID == uuid.Nil {
		return
	}

	// JSON branch: attach a file already stored via /contracts/ai/extract
	// (avoids a second upload of the same bytes).
	if strings.HasPrefix(c.ContentType(), "application/json") {
		var input struct {
			FileID string `json:"file_id" binding:"required"`
		}
		if err := c.ShouldBindJSON(&input); err != nil {
			response.BadRequest(c, "file_id is required")
			return
		}
		var filename, storedMime string
		var storedSize int64
		err := h.db.QueryRow(`
			SELECT filename, COALESCE(mime_type, ''), size FROM uploaded_files
			WHERE id = $1 AND (tenant_id = $2 OR tenant_id IS NULL)
		`, input.FileID, tenantID).Scan(&filename, &storedMime, &storedSize)
		if err == sql.ErrNoRows {
			response.NotFound(c, "Uploaded file")
			return
		}
		if err != nil {
			response.InternalError(c, "Failed to attach file")
			return
		}
		h.recordContractFileVersion(c, tenantID, userID, contractID, input.FileID, filename, storedMime, storedSize)
		return
	}

	file, header, err := c.Request.FormFile("file")
	if err != nil {
		response.BadRequest(c, "No file provided")
		return
	}
	defer file.Close()

	fileID, mimeType, size, uploadErr := h.persistContractFileBytes(c, file, header.Filename, header.Size, tenantID, userID)
	if uploadErr != nil {
		return // response already written
	}

	h.recordContractFileVersion(c, tenantID, userID, contractID, fileID, header.Filename, mimeType, size)
}

// recordContractFileVersion inserts the next contract_files version row
// and writes the HTTP response.
func (h *Handler) recordContractFileVersion(c *gin.Context, tenantID, userID, contractID uuid.UUID, fileID, filename, mimeType string, size int64) {
	var version int
	if err := h.db.QueryRow(`SELECT COALESCE(MAX(version), 0) + 1 FROM contract_files WHERE contract_id = $1`,
		contractID).Scan(&version); err != nil {
		h.log.Error("Failed to compute file version", "error", err)
		response.InternalError(c, "Failed to upload file")
		return
	}

	id := uuid.New()
	if _, err := h.db.Exec(`
		INSERT INTO contract_files (id, tenant_id, contract_id, version, file_id, original_name, file_size, mime_type, uploaded_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`, id, tenantID, contractID, version, fileID, filename, size, nullIfEmptyStr(mimeType), nullableUUID(userID)); err != nil {
		h.log.Error("Failed to record contract file", "error", err)
		response.InternalError(c, "Failed to upload file")
		return
	}

	h.contractAudit(tenantID, userID, contractID, "file_uploaded", nil, map[string]interface{}{
		"file_name": filename,
		"version":   version,
	})

	mt := mimeType
	response.Created(c, entity.ContractFile{
		ID: id, ContractID: contractID, Version: version, FileID: fileID,
		OriginalName: filename, FileSize: size, MimeType: &mt,
		UploadedAt: time.Now(),
	})
}

// nullIfEmptyStr stores empty strings as SQL NULL.
func nullIfEmptyStr(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

// persistContractFileBytes validates size/type and stores the bytes in
// uploaded_files. Writes the HTTP error response itself on failure.
func (h *Handler) persistContractFileBytes(c *gin.Context, file io.ReadSeeker, filename string, declaredSize int64, tenantID, userID uuid.UUID) (fileID, mimeType string, size int64, err error) {
	if declaredSize > h.config.Storage.MaxFileSize {
		response.Error(c, http.StatusBadRequest, "FILE_TOO_LARGE",
			fmt.Sprintf("File size exceeds maximum allowed size of %d bytes", h.config.Storage.MaxFileSize))
		return "", "", 0, fmt.Errorf("too large")
	}

	buffer := make([]byte, 512)
	n, _ := file.Read(buffer)
	mimeType = http.DetectContentType(buffer[:n])
	file.Seek(0, io.SeekStart)

	ext := strings.ToLower(filepath.Ext(filename))
	if mimeType == "" || strings.HasPrefix(mimeType, "application/octet-stream") || strings.HasPrefix(mimeType, "application/zip") {
		if mapped := getMimeTypeFromExtension(ext); mapped != "" {
			mimeType = mapped
		}
	}
	if !isAllowedExtension(ext) && getMimeTypeFromExtension(ext) == "" {
		response.Error(c, http.StatusBadRequest, "INVALID_FILE_TYPE", "File type is not allowed")
		return "", "", 0, fmt.Errorf("bad type")
	}

	content, readErr := io.ReadAll(file)
	if readErr != nil {
		response.InternalError(c, "Failed to read file")
		return "", "", 0, readErr
	}

	fileID = generateFileID()
	if insertErr := h.insertUploadedFile(fileID, filename, mimeType, int64(len(content)), content, tenantID, userID); insertErr != nil {
		h.log.Error("Failed to store contract file", "error", insertErr)
		response.InternalError(c, "Failed to save file")
		return "", "", 0, insertErr
	}
	return fileID, mimeType, int64(len(content)), nil
}

// DownloadContractFile — GET /contracts/:id/files/:fileId/download
// Tenant-scoped file serving (unlike the public /files/:id route).
func (h *Handler) DownloadContractFile(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	contractID := h.requireContract(c, tenantID)
	if contractID == uuid.Nil {
		return
	}
	cfID, err := uuid.Parse(c.Param("fileId"))
	if err != nil {
		response.BadRequest(c, "Invalid file ID")
		return
	}

	var storedID, originalName string
	err = h.db.QueryRow(`
		SELECT file_id, original_name FROM contract_files
		WHERE id = $1 AND contract_id = $2 AND tenant_id = $3 AND deleted_at IS NULL
	`, cfID, contractID, tenantID).Scan(&storedID, &originalName)
	if err == sql.ErrNoRows {
		response.NotFound(c, "File")
		return
	}
	if err != nil {
		h.log.Error("Failed to look up contract file", "error", err)
		response.InternalError(c, "Failed to download file")
		return
	}

	var mimeType string
	var content []byte
	if err := h.db.QueryRow(`SELECT mime_type, content FROM uploaded_files WHERE id = $1`, storedID).Scan(&mimeType, &content); err != nil {
		h.log.Error("Failed to read stored file", "error", err)
		response.InternalError(c, "Failed to download file")
		return
	}
	if mimeType == "" {
		mimeType = http.DetectContentType(content)
	}
	c.Header("Content-Disposition", fmt.Sprintf("inline; filename=\"%s\"", originalName))
	c.Data(http.StatusOK, mimeType, content)
}

// DeleteContractFile — DELETE /contracts/:id/files/:fileId (soft delete;
// version numbers of remaining files are untouched).
func (h *Handler) DeleteContractFile(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	userID, _ := middleware.GetUserID(c)
	contractID := h.requireContract(c, tenantID)
	if contractID == uuid.Nil {
		return
	}
	cfID, err := uuid.Parse(c.Param("fileId"))
	if err != nil {
		response.BadRequest(c, "Invalid file ID")
		return
	}

	res, err := h.db.Exec(`
		UPDATE contract_files SET deleted_at = $1
		WHERE id = $2 AND contract_id = $3 AND tenant_id = $4 AND deleted_at IS NULL
	`, time.Now(), cfID, contractID, tenantID)
	if err != nil {
		h.log.Error("Failed to delete contract file", "error", err)
		response.InternalError(c, "Failed to delete file")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		response.NotFound(c, "File")
		return
	}

	h.contractAudit(tenantID, userID, contractID, "file_deleted", nil, map[string]interface{}{"file_row_id": cfID})
	response.Success(c, gin.H{"message": "File deleted"})
}

// ── Links (polymorphic) ─────────────────────────────────────────────────

// resolveContractLinkTitle looks up a display name for a linked record.
// Unknown/missing records return ("", false).
func (h *Handler) resolveContractLinkTitle(tenantID uuid.UUID, module, linkedID string) (string, bool) {
	var title sql.NullString
	var err error
	switch module {
	case "crm_deal":
		err = h.db.QueryRow(`SELECT name FROM opportunities WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL`, linkedID, tenantID).Scan(&title)
	case "crm_lead":
		err = h.db.QueryRow(`
			SELECT contact_name || COALESCE(' · ' || NULLIF(company_name, ''), '')
			FROM leads WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL`, linkedID, tenantID).Scan(&title)
	case "construction_object":
		intID, convErr := strconv.ParseInt(linkedID, 10, 64)
		if convErr != nil {
			return "", false
		}
		err = h.db.QueryRow(`SELECT name FROM construction_projects WHERE id = $1 AND tenant_id = $2`, intID, tenantID).Scan(&title)
	case "purchase_order":
		err = h.db.QueryRow(`SELECT order_number FROM purchase_orders WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL`, linkedID, tenantID).Scan(&title)
	case "sale_order":
		err = h.db.QueryRow(`SELECT order_number FROM sales_orders WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL`, linkedID, tenantID).Scan(&title)
	default:
		return "", false
	}
	if err != nil {
		return "", false
	}
	return title.String, true
}

// ListContractLinks — GET /contracts/:id/links
func (h *Handler) ListContractLinks(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	contractID := h.requireContract(c, tenantID)
	if contractID == uuid.Nil {
		return
	}

	rows, err := h.db.Query(`
		SELECT id, contract_id, linked_module, linked_id, created_at
		FROM contract_links WHERE contract_id = $1 AND tenant_id = $2
		ORDER BY created_at
	`, contractID, tenantID)
	if err != nil {
		h.log.Error("Failed to list contract links", "error", err)
		response.InternalError(c, "Failed to list links")
		return
	}
	defer rows.Close()

	items := []entity.ContractLink{}
	for rows.Next() {
		var l entity.ContractLink
		if err := rows.Scan(&l.ID, &l.ContractID, &l.LinkedModule, &l.LinkedID, &l.CreatedAt); err != nil {
			response.InternalError(c, "Failed to list links")
			return
		}
		if title, found := h.resolveContractLinkTitle(tenantID, l.LinkedModule, l.LinkedID); found {
			l.LinkedTitle = title
		}
		items = append(items, l)
	}
	response.Success(c, items)
}

// CreateContractLink — POST /contracts/:id/links {"linked_module","linked_id"}
func (h *Handler) CreateContractLink(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	userID, _ := middleware.GetUserID(c)
	contractID := h.requireContract(c, tenantID)
	if contractID == uuid.Nil {
		return
	}

	var input struct {
		LinkedModule string `json:"linked_module" binding:"required"`
		LinkedID     string `json:"linked_id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid input")
		return
	}

	title, found := h.resolveContractLinkTitle(tenantID, input.LinkedModule, input.LinkedID)
	if !found {
		response.NotFound(c, "Linked record")
		return
	}

	id := uuid.New()
	if _, err := h.db.Exec(`
		INSERT INTO contract_links (id, tenant_id, contract_id, linked_module, linked_id, created_by)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, id, tenantID, contractID, input.LinkedModule, input.LinkedID, nullableUUID(userID)); err != nil {
		if strings.Contains(err.Error(), "duplicate key") {
			response.Conflict(c, "This record is already linked")
			return
		}
		h.log.Error("Failed to create contract link", "error", err)
		response.InternalError(c, "Failed to create link")
		return
	}

	h.contractAudit(tenantID, userID, contractID, "link_added", nil, map[string]interface{}{
		"linked_module": input.LinkedModule,
		"linked_id":     input.LinkedID,
		"linked_title":  title,
	})

	response.Created(c, entity.ContractLink{
		ID: id, ContractID: contractID, LinkedModule: input.LinkedModule,
		LinkedID: input.LinkedID, LinkedTitle: title, CreatedAt: time.Now(),
	})
}

// DeleteContractLink — DELETE /contracts/:id/links/:linkId
func (h *Handler) DeleteContractLink(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	userID, _ := middleware.GetUserID(c)
	contractID := h.requireContract(c, tenantID)
	if contractID == uuid.Nil {
		return
	}
	linkID, err := uuid.Parse(c.Param("linkId"))
	if err != nil {
		response.BadRequest(c, "Invalid link ID")
		return
	}

	res, err := h.db.Exec(`DELETE FROM contract_links WHERE id = $1 AND contract_id = $2 AND tenant_id = $3`,
		linkID, contractID, tenantID)
	if err != nil {
		h.log.Error("Failed to delete contract link", "error", err)
		response.InternalError(c, "Failed to delete link")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		response.NotFound(c, "Link")
		return
	}

	h.contractAudit(tenantID, userID, contractID, "link_removed", nil, map[string]interface{}{"link_id": linkID})
	response.Success(c, gin.H{"message": "Link removed"})
}

// ── Invoices / payments rollup ──────────────────────────────────────────

// ListContractInvoices — GET /contracts/:id/invoices
// Returns sales + purchase invoices referencing this contract with the
// paid/outstanding rollup.
func (h *Handler) ListContractInvoices(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	contractID := h.requireContract(c, tenantID)
	if contractID == uuid.Nil {
		return
	}

	rows, err := h.db.Query(`
		SELECT id, 'sales', invoice_number, invoice_date, due_date,
		       COALESCE(total_amount, 0), COALESCE(amount_paid, 0), COALESCE(amount_due, 0), status
		FROM sales_invoices
		WHERE contract_id = $1 AND tenant_id = $2 AND deleted_at IS NULL
		UNION ALL
		SELECT id, 'purchase', invoice_number, invoice_date, due_date,
		       COALESCE(total_amount, 0), COALESCE(amount_paid, 0), COALESCE(amount_due, 0), status
		FROM purchase_invoices
		WHERE contract_id = $1 AND tenant_id = $2 AND deleted_at IS NULL
		ORDER BY 4 DESC
	`, contractID, tenantID)
	if err != nil {
		h.log.Error("Failed to list contract invoices", "error", err)
		response.InternalError(c, "Failed to list invoices")
		return
	}
	defer rows.Close()

	items := []entity.ContractInvoiceRow{}
	var invoicedTotal, paidTotal float64
	for rows.Next() {
		var r entity.ContractInvoiceRow
		if err := rows.Scan(&r.ID, &r.Kind, &r.InvoiceNumber, &r.InvoiceDate, &r.DueDate, &r.TotalAmount, &r.AmountPaid, &r.AmountDue, &r.Status); err != nil {
			response.InternalError(c, "Failed to list invoices")
			return
		}
		if r.Status != "cancelled" {
			invoicedTotal += r.TotalAmount
			paidTotal += r.AmountPaid
		}
		items = append(items, r)
	}

	var effective float64
	h.db.QueryRow(`
		SELECT COALESCE(c.value, 0) + COALESCE((
			SELECT SUM(a.amount_delta) FROM contract_amendments a
			WHERE a.contract_id = c.id AND a.deleted_at IS NULL
		), 0)
		FROM procurement_contracts c WHERE c.id = $1 AND c.tenant_id = $2
	`, contractID, tenantID).Scan(&effective)

	response.Success(c, gin.H{
		"invoices":         items,
		"invoiced_total":   invoicedTotal,
		"paid_total":       paidTotal,
		"effective_amount": effective,
		"outstanding":      effective - paidTotal,
	})
}

// AttachContractInvoice — POST /contracts/:id/invoices
// Links (or with detach=true unlinks) an existing sales/purchase invoice
// to this contract ("shartnoma asosida" retrofit for already-issued docs).
func (h *Handler) AttachContractInvoice(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	userID, _ := middleware.GetUserID(c)
	contractID := h.requireContract(c, tenantID)
	if contractID == uuid.Nil {
		return
	}

	var input struct {
		InvoiceID string `json:"invoice_id" binding:"required"`
		Kind      string `json:"kind" binding:"required"` // sales | purchase
		Detach    bool   `json:"detach,omitempty"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "invoice_id and kind are required")
		return
	}
	invoiceID, err := uuid.Parse(input.InvoiceID)
	if err != nil {
		response.BadRequest(c, "Invalid invoice ID")
		return
	}

	table := ""
	switch input.Kind {
	case "sales":
		table = "sales_invoices"
	case "purchase":
		table = "purchase_invoices"
	default:
		response.BadRequest(c, "kind must be 'sales' or 'purchase'")
		return
	}

	var target interface{}
	action := "invoice_detached"
	if !input.Detach {
		target = contractID
		action = "invoice_attached"
	}
	res, err := h.db.Exec(
		fmt.Sprintf(`UPDATE %s SET contract_id = $1, updated_at = NOW() WHERE id = $2 AND tenant_id = $3 AND deleted_at IS NULL`, table),
		target, invoiceID, tenantID)
	if err != nil {
		h.log.Error("Failed to link invoice to contract", "error", err)
		response.InternalError(c, "Failed to link invoice")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		response.NotFound(c, "Invoice")
		return
	}

	h.contractAudit(tenantID, userID, contractID, action, nil, map[string]interface{}{
		"invoice_id": invoiceID, "kind": input.Kind,
	})
	response.Success(c, gin.H{"message": "OK"})
}

// ── Linked tasks (Vazifalar via task_links) ─────────────────────────────

// ListContractTasks — GET /contracts/:id/tasks
func (h *Handler) ListContractTasks(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	contractID := h.requireContract(c, tenantID)
	if contractID == uuid.Nil {
		return
	}

	rows, err := h.db.Query(`
		SELECT t.id, t.board_id, b.name, t.title, t.priority, t.due_date, t.completed_at, col.name
		FROM task_links tl
		JOIN tasks t ON t.id = tl.task_id AND t.tenant_id = tl.tenant_id
		JOIN task_boards b ON b.id = t.board_id
		JOIN task_columns col ON col.id = t.column_id
		WHERE tl.tenant_id = $1 AND tl.linked_module = 'contract' AND tl.linked_id = $2
		  AND t.archived_at IS NULL
		ORDER BY t.created_at DESC
	`, tenantID, contractID.String())
	if err != nil {
		h.log.Error("Failed to list contract tasks", "error", err)
		response.InternalError(c, "Failed to list tasks")
		return
	}
	defer rows.Close()

	type taskRow struct {
		ID          uuid.UUID  `json:"id"`
		BoardID     uuid.UUID  `json:"board_id"`
		BoardName   string     `json:"board_name"`
		Title       string     `json:"title"`
		Priority    string     `json:"priority"`
		DueDate     *time.Time `json:"due_date,omitempty"`
		CompletedAt *time.Time `json:"completed_at,omitempty"`
		ColumnName  string     `json:"column_name"`
	}
	items := []taskRow{}
	for rows.Next() {
		var (
			t           taskRow
			dueDate     sql.NullTime
			completedAt sql.NullTime
		)
		if err := rows.Scan(&t.ID, &t.BoardID, &t.BoardName, &t.Title, &t.Priority, &dueDate, &completedAt, &t.ColumnName); err != nil {
			response.InternalError(c, "Failed to list tasks")
			return
		}
		if dueDate.Valid {
			d := dueDate.Time
			t.DueDate = &d
		}
		if completedAt.Valid {
			d := completedAt.Time
			t.CompletedAt = &d
		}
		items = append(items, t)
	}
	response.Success(c, items)
}

// ── Activity feed ───────────────────────────────────────────────────────

// GetContractActivity — GET /contracts/:id/activity
func (h *Handler) GetContractActivity(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	contractID := h.requireContract(c, tenantID)
	if contractID == uuid.Nil {
		return
	}

	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	if limit < 1 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	rows, err := h.db.Query(`
		SELECT al.id, al.action, al.old_values, al.new_values, al.created_at,
		       TRIM(COALESCE(u.first_name, '') || ' ' || COALESCE(u.last_name, ''))
		FROM audit_logs al
		LEFT JOIN users u ON u.id = al.user_id
		WHERE al.tenant_id = $1 AND al.entity_type = 'contract' AND al.entity_id = $2
		ORDER BY al.created_at DESC
		LIMIT $3
	`, tenantID, contractID, limit)
	if err != nil {
		h.log.Error("Failed to load contract activity", "error", err)
		response.InternalError(c, "Failed to load activity")
		return
	}
	defer rows.Close()

	type activityRow struct {
		ID        uuid.UUID       `json:"id"`
		Action    string          `json:"action"`
		OldValues json.RawMessage `json:"old_values,omitempty" swaggertype:"object"`
		NewValues json.RawMessage `json:"new_values,omitempty" swaggertype:"object"`
		UserName  string          `json:"user_name"`
		CreatedAt time.Time       `json:"created_at"`
	}
	items := []activityRow{}
	for rows.Next() {
		var (
			a         activityRow
			oldValues []byte
			newValues []byte
		)
		if err := rows.Scan(&a.ID, &a.Action, &oldValues, &newValues, &a.CreatedAt, &a.UserName); err != nil {
			response.InternalError(c, "Failed to load activity")
			return
		}
		a.OldValues = oldValues
		a.NewValues = newValues
		items = append(items, a)
	}
	response.Success(c, items)
}
