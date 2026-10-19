# Database Backup & Disaster Recovery

## 1. Automated PostgreSQL Backups

Use `pg_dump` with custom compressed format for consistent point-in-time recovery:

```bash
# Backup PostgreSQL Database
docker exec -t whatsapp_postgres pg_dump -U whatsapp_user -Fc whatsapp_platform > /backups/whatsapp_platform_$(date +%Y%m%d_%H%M%S).dump
```

To schedule a daily backup cron:
```bash
0 2 * * * docker exec -t whatsapp_postgres pg_dump -U whatsapp_user -Fc whatsapp_platform > /backups/db_$(date +\%F).dump
```

## 2. Disaster Recovery & Database Restoration

To restore from a backup file:
```bash
# 1. Stop backend services to prevent write contention
docker compose stop api

# 2. Restore database
cat /backups/db_backup.dump | docker exec -i whatsapp_postgres pg_restore -U whatsapp_user -d whatsapp_platform --clean --if-exists

# 3. Restart backend
docker compose start api
```

## 3. Redis Persistence
Redis is configured with AOF (`appendonly yes`). Redis snapshots (`dump.rdb`) and append-only files reside in the `redis_data` volume and can be backed up directly.
