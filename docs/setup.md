# Local Setup & Developer Guide

## Prerequisites & Supported Versions

Ensure the following tools are installed on your machine:
- **Go**: `1.22+` (Installed: `1.23.6`)
- **Node.js**: `18.x` or `20.x` or `24.x` (Installed: `24.15.0`)
- **npm**: `10.x+` (Installed: `11.12.1`)
- **Git**: `2.40+`
- **Docker & Docker Compose**: Optional for full containerized stack, or run standalone locally with SQLite/embedded mode.

---

## Quickstart (Option A: Full Docker Compose)

To start the full stack (Postgres 16, Redis 7, Go API, React Web, and Strapi CMS):

```powershell
# 1. Clone or navigate to the repository
cd C:\Users\satya\.gemini\antigravity\scratch\whatsapp-automation

# 2. Copy the environment configuration
Copy-Item .env.example .env

# 3. Spin up all containers
docker compose up -d

# 4. Check container health
docker compose ps
```

Services will be accessible at:
- **Web Dashboard**: `http://localhost:5173`
- **Go REST API & Webhooks**: `http://localhost:8080/api/v1`
- **API Health Check**: `http://localhost:8080/api/v1/health`
- **Strapi CMS Admin**: `http://localhost:1337/admin`

---

## Quickstart (Option B: Standalone Zero-Dependency Local Dev)

You can run both the Go backend and React frontend directly on Windows without Docker!

### 1. Backend Setup
```powershell
cd C:\Users\satya\.gemini\antigravity\scratch\whatsapp-automation\apps\api

# Download dependencies
go mod tidy

# Run server (default uses SQLite and In-Memory queue for instant run)
go run cmd/server/main.go
```

The Go server starts on `http://localhost:8080`.
Default credentials pre-seeded:
- Email: `admin@whatsapp-platform.local`
- Password: `AdminPassword2026!`

### 2. Frontend Setup
```powershell
cd C:\Users\satya\.gemini\antigravity\scratch\whatsapp-automation\apps\web

# Install npm packages
npm install

# Start Vite dev server
npm run dev
```

The web dashboard is live at `http://localhost:5173`.

---

## Running Tests

```powershell
# Run backend Go unit and integration tests
cd C:\Users\satya\.gemini\antigravity\scratch\whatsapp-automation\apps\api
go test -v ./...

# Run frontend tests
cd C:\Users\satya\.gemini\antigravity\scratch\whatsapp-automation\apps\web
npm test
```
