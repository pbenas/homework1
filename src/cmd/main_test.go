package main

import (
	"bytes"
	"context"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseConfigDefaults(t *testing.T) {
	cfg, err := parseConfig(nil, func(string) string { return "" })
	if err != nil {
		t.Fatalf("parseConfig() error = %v", err)
	}
	if cfg.port != 8080 || cfg.backend != "memory" || cfg.dataDir != "./data" {
		t.Fatalf("parseConfig() = %+v", cfg)
	}
}

func TestParseConfigEnvironmentAndFlagPrecedence(t *testing.T) {
	environment := map[string]string{
		envPort:    "9000",
		envBackend: "disk",
		envDataDir: "/environment-data",
	}
	getenv := func(key string) string { return environment[key] }

	cfg, err := parseConfig([]string{
		"--port=9001",
		"--backend=memory",
		"--data-dir=/flag-data",
	}, getenv)
	if err != nil {
		t.Fatalf("parseConfig() error = %v", err)
	}
	if cfg.port != 9001 || cfg.backend != "memory" || cfg.dataDir != "/flag-data" {
		t.Fatalf("parseConfig() = %+v", cfg)
	}
}

func TestParseConfigRejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "port too low", args: []string{"--port=0"}},
		{name: "port too high", args: []string{"--port=65536"}},
		{name: "unknown backend", args: []string{"--backend=remote"}},
		{name: "empty disk directory", args: []string{"--backend=disk", "--data-dir="}},
		{name: "unknown flag", args: []string{"--unknown"}},
		{name: "positional argument", args: []string{"unexpected"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := parseConfig(test.args, func(string) string { return "" }); err == nil {
				t.Fatal("parseConfig() error = nil")
			}
		})
	}
}

func TestParseConfigRejectsInvalidEnvironmentPort(t *testing.T) {
	_, err := parseConfig(nil, func(key string) string {
		if key == envPort {
			return "not-a-number"
		}
		return ""
	})
	if err == nil {
		t.Fatal("parseConfig() error = nil")
	}
}

func TestRequestLogger(t *testing.T) {
	var output bytes.Buffer
	logger := log.New(&output, "", 0)
	mux := http.NewServeMux()
	mux.HandleFunc("PUT /objects/{bucket}/{objectID}", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
	})
	handler := requestLogger(mux, logger)

	request := httptest.NewRequest(http.MethodPut, "/objects/documents/welcome", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	logLine := output.String()
	for _, expected := range []string{
		"method=PUT",
		"bucket=\"documents\"",
		"object=\"welcome\"",
		"status=201",
	} {
		if !strings.Contains(logLine, expected) {
			t.Errorf("log output %q does not contain %q", logLine, expected)
		}
	}
}

func TestRequestLoggerForUnmatchedRoute(t *testing.T) {
	var output bytes.Buffer
	logger := log.New(&output, "", 0)
	handler := requestLogger(http.NewServeMux(), logger)

	request := httptest.NewRequest(http.MethodGet, "/unknown", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	logLine := output.String()
	for _, expected := range []string{
		"bucket=\"-\"",
		"object=\"-\"",
		"status=404",
	} {
		if !strings.Contains(logLine, expected) {
			t.Errorf("log output %q does not contain %q", logLine, expected)
		}
	}
}

func TestResponseRecorder(t *testing.T) {
	underlying := httptest.NewRecorder()
	recorder := &responseRecorder{ResponseWriter: underlying}
	if _, err := recorder.Write([]byte("body")); err != nil {
		t.Fatal(err)
	}
	recorder.WriteHeader(http.StatusCreated)
	if recorder.status != http.StatusOK || underlying.Code != http.StatusOK || underlying.Body.String() != "body" {
		t.Fatalf("recorder=%+v, response=%+v", recorder, underlying)
	}
}

func TestNewStore(t *testing.T) {
	if storage, err := newStore(config{backend: "memory"}); err != nil || storage == nil {
		t.Fatalf("memory store = %v, %v", storage, err)
	}
	if storage, err := newStore(config{backend: "disk", dataDir: t.TempDir()}); err != nil || storage == nil {
		t.Fatalf("disk store = %v, %v", storage, err)
	}
	if _, err := newStore(config{backend: "disk"}); err == nil {
		t.Fatal("disk store with empty path error = nil")
	}
}

func TestRunRejectsInvalidConfig(t *testing.T) {
	if err := run([]string{"--port=0"}); err == nil {
		t.Fatal("run() error = nil")
	}
}

func TestRunServerGracefulShutdown(t *testing.T) {
	for _, cfg := range []config{
		{port: 0, backend: "memory"},
		{port: 0, backend: "disk", dataDir: t.TempDir()},
	} {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		var output bytes.Buffer
		if err := runServer(ctx, cfg, log.New(&output, "", 0)); err != nil {
			t.Fatalf("runServer(%s) error = %v", cfg.backend, err)
		}
		if !strings.Contains(output.String(), "server stopped") {
			t.Fatalf("log output = %q", output.String())
		}
	}
}

func TestRunServerBindError(t *testing.T) {
	listener, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port

	err = runServer(context.Background(), config{port: port, backend: "memory"}, log.New(&bytes.Buffer{}, "", 0))
	if err == nil || !strings.Contains(err.Error(), "serve HTTP") {
		t.Fatalf("runServer() error = %v", err)
	}
}

func TestEnvironmentHelpers(t *testing.T) {
	getenv := func(key string) string {
		return map[string]string{"set": " 42 ", "text": "value"}[key]
	}
	if value, err := envInt(getenv, "set", 1); err != nil || value != 42 {
		t.Fatalf("envInt() = %d, %v", value, err)
	}
	if value, err := envInt(getenv, "missing", 7); err != nil || value != 7 {
		t.Fatalf("envInt() fallback = %d, %v", value, err)
	}
	if value := envString(getenv, "text", "fallback"); value != "value" {
		t.Fatalf("envString() = %q", value)
	}
}
