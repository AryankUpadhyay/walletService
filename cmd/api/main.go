// Package main is the entry point for the Wallet Service HTTP API.
// It wires all dependencies together (storage → service → handler → router)
// and starts the HTTP server with graceful shutdown.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/walletservice/internal/handler"
	"github.com/walletservice/internal/service"
	"github.com/walletservice/internal/storage/memory"
)

func main() {
	// --- Structured Logging ---
	// Initialize slog with JSON handler for production-grade structured logs.
	// All log output is JSON, making it parseable by log aggregation systems
	// (e.g., Datadog, Splunk, CloudWatch Logs).
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	// Configuration — in production, read from env vars or config file.
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	// --- Dependency Wiring ---
	// 1. Storage layer (in-memory, swappable to Redis/DynamoDB)
	walletStore := memory.NewWalletStore()
	txnStore := memory.NewTransactionStore()
	idempotencyStore := memory.NewIdempotencyStore()

	// 2. Service layer (depends on repository interfaces)
	walletSvc := service.NewWalletService(walletStore, txnStore, idempotencyStore)

	// 3. Handler layer (depends on service interface)
	walletHandler := handler.NewWalletHandler(walletSvc)

	// 4. Router (wires handlers to HTTP endpoints)
	router := handler.NewRouter(walletHandler)

	// --- HTTP Server ---
	srv := &http.Server{
		Addr:         fmt.Sprintf(":%s", port),
		Handler:      router,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Start server in a goroutine so we can handle shutdown signals.
	go func() {
		slog.Info("wallet service starting",
			"port", port,
			"storage", "in-memory",
		)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server error",
				"error", err,
			)
			os.Exit(1)
		}
	}()

	// --- Graceful Shutdown ---
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	slog.Info("shutting down server")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		slog.Error("server forced to shutdown",
			"error", err,
		)
		os.Exit(1)
	}

	slog.Info("server exited gracefully")
}
