package node

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func metricsHandler() http.Handler {
	return promhttp.Handler()
}

func startMetricsServer(ctx context.Context, listen string) *http.Server {
	mux := http.NewServeMux()
	mux.Handle("/metrics", metricsHandler())
	srv := &http.Server{
		Addr:              listen,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("metrics server warning: %v", err)
		}
	}()

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			log.Printf("metrics shutdown warning: %v", err)
		}
	}()

	log.Printf("metrics server listening on %s", listen)
	return srv
}

func stopMetricsServer(srv *http.Server) error {
	if srv == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("metrics shutdown: %w", err)
	}
	return nil
}
