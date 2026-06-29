package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"mime"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime/debug"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/pbenas/homework1/src/api"
	"github.com/pbenas/homework1/src/service"
	"github.com/pbenas/homework1/src/store"
)

const (
	envPort          = "OBJECT_STORE_PORT"
	envBindAddress   = "OBJECT_STORE_BIND_ADDRESS"
	envBackend       = "OBJECT_STORE_BACKEND"
	envDataDir       = "OBJECT_STORE_DATA_DIR"
	envMaxObjectSize = "OBJECT_STORE_MAX_OBJECT_SIZE"

	defaultMaxObjectSize = 1 << 30 // 1 GiB
	maxIdentifierBytes   = 180     // remains below common 255-byte filename limits after base64 encoding
)

type config struct {
	port          int
	bindAddress   string
	backend       string
	dataDir       string
	maxObjectSize int64
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

	serverContext, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return runServer(serverContext, cfg, log.Default())
}

func runServer(serverContext context.Context, cfg config, logger *log.Logger) error {
	storage, err := newStore(cfg)
	if err != nil {
		return err
	}
	if closer, ok := storage.(interface{ Close() error }); ok {
		defer func() {
			if err := closer.Close(); err != nil {
				logger.Printf("close storage: %v", err)
			}
		}()
	}
	handler := newHTTPHandler(storage, cfg.maxObjectSize, logger)
	server := &http.Server{
		Addr:              net.JoinHostPort(cfg.bindAddress, strconv.Itoa(cfg.port)),
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	logger.Printf("object server listening on %s with %s backend", server.Addr, cfg.backend)
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
		logger.Printf("shutdown signal received; waiting for active requests")
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
	logger.Printf("server stopped")
	return nil
}

func newHTTPHandler(storage store.Store, maxObjectSize int64, logger *log.Logger) http.Handler {
	implementation := service.New(storage)
	strictOptions := api.StrictHTTPServerOptions{
		RequestErrorHandlerFunc: func(w http.ResponseWriter, _ *http.Request, err error) {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
				return
			}
			http.Error(w, "invalid request", http.StatusBadRequest)
		},
		ResponseErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) {
			logger.Printf("request failed method=%s path=%q error=%v", r.Method, r.URL.Path, err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
		},
	}
	handler := api.HandlerWithOptions(
		api.NewStrictHandlerWithOptions(implementation, nil, strictOptions),
		api.StdHTTPServerOptions{Middlewares: []api.MiddlewareFunc{
			func(next http.Handler) http.Handler { return validateRequests(next, maxObjectSize) },
		}},
	)
	handler = requestLogger(handler, logger)
	return handler
}

type responseRecorder struct {
	http.ResponseWriter
	status int
}

// Unwrap lets http.ResponseController access optional features of the original
// writer without the logging middleware hiding them.
func (r *responseRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

func (r *responseRecorder) Flush() {
	_ = http.NewResponseController(r.ResponseWriter).Flush()
}

func (r *responseRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return http.NewResponseController(r.ResponseWriter).Hijack()
}

func (r *responseRecorder) Push(target string, options *http.PushOptions) error {
	if pusher, ok := r.ResponseWriter.(http.Pusher); ok {
		return pusher.Push(target, options)
	}
	return http.ErrNotSupported
}

func (r *responseRecorder) ReadFrom(reader io.Reader) (int64, error) {
	if r.status == 0 {
		r.WriteHeader(http.StatusOK)
	}
	if readerFrom, ok := r.ResponseWriter.(io.ReaderFrom); ok {
		return readerFrom.ReadFrom(reader)
	}
	return io.Copy(struct{ io.Writer }{r.ResponseWriter}, reader)
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
		defer func() {
			if recovered := recover(); recovered != nil {
				logger.Printf("panic method=%s path=%q value=%v stack=%s", r.Method, r.URL.Path, recovered, debug.Stack())
				if recorder.status == 0 {
					http.Error(recorder, "internal server error", http.StatusInternalServerError)
				}
			}
			status := recorder.status
			if status == 0 {
				status = http.StatusOK
			}
			bucket, objectID := requestIdentifiers(r)
			logger.Printf(
				"request method=%s bucket=%q object=%q status=%d duration=%s",
				r.Method, bucket, objectID, status,
				time.Since(started).Round(time.Microsecond),
			)
		}()
		next.ServeHTTP(recorder, r)
	})
}

func requestIdentifiers(r *http.Request) (string, string) {
	bucket, objectID := r.PathValue("bucket"), r.PathValue("objectID")
	if bucket == "" {
		bucket = "-"
	}
	if objectID == "" {
		objectID = "-"
	}
	return bucket, objectID
}

func validateRequests(next http.Handler, maxObjectSize int64) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bucket, objectID := r.PathValue("bucket"), r.PathValue("objectID")
		if bucket != "" || objectID != "" {
			if !validIdentifier(bucket) || !validIdentifier(objectID) {
				http.Error(w, "invalid bucket or object ID", http.StatusBadRequest)
				return
			}
		}
		if r.Method == http.MethodPut && bucket != "" {
			mediaType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
			if err != nil || !strings.EqualFold(mediaType, "text/plain") ||
				(params["charset"] != "" && !strings.EqualFold(params["charset"], "utf-8")) {
				http.Error(w, "Content-Type must be text/plain with UTF-8 charset", http.StatusUnsupportedMediaType)
				return
			}
			limitedBody := http.MaxBytesReader(w, r.Body, maxObjectSize)
			data, err := io.ReadAll(limitedBody)
			if err != nil {
				var tooLarge *http.MaxBytesError
				if errors.As(err, &tooLarge) {
					http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
					return
				}
				http.Error(w, "invalid request body", http.StatusBadRequest)
				return
			}
			if !utf8.Valid(data) {
				http.Error(w, "request body must be valid UTF-8", http.StatusBadRequest)
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(data))
		}
		next.ServeHTTP(w, r)
	})
}

func validIdentifier(value string) bool {
	return value != "" && utf8.ValidString(value) && len(value) <= maxIdentifierBytes && !strings.Contains(value, "/")
}

func parseConfig(args []string, getenv func(string) string) (config, error) {
	port, err := envInt(getenv, envPort, 8080)
	if err != nil {
		return config{}, err
	}
	maxObjectSize, err := envInt64(getenv, envMaxObjectSize, defaultMaxObjectSize)
	if err != nil {
		return config{}, err
	}
	cfg := config{
		port:          port,
		bindAddress:   envString(getenv, envBindAddress, "127.0.0.1"),
		backend:       envString(getenv, envBackend, "memory"),
		dataDir:       envString(getenv, envDataDir, "./data"),
		maxObjectSize: maxObjectSize,
	}

	flags := flag.NewFlagSet("object-server", flag.ContinueOnError)
	flags.IntVar(&cfg.port, "port", cfg.port, "HTTP listening port (env: "+envPort+")")
	flags.StringVar(&cfg.bindAddress, "bind-address", cfg.bindAddress, "HTTP bind IP address (env: "+envBindAddress+")")
	flags.StringVar(&cfg.backend, "backend", cfg.backend, "storage backend: memory or disk (env: "+envBackend+")")
	flags.StringVar(&cfg.dataDir, "data-dir", cfg.dataDir, "disk backend directory (env: "+envDataDir+")")
	flags.Int64Var(&cfg.maxObjectSize, "max-object-size", cfg.maxObjectSize, "maximum object size in bytes (env: "+envMaxObjectSize+")")
	if err := flags.Parse(args); err != nil {
		return config{}, err
	}
	if flags.NArg() != 0 {
		return config{}, fmt.Errorf("unexpected positional arguments: %s", strings.Join(flags.Args(), " "))
	}
	cfg.backend = strings.ToLower(strings.TrimSpace(cfg.backend))
	cfg.bindAddress = strings.TrimSpace(cfg.bindAddress)
	if cfg.port < 1 || cfg.port > 65535 {
		return config{}, fmt.Errorf("port must be between 1 and 65535")
	}
	if net.ParseIP(strings.TrimSpace(cfg.bindAddress)) == nil {
		return config{}, fmt.Errorf("bind-address must be a valid IP address")
	}
	if cfg.maxObjectSize < 1 {
		return config{}, fmt.Errorf("max-object-size must be positive")
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

func envInt64(getenv func(string) string, key string, fallback int64) (int64, error) {
	value := strings.TrimSpace(getenv(key))
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer: %w", key, err)
	}
	return parsed, nil
}
