.PHONY: all build run test clean docker-build docker-up docker-down migrate lint fmt help

# Application variables
APP_NAME := genix-backend
VERSION := 2.0.0
BUILD_TIME := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -ldflags="-w -s -X main.Version=$(VERSION) -X main.BuildTime=$(BUILD_TIME)"

# Go variables
GO := go
GOFLAGS := -v
GOTEST := $(GO) test
GOBUILD := $(GO) build

# Default target
all: build

## build: Build the application
build:
	@echo "Building $(APP_NAME)..."
	@$(GOBUILD) $(LDFLAGS) -o bin/$(APP_NAME) ./cmd/api

## build-linux: Build for Linux
build-linux:
	@echo "Building $(APP_NAME) for Linux..."
	@GOOS=linux GOARCH=amd64 $(GOBUILD) $(LDFLAGS) -o bin/$(APP_NAME)-linux-amd64 ./cmd/api

## run: Run the application
run:
	@echo "Running $(APP_NAME)..."
	@$(GO) run ./cmd/api

## dev: Run with hot reload (requires air)
dev:
	@which air > /dev/null || (echo "Installing air..." && go install github.com/cosmtrek/air@latest)
	@air

## test: Run tests
test:
	@echo "Running tests..."
	@$(GOTEST) -v -race -cover ./...

## test-coverage: Run tests with coverage report
test-coverage:
	@echo "Running tests with coverage..."
	@$(GOTEST) -v -race -coverprofile=coverage.out ./...
	@$(GO) tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report generated: coverage.html"

## lint: Run linter
lint:
	@which golangci-lint > /dev/null || (echo "Installing golangci-lint..." && go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest)
	@golangci-lint run ./...

## fmt: Format code
fmt:
	@echo "Formatting code..."
	@$(GO) fmt ./...
	@which goimports > /dev/null || (echo "Installing goimports..." && go install golang.org/x/tools/cmd/goimports@latest)
	@goimports -w .

## tidy: Tidy and verify module dependencies
tidy:
	@echo "Tidying module dependencies..."
	@$(GO) mod tidy
	@$(GO) mod verify

## clean: Clean build artifacts
clean:
	@echo "Cleaning..."
	@rm -rf bin/
	@rm -f coverage.out coverage.html

## docker-build: Build Docker image
docker-build:
	@echo "Building Docker image..."
	@docker build -t $(APP_NAME):$(VERSION) -t $(APP_NAME):latest .

## docker-up: Start Docker containers
docker-up:
	@echo "Starting Docker containers..."
	@docker-compose up -d

## docker-up-dev: Start Docker containers with dev tools
docker-up-dev:
	@echo "Starting Docker containers with dev tools..."
	@docker-compose --profile dev up -d

## docker-down: Stop Docker containers
docker-down:
	@echo "Stopping Docker containers..."
	@docker-compose down

## docker-logs: View Docker container logs
docker-logs:
	@docker-compose logs -f api

## docker-clean: Remove Docker containers and volumes
docker-clean:
	@echo "Cleaning Docker resources..."
	@docker-compose down -v --rmi local

## migrate-create: Create a new migration (usage: make migrate-create name=migration_name)
migrate-create:
	@if [ -z "$(name)" ]; then echo "Usage: make migrate-create name=migration_name"; exit 1; fi
	@echo "Creating migration: $(name)"
	@touch migrations/$$(date +%Y%m%d%H%M%S)_$(name).sql
	@echo "-- Migration: $(name)" > migrations/$$(date +%Y%m%d%H%M%S)_$(name).sql

## migrate-up: Run database migrations
migrate-up:
	@echo "Running migrations..."
	@$(GO) run ./cmd/migrate up

## migrate-down: Rollback last migration
migrate-down:
	@echo "Rolling back migration..."
	@$(GO) run ./cmd/migrate down

## swagger: Generate Swagger documentation
swagger:
	@which swag > /dev/null || (echo "Installing swag..." && go install github.com/swaggo/swag/cmd/swag@latest)
	@swag init -g cmd/api/main.go -o docs

## deps: Install dependencies
deps:
	@echo "Installing dependencies..."
	@$(GO) mod download

## deps-update: Update dependencies
deps-update:
	@echo "Updating dependencies..."
	@$(GO) get -u ./...
	@$(GO) mod tidy

## generate: Run go generate
generate:
	@echo "Running go generate..."
	@$(GO) generate ./...

## check: Run all checks (fmt, lint, test)
check: fmt lint test

## install-tools: Install development tools
install-tools:
	@echo "Installing development tools..."
	@go install github.com/cosmtrek/air@latest
	@go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
	@go install github.com/swaggo/swag/cmd/swag@latest
	@go install golang.org/x/tools/cmd/goimports@latest

## help: Show this help message
help:
	@echo "GenixERP Backend - Makefile Commands"
	@echo ""
	@echo "Usage: make [target]"
	@echo ""
	@echo "Targets:"
	@sed -n 's/^##//p' $(MAKEFILE_LIST) | column -t -s ':' | sed -e 's/^/ /'
