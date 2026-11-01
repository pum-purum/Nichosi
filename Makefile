.RECIPEPREFIX = >
.PHONY: help re down run test vet install dev types types-go mock

help:
> @grep -E '^[a-z-]+:.*##' Makefile | sed 's/:.*##/ -/'

re: ## build and start Nichosi in Docker
> test -f connections.json || cp connections.example.json connections.json
> docker compose -f deploy/docker-compose.yml up -d --build

down: ## stop the Nichosi container
> docker compose -f deploy/docker-compose.yml down

run: ## run backend on 127.0.0.1:8080
> cd backend && go run ./cmd/nichosi

test: ## backend tests
> cd backend && go test ./...

vet: ## backend static checks
> cd backend && go vet ./...

install: ## install frontend deps
> cd frontend && corepack pnpm install

dev: ## frontend dev server
> cd frontend && corepack pnpm dev

types: ## generate TS types from the OpenAPI contract
> cd frontend && npx openapi-typescript ../api/openapi.yaml -o src/lib/api/schema.d.ts

types-go: ## generate Go models from the OpenAPI contract
> cd backend && go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.4.1 -generate types -package main -o cmd/nichosi/api.gen.go ../api/openapi.yaml

mock: ## mock API server from the contract (for frontend work without backend)
> npx @stoplight/prism-cli mock api/openapi.yaml -p 4010
