# Site Counter API

A simple visitor counter API — like the hit counters on sites from the late 90s/early 2000s. Supports multiple sites via a site ID, with separate endpoints for reading and incrementing so you can query without side effects.

**AI Advisory**: This project was partially or fully written by AI.

## How It Works

Each site has a unique ID. Counters are stored in Redis using the key `counter:{siteID}`. The increment operation uses Redis `INCR` which is atomic, so concurrent hits won't race.

## Endpoints

### Get counter
```
GET /counter/{siteID}
```
Returns the current count without modifying it.

**Response**
```json
{"site_id": "my-site", "count": 42}
```

### Increment counter
```
POST /counter/{siteID}/increment
```
Atomically increments the counter and returns the new value. Unknown sites start from 0.

**Response**
```json
{"site_id": "my-site", "count": 43}
```

## Site ID Rules

- Must start with a letter or number (not a hyphen)
- Can contain letters, numbers, and hyphens
- Max 63 characters

Valid: `my-site`, `site1`, `coolblog-2024`
Invalid: `-mysite`, `my site`, `has/slash`

## Configuration

| Env var | Default | Description |
|---|---|---|
| `REDIS_ADDR` | `localhost:6379` | Redis server address |
| `ADDR` | `:8080` | Address the HTTP server binds to |
| `ALLOWED_ORIGINS` | `*` | Comma-separated list of allowed CORS origins (e.g. `https://foo.com,https://bar.com`). Defaults to `*` if neither this nor `ALLOWED_ORIGINS_FILE` is set. |
| `ALLOWED_ORIGINS_FILE` | — | Path to a file containing allowed CORS origins, one per line. Blank lines and lines starting with `#` are ignored. Combined with `ALLOWED_ORIGINS` if both are set. |

When specific origins are configured, the server echoes the matched origin back in the `Access-Control-Allow-Origin` header (with a `Vary: Origin` header). Requests from unlisted origins receive no CORS headers.

**Example origins file:**
```
# Allowed origins
https://example.com
https://app.example.com
```

## Deployment

### Pre-built image (recommended)

Images are published to the GitHub Container Registry for `linux/amd64` and `linux/arm64`:

```
ghcr.io/bencmorrison/sitevisitorcounter:latest
```

Pull and run (bring your own Redis):
```sh
docker pull ghcr.io/bencmorrison/sitevisitorcounter:latest
docker run -p 8080:8080 -e REDIS_ADDR=your-redis-host:6379 ghcr.io/bencmorrison/sitevisitorcounter:latest
```

### Docker Compose with pre-built image

```yaml
services:
  app:
    image: ghcr.io/bencmorrison/sitevisitorcounter:latest
    ports:
      - "8080:8080"
    environment:
      REDIS_ADDR: redis:6379
      ALLOWED_ORIGINS: https://example.com
    depends_on:
      - redis
  redis:
    image: redis:alpine
    volumes:
      - redis_data:/data

volumes:
  redis_data:
```

```sh
docker compose up
```

### Build from source

```sh
docker build -t site-visitor-counter .
docker run -p 8080:8080 -e REDIS_ADDR=your-redis-host:6379 site-visitor-counter
```

### Run locally

Requires Go 1.24+ and a running Redis instance.
```sh
go run .
```

## Usage Example

Embed in a site by hitting the increment endpoint on each page load, then display the count:

```js
fetch('https://your-host/counter/my-site/increment', { method: 'POST' })
  .then(r => r.json())
  .then(data => document.getElementById('counter').textContent = data.count)
```

To display the count without incrementing (e.g. in an admin dashboard):
```sh
curl https://your-host/counter/my-site
```

## Running Tests

Requires a running Redis instance. Set `REDIS_ADDR` if it's not on `localhost:6379`:
```sh
REDIS_ADDR=redis:6379 go test -v ./...
```
