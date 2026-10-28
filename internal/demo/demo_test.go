package demo

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandler(t *testing.T) {
	h := Handler()
	for path, wantType := range map[string]string{
		"/demo/":          "text/html",
		"/demo/app.js":    "text/javascript",
		"/demo/style.css": "text/css",
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Errorf("%s: status %d", path, rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, wantType) {
			t.Errorf("%s: content type %q, want %s", path, ct, wantType)
		}
		if rec.Header().Get("Content-Security-Policy") == "" {
			t.Errorf("%s: no content security policy", path)
		}
	}
}
