SHELL := /bin/sh

.PHONY: dev dev-full down fmt fmt-check lint test build vuln integration kubernetes-integration failure-tests e2e migrate kind-up kind-down seed

GOCACHE ?= /tmp/forge-go-cache
GOMODCACHE ?= /tmp/forge-go-mod
FORGE_TEST_DATABASE_URL ?= postgres://forge:forge@localhost:55432/forge?sslmode=disable
GO_PACKAGES := ./apps/api ./apps/migrate ./apps/seed ./apps/worker ./internal/...
GO_FILES := $$(find apps internal tests examples -path '*/node_modules' -prune -o -name '*.go' -type f -print)

dev:
	docker compose up --build

dev-full: kind-up
	docker compose --profile runtime up --build

down:
	docker compose down

fmt:
	gofmt -w $(GO_FILES)
	npm --prefix apps/web run lint -- --fix

fmt-check:
	test -z "$$(gofmt -l $(GO_FILES))"

lint: fmt-check
	GOCACHE=$(GOCACHE) GOMODCACHE=$(GOMODCACHE) go vet $(GO_PACKAGES)
	npm --prefix apps/web run lint
	npm --prefix apps/web run typecheck

test:
	GOCACHE=$(GOCACHE) GOMODCACHE=$(GOMODCACHE) go test -race $(GO_PACKAGES)
	npm --prefix apps/web test

build:
	GOCACHE=$(GOCACHE) GOMODCACHE=$(GOMODCACHE) go build $(GO_PACKAGES)
	npm --prefix apps/web run build

vuln:
	GOCACHE=$(GOCACHE) GOMODCACHE=$(GOMODCACHE) go run golang.org/x/vuln/cmd/govulncheck@v1.7.0 $(GO_PACKAGES)

integration:
	FORGE_TEST_DATABASE_URL=$(FORGE_TEST_DATABASE_URL) GOCACHE=$(GOCACHE) GOMODCACHE=$(GOMODCACHE) go test -count=1 -tags=integration ./tests/integration/...

kubernetes-integration:
	test -n "$(FORGE_TEST_IMAGE)"
	FORGE_TEST_IMAGE=$(FORGE_TEST_IMAGE) GOCACHE=$(GOCACHE) GOMODCACHE=$(GOMODCACHE) go test -count=1 -tags=kubernetes -v ./tests/kubernetes/...

failure-tests:
	GOCACHE=$(GOCACHE) GOMODCACHE=$(GOMODCACHE) ./scripts/failure-tests.sh

e2e:
	./scripts/e2e.sh

migrate:
	go run ./apps/migrate up

kind-up:
	./scripts/kind-up.sh

kind-down:
	./scripts/kind-down.sh

seed:
	go run ./apps/seed
