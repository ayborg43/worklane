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
- Dark mode, mobile hamburger nav, installable as a PWA

## Running locally

```bash
cp .env.example .env          # edit DATABASE_URL if needed
docker compose up -d          # starts Postgres
go run ./cmd/server           # applies migrations, listens on :8080
```

Then open http://localhost:8080.

## Project layout

- `cmd/server` — entrypoint, wires up all modules
- `internal/<module>` — one package per feature area (auth, projects, timesheets, chat, notifications, settings, attachments, uploads, db)
- `web/templates` — server-rendered HTML templates
- `web/static` — CSS, vendored JS (htmx, Alpine, frappe-gantt), PWA assets
