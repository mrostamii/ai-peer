package ollama

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealthCheckOK(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"models":[]}`))
	}))
	defer srv.Close()

	cli := New(srv.URL)
	if err := cli.HealthCheck(context.Background()); err != nil {
		t.Fatalf("HealthCheck() error = %v", err)
	}
}

func TestListModels(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"models":[{"name":"llama3.2:latest"},{"model":"qwen2.5:7b"}]}`))
	}))
	defer srv.Close()

	cli := New(srv.URL)
	got, err := cli.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels() error = %v", err)
	}
	if len(got) != 2 || got[0] != "llama3.2:latest" || got[1] != "qwen2.5:7b" {
		t.Fatalf("unexpected models: %#v", got)
	}
}
