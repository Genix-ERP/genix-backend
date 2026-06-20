package handler

import (
	"strconv"

	"github.com/gin-gonic/gin"
)

// optPagination reads OPT-IN pagination params shared by the construction list
// endpoints. When the client sends neither `page` nor `page_size`, paginate is
// false and the caller must return the full list exactly as before (so existing
// web callers are unaffected). Mobile sends page+page_size to page through.
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
	paginate = pageStr != "" || sizeStr != ""
	page, _ = strconv.Atoi(pageStr)
	pageSize, _ = strconv.Atoi(sizeStr)
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	offset = (page - 1) * pageSize
	return
}
