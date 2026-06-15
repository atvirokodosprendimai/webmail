.PHONY: dev build test lint templ-gen migrate-up migrate-down ci-imap-gate clean

BIN := bin/webmail
PKG := ./...
GOOSE := go tool goose
TEMPL := go tool templ
DB_PATH ?= ./data/webmail.db
MIGRATIONS_DIR := ./internal/db/migrations

dev: templ-gen
	go run ./cmd/webmail

build: templ-gen
	mkdir -p bin
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o $(BIN) ./cmd/webmail

test:
	go test -race -count=1 $(PKG)

lint:
	go vet $(PKG)

templ-gen:
	$(TEMPL) generate

migrate-up:
	$(GOOSE) -dir $(MIGRATIONS_DIR) sqlite3 $(DB_PATH) up

migrate-down:
	$(GOOSE) -dir $(MIGRATIONS_DIR) sqlite3 $(DB_PATH) down

ci-imap-gate:
	./scripts/check-imap-wrapper.sh

clean:
	rm -rf bin/ data/webmail.db
