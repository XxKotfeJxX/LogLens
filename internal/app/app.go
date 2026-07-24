package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"loglens/internal/api"
	"loglens/internal/importjob"
	"loglens/internal/store"
)

func Run(ctx context.Context) error {
	config, err := LoadConfig()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	level := slog.LevelInfo
	switch config.LogLevel {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level})))

	storage, err := store.Open(ctx, config.DatabasePath)
	if err != nil {
		return err
	}
	defer storage.Close()
	if err := storage.InterruptRunningJobs(ctx); err != nil {
		return fmt.Errorf("recover interrupted jobs: %w", err)
	}

	imports := importjob.NewManager(storage, config.ImportBatchSize, config.MaxEntrySize)
	handler := api.New(storage, imports, api.Config{
		WebOrigin: config.WebOrigin, SourceStoragePath: config.SourceStoragePath,
		TempStoragePath: config.TempStoragePath, MaxUploadSize: config.MaxUploadSize,
		MaxEntrySize: config.MaxEntrySize, ImportRoot: config.ImportRoot,
		AllowServerImport: config.AllowServerImport,
	})
	server := &http.Server{
		Addr: config.Address(), Handler: handler,
		ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second,
	}
	errorChannel := make(chan error, 1)
	go func() {
		slog.Info("loglens API listening", "address", config.Address())
		errorChannel <- server.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return server.Shutdown(shutdownCtx)
	case err := <-errorChannel:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}
