package handler

import (
	"strconv"

	"github.com/gin-gonic/gin"
)

// optPagination reads OPT-IN pagination params shared by the list endpoints.
// When the client sends none of `page`, `page_size` or `limit`, paginate is
// false and the caller must return the full list exactly as before (so existing
// web callers are unaffected). Mobile sends page+page_size to page through.
//
// `limit` is accepted as an alias for `page_size`. Without it there is a trap:
// a client asking for `?limit=50` sent no recognised param, so paginate stayed
// false and it silently received the ENTIRE table instead of 50 rows — the
// opposite of what it asked for, and worst exactly on the biggest tenants. No
// function calling this helper reads `limit` on its own, so the alias cannot
// collide with a caller's own meaning for it.
//
//	paginate, page, pageSize, offset := optPagination(c)
//	if paginate { query += " LIMIT $N OFFSET $M"; args = append(args, pageSize, offset) }
//	...
//	if !paginate { response.Success(c, items); return }
//	// count total over the same WHERE, then:
//	response.Paginated(c, items, page, pageSize, total)
//
// pageSize defaults to 20 and is capped at 100 to keep the phone light.
func optPagination(c *gin.Context) (paginate bool, page, pageSize, offset int) {
	pageStr := c.Query("page")
	sizeStr := c.Query("page_size")
	if sizeStr == "" {
		sizeStr = c.Query("limit")
	}
	paginate = pageStr != "" || sizeStr != ""
	page, _ = strconv.Atoi(pageStr)
	pageSize, _ = strconv.Atoi(sizeStr)
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}
	offset = (page - 1) * pageSize
	return
}
