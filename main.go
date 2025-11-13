package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/n3wscott/phonebook/internal/config"
	"github.com/n3wscott/phonebook/internal/fswatch"
	"github.com/n3wscott/phonebook/internal/httpapi"
	"github.com/n3wscott/phonebook/internal/load"
	"github.com/n3wscott/phonebook/internal/xmlgen"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load(os.Args[1:])
	if err != nil {
		return err
	}

	level, err := config.ToSlogLevel(cfg.LogLevel)
	if err != nil {
		return err
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))

	loader := load.New(cfg.Dir, logger)
	server := httpapi.NewServer(httpapi.Config{
		Addr:       cfg.Addr,
		BasePath:   cfg.BasePath,
		TLSCert:    cfg.TLSCert,
		TLSKey:     cfg.TLSKey,
		AllowDebug: cfg.LogLevel == "debug",
	}, logger)

	if err := rebuild(loader, server, logger); err != nil {
		return fmt.Errorf("initial load failed: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	watcher, err := fswatch.New(cfg.Dir, cfg.Debounce, logger)
	if err != nil {
		return err
	}
	if err := watcher.Start(ctx, func() {
		if err := rebuild(loader, server, logger); err != nil {
			logger.Warn("reload failed", "err", err)
		}
	}); err != nil {
		return err
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Start(ctx)
	}()

	select {
	case <-ctx.Done():
		<-errCh
		return nil
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) || err == nil {
			return nil
		}
		return err
	}
}

func rebuild(loader *load.Loader, server *httpapi.Server, logger *slog.Logger) error {
	result, err := loader.Load()
	if err != nil {
		return err
	}
	xmlBytes, err := xmlgen.Build(result.Contacts)
	if err != nil {
		return err
	}
	server.Update(result.Contacts, xmlBytes, result.LastModified())
	contacts, version := server.Stats()
	logger.Info("reloaded phonebook", "contacts", contacts, "version", version)
	return nil
}
