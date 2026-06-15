// Command webmail is the ORBITAL mail terminal server.
//
// Without subcommand: runs the HTTP server + background poll worker.
// With `user add <email>`: opens DB, prompts for password on stdin,
// inserts a webmail user. No self-signup — admins seed users this way.
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
	"time"

	"github.com/atvirokodosprendimai/webmail/internal/auth"
	"github.com/atvirokodosprendimai/webmail/internal/config"
	"github.com/atvirokodosprendimai/webmail/internal/db"
	"github.com/atvirokodosprendimai/webmail/internal/httpx"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "user" {
		if err := runUserCLI(os.Args[2:]); err != nil {
			slog.Error("user cli", "err", err)
			os.Exit(1)
		}
		return
	}
	if err := runServer(); err != nil {
		slog.Error("server", "err", err)
		os.Exit(1)
	}
}

func runServer() error {
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(log)

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if err := ensureDataDirs(cfg); err != nil {
		return err
	}
	gdb, err := db.Open(cfg.DBPath, cfg.MigrateOnBoot)
	if err != nil {
		return err
	}
	log.Info("db ready", "path", cfg.DBPath)

	authRepo := auth.NewRepo(gdb)
	sess := auth.NewSessions(cfg.SessionMaxAge)
	authHandler := &auth.Handler{Repo: authRepo, Sess: sess}

	handler := httpx.New(httpx.Deps{
		Auth:     authHandler,
		Sessions: sess,
		AuthRepo: authRepo,
	})

	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		log.Info("http listening", "addr", cfg.Listen)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("http", "err", err)
			stop()
		}
	}()

	<-ctx.Done()
	log.Info("shutdown")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

func ensureDataDirs(cfg config.Config) error {
	for _, dir := range []string{cfg.UploadsDir, dirOf(cfg.DBPath)} {
		if dir == "" {
			continue
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("mkdir %q: %w", dir, err)
		}
	}
	return nil
}

func dirOf(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' || path[i] == '\\' {
			return path[:i]
		}
	}
	return ""
}
