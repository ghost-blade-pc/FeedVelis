package observability

import (
	"context"
	"errors"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"net/http"
	"time"
)

type MetricsServer struct{ server *http.Server }

func NewMetricsServer(address string, gatherer prometheus.Gatherer) *MetricsServer {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(gatherer, promhttp.HandlerOpts{}))
	return &MetricsServer{server: &http.Server{Addr: address, Handler: mux, ReadHeaderTimeout: 5 * time.Second}}
}
func (s *MetricsServer) Run(ctx context.Context) error {
	done := make(chan error, 1)
	go func() { done <- s.server.ListenAndServe() }()
	select {
	case err := <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return s.server.Shutdown(shutdownCtx)
	}
}
func (s *MetricsServer) Handler() http.Handler { return s.server.Handler }
