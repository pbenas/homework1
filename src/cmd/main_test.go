package main

import (
	"bytes"
	"log"
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
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := parseConfig(test.args, func(string) string { return "" }); err == nil {
				t.Fatal("parseConfig() error = nil")
			}
		})
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
