# Simple Build Server

A lightweight, containerized build server written in Go. Triggers builds instantly via API or web dashboard.

## Installation

### Configure apps and credentials

Edit `config.yaml`:

```yaml
apps:
  my-app:
    token: "<bcrypt hash>"  # API bearer token
dashboard:
  username: "admin"
  password_hash: "<bcrypt hash>"  # dashboard login password
```

Generate bcrypt hashes with:

```bash
go run ./cmd/hashpassword my-secret-token
```

### Define build and test scripts

For each app in `config.yaml`, create a directory under `/opt/<app-name>/` with at least a `build.sh`.

Optional scripts:
- `test.sh` — run tests after a successful build
- `get-git-url.sh` — output the source repository URL
- `get-git-hash.sh` — output the latest commit hash

See `opt/app-a/` for an example.

## Build and run

```bash
docker build --tag redeploy .
docker run --rm -d -p 8080:8080 --name redeploy redeploy
```

### Dashboard

Open http://localhost:8080/dashboard/

Default credentials: `oli` / `oli`

### API

Trigger a rebuild from your CI pipeline:

```bash
curl -X POST -H "Authorization: Bearer 123" http://localhost:8080/api/rebuild/app-b
```

Builds start immediately (no polling delay).

## Architecture

- Single Go binary — no Apache, no cron, no CGI
- Event-driven builds (instant trigger, no 60s polling)
- Bearer token auth for API, bcrypt-hashed credentials
- Runs as non-root user in container
- Multi-stage Docker build (~50MB image + your build tools)
