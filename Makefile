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
# NOTE: clean targets MUST NEVER touch .env — user credentials live there
# and the file is gitignored so deletion is unrecoverable.

# Smoke-test that boots the binary on an isolated DB without touching
# the user's data/ or .env. Always use this for local boot tests.
smoke:
	@mkdir -p tmp/smoke
	@cp .env.example tmp/smoke/.env
	@KEY=$$(openssl rand -hex 32) && sed -i '' "s/dev-only-change-me-32-bytes-min!!/$$KEY/g" tmp/smoke/.env
	@cd tmp/smoke && env $$(cat .env | grep -v '^#' | xargs) WEBMAIL_DB_PATH=./smoke.db WEBMAIL_UPLOADS_DIR=./uploads ../../bin/webmail &
	@sleep 2
	@curl -s -o /dev/null -w "/healthz: %%{http_code}\n" http://127.0.0.1:8080/healthz || true
	@pkill -f "bin/webmail" 2>/dev/null || true
	@rm -rf tmp/smoke
