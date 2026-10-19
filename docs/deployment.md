# Production Deployment Guide

## 1. Production Architecture Overview

For production deployments, the system is designed to run behind a reverse proxy (e.g. Nginx, Traefik, or AWS ALB) with automated TLS termination:

```
[Internet] --> [HTTPS Port 443 / Nginx]
                   |
                   +--> [Web Dashboard / static files on /]
                   +--> [Go API on /api/v1/]
                   +--> [Strapi CMS on /admin & /content/]
```

## 2. Linux / VPS Docker Deployment

1. Provision an Ubuntu 22.04+ or Debian 12 virtual server with at least 2 vCPUs and 4GB RAM.
2. Install Docker and Docker Compose plugin:
   ```bash
   sudo apt update && sudo apt install -y docker.io docker-compose-plugin
   ```
3. Clone repository and configure `.env`:
   ```bash
   git clone <repo-url> /opt/whatsapp-automation
   cd /opt/whatsapp-automation
   cp .env.example .env
   # Update DB_PASSWORD, JWT_SECRET, ENCRYPTION_KEY, and META credentials
   ```
4. Build and start services:
   ```bash
   docker compose -f docker-compose.yml up -d --build
   ```

## 3. Environment Hardening
- Set `APP_ENV=production` and `LOG_LEVEL=info`.
- Use a minimum 32-character high-entropy secret for `JWT_SECRET` and `ENCRYPTION_KEY`.
- Enable SSL on PostgreSQL (`DB_SSLMODE=require`).
- Ensure Redis is password-protected or bound strictly to the internal Docker network.
