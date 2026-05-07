# TodoStuff

A self-hosted Go to-do app with SQLite storage and a calm, responsive UI. Single binary, no JavaScript build step in the client (just HTMX), and your data stays on your machine.

![Today view](docs/screenshots/today.png)

## Features

What's working today:

- **Smart lists** — Today, Inbox, Upcoming, Anytime, Logbook. Inbox is GTD-strict (no project *and* no due date) so it stays a clean triage queue. Upcoming groups by due date with smart labels ("Tomorrow", "Friday", "22 May").
- **Projects** with custom Heroicon icons and reorderable up/down arrows. Tasks moved out of a project go to the Inbox.
- **Due dates and times** with quick chips ("Today" / "Tomorrow" / "Next week" / "Clear") and overdue/today colour cues on the row.
- **Important flag** with a star indicator on the row.
- **Markdown notes** rendered with goldmark (GFM: tables, strikethrough, autolinks). Click-to-edit: rendered preview by default, source-edit textarea on click.
- **Recurring tasks** with two regeneration modes — *fixed schedule* (anchor on the previous due date, e.g. car insurance) and *after completion* (anchor on the completion timestamp, e.g. flu vaccine). On-create instances regenerate the moment the current one is completed.
- **Reminders** as a curated offset before the task's due datetime (5 / 10 / 15 / 30 minutes, 1 / 2 / 4 / 8 / 12 / 24 hours before, or "at the time"). Browser notifications fire via JS polling against a `/api/reminders/due` endpoint.
- **Pushover** — paste your Pushover user key under `/profile`, flip the toggle, and reminders ping your phone via [Pushover](https://pushover.net) the moment they're due. A server-side dispatcher delivers each reminder exactly once on its own channel, independent of the browser poller. There's a "Send test notification" button so you can verify the wiring without waiting for a real reminder.
- **Profile page** at `/profile` — edit your name, configure Pushover, and change your password. Email changes stay an admin operation.
- **Quick-add modal** reachable from the sidebar `+` button or a floating-action button on every page. "Fire and forget" capture: type a title, hit Enter, see a green check, keep typing.
- **Slide-over detail panel** — click any task to open it; edit title, project, dates, reminder, recurrence, importance, and notes inline. The list updates as you type.
- **Multi-user** with first-run admin onboarding (`/setup`), bcrypt password hashing, and HttpOnly + SameSite=Strict session cookies.
- **Single-binary deployment** — Go binary plus an embedded migrations FS and embedded HTML templates. Just point `DATABASE_PATH` at a writable directory.

### Detail panel

Edit everything about a task in one place. Notes live as a clickable rendered preview by default; click to edit the markdown source, blur to swap back. Date/time chips set common values in one click. The Reminder section appears once both date and time are set.

![Detail panel](docs/screenshots/detail-panel.png)

### Project view

A project is just a smart list filtered down to one project. The per-row project tag is suppressed because it would be redundant.

![Project view](docs/screenshots/project.png)

### Upcoming

Future tasks grouped by due date with smart day labels.

![Upcoming view](docs/screenshots/upcoming.png)

### Quick add

Capture from anywhere — fire-and-forget, no view-context required.

![Quick-add modal](docs/screenshots/quick-add.png)

### Profile

Configure Pushover and change your password. The "Send test notification" button verifies the wiring without waiting for a real reminder; if the server isn't configured with a `PUSHOVER_APP_TOKEN` you'll see an inline notice on the page.

![Profile](docs/screenshots/profile.png)

### Logbook

Completed tasks grouped by completion date. Click the checkmark again to restore.

![Logbook](docs/screenshots/logbook.png)

## Roadmap

Next up:

- **Responsive layout** — tablet two-pane and mobile single-pane with full-screen detail.
- **Drag-and-drop reordering + keyboard shortcuts** — `n` for new task, `Enter` to save, `Esc` to close, drag to reorder.
- **User management for admins** — list / create / edit / delete users from an `/admin/users` page.
- **Docker + docker-compose** — multi-stage build, volume-mounted SQLite, `.env.example`.
- **Final polish** — error pages, loading states, empty states, favicon.

Further out: subtasks, project areas, tags, search, dark mode, iCal/CalDAV sync, PWA, sharing, attachments, email reminders, webhooks, and a fix for browser notifications on Safari/Chrome localhost (currently flaky).

## Quick start

```bash
git clone <this-repo> todostuff
cd todostuff
make css       # builds Tailwind once
go run ./cmd/todostuff
```

Then open `http://localhost:8080`. The first request creates the SQLite DB at `./data/todostuff.db` and redirects you to `/setup` to create the admin user.

### Configuration

| Variable | Default | Notes |
|---|---|---|
| `DATABASE_PATH` | `./data/todostuff.db` | SQLite file. Parent directory is created on boot. |
| `PORT` | `8080` | HTTP listen port. |
| `COOKIE_SECURE` | `false` | Set to `true` behind HTTPS in production. |
| `TZ` | system default | Timezone for due-date / due-time / reminder calculations. The browser posts `<input type="date">` / `time` values without a timezone; the server interprets them in `TZ`. |
| `PUSHOVER_APP_TOKEN` | unset | Optional. Application token from your [Pushover](https://pushover.net) account. When set, the server runs a 60-second-tick dispatcher that delivers due reminders to users who've enabled Pushover in `/profile`. Unset = feature disabled, no dispatcher overhead. |

### Make targets

| Target | What it does |
|---|---|
| `make build` | Build the binary into `./bin/todostuff`. |
| `make run` | Build then run on `:8080`. |
| `make dev` | Run with verbose logging. |
| `make css` | One-shot Tailwind v4 build into `web/static/css/app.css`. |
| `make css-watch` | Watch mode for Tailwind during development. |
| `make tidy` | `go mod tidy`. |

## Tech stack

- **Backend:** Go (stdlib + [chi](https://github.com/go-chi/chi)).
- **Storage:** SQLite via [`mattn/go-sqlite3`](https://github.com/mattn/go-sqlite3) (CGO), WAL mode, foreign keys on, embedded migrations.
- **Auth:** bcrypt (cost 12) + 32-byte session tokens, HttpOnly + SameSite=Strict cookies, 30-day expiry.
- **Templates:** Go `html/template`, embedded via `embed.FS`. Layouts + partials parsed alongside each page.
- **Frontend:** Server-rendered HTML + [HTMX 2](https://htmx.org/) (vendored). No framework, no build step on the client.
- **Styling:** [Tailwind CSS v4](https://tailwindcss.com/), driven by `@tailwindcss/cli`.
- **Icons:** [Heroicons v2](https://heroicons.com/) inlined as templated SVGs.
- **Markdown:** [goldmark](https://github.com/yuin/goldmark) with the GFM extension. Rendered HTML is stored alongside the source on save.

## Project layout

```
cmd/todostuff/         Entry point.
internal/
  auth/                Sessions, password hashing, request-context helpers.
  database/            Open + Migrate.
  handlers/            HTTP handlers.
  middleware/          RequireAuth, AllowHead.
  models/              Pure structs that mirror DB rows.
  notifications/       Outbound channels (Pushover today).
  render/              Template loader + funcs.
  services/            Business logic (Tasks, Projects, recurrence, reminders).
migrations/            Numbered SQL files, applied lexicographically once.
web/
  static/              Compiled CSS + vendored HTMX + app.js.
  templates/           layouts/, pages/, partials/.
docs/screenshots/      README screenshots.
LICENSE.md             GNU GPL v3.
```

## License

GNU General Public License v3 or later — see [LICENSE.md](LICENSE.md).
