# Swagger Setup Guide

## Overview
Swagger/OpenAPI documentation has been configured for the GenixERP backend API. The code changes have been applied, but you need to install Go and generate the documentation.

## Prerequisites
- Go 1.24+ installed and in your PATH

## Setup Steps

### 1. Install Go
If Go is not installed, install it from https://go.dev/dl/

Verify installation:
```bash
go version
```

### 2. Install Swagger Dependencies
```bash
cd genix-backend
go get -u github.com/swaggo/swag/cmd/swag
go get -u github.com/swaggo/gin-swagger
go get -u github.com/swaggo/files
go mod tidy
```

### 3. Generate Swagger Documentation
```bash
make swagger
```

Or manually:
```bash
swag init -g cmd/api/main.go -o docs
```

This will create a `docs/` folder with:
- `docs.go` - Generated Go code
- `swagger.json` - OpenAPI JSON specification
- `swagger.yaml` - OpenAPI YAML specification

### 4. Start the Backend
```bash
make run
# or
go run cmd/api/main.go
```

### 5. Access Swagger UI
Once the server is running, open your browser and navigate to:
```
http://localhost:8080/swagger/index.html
```

## API Documentation

The Swagger UI provides:
- **Interactive API documentation** - Test endpoints directly from the browser
- **Request/Response schemas** - See all data structures
- **Authentication** - Test authenticated endpoints with Bearer tokens
- **Try it out** - Execute API calls and see live responses

## Adding Swagger Annotations to Endpoints

To document your API endpoints, add Swagger annotations above your handler functions:

```go
// @Summary Get user by ID
// @Description Retrieve a single user by their ID
// @Tags users
// @Accept json
// @Produce json
// @Param id path string true "User ID"
// @Success 200 {object} models.User
// @Failure 404 {object} response.Error
// @Failure 500 {object} response.Error
// @Security BearerAuth
// @Router /users/{id} [get]
func (h *Handler) GetUser(c *gin.Context) {
    // handler implementation
}
```

After adding annotations, regenerate docs:
```bash
make swagger
```

## Swagger Configuration

Main configuration is in `cmd/api/main.go`:
- **Title**: GenixERP API
- **Version**: 2.0
- **Base Path**: /api/v1
- **Host**: localhost:8080
- **Security**: Bearer Authentication (JWT)

## Troubleshooting

### Swagger UI shows "Failed to load API definition"
- Make sure you've run `make swagger` to generate the docs
- Ensure the `docs/` folder exists
- Restart the server after generating docs

### "swag: command not found"
Install swag CLI:
```bash
go install github.com/swaggo/swag/cmd/swag@latest
```

Make sure `$GOPATH/bin` is in your PATH:
```bash
export PATH=$PATH:$(go env GOPATH)/bin
```

### Import cycle error
Make sure the docs import in main.go uses a blank identifier:
```go
_ "github.com/genixerp/genix-backend/docs"
```

## Resources

- [Swaggo Documentation](https://github.com/swaggo/swag)
- [Gin Swagger Integration](https://github.com/swaggo/gin-swagger)
- [OpenAPI Specification](https://swagger.io/specification/)
