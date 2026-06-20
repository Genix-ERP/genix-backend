package handler

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/genixerp/genix-backend/internal/middleware"
	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// FileUploadResponse represents the response after file upload
type FileUploadResponse struct {
	ID        string `json:"id"`
	URL       string `json:"url"`
	Filename  string `json:"filename"`
	Size      int64  `json:"size"`
	MimeType  string `json:"mime_type"`
	CreatedAt string `json:"created_at"`
}

// insertUploadedFile persists an uploaded file's bytes in the database. It is
// shared by every upload path (generic /files/upload, work-order attachments,
// etc.) so that file storage is centralized in Postgres rather than on disk.
// tenantID / userID may be uuid.Nil, in which case they are stored as NULL.
func (h *Handler) insertUploadedFile(id, filename, mimeType string, size int64, content []byte, tenantID, userID uuid.UUID) error {
	_, err := h.db.Exec(`
		INSERT INTO uploaded_files (id, filename, mime_type, size, content, tenant_id, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, id, filename, mimeType, size, content, nullableUUID(tenantID), nullableUUID(userID))
	return err
}

// nullableUUID returns nil for the zero UUID so it is stored as SQL NULL.
func nullableUUID(id uuid.UUID) interface{} {
	if id == uuid.Nil {
		return nil
	}
	return id
}

// UploadFile handles file upload, storing the bytes in the database.
func (h *Handler) UploadFile(c *gin.Context) {
	// Get the file from the request
	file, header, err := c.Request.FormFile("file")
	if err != nil {
		response.Error(c, http.StatusBadRequest, "FILE_REQUIRED", "No file provided")
		return
	}
	defer file.Close()

	// Validate file size
	if header.Size > h.config.Storage.MaxFileSize {
		response.Error(c, http.StatusBadRequest, "FILE_TOO_LARGE",
			fmt.Sprintf("File size exceeds maximum allowed size of %d bytes", h.config.Storage.MaxFileSize))
		return
	}

	// Detect MIME type from the first 512 bytes
	buffer := make([]byte, 512)
	n, _ := file.Read(buffer)
	mimeType := http.DetectContentType(buffer[:n])

	// Reset file reader position
	file.Seek(0, io.SeekStart)

	// If detection is too generic, try to resolve from file extension
	if mimeType == "" || strings.HasPrefix(mimeType, "application/octet-stream") || strings.HasPrefix(mimeType, "application/zip") {
		if ext := strings.ToLower(filepath.Ext(header.Filename)); ext != "" {
			if mapped := getMimeTypeFromExtension(ext); mapped != "" {
				mimeType = mapped
			}
		}
	}

	// Validate MIME type if restrictions are set
	if len(h.config.Storage.AllowedMimeTypes) > 0 {
		allowed := false
		for _, allowedType := range h.config.Storage.AllowedMimeTypes {
			if strings.HasPrefix(mimeType, allowedType) {
				allowed = true
				break
			}
		}
		// Fall back: allow if the file extension is in the known-safe list
		if !allowed {
			if ext := strings.ToLower(filepath.Ext(header.Filename)); ext != "" {
				if isAllowedExtension(ext) {
					allowed = true
				}
			}
		}
		if !allowed {
			response.Error(c, http.StatusBadRequest, "INVALID_FILE_TYPE",
				fmt.Sprintf("File type %s is not allowed", mimeType))
			return
		}
	}

	// Read the full content into memory for database storage
	content, err := io.ReadAll(file)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "READ_ERROR", "Failed to read file")
		return
	}

	// Generate unique file ID (also serves as the public capability token)
	fileID := generateFileID()

	// Ownership is optional but normally present (upload requires auth)
	tenantID, _ := middleware.GetTenantID(c)
	userID, _ := middleware.GetUserID(c)

	if err := h.insertUploadedFile(fileID, header.Filename, mimeType, int64(len(content)), content, tenantID, userID); err != nil {
		h.log.Error("Failed to store uploaded file", "error", err)
		response.Error(c, http.StatusInternalServerError, "STORAGE_ERROR", "Failed to save file")
		return
	}

	resp := FileUploadResponse{
		ID:        fileID,
		URL:       fmt.Sprintf("/api/v1/files/%s", fileID),
		Filename:  header.Filename,
		Size:      int64(len(content)),
		MimeType:  mimeType,
		CreatedAt: time.Now().Format(time.RFC3339),
	}

	h.log.Info("File uploaded successfully", "id", fileID, "filename", header.Filename, "size", len(content))
	response.Created(c, resp)
}

// GetFile serves a file by ID from the database.
func (h *Handler) GetFile(c *gin.Context) {
	fileID := c.Param("id")
	if fileID == "" {
		response.NotFound(c, "File")
		return
	}

	var (
		filename string
		mimeType string
		content  []byte
	)
	err := h.db.QueryRow(`
		SELECT filename, mime_type, content FROM uploaded_files WHERE id = $1
	`, fileID).Scan(&filename, &mimeType, &content)
	if err == sql.ErrNoRows {
		response.NotFound(c, "File")
		return
	}
	if err != nil {
		h.log.Error("Failed to read uploaded file", "error", err, "id", fileID)
		response.Error(c, http.StatusInternalServerError, "READ_ERROR", "Failed to read file")
		return
	}

	if mimeType == "" {
		mimeType = http.DetectContentType(content)
	}

	if filename != "" {
		c.Header("Content-Disposition", fmt.Sprintf("inline; filename=\"%s\"", filename))
	}
	// Files are immutable (content is addressed by a random id), so allow the
	// browser to cache them aggressively — important for logos/favicons.
	c.Header("Cache-Control", "public, max-age=31536000, immutable")
	c.Data(http.StatusOK, mimeType, content)
}

// DeleteFile deletes a file by ID from the database.
func (h *Handler) DeleteFile(c *gin.Context) {
	fileID := c.Param("id")
	if fileID == "" {
		response.NotFound(c, "File")
		return
	}

	res, err := h.db.Exec(`DELETE FROM uploaded_files WHERE id = $1`, fileID)
	if err != nil {
		h.log.Error("Failed to delete uploaded file", "error", err, "id", fileID)
		response.Error(c, http.StatusInternalServerError, "STORAGE_ERROR", "Failed to delete file")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		response.NotFound(c, "File")
		return
	}

	response.NoContent(c)
}

// generateFileID generates a unique file ID
func generateFileID() string {
	bytes := make([]byte, 16)
	rand.Read(bytes)
	return hex.EncodeToString(bytes)
}

// getMimeTypeFromExtension returns the canonical MIME type for a given file extension.
// Used as a fallback when http.DetectContentType returns a generic type like
// application/octet-stream (e.g. for .xls OLE2 files).
func getMimeTypeFromExtension(ext string) string {
	extToMime := map[string]string{
		".jpg":  "image/jpeg",
		".jpeg": "image/jpeg",
		".png":  "image/png",
		".gif":  "image/gif",
		".webp": "image/webp",
		".svg":  "image/svg+xml",
		".pdf":  "application/pdf",
		".txt":  "text/plain",
		".csv":  "text/csv",
		".html": "text/html",
		".json": "application/json",
		".xml":  "application/xml",
		".zip":  "application/zip",
		".rar":  "application/vnd.rar",
		".7z":   "application/x-7z-compressed",
		".doc":  "application/msword",
		".docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		".xls":  "application/vnd.ms-excel",
		".xlsx": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
		".ppt":  "application/vnd.ms-powerpoint",
		".pptx": "application/vnd.openxmlformats-officedocument.presentationml.presentation",
	}
	return extToMime[ext]
}

// isAllowedExtension returns true when the file extension is in the allow-list
// of known-safe document/image/archive formats. This acts as a safety net when
// MIME detection fails or returns a generic content type.
func isAllowedExtension(ext string) bool {
	allowed := map[string]bool{
		".jpg": true, ".jpeg": true, ".png": true, ".gif": true, ".webp": true,
		".pdf": true, ".txt": true, ".csv": true,
		".doc": true, ".docx": true,
		".xls": true, ".xlsx": true,
		".ppt": true, ".pptx": true,
		".zip": true, ".rar": true, ".7z": true,
	}
	return allowed[ext]
}
