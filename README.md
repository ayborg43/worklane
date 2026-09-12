# Worklane

A self-contained project management suite built in Go — projects with
List/Kanban/Gantt views, timesheets with an approval workflow, an internal
Discuss chat, and admin-configurable outbound mail — all server-rendered
with no JS build step.

## Stack

- Go (`net/http`, Go 1.22+ routing) + PostgreSQL (`pgx/v5`, no ORM)
- Server-rendered HTML (`html/template`) enhanced with htmx + Alpine.js, vendored as static files
- gorilla/websocket for real-time Discuss chat
- CSS custom-property design system with light/dark themes
- PWA-installable (manifest + service worker), mobile-responsive

## Features

- Auth: registration, login, sessions
- Projects: List / Kanban / Gantt views, task dependencies, comments, file attachments, membership & permissions
- Timesheets: weekly logging, owner approval workflow, reporting
- Discuss: channels & DMs, unread indicators, file attachments, emoji picker
- Notifications: in-app bell + optional email delivery (Settings → Mail, admin-only)
- User Access (Settings, admin-only): per-user, per-module allow/block (Projects, CRM, Helpdesk, Timesheets, Time Off, Calendar, Social Posts, Discuss) — a blocked module disappears from that user's nav, dashboard, and search results, and its routes 404
- AI rephrasing: a "Rephrase" button on task/ticket descriptions, wiki pages, and social posts, backed by any OpenAI-compatible endpoint (Settings → AI rephrasing, admin-only — set a base URL, API key, and model; works unmodified against OpenAI itself, Ollama's OpenAI-compatible mode, OpenRouter, Groq, etc.)
- Dark mode, mobile hamburger nav, installable as a PWA

## Running locally

```bash
cp .env.example .env          # edit DATABASE_URL if needed
docker compose up -d          # starts Postgres
go run ./cmd/server           # applies migrations, listens on :8080
```

Then open http://localhost:8080.

## Deploying with Dokploy

The repo ships a production `Dockerfile` and `docker-compose.prod.yml` (app +
Postgres, with named volumes for the database and uploaded attachments).
Migrations and the first-admin bootstrap run automatically on startup — no
manual setup step required.

1. In Dokploy, create a new **Application** of type **Docker Compose**,
   point it at this repo, and set the compose file path to
   `docker-compose.prod.yml`.
2. In the app's **Environment** tab, set:
   - `POSTGRES_PASSWORD` — required, no default
   - `BASE_URL` — e.g. `https://worklane.yourdomain.com` (used to build
     links in emailed notifications — task assignments, due-date reminders,
     timesheet approvals)
   - `POSTGRES_USER` / `POSTGRES_DB` — optional, default to `worklane`
   - `ADMIN_EMAIL` — optional. On every startup, promotes the matching
     *already-registered* user to admin. It never creates an account or
     sets/changes a password, so it's safe to leave set permanently — it
     won't reset anything on a later redeploy. Mainly useful if you ever
     need to re-grant admin without a manual SQL step.
3. In **Domains**, point a domain at the `app` service, container port
   `8080`. Dokploy handles TLS via Let's Encrypt.
4. Deploy. Health check is `GET /health`.
5. Register an account — the very first user to register is automatically
   made an admin, unlocking **Settings → Mail** to configure real outbound
   SMTP (or a catcher like Mailpit while testing).

Postgres data and uploaded attachments persist across redeploys via the
`db_data` and `app_data` named volumes. If you'd rather use Dokploy's
built-in managed Postgres instead of the bundled `db` service, drop it from
the compose file and point `DATABASE_URL` at that instance instead.

## Project layout

- `cmd/server` — entrypoint, wires up all modules
- `internal/<module>` — one package per feature area (auth, projects, timesheets, chat, notifications, settings, attachments, uploads, db)
- `web/templates` — server-rendered HTML templates
- `web/static` — CSS, vendored JS (htmx, Alpine, frappe-gantt), PWA assets
