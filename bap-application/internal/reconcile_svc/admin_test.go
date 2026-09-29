package reconcilesvc

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestRequireAdminKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	call := func(configured, sent string) int {
		r := gin.New()
		r.GET("/x", RequireAdminKey(configured), func(c *gin.Context) { c.Status(http.StatusOK) })
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		if sent != "" {
			req.Header.Set("X-Admin-Key", sent)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w.Code
	}
	for _, tc := range []struct {
		configured, sent string
		want             int
	}{
		{"", "anything", http.StatusServiceUnavailable}, // no key configured: disabled
		{"secret", "", http.StatusUnauthorized},
		{"secret", "wrong", http.StatusUnauthorized},
		{"secret", "secret", http.StatusOK},
	} {
		if got := call(tc.configured, tc.sent); got != tc.want {
			t.Errorf("configured=%q sent=%q: status %d, want %d", tc.configured, tc.sent, got, tc.want)
		}
	}
}
