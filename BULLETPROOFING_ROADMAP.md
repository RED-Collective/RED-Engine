# RED Engine — Bulletproofing Roadmap

This document is for operators preparing to expose a RED Engine node to the public internet. The features below are not yet implemented but are essential before any production deployment.

> **⚠️ The current codebase is designed for LOCAL TESTING and TRUSTED NETWORKS only.**
> Do NOT expose a node to the public internet until these items are addressed.

## Required Before Public Exposure

### Rate Limiting
- No rate limiting on any API endpoint.
- An attacker can flood `/api/` or `/content/` with requests.
- **Needed:** Per-IP rate limiting on admin endpoints and content serving.

### Request Size Caps
- No limit on request body size.
- An attacker can send multi-gigabyte payloads to `/import` or `/admin/peers/add`.
- **Needed:** Configurable max body size per endpoint.

### Token Rotation & Security
- Admin token is set once at setup and never expires.
- Webhook secret cannot be rotated via the API.
- **Needed:** Token rotation endpoint, expiry support.

### TLS / HTTPS
- The `Caddyfile` in this repo listens on port 80 only.
- `docker-compose.yml` exposes port 443 but has no TLS configuration.
- **Needed:** Production TLS with auto-renewal (Caddy magic `tls` directive).

### Container Hardening
- The node runs as `reduser` (non-root), which is good.
- But the Dockerfile uses `golang:1.26-alpine` — pin to a specific patch version.
- **Needed:** Read-only root filesystem, dropped capabilities, seccomp profile.

### Logging & Auditing
- Admin actions are not logged.
- Failed auth attempts are not recorded.
- **Needed:** Audit log for admin actions, auth failure tracking.

### Backup & Recovery
- Automatic backups on startup only.
- No encrypted backup option.
- **Needed:** Scheduled backups, encrypted backup support.

### Network Security
- SSRF protection exists (`SafeClient`) but has an escape hatch (`RED_ALLOW_PRIVATE_SYNC`).
- No IP allowlist/blocklist for peer connections.
- **Needed:** Peer allowlist, stricter SSRF defaults.

---

## Deployment Checklist

When these items are implemented:

- [ ] Rate limiting configured
- [ ] Request size caps set
- [ ] TLS/HTTPS enabled
- [ ] Admin token rotated
- [ ] Read-only root filesystem
- [ ] Audit logging enabled
- [ ] Backups scheduled
- [ ] Peer allowlist configured
- [ ] Container capabilities dropped
- [ ] Security scan completed
