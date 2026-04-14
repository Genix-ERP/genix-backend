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

	"github.com/genixerp/genix-backend/internal/pkg/response"
	"github.com/gin-gonic/gin"
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

// UploadFile handles file upload
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

	// Detect MIME type
	buffer := make([]byte, 512)
	_, err = file.Read(buffer)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "READ_ERROR", "Failed to read file")
		return
	}
	mimeType := http.DetectContentType(buffer)

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

	// Generate unique file ID
	fileID := generateFileID()

	// Get file extension
	ext := filepath.Ext(header.Filename)
	if ext == "" {
		// Try to determine extension from MIME type
		ext = getExtensionFromMimeType(mimeType)
	}

	// Create storage directory structure: storage/uploads/YYYY/MM/
	now := time.Now()
	subDir := fmt.Sprintf("%d/%02d", now.Year(), now.Month())
	storageDir := filepath.Join(h.config.Storage.LocalPath, "uploads", subDir)

	// Create directory if it doesn't exist
	if err := os.MkdirAll(storageDir, 0755); err != nil {
		h.log.Error("Failed to create storage directory", "error", err)
		response.Error(c, http.StatusInternalServerError, "STORAGE_ERROR", "Failed to create storage directory")
		return
	}

	// Full filename with ID and extension
	storedFilename := fileID + ext
	filePath := filepath.Join(storageDir, storedFilename)

	// Create the destination file
	dst, err := os.Create(filePath)
	if err != nil {
		h.log.Error("Failed to create file", "error", err)
		response.Error(c, http.StatusInternalServerError, "STORAGE_ERROR", "Failed to save file")
		return
	}
	defer dst.Close()

	// Copy the uploaded file to destination
	if _, err := io.Copy(dst, file); err != nil {
		h.log.Error("Failed to write file", "error", err)
		response.Error(c, http.StatusInternalServerError, "STORAGE_ERROR", "Failed to write file")
		return
	}

	// Generate the URL for the file
	// URL format: /api/v1/files/{id}
	fileURL := fmt.Sprintf("/api/v1/files/%s", fileID)

	// Also store file metadata in a simple way (could be moved to DB later)
	metadataPath := filepath.Join(storageDir, fileID+".meta")
	metadata := fmt.Sprintf("%s\n%s\n%d\n%s\n%s",
		header.Filename,
		mimeType,
		header.Size,
		filePath,
		time.Now().Format(time.RFC3339),
	)
	os.WriteFile(metadataPath, []byte(metadata), 0644)

	resp := FileUploadResponse{
		ID:        fileID,
		URL:       fileURL,
		Filename:  header.Filename,
		Size:      header.Size,
		MimeType:  mimeType,
		CreatedAt: time.Now().Format(time.RFC3339),
	}

	h.log.Info("File uploaded successfully", "id", fileID, "filename", header.Filename, "size", header.Size)
	response.Created(c, resp)
}

// GetFile serves a file by ID
func (h *Handler) GetFile(c *gin.Context) {
	fileID := c.Param("id")
	if fileID == "" {
		response.NotFound(c, "File")
		return
	}

	// Search for the file in storage directories
	// Files are stored in: storage/uploads/YYYY/MM/
	storageBase := filepath.Join(h.config.Storage.LocalPath, "uploads")

	var filePath string
	var originalFilename string
	var mimeType string

	// Walk through the storage directory to find the file
	err := filepath.Walk(storageBase, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // Skip errors
		}
		if info.IsDir() {
			return nil
		}
		// Check if this is a metadata file for our file ID
		if strings.HasSuffix(info.Name(), ".meta") && strings.HasPrefix(info.Name(), fileID) {
			// Read metadata
			data, err := os.ReadFile(path)
			if err != nil {
				return nil
			}
			lines := strings.Split(string(data), "\n")
			if len(lines) >= 4 {
				originalFilename = lines[0]
				mimeType = lines[1]
				filePath = lines[3]
			}
			return filepath.SkipAll // Found it, stop searching
		}
		return nil
	})

	if err != nil || filePath == "" {
		// Try to find file directly by pattern
		err = filepath.Walk(storageBase, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if info.IsDir() {
				return nil
			}
			// Check if filename starts with our file ID (excluding .meta files)
			if strings.HasPrefix(info.Name(), fileID) && !strings.HasSuffix(info.Name(), ".meta") {
				filePath = path
				originalFilename = info.Name()
				return filepath.SkipAll
			}
			return nil
		})
	}

	if filePath == "" {
		response.NotFound(c, "File")
		return
	}

	// Check if file exists
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		response.NotFound(c, "File")
		return
	}

	// Detect content type if not found in metadata
	if mimeType == "" {
		file, err := os.Open(filePath)
		if err != nil {
			response.Error(c, http.StatusInternalServerError, "READ_ERROR", "Failed to read file")
			return
		}
		defer file.Close()

		buffer := make([]byte, 512)
		file.Read(buffer)
		mimeType = http.DetectContentType(buffer)
	}

	// Set headers
	c.Header("Content-Type", mimeType)
	if originalFilename != "" {
		c.Header("Content-Disposition", fmt.Sprintf("inline; filename=\"%s\"", originalFilename))
	}

	// Serve the file
	c.File(filePath)
}

// DeleteFile deletes a file by ID
func (h *Handler) DeleteFile(c *gin.Context) {
	fileID := c.Param("id")
	if fileID == "" {
		response.NotFound(c, "File")
		return
	}

	// Search for the file in storage directories
	storageBase := filepath.Join(h.config.Storage.LocalPath, "uploads")
	var found bool

	filepath.Walk(storageBase, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if strings.HasPrefix(info.Name(), fileID) {
			os.Remove(path)
			found = true
		}
		return nil
	})

	if !found {
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

// getExtensionFromMimeType returns file extension based on MIME type
func getExtensionFromMimeType(mimeType string) string {
	mimeToExt := map[string]string{
		"image/jpeg":      ".jpg",
		"image/png":       ".png",
		"image/gif":       ".gif",
		"image/webp":      ".webp",
		"image/svg+xml":   ".svg",
		"application/pdf": ".pdf",
		"text/plain":      ".txt",
		"text/html":       ".html",
		"text/css":        ".css",
		"application/json": ".json",
		"application/xml":  ".xml",
		"application/zip":  ".zip",
		"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet": ".xlsx",
		"application/vnd.ms-excel": ".xls",
		"application/msword":       ".doc",
		"application/vnd.openxmlformats-officedocument.wordprocessingml.document": ".docx",
	}

	for prefix, ext := range mimeToExt {
		if strings.HasPrefix(mimeType, prefix) {
			return ext
		}
	}
	return ""
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
