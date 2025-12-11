package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"transola/internal/config"
	"transola/internal/db"
	"transola/internal/httpx"
	"transola/internal/storage"
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-signals
		cancel()
	}()

	cfg := config.Load()
	if err := cfg.Validate(); err != nil {
		log.Fatalf("invalid config: %v", err)
	}

	logger := log.New(os.Stdout, "", log.LstdFlags)

	metaStore, err := db.Open(cfg.DBPath, cfg.AdminEmail, cfg.AdminPassword)
	if err != nil {
		log.Fatalf("db init failed: %v", err)
	}

	store, err := storage.NewLocal(cfg.DataRoot)
	if err != nil {
		log.Fatalf("storage init failed: %v", err)
	}
	if err := store.Ready(ctx); err != nil {
		log.Fatalf("storage not ready: %v", err)
	}

	srv := httpx.NewServer(cfg, store, metaStore, logger)

	server := &http.Server{
		Addr:    cfg.HTTPAddr,
		Handler: srv.Handler(),
	}

	go func() {
		logger.Printf("transola server listening on %s", cfg.HTTPAddr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Fatalf("http server error: %v", err)
		}
	}()

	<-ctx.Done()
	cancel()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Printf("graceful shutdown error: %v", err)
	}
	logger.Println("server stopped")
}
