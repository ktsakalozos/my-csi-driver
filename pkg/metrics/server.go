package metrics

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	klog "k8s.io/klog/v2"
)

// Server manages the metrics HTTP server
type Server struct {
	port       int
	registry   *prometheus.Registry
	httpServer *http.Server
}

// NewServer creates a new metrics server
func NewServer(port int) *Server {
	return &Server{
		port:     port,
		registry: prometheus.NewRegistry(),
	}
}

// RegisterCollector registers a prometheus collector
func (s *Server) RegisterCollector(collector prometheus.Collector) error {
	return s.registry.Register(collector)
}

// Start starts the metrics HTTP server in a goroutine
func (s *Server) Start() error {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(s.registry, promhttp.HandlerOpts{}))

	s.httpServer = &http.Server{
		Addr:    fmt.Sprintf(":%d", s.port),
		Handler: mux,
	}

	go func() {
		klog.Infof("Starting metrics server on port %d", s.port)
		if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			klog.Errorf("Metrics server failed: %v", err)
		}
	}()

	return nil
}

// Stop gracefully stops the metrics HTTP server
func (s *Server) Stop() error {
	if s.httpServer == nil {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	klog.Info("Stopping metrics server")
	return s.httpServer.Shutdown(ctx)
}
