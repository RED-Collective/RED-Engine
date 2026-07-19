# RED Engine — Installation Guide

## Prerequisites

### Required
- **Git** — [git-scm.com](https://git-scm.com/)
- **Go 1.21+** — [go.dev](https://go.dev/dl/) (only needed for local dev / tests)
- **Node.js 20+** — [nodejs.org](https://nodejs.org/) (includes npm, needed for frontend)

### Optional but recommended
- **Docker** with `docker compose` V2, **or** Podman with `podman-compose` — for containerized deployment
- **Make** — for convenience commands (`make build`, `make test`, etc.)

### Platform notes

| Platform | Shell | Container tool |
|----------|-------|----------------|
| **Linux** | bash | Docker or Podman |
| **macOS** | bash | Docker (Docker Desktop) or Podman |
| **Windows** | PowerShell | Docker Desktop (WSL2 backend) |

---

## Quick Start (Linux / macOS)

```bash
# 1. Clone
git clone https://github.com/RED-Collective/RED-Engine.git
cd RED-Engine

# 2. Run setup wizard
./setup.sh

# 3. Or — manual install (no wizard):
./setup.sh install
```

After install, the node is at `http://localhost:8080`. The admin panel is at `http://localhost:8080/-/admin`.

---

## Quick Start (Windows)

```powershell
# 1. Clone
git clone https://github.com/RED-Collective/RED-Engine.git
cd RED-Engine

# 2. Build frontend
cd internal/router/red-engine-frontend
npm install
npm run build
cd ..\..

# 3. Build Go binary
go build -o red.exe .\cmd\red

# 4. Run
.\red.exe
```

---

## Step-by-Step Manual Setup

### 1. Clone the repo

```bash
git clone https://github.com/RED-Collective/RED-Engine.git
cd RED-Engine
```

### 2. Build the frontend

```bash
cd internal/router/red-engine-frontend
npm install
npm run build
cd ../..
```

### 3. Build the Go backend

```bash
go build -o red ./cmd/red
```

### 4. Configure

Edit `config.json` to set your admin token:

```json
{
  "addr": ":8080",
  "dataDir": "data",
  "adminToken": "your-secure-random-token",
  "WebDir": "FRONTEND_BUILD"
}
```

Generate a token: `openssl rand -hex 24`

### 5. Run

```bash
./red
```

Or with live reload (requires [air](https://github.com/air-verse/air)):
```bash
air -c .air.toml -- "-config=config.json"
```

---

## Development

Start both backend + frontend with hot reload:

```bash
# Linux / macOS
./red-dev.sh
```

This starts:
- **Go backend** on `:8080` (with live reload via `air`)
- **Vite dev server** on `:5173` (with HMR — changes appear instantly)

Open `http://localhost:5173` in your browser.

### Frontend only (if backend is already running)

```bash
cd internal/router/red-engine-frontend
npm install
npm run dev
```

### Backend only (if frontend is already built)

```bash
go run ./cmd/red
```

---

## Docker / Podman Deployment

### Using docker compose

```bash
# 1. Copy the env template
cp .env.example .env

# 2. Edit .env — set at minimum RED_ADMIN_TOKEN:
#    RED_ADMIN_TOKEN=your-secure-token

# 3. Build and start
docker compose up -d
```

### Using Podman

```bash
# 1. Build the container image
./red-engine.sh build

# 2. Run setup wizard
./red-engine.sh setup

# 3. Start the node
./red-engine.sh start
```

### Port conflicts?

If ports 80 or 443 are in use (common if you have IIS, Apache, nginx), change them in `docker-compose.yml` or use the Podman script with a custom port:

```bash
./red-engine.sh setup -p 8080
./red-engine.sh start -p 8080
```

---

## Testing Federation (Two Nodes)

```bash
# Quick demo — starts 2 nodes (requires cloudflared)
make demo

# Or manually:
./scripts/two-node.sh
```

| Node | URL | Admin token | Content dir |
|------|-----|-------------|-------------|
| A | `http://localhost:8080` | `dev-token-A` | `data/` |
| B | `http://localhost:8081` | `dev-token-B` | `data1/` |

---

## Importing Content

1. Go to `http://localhost:8080/-/admin`
2. Click **Import**
3. Enter a Git URL (ending in `.git`), a raw markdown URL, or an archive URL
4. The engine clones the content and serves it immediately

### From a GitHub repo

```
https://github.com/your-username/your-vault.git
```

The engine clones it under `data/<vault-name>/` and the articles appear at `/articles`.

---

## Configuration Reference

### config.json

| Key | Default | Description |
|-----|---------|-------------|
| `addr` | `:8080` | Listen address |
| `dataDir` | `data` | Content directory |
| `adminToken` | (auto) | Admin panel token |
| `webhookSecret` | `""` | GitHub webhook HMAC secret |
| `WebDir` | `FRONTEND_BUILD` | Compiled frontend directory |

### Environment Variables

All `config.json` keys can be overridden by environment variables (`RED_ADDR`, `RED_DATA_DIR`, `RED_ADMIN_TOKEN`, etc.). See `.env.example` for the full list.

---

## Troubleshooting

| Problem | Likely cause | Fix |
|---------|-------------|-----|
| `./setup.sh: command not found` | Not in repo root | `cd RED-Engine` first |
| `go: command not found` | Go not installed | Install Go 1.21+ from [go.dev](https://go.dev/dl/) |
| `npm: command not found` | Node.js not installed | Install Node.js 20+ from [nodejs.org](https://nodejs.org/) |
| Caddy won't start (port 80/443) | Ports in use | Change ports in `docker-compose.yml` |
| Admin panel returns 401 | No admin token set | Set `RED_ADMIN_TOKEN` in `.env` or `config.json` |
| Frontend shows blank page | Frontend not built | Run `make build-frontend` |
| `cloudflared: command not found` | Cloudflare Tunnel not installed | [Install cloudflared](https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/downloads/) |
| `make: command not found` | Make not installed | Install make, or run commands directly |
