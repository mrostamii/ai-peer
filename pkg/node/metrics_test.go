package node

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMetricsHandlerServesPrometheusFormat(t *testing.T) {
	t.Parallel()
	h := metricsHandler()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}
	ct := strings.ToLower(rr.Header().Get("Content-Type"))
	if !strings.Contains(ct, "text/plain") && !strings.Contains(ct, "openmetrics") {
		t.Fatalf("unexpected content type: %q", rr.Header().Get("Content-Type"))
	}
	if rr.Body.Len() == 0 {
		t.Fatal("expected non-empty metrics response body")
	}
}
