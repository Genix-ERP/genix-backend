package handler

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// System admin constants for lead creation
const (
	systemTenantID = "a594a3aa-ee4d-4cdd-bf69-56fc4812bce6"
	systemOrgID    = "5b011a44-4b28-460c-9c3f-a637fb8573bf"
	systemAdminID  = "657d6aa9-913b-474d-9ef1-ba68de760feb"
)

type ContactSubmissionInput struct {
	FullName string `json:"full_name"`
	Phone    string `json:"phone"`
	Email    string `json:"email"`
	Company  string `json:"company"`
}

func (h *Handler) SubmitContactForm(c *gin.Context) {
	var input ContactSubmissionInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body"})
		return
	}

	fullName := strings.TrimSpace(input.FullName)
	phone := strings.TrimSpace(input.Phone)
	email := strings.TrimSpace(input.Email)
	company := strings.TrimSpace(input.Company)

	// Validate required fields
	if fullName == "" || phone == "" || company == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Full name, phone, and company are required"})
		return
	}

	tx, err := h.db.Begin()
	if err != nil {
		h.log.Error("Failed to begin transaction", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to submit contact form"})
		return
	}
	defer tx.Rollback()

	// Save contact submission
	_, err = tx.Exec(`
		INSERT INTO contact_submissions (full_name, phone, email, company)
		VALUES ($1, $2, $3, $4)
	`, fullName, phone, email, company)
	if err != nil {
		h.log.Error("Failed to save contact submission", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to submit contact form"})
		return
	}

	// Create CRM lead for system admin
	if email == "" {
		email = phone + "@website.lead"
	}
	_, err = tx.Exec(`
		INSERT INTO leads (tenant_id, organization_id, contact_name, company_name, email, phone, status, source, notes, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, 'new', 'website', 'Submitted via website contact form', $7)
	`, systemTenantID, systemOrgID, fullName, company, email, phone, systemAdminID)
	if err != nil {
		h.log.Error("Failed to create lead from contact form", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to submit contact form"})
		return
	}

	if err = tx.Commit(); err != nil {
		h.log.Error("Failed to commit transaction", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to submit contact form"})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"message": "Contact form submitted successfully"})
}
