package node

import (
	"context"
	"net"
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

func TestStartMetricsServerFailsOnPortConflict(t *testing.T) {
	t.Parallel()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen() error = %v", err)
	}
	defer ln.Close()

	_, err = startMetricsServer(context.Background(), ln.Addr().String())
	if err == nil {
		t.Fatal("expected startMetricsServer() to fail on port conflict")
	}
}
