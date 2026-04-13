# Genderize API — Name Classifier

A lightweight Go HTTP server that wraps the [Genderize.io](https://genderize.io) API with processing logic and structured responses.

## Endpoint

### `GET /api/classify?name={name}`

**Success (200)**
```json
{
  "status": "success",
  "data": {
    "name": "john",
    "gender": "male",
    "probability": 0.99,
    "sample_size": 1234,
    "is_confident": true,
    "processed_at": "2026-04-13T10:00:00Z"
  }
}
```

**Error**
```json
{ "status": "error", "message": "<reason>" }
```

| Status | Cause |
|--------|-------|
| 400 | Missing or empty `name` parameter |
| 422 | `name` is not a valid string (e.g. JSON object) |
| 422 | No prediction available (null gender or 0 count) |
| 502 | Upstream Genderize API unreachable |
| 500 | Internal parsing error |

**`is_confident`** is `true` only when `probability >= 0.7` AND `sample_size >= 100`.

---

## Running locally

```bash
# Run directly
go run main.go

# Or build and run
go build -o server .
./server

# Custom port
PORT=3000 go run main.go
```

Server starts on port `8080` by default (or `$PORT` env var).

Test it:
```bash
curl "http://localhost:8080/api/classify?name=john"
curl "http://localhost:8080/api/classify?name=james"
curl "http://localhost:8080/api/classify"           # 400
curl "http://localhost:8080/api/classify?name="     # 400
```

---

## Deployment

### Railway
1. Push repo to GitHub
2. New project → Deploy from GitHub repo
3. Railway auto-detects Go and uses the `$PORT` env var — nothing to configure

### Fly.io
```bash
fly launch   # detects Go automatically
fly deploy
```

### Vercel (via Docker)
Use the included `Dockerfile`. Set `PORT` if needed.

### Heroku
```bash
heroku create
git push heroku main
```
Add a `Procfile`:
```
web: ./server
```

---

## Project structure

```
.
├── main.go      # All server logic
├── go.mod       # Module definition (no external deps)
├── Dockerfile   # Multi-stage build
└── README.md
```

No external packages — pure Go standard library.
