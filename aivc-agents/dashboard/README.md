# Workflow console

React (Vite) UI that walks every HTTP action on the AIVC agents API in four stages:
Probe → Policy → Supervisor → AP workflow.

## Develop

```bash
# terminal 1 — API
make serve

# terminal 2 — Vite with proxy to :8000
cd dashboard && npm install && npm run dev
```

Open http://localhost:5173/dashboard/

## Ship with the API

```bash
make dashboard   # writes src/aivc/static/dashboard
make serve       # serves it at http://localhost:8000/dashboard/
```

`/` redirects to `/dashboard/` when the build is present.
