package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"opsdrop/internal/config"
	"opsdrop/internal/db"
	"opsdrop/internal/server"
	"opsdrop/internal/version"
)

func main() {
	log.Printf("OpsDrop server version %s (commit: %s, built: %s)", version.Version, version.Commit, version.Date)

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	database, err := db.New(cfg.DatabasePath)
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	defer database.Close()
	if err := db.EnsureMigrations(context.Background(), database.Conn()); err != nil {
		log.Fatalf("apply migrations: %v", err)
	}

	srv := server.New(cfg, database)

	httpServer := &http.Server{
		Addr:         cfg.Address,
		Handler:      srv.Router(),
		ReadTimeout:  5 * time.Minute,
		WriteTimeout: 10 * time.Minute,
		IdleTimeout:  120 * time.Second,
	}

	go func() {
		if cfg.TLSEnabled {
			log.Printf("HTTPS server listening on %s", cfg.Address)
			if err := httpServer.ListenAndServeTLS(cfg.TLSCertPath, cfg.TLSKeyPath); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Fatalf("server error: %v", err)
			}
		} else {
			log.Printf("HTTP server listening on %s (TLS disabled)", cfg.Address)
			if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Fatalf("server error: %v", err)
			}
		}
	}()

	waitForShutdown(httpServer)
}

func waitForShutdown(srv *http.Server) {
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	log.Println("shutting down...")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("graceful shutdown failed: %v", err)
	}
}
