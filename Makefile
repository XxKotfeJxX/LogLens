.PHONY: api web test lint build check containers

api:
	go run ./cmd/loglens

web:
	pnpm --dir web dev

test:
	go test ./...
	pnpm --dir web test

lint:
	go vet ./...
	pnpm --dir web lint
	pnpm --dir web format:check

build:
	go build ./cmd/loglens
	pnpm --dir web typecheck
	pnpm --dir web build

containers:
	docker compose build

check: lint test build

