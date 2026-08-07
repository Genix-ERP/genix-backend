package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// The vipiska routes put a `:id` wildcard and a static `lines` segment at the
// same depth under /bank-statement-imports. Gin panics at REGISTRATION on a
// conflicting wildcard, which would take the whole service down on boot — not
// just this feature. Assert both that registration survives and that each
// pattern dispatches to the right handler.
func TestVipiskaRoutesRegisterAndDispatch(t *testing.T) {
	gin.SetMode(gin.TestMode)

	r := gin.New() // panics here on a wildcard conflict
	g := r.Group("/bank-statement-imports")
	g.POST("", func(c *gin.Context) { c.String(200, "import1c") })
	g.GET("", func(c *gin.Context) { c.String(200, "list") })
	g.POST("/vipiska", func(c *gin.Context) { c.String(200, "vipiska") })
	g.GET("/:id/transactions", func(c *gin.Context) { c.String(200, "txns:"+c.Param("id")) })
	g.PUT("/lines/:lineId/accounts", func(c *gin.Context) { c.String(200, "accts:"+c.Param("lineId")) })
	g.POST("/lines/:lineId/confirm", func(c *gin.Context) { c.String(200, "confirm:"+c.Param("lineId")) })
	g.POST("/lines/:lineId/reject", func(c *gin.Context) { c.String(200, "reject:"+c.Param("lineId")) })

	cases := []struct{ method, path, want string }{
		{http.MethodPost, "/bank-statement-imports", "import1c"},
		{http.MethodGet, "/bank-statement-imports", "list"},
		{http.MethodPost, "/bank-statement-imports/vipiska", "vipiska"},
		{http.MethodGet, "/bank-statement-imports/imp-1/transactions", "txns:imp-1"},
		{http.MethodPut, "/bank-statement-imports/lines/ln-9/accounts", "accts:ln-9"},
		{http.MethodPost, "/bank-statement-imports/lines/ln-9/confirm", "confirm:ln-9"},
		{http.MethodPost, "/bank-statement-imports/lines/ln-9/reject", "reject:ln-9"},
	}
	for _, tc := range cases {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
		if w.Body.String() != tc.want {
			t.Errorf("%s %s -> %q (status %d), want %q", tc.method, tc.path, w.Body.String(), w.Code, tc.want)
		}
	}
}
