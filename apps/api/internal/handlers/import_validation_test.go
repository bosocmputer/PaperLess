package handlers

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// TestImport_Validation_HTTP confirms that invalid doc_date / amount inputs
// return a clean 400 (invalid_request) through the real Import handler, never a
// Postgres-cast 500. Validation runs before any storage or DB write, so a nil
// store/engine is safe on the bad-input path (we never reach h.store.Put).
//
// This closes the Phase 3 Step 1c gap: the delivery tested the parseDate /
// validateDecimal helpers in isolation but not the HTTP decision point where
// the 4xx-vs-500 outcome actually matters.
func TestImport_Validation_HTTP(t *testing.T) {
	pool := validationPool(t)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := NewDocumentHandler(pool, nil, nil, zap.NewNop())
	r.POST("/documents/import", fakeAuth(1, "document_admin"), h.Import)

	cases := []struct {
		name     string
		fields   map[string]string
		wantCode int
	}{
		{"bad doc_date (impossible month/day)", map[string]string{"doc_format_code": "POP", "doc_no": "X1", "doc_date": "2024-13-99"}, http.StatusBadRequest},
		{"bad amount (double dot)", map[string]string{"doc_format_code": "POP", "doc_no": "X2", "amount": "12.3.4"}, http.StatusBadRequest},
		{"amount with letters", map[string]string{"doc_format_code": "POP", "doc_no": "X3", "amount": "1000abc"}, http.StatusBadRequest},
		{"slash date", map[string]string{"doc_format_code": "POP", "doc_no": "X4", "doc_date": "2024/01/01"}, http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			mw := multipart.NewWriter(&buf)
			for k, v := range tc.fields {
				_ = mw.WriteField(k, v)
			}
			fw, _ := mw.CreateFormFile("file", "t.pdf")
			_, _ = fw.Write([]byte("%PDF-1.4 fake"))
			_ = mw.Close()

			w := httptest.NewRecorder()
			req := httptest.NewRequest("POST", "/documents/import", &buf)
			req.Header.Set("Content-Type", mw.FormDataContentType())
			r.ServeHTTP(w, req)
			t.Logf("%s → %d %s", tc.name, w.Code, w.Body.String())
			if w.Code != tc.wantCode {
				t.Errorf("%s: want %d, got %d", tc.name, tc.wantCode, w.Code)
			}
			if w.Code >= 500 {
				t.Errorf("%s: got 5xx — validation must catch this as a clean 4xx", tc.name)
			}
		})
	}
}
