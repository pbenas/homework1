package main

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"testing"

	"github.com/pbenas/homework1/internal/config"
)

func TestNewStore(t *testing.T) {
	for _, cfg := range []config.Config{
		{Backend: config.BackendMemory},
		{Backend: config.BackendDisk, DataDir: t.TempDir()},
	} {
		storage, err := newStore(cfg)
		if err != nil || storage == nil {
			t.Fatalf("newStore(%q) = %v, %v", cfg.Backend, storage, err)
		}
		if closer, ok := storage.(io.Closer); ok {
			t.Cleanup(func() { _ = closer.Close() })
		}
	}
	if _, err := newStore(config.Config{Backend: "unknown"}); err == nil {
		t.Fatal("newStore() error = nil")
	}
}

func TestRunRejectsInvalidConfig(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := run(context.Background(), []string{"--port=0"}, func(string) string { return "" }, logger); err == nil {
		t.Fatal("run() error = nil")
	}
}

func TestRunStopsWithContext(t *testing.T) {
	port := availablePort(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := run(
		ctx,
		[]string{"--port=" + strconv.Itoa(port)},
		func(string) string { return "" },
		logger,
	); err != nil {
		t.Fatalf("run() error = %v", err)
	}
}

func TestRunClosesDiskStore(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := run(
		ctx,
		[]string{"--backend=disk", "--data-dir=" + t.TempDir()},
		func(string) string { return "" },
		logger,
	); err != nil {
		t.Fatalf("run() error = %v", err)
	}
}

func TestExecute(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if status := execute(context.Background(), []string{"--port=0"}, func(string) string { return "" }, logger); status != 1 {
		t.Fatalf("error status = %d", status)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	args := []string{"--port=" + strconv.Itoa(availablePort(t))}
	if status := execute(ctx, args, func(string) string { return "" }, logger); status != 0 {
		t.Fatalf("success status = %d", status)
	}
}

func availablePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return port
}

func TestExecuteWithSignalsReportsConfigError(t *testing.T) {
	var output bytes.Buffer
	status := executeWithSignals([]string{"--port=0"}, func(string) string { return "" }, &output)
	if status != 1 || !strings.Contains(output.String(), "server stopped with an error") {
		t.Fatalf("status=%d output=%q", status, output.String())
	}
}
