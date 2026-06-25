package handler

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// contractAttachment is the API shape for an uploaded contract document.
type contractAttachment struct {
	ID           uuid.UUID `json:"id"`
	FileName     string    `json:"file_name"`
	OriginalName string    `json:"original_name"`
	MimeType     string    `json:"mime_type"`
	FileSize     int64     `json:"file_size"`
	URL          string    `json:"url"`
	Description  string    `json:"description"`
	CreatedAt    time.Time `json:"created_at"`
}

// ListContractAttachments returns the documents attached to a contract.
func (h *Handler) ListContractAttachments(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	contractID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid contract ID")
		return
	}

	rows, err := h.db.Query(`
		SELECT id, file_name, original_name, mime_type, file_size,
		       COALESCE(metadata->>'description', '') as description, created_at
		FROM attachments
		WHERE tenant_id = $1 AND entity_type = 'contract' AND entity_id = $2
		ORDER BY created_at DESC`, tenantID, contractID)
	if err != nil {
		response.InternalError(c, "Failed to list attachments")
		return
	}
	defer rows.Close()

	attachments := []contractAttachment{}
	for rows.Next() {
		var a contractAttachment
		if err := rows.Scan(&a.ID, &a.FileName, &a.OriginalName, &a.MimeType, &a.FileSize, &a.Description, &a.CreatedAt); err != nil {
			continue
		}
		a.URL = "/api/v1/files/" + a.FileName
		attachments = append(attachments, a)
	}
	response.Success(c, attachments)
}

// UploadContractAttachment stores a document and links it to a contract.
func (h *Handler) UploadContractAttachment(c *gin.Context) {
	tenantID, ok := middleware.GetTenantID(c)
	if !ok || tenantID == uuid.Nil {
		response.Unauthorized(c, "Tenant not found")
		return
	}
	userID, _ := middleware.GetUserID(c)

	contractID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid contract ID")
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
		VALUES ($1, $2, $3, 'contract', $4, $5, $6, $7, $8, $9, $10::jsonb)
	`, attachID, tenantID, userID, contractID, storedName, header.Filename, mimeType, header.Size, filePath, metadata)
	if err != nil {
		h.log.Error("Failed to save contract attachment record", "error", err)
		response.InternalError(c, "Failed to save attachment")
		return
	}

	// Keep the most recent document as the contract's primary document_url.
	h.db.Exec(`UPDATE procurement_contracts SET document_url = $1, updated_at = NOW() WHERE id = $2 AND tenant_id = $3`,
		"/api/v1/files/"+storedName, contractID, tenantID)

	response.Created(c, contractAttachment{
		ID: attachID, FileName: storedName, OriginalName: header.Filename,
		MimeType: mimeType, FileSize: header.Size, URL: "/api/v1/files/" + storedName,
		Description: description, CreatedAt: now,
	})
}

// DeleteContractAttachment removes a contract document.
func (h *Handler) DeleteContractAttachment(c *gin.Context) {
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

	if _, err = h.db.Exec(`DELETE FROM attachments WHERE id = $1 AND tenant_id = $2`, attachID, tenantID); err != nil {
		response.InternalError(c, "Failed to delete attachment")
		return
	}

	if storagePath != "" {
		os.Remove(storagePath)
		os.Remove(storagePath + ".meta")
	}
	response.Success(c, gin.H{"message": "Attachment deleted"})
}
