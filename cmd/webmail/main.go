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
	"strings"
	"syscall"
	"time"

	"github.com/atvirokodosprendimai/webmail/internal/auth"
	"github.com/atvirokodosprendimai/webmail/internal/bookmarks"
	"github.com/atvirokodosprendimai/webmail/internal/config"
	"github.com/atvirokodosprendimai/webmail/internal/db"
	"github.com/atvirokodosprendimai/webmail/internal/httpx"
	"github.com/atvirokodosprendimai/webmail/internal/mailbox"
	"github.com/atvirokodosprendimai/webmail/internal/notes"
	"github.com/atvirokodosprendimai/webmail/internal/projects"
	"github.com/atvirokodosprendimai/webmail/internal/uploads"
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "user":
			if err := runUserCLI(os.Args[2:]); err != nil {
				slog.Error("user cli", "err", err)
				os.Exit(1)
			}
			return
		case "healthcheck":
			if err := runHealthcheck(os.Args[2:]); err != nil {
				_, _ = os.Stderr.WriteString(err.Error() + "\n")
				os.Exit(1)
			}
			return
		}
	}
	if err := runServer(); err != nil {
		slog.Error("server", "err", err)
		os.Exit(1)
	}
}

// runHealthcheck hits /healthz on localhost and exits 0 on 200, non-zero
// otherwise. Used by docker compose healthcheck — distroless/static
// has no wget/curl, so the binary itself answers.
func runHealthcheck(args []string) error {
	url := "http://127.0.0.1:8080/healthz"
	if v := os.Getenv("WEBMAIL_LISTEN"); v != "" {
		port := v
		if i := strings.LastIndex(v, ":"); i >= 0 {
			port = v[i:]
		}
		url = "http://127.0.0.1" + port + "/healthz"
	}
	if len(args) > 0 {
		url = args[0]
	}
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("healthcheck: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("healthcheck: status %d", resp.StatusCode)
	}
	return nil
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
	sess := auth.NewSessions(cfg.SessionMaxAge, auth.NewStore(gdb))
	authHandler := &auth.Handler{Repo: authRepo, Sess: sess}

	mailboxRepo := mailbox.NewRepo(gdb)
	bus := mailbox.NewBus()
	imapCfg := mailbox.AccountConfig{
		Host: cfg.IMAPHost, Port: cfg.IMAPPort,
		Username: cfg.IMAPUsername, Password: cfg.IMAPPassword,
		TLSMode: cfg.IMAPTLS,
	}
	accountID, err := mailboxRepo.EnsureAccount(context.Background(), imapCfg)
	if err != nil {
		return err
	}
	mailboxSvc := &mailbox.Service{Cfg: imapCfg, Repo: mailboxRepo, Bus: bus, Log: log}

	projectsRepo := projects.NewRepo(gdb)
	notesRepo := notes.NewRepo(gdb)
	bookmarksRepo := bookmarks.NewRepo(gdb)
	uploadStore := &uploads.Store{Root: cfg.UploadsDir}
	if err := uploadStore.EnsureRoot(); err != nil {
		return err
	}

	app := &httpx.App{
		Cfg:           cfg,
		DB:            gdb,
		AuthRepo:      authRepo,
		AuthHandler:   authHandler,
		Sessions:      sess,
		MailboxRepo:   mailboxRepo,
		MailboxSvc:    mailboxSvc,
		Bus:           bus,
		ProjectsRepo:  projectsRepo,
		NotesRepo:     notesRepo,
		BookmarksRepo: bookmarksRepo,
		Uploads:       uploadStore,
	}

	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           httpx.New(app),
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	poll := &mailbox.PollWorker{
		Cfg:           imapCfg,
		AccountID:     accountID,
		Interval:      cfg.PollInterval,
		FlagSyncEvery: cfg.FlagSyncEvery,
		Repo:          mailboxRepo,
		Bus:           bus,
		Log:           log,
	}
	if cfg.IMAPHost != "" && cfg.IMAPUsername != "" && cfg.IMAPPassword != "" {
		poll.Start(ctx)
	} else {
		log.Warn("IMAP not fully configured — poll worker disabled")
	}

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
