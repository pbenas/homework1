package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/pbenas/homework1/internal/config"
	"github.com/pbenas/homework1/internal/httpserver"
	"github.com/pbenas/homework1/internal/service"
	"github.com/pbenas/homework1/internal/store"
)

func main() {
	os.Exit(executeWithSignals(os.Args[1:], os.Getenv, os.Stderr))
}

func executeWithSignals(args []string, getenv func(string) string, output io.Writer) int {
	logger := slog.New(slog.NewTextHandler(output, nil))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return execute(ctx, args, getenv, logger)
}

func execute(ctx context.Context, args []string, getenv func(string) string, logger *slog.Logger) int {
	if err := run(ctx, args, getenv, logger); err != nil {
		logger.Error("server stopped with an error", "error", err)
		return 1
	}
	return 0
}

func run(ctx context.Context, args []string, getenv func(string) string, logger *slog.Logger) error {
	cfg, err := config.Load(args, getenv)
	if err != nil {
		return err
	}
	storage, err := newStore(cfg)
	if err != nil {
		return err
	}
	if closer, ok := storage.(io.Closer); ok {
		defer func() {
			if err := closer.Close(); err != nil {
				logger.Error("close storage", "error", err)
			}
		}()
	}

	server := httpserver.New(httpserver.Config{
		Address:       net.JoinHostPort(cfg.BindAddress, strconv.Itoa(cfg.Port)),
		MaxObjectSize: cfg.MaxObjectSize,
	}, storage, logger)
	return server.Run(ctx)
}

func newStore(cfg config.Config) (service.ObjectStore, error) {
	switch cfg.Backend {
	case config.BackendMemory:
		return store.NewMemory(), nil
	case config.BackendDisk:
		return store.NewDisk(cfg.DataDir)
	default:
		return nil, fmt.Errorf("unsupported backend %q", cfg.Backend)
	}
}
