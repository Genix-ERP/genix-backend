package listparams

import (
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

// Params holds common list query params: page, page_size, sort, order, search
type Params struct {
	Page     int
	PageSize int
	Sort     string
	Order    string // "asc" or "desc"
	Search   string
}

// Parse reads page/page_size/sort/order/search from query string.
// `allowedSort` is a whitelist; if the requested sort is not in it, defaultSort is used.
// `defaultSort` is the column to sort by when none is specified.
func Parse(c *gin.Context, allowedSort []string, defaultSort string) Params {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 10000 {
		pageSize = 20
	}

	sort := c.DefaultQuery("sort", defaultSort)
	// Whitelist check — reject unknown columns to prevent SQL injection
	allowed := false
	for _, s := range allowedSort {
		if s == sort {
			allowed = true
			break
		}
	}
	if !allowed {
		sort = defaultSort
	}

	order := strings.ToLower(c.DefaultQuery("order", "desc"))
	if order != "asc" && order != "desc" {
		order = "desc"
	}

	return Params{
		Page:     page,
		PageSize: pageSize,
		Sort:     sort,
		Order:    order,
		Search:   strings.TrimSpace(c.Query("search")),
	}
}

// Offset returns SQL OFFSET value
func (p Params) Offset() int {
	return (p.Page - 1) * p.PageSize
}

// OrderClause returns " ORDER BY <sort> <order>"
// Use with table alias prefix via prefix arg (e.g. "so.") or empty for no prefix.
func (p Params) OrderClause(prefix string) string {
	return " ORDER BY " + prefix + p.Sort + " " + strings.ToUpper(p.Order)
}
