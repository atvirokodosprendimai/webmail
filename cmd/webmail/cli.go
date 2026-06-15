package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"

	"github.com/atvirokodosprendimai/webmail/internal/auth"
	"github.com/atvirokodosprendimai/webmail/internal/config"
	"github.com/atvirokodosprendimai/webmail/internal/db"
	"golang.org/x/term"
)

func runUserCLI(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: webmail user <add|list> [args]")
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	gdb, err := db.Open(cfg.DBPath, cfg.MigrateOnBoot)
	if err != nil {
		return err
	}
	repo := auth.NewRepo(gdb)
	ctx := context.Background()

	switch args[0] {
	case "add":
		return userAdd(ctx, repo, args[1:])
	default:
		return fmt.Errorf("unknown subcommand %q", args[0])
	}
}

func userAdd(ctx context.Context, repo *auth.Repo, args []string) error {
	if len(args) < 1 {
		return errors.New("usage: webmail user add <email> [display-name] [--admin]")
	}
	email := strings.ToLower(strings.TrimSpace(args[0]))
	displayName := ""
	role := auth.RoleMember
	for _, a := range args[1:] {
		switch a {
		case "--admin":
			role = auth.RoleAdmin
		default:
			if displayName == "" {
				displayName = a
			}
		}
	}
	if displayName == "" {
		displayName = email
	}

	fmt.Printf("password for %s: ", email)
	pw1, err := term.ReadPassword(int(syscall.Stdin))
	fmt.Println()
	if err != nil {
		return fmt.Errorf("read password: %w", err)
	}
	fmt.Print("confirm: ")
	pw2, err := term.ReadPassword(int(syscall.Stdin))
	fmt.Println()
	if err != nil {
		return fmt.Errorf("read confirm: %w", err)
	}
	if string(pw1) != string(pw2) {
		return errors.New("passwords do not match")
	}
	if len(pw1) < 8 {
		return errors.New("password must be at least 8 chars")
	}

	id, err := repo.Create(ctx, email, displayName, string(pw1), role)
	if err != nil {
		return err
	}
	fmt.Printf("user created: id=%s email=%s role=%s\n", id, email, role)
	_ = bufio.NewReader(os.Stdin) // keep bufio reachable if we add stdin email later
	return nil
}
