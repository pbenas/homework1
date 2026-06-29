// Package httpserver exposes the object service over HTTP.
package httpserver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/pbenas/homework1/internal/api"
	"github.com/pbenas/homework1/internal/service"
)

const (
	readHeaderTimeout = 5 * time.Second
	readTimeout       = 15 * time.Second
	writeTimeout      = 30 * time.Second
	idleTimeout       = 60 * time.Second
	shutdownTimeout   = 10 * time.Second
)

// Config contains HTTP-specific server configuration.
type Config struct {
	Address       string
	MaxObjectSize int64
}

// Server owns the HTTP lifecycle for an object service.
type Server struct {
	http   *http.Server
	logger *slog.Logger
}

// New creates a configured HTTP server.
func New(cfg Config, storage service.ObjectStore, logger *slog.Logger) *Server {
	implementation := service.New(storage)
	strictOptions := api.StrictHTTPServerOptions{
		RequestErrorHandlerFunc:  requestErrorHandler,
		ResponseErrorHandlerFunc: responseErrorHandler(logger),
	}
	handler := api.HandlerWithOptions(
		api.NewStrictHandlerWithOptions(implementation, nil, strictOptions),
		api.StdHTTPServerOptions{Middlewares: []api.MiddlewareFunc{
			func(next http.Handler) http.Handler {
				return requestValidationMiddleware(next, cfg.MaxObjectSize)
			},
		}},
	)

	return &Server{
		http: &http.Server{
			Addr:              cfg.Address,
			Handler:           requestLoggingMiddleware(handler, logger),
			ReadHeaderTimeout: readHeaderTimeout,
			ReadTimeout:       readTimeout,
			WriteTimeout:      writeTimeout,
			IdleTimeout:       idleTimeout,
		},
		logger: logger,
	}
}

func requestErrorHandler(w http.ResponseWriter, _ *http.Request, err error) {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
		return
	}
	http.Error(w, "invalid request", http.StatusBadRequest)
}

func responseErrorHandler(logger *slog.Logger) func(http.ResponseWriter, *http.Request, error) {
	return func(w http.ResponseWriter, request *http.Request, err error) {
		logger.Error("request failed", "method", request.Method, "path", request.URL.Path, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}
}

// Run serves requests until ctx is cancelled or the server fails.
func (server *Server) Run(ctx context.Context) error {
	server.logger.Info("object server listening", "address", server.http.Addr)
	serveErrors := make(chan error, 1)
	go func() {
		serveErrors <- server.http.ListenAndServe()
	}()

	select {
	case err := <-serveErrors:
		if !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serve HTTP: %w", err)
		}
		return nil
	case <-ctx.Done():
		server.logger.Info("shutdown signal received; waiting for active requests")
	}

	shutdownContext, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := server.http.Shutdown(shutdownContext); err != nil {
		_ = server.http.Close()
		return fmt.Errorf("graceful shutdown: %w", err)
	}
	if err := <-serveErrors; !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve HTTP: %w", err)
	}
	server.logger.Info("server stopped")
	return nil
}
