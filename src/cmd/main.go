package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/pbenas/homework1/src/api"
	"github.com/pbenas/homework1/src/service"
	"github.com/pbenas/homework1/src/store"
)

const (
	envPort    = "OBJECT_STORE_PORT"
	envBackend = "OBJECT_STORE_BACKEND"
	envDataDir = "OBJECT_STORE_DATA_DIR"
)

type config struct {
	port    int
	backend string
	dataDir string
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		log.Fatal(err)
	}
}

func run(args []string) error {
	cfg, err := parseConfig(args, os.Getenv)
	if err != nil {
		return err
	}

	storage, err := newStore(cfg)
	if err != nil {
		return err
	}
	implementation := service.New(storage)
	handler := requestLogger(api.Handler(api.NewStrictHandler(implementation, nil)), log.Default())
	server := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.port),
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}

	serverContext, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	log.Printf("object server listening on %s with %s backend", server.Addr, cfg.backend)
	serveErrors := make(chan error, 1)
	go func() {
		serveErrors <- server.ListenAndServe()
	}()

	select {
	case err := <-serveErrors:
		if !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serve HTTP: %w", err)
		}
		return nil
	case <-serverContext.Done():
		log.Printf("shutdown signal received; waiting for active requests")
	}

	shutdownContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownContext); err != nil {
		_ = server.Close()
		return fmt.Errorf("graceful shutdown: %w", err)
	}
	if err := <-serveErrors; !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve HTTP: %w", err)
	}
	log.Printf("server stopped")
	return nil
}

type responseRecorder struct {
	http.ResponseWriter
	status int
}

func (r *responseRecorder) WriteHeader(status int) {
	if r.status != 0 {
		return
	}
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (r *responseRecorder) Write(body []byte) (int, error) {
	if r.status == 0 {
		r.WriteHeader(http.StatusOK)
	}
	return r.ResponseWriter.Write(body)
}

func requestLogger(next http.Handler, logger *log.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recorder := &responseRecorder{ResponseWriter: w}
		started := time.Now()
		next.ServeHTTP(recorder, r)

		status := recorder.status
		if status == 0 {
			status = http.StatusOK
		}
		bucket := r.PathValue("bucket")
		if bucket == "" {
			bucket = "-"
		}
		objectID := r.PathValue("objectID")
		if objectID == "" {
			objectID = "-"
		}
		logger.Printf(
			"request method=%s bucket=%q object=%q status=%d duration=%s",
			r.Method,
			bucket,
			objectID,
			status,
			time.Since(started).Round(time.Microsecond),
		)
	})
}

func parseConfig(args []string, getenv func(string) string) (config, error) {
	port, err := envInt(getenv, envPort, 8080)
	if err != nil {
		return config{}, err
	}
	cfg := config{
		port:    port,
		backend: envString(getenv, envBackend, "memory"),
		dataDir: envString(getenv, envDataDir, "./data"),
	}

	flags := flag.NewFlagSet("object-server", flag.ContinueOnError)
	flags.IntVar(&cfg.port, "port", cfg.port, "HTTP listening port (env: "+envPort+")")
	flags.StringVar(&cfg.backend, "backend", cfg.backend, "storage backend: memory or disk (env: "+envBackend+")")
	flags.StringVar(&cfg.dataDir, "data-dir", cfg.dataDir, "disk backend directory (env: "+envDataDir+")")
	if err := flags.Parse(args); err != nil {
		return config{}, err
	}
	if flags.NArg() != 0 {
		return config{}, fmt.Errorf("unexpected positional arguments: %s", strings.Join(flags.Args(), " "))
	}
	cfg.backend = strings.ToLower(strings.TrimSpace(cfg.backend))
	if cfg.port < 1 || cfg.port > 65535 {
		return config{}, fmt.Errorf("port must be between 1 and 65535")
	}
	if cfg.backend != "memory" && cfg.backend != "disk" {
		return config{}, fmt.Errorf("backend must be memory or disk")
	}
	if cfg.backend == "disk" && strings.TrimSpace(cfg.dataDir) == "" {
		return config{}, fmt.Errorf("data-dir cannot be empty for the disk backend")
	}
	return cfg, nil
}

func newStore(cfg config) (store.Store, error) {
	if cfg.backend == "memory" {
		return store.NewMemory(), nil
	}
	return store.NewDisk(cfg.dataDir)
}

func envInt(getenv func(string) string, key string, fallback int) (int, error) {
	value := strings.TrimSpace(getenv(key))
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer: %w", key, err)
	}
	return parsed, nil
}

func envString(getenv func(string) string, key, fallback string) string {
	if value := getenv(key); value != "" {
		return value
	}
	return fallback
}
