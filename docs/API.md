# TodoStuff JSON API (v1)

A JSON API that covers everything the web client can do: sign in, read the smart lists, create, edit, complete, reorder and delete tasks and projects, search, manage your profile, and administer users. It is built for a native client (for example C++/Qt) that shows **desktop notifications for reminders**.

- **Base URL:** `http://<host>:8636/api/v1`
- **Format:** JSON request and response bodies, UTF-8.
- **Auth:** `Authorization: Bearer <token>` (see [Authentication](#authentication)).
- **Source:** `internal/handlers/api*.go`. Tests live in `internal/handlers/api_test.go`.

The web client's own HTMX endpoints (`/tasks`, `/projects`, `/api/search`, `/api/reminders/due`, …) are unchanged. They return HTML fragments and are not part of this API.

---

## Contents

1. [Quick start](#quick-start)
2. [Conventions](#conventions)
3. [Authentication](#authentication)
4. [Objects](#objects)
5. [Endpoints](#endpoints)
   - [Server status & first-run setup](#server-status--first-run-setup)
   - [Session](#session)
   - [Change detection](#change-detection)
   - [Profile (me)](#profile-me)
   - [Tasks](#tasks)
   - [Search](#search)
   - [Reminders](#reminders)
   - [Projects](#projects)
   - [Admin: users](#admin-users)
6. [Replicating the web client](#replicating-the-web-client)
7. [Notifications: recommended client strategy](#notifications-recommended-client-strategy)
8. [Error codes](#error-codes)
9. [Endpoint summary](#endpoint-summary)
10. [CalDAV](#caldav)

---

## Quick start

```bash
BASE=http://localhost:8636/api/v1

# 1. Sign in and grab a token
TOKEN=$(curl -s -X POST $BASE/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"email":"me@example.com","password":"hunter22"}' | jq -r .token)

# 2. Today's tasks
curl -s "$BASE/tasks?view=today" -H "Authorization: Bearer $TOKEN" | jq

# 3. Add a task due tomorrow at 09:30 with a reminder 15 minutes before
curl -s -X POST $BASE/tasks -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"title":"Call the bank","due_date":"2026-09-29","due_time":"09:30","reminder_offset_minutes":15}'

# 4. Poll for due reminders (call every ~30-60s)
curl -s -X POST $BASE/reminders/claim -H "Authorization: Bearer $TOKEN"
```

---

## Conventions

### Requests

- Send bodies as `Content-Type: application/json`. Any other content type returns **415**. An empty body is treated as `{}`, so endpoints whose fields are all optional can be called with no body.
- Bodies are capped at 1 MiB (**413**).
- **Unknown fields are rejected** with **400** so typos fail loudly and don't silently do nothing.
- IDs are opaque strings (UUIDs today). Don't parse them.

### PATCH semantics

Every `PATCH` is a partial update:

| In the body | Meaning |
|---|---|
| key absent | leave the field unchanged |
| `"key": null` | clear the field (nullable fields only) |
| `"key": value` | set the field |

For nullable string fields, `""` also clears.

### Responses

- A single resource is returned as a bare object (`{"id": …, …}`).
- Collections are wrapped in a named key (`{"tasks": […]}`, `{"projects": […]}`, `{"users": […]}`). They are always arrays, never `null`.
- Successful actions with nothing to return use **204 No Content**.
- Every field in an object is always present; absent values are `null`, never omitted.

### Dates, times and timezones

| Kind | Format | Example | Notes |
|---|---|---|---|
| Timestamp | RFC 3339, UTC | `"2026-09-28T13:02:01.123456Z"` | `created_at`, `updated_at`, `completed_at`, `reminder_at`, `expires_at` |
| Due date | `YYYY-MM-DD` | `"2026-09-29"` | A calendar date, no zone. |
| Due time | `HH:MM`, 24-hour | `"09:30"` | Wall-clock time, no zone. |

A due date and time are **wall-clock values in the server's timezone** (the `TZ` env var). The server uses that zone to turn a due date, due time and reminder offset into the absolute `reminder_at` instant. `GET /status` reports the zone (`timezone`, `utc_offset_seconds`). If the client runs in the same zone as the server, which is the normal self-hosted case, you can display the values as they are.

### Errors

Every error has the same envelope:

```json
{ "error": { "code": "validation_failed", "message": "due_time requires a due_date." } }
```

Branch on `code`; `message` is for humans and may change. See [Error codes](#error-codes).

---

## Authentication

1. `POST /auth/login` with email and password returns a **session token**.
2. Send it on every request as `Authorization: Bearer <token>`.
3. By default the token lasts **365 days** (a "remembered device"). Pass `"remember": false` for 30 days. Store it securely, for example with QtKeychain / the OS keychain.
4. `POST /auth/logout` revokes the token.

Tokens are ordinary sessions, the same kind the browser gets as a cookie. They are revoked when:

- the client calls `/auth/logout`,
- they expire,
- an admin resets that user's password, or
- the user is deleted.

Any request with a missing, expired or revoked token gets **401** `unauthorized`. When that happens, drop the token and show the login screen.

If there is no `Authorization` header, the API falls back to the browser session cookie, so you can explore the API from a logged-in browser tab. An `Authorization` header that is present but invalid is always a 401; the cookie is not tried.

---

## Objects

### Task

```json
{
  "id": "3e93034e-2a45-479f-af9f-760d372510ac",
  "title": "Call the bank",
  "notes": "Ask about **fees**",
  "notes_html": "<p>Ask about <strong>fees</strong></p>\n",
  "project_id": "b1c2…",
  "project_name": "Admin",
  "project_icon": "briefcase",
  "is_important": true,
  "due_date": "2026-09-29",
  "due_time": "09:30",
  "reminder_offset_minutes": 15,
  "reminder_at": "2026-09-29T08:15:00Z",
  "completed_at": null,
  "position": 42,
  "recurrence": { "frequency": "weeks", "interval": 2, "regeneration_type": "on_create" },
  "created_at": "2026-09-28T12:00:00Z",
  "updated_at": "2026-09-28T12:05:00Z"
}
```

| Field | Type | Writable | Notes |
|---|---|---|---|
| `id` | string | – | |
| `title` | string | ✓ | Required, non-empty. |
| `notes` | string\|null | ✓ | Markdown source (GitHub-flavoured: tables, strikethrough, autolinks). |
| `notes_html` | string\|null | – | Server-rendered HTML of `notes`. Raw HTML in the notes is escaped, so it's safe to show in a `QTextBrowser`. |
| `project_id` | string\|null | ✓ | `null` = no project. Must be one of *your* projects. |
| `project_name`, `project_icon` | string\|null | – | Joined from the project, for list rows. |
| `is_important` | bool | ✓ | The star flag. |
| `due_date` | `YYYY-MM-DD`\|null | ✓ | Clearing it also clears `due_time` and the reminder. |
| `due_time` | `HH:MM`\|null | ✓ | Needs a `due_date`. Clearing it also clears the reminder. |
| `reminder_offset_minutes` | int\|null | ✓ | How long before the due date and time the reminder fires. Must be one of the [reminder offsets](#reminder-offsets). Needs both `due_date` and `due_time`. |
| `reminder_at` | timestamp\|null | – | Derived: `due_date + due_time − offset`, in the server's timezone. Moves automatically when the date or time changes. |
| `completed_at` | timestamp\|null | – | Set with `/complete` and cleared with `/uncomplete`. |
| `position` | int | – | Manual sort key; change it with `/tasks/reorder`. |
| `recurrence` | object\|null | ✓ | See [Recurrence](#recurrence). |
| `created_at`, `updated_at` | timestamp | – | |

#### Reminder offsets

| Minutes | Web UI label |
|---|---|
| `null` | None |
| `0` | At the time |
| `5`, `10`, `15`, `30` | N minutes before |
| `60`, `120`, `240`, `480`, `720` | 1 / 2 / 4 / 8 / 12 hours before |
| `1440` | 24 hours before |

Any other value gets **422**.

#### Recurrence

```json
{ "frequency": "weeks", "interval": 2, "regeneration_type": "on_complete" }
```

| Field | Values | Notes |
|---|---|---|
| `frequency` | `days` \| `weeks` \| `months` \| `years` | Required. |
| `interval` | int ≥ 1 | Every *N* units. Defaults to 1 (0 or negative becomes 1). |
| `regeneration_type` | `on_create` \| `on_complete` | Defaults to `on_create`. **`on_create`** (fixed schedule): the next due date is the previous due date + interval, for example car insurance. **`on_complete`**: the next due date is the completion time + interval, for example a flu jab. |

The next instance is created when you complete the current one: it's the `next_task` in the [complete](#post-taskscomplete) response. The new instance copies the title, notes, project, importance and due time. It does **not** copy the reminder offset (same as the web client). A recurring task that has no due date anchors on "now".

Set `"recurrence": null` to stop a task recurring.

### Project

```json
{
  "id": "b1c2…",
  "name": "Work",
  "icon": "briefcase",
  "position": 3,
  "created_at": "2026-09-01T10:00:00Z",
  "updated_at": "2026-09-20T08:00:00Z"
}
```

| Field | Type | Writable | Notes |
|---|---|---|---|
| `name` | string | ✓ | Required, non-empty. |
| `icon` | string\|null | ✓ | An icon ID (see below). `null` means the default `folder`. |
| `position` | int | – | Sidebar order. Change it with `/projects/reorder` or `/projects/{id}/move`. |

**Project icon IDs.** Most are Heroicons v2 outline names; the SVG paths live in `web/templates/partials/icons.html`. The server stores the string as given and doesn't validate it; the web client shows `folder` for unknown or empty values.

`folder` (default), `briefcase`, `home`, `book`, `academic-cap`, `heart`, `shopping-cart`, `gift`, `bolt`, `code`, `paint-brush`, `music`, `camera`, `map-pin`, `beaker`, `trophy`, `archive-box`, `star`, `bell`, `bookmark`, `cake`, `chart-bar`, `chat-bubble`, `clipboard`, `clock`, `cog`, `credit-card`, `envelope`, `flag`, `globe`, `key`, `lightbulb`, `newspaper`, `phone`, `rocket`, `users`, `car`, `truck`, `paper-airplane`, `bowl-food`, `mug`, `paw`, `leaf`, `dumbbell`, `medical`, `tv`, `puzzle-piece`, `wrench-screwdriver`, `computer-desktop`, `shield-check`, `sun`, `dog`, `cat`, `bird`, `reptile`

### User / Me

```json
{
  "id": "…",
  "email": "me@example.com",
  "name": "Andy",
  "is_admin": true,
  "pushover_enabled": true,
  "pushover_user_key": "uQiRzpo4DXghDmr9QzzfQu27cmVRsG",
  "created_at": "…",
  "updated_at": "…"
}
```

`pushover_user_key` appears only on **your own** profile (`/me`, login and setup responses). Admin user listings leave it out.

---

## Endpoints

Endpoints need a token unless they're marked **public**.

### Server status & first-run setup

#### `GET /status` (public)

Use this to discover the server, decide whether to show a "create admin" screen, and learn the server's timezone.

```json
{
  "name": "TodoStuff",
  "api_version": 1,
  "needs_setup": false,
  "pushover_available": true,
  "timezone": "Europe/London",
  "utc_offset_seconds": 3600,
  "server_time": "2026-09-28T13:02:01Z"
}
```

- `needs_setup`: true while no users exist. Call `POST /setup`.
- `pushover_available`: whether the server has `PUSHOVER_APP_TOKEN` set.

#### `POST /setup` (public)

Creates the first admin. It only works while `needs_setup` is true; after that it returns **409** `already_set_up`.

```json
{ "email": "me@example.com", "password": "at-least-8", "name": "Andy" }
```

→ **201** with a [session response](#post-authlogin-public). The new admin is already signed in.

Returns **422** for an invalid email, an empty name, or a password shorter than 8 characters.

### Session

#### `POST /auth/login` (public)

```json
{ "email": "me@example.com", "password": "…", "remember": true }
```

`remember` is optional and defaults to `true` (365-day token; `false` gives 30 days). The email is case-insensitive.

→ **200**

```json
{
  "token": "Zm9v…",
  "expires_at": "2027-09-28T13:02:01Z",
  "user": { …Me… }
}
```

→ **401** `invalid_credentials`

#### `POST /auth/logout`

Revokes the token used to make this request. → **204**

### Change detection

#### `GET /sync`

```json
{ "sync_token": "9f86d081884c7d659a2feaa0c55ad015" }
```

This is an opaque fingerprint of the user's tasks, projects and profile. It changes after **any** write: create, edit, complete, reorder, delete, or a recurrence change, whether it came from this client, the web UI or another device. Poll it cheaply, every 15–60 seconds, and refetch your lists only when it changes. That replaces the web client's blunt 5-minute auto-refresh.

### Profile (me)

#### `GET /me`

→ **200** Me object.

#### `PATCH /me`

```json
{ "name": "Andy J", "pushover_user_key": "uQiRzpo…" }
```

Both fields are optional. A non-empty `pushover_user_key` turns on Pushover delivery for your reminders; `null` or `""` turns it off. Email can't be changed here; an admin changes it with `PATCH /admin/users/{id}`.

→ **200** updated Me.

#### `POST /me/password`

```json
{ "current_password": "…", "new_password": "at-least-8" }
```

→ **204**. **422** `incorrect_password` if the current password is wrong, or `validation_failed` if the new one is too short. Other tokens stay valid.

#### `POST /me/pushover/test`

Sends a test Pushover notification. The body is optional: `{"pushover_user_key": "…"}` tests a key before you save it; otherwise the saved key is used.

→ **204** on success.

| Status | Code | When |
|---|---|---|
| 503 | `pushover_unavailable` | The server has no `PUSHOVER_APP_TOKEN`. |
| 422 | `validation_failed` | No key was supplied or saved. |
| 422 | `invalid_pushover_key` | Pushover rejected the key. |
| 502 | `pushover_error` | Pushover couldn't be reached. |

#### `GET /me/app-passwords` · `POST /me/app-passwords` · `DELETE /me/app-passwords/{id}`

App passwords are the credentials for the [CalDAV endpoint](#caldav), one per device. `POST` takes `{"label": "iPhone"}` and answers **201** with the only copy of the password:

```json
{ "id": "dc9c…", "label": "iPhone", "password": "aq24-m7vd-r9fb-edgt", "last_used_at": null, "created_at": "2026-10-20T08:16:17Z" }
```

`GET` lists them as `{"app_passwords": [...]}` without `password`. `last_used_at` is the device's last sync, to the minute. `DELETE` revokes one (**204**, or **404** `not_found`).

### Tasks

#### `GET /tasks?view=<view>` · `GET /tasks?project_id=<id>`

Returns one of the sidebar's smart lists, or one project's open tasks. It uses exactly the same query and order as the web page. Pass `view` **or** `project_id`, not both. With neither, you get `anytime`.

| `view` | Contents | Order |
|---|---|---|
| `today` | Open tasks that are due today, overdue, **or undated** | Timed tasks first by time, then untimed; then `position` |
| `inbox` | Open tasks with **no project and no due date** (GTD-style triage queue) | `position` |
| `upcoming` | Open tasks due after today | `due_date`, then time (untimed last), then `position` |
| `anytime` | All open tasks | Tasks with a project first, then `position` |
| `logbook` | Completed tasks | `completed_at`, newest first |
| `project_id=…` | That project's open tasks | `position` |

→ **200** `{"tasks": [Task, …]}`. **404** if `project_id` isn't one of your projects; **400** for an unknown view.

> "Today" is the server's local date (its `TZ`), the same zone used for reminders and the web UI. A task due on that date or earlier is in `today`; a later one is in `upcoming`.

#### `GET /tasks/{id}`

→ **200** Task. **404** if it doesn't exist or belongs to someone else.

#### `POST /tasks`

Creates a task. Accepts every writable Task field; only `title` is required.

```json
{
  "title": "Pay car insurance",
  "notes": "Policy #123",
  "project_id": "b1c2…",
  "is_important": false,
  "due_date": "2026-10-01",
  "due_time": "09:00",
  "reminder_offset_minutes": 60,
  "recurrence": { "frequency": "years", "interval": 1, "regeneration_type": "on_create" }
}
```

→ **201** Task. New tasks go to the end of the manual order.

Where the new task appears (same behaviour as the web quick-add):

- no project and no date → Inbox (and Today, because undated tasks show in Today)
- a date → Today or Upcoming
- a project → that project

#### `PATCH /tasks/{id}`

Partial update with the same fields as `POST`. See [PATCH semantics](#patch-semantics).

```json
{ "due_date": "2026-10-02" }
```

→ **200** the updated Task.

Rules the server enforces:

- The whole body is validated before anything is written; a **422** means nothing changed.
- `due_date: null` also clears `due_time` and the reminder.
- `due_time: null` also clears the reminder.
- Setting `due_time` needs a due date, either the existing one or one in the same request.
- `reminder_offset_minutes` needs both a due date and a due time once the patch is applied.
- **Changing the date or time of a task with a reminder moves `reminder_at` too.** If the reminder already fired and its time actually changes, it will fire again at the new time.
- `recurrence: null` removes the schedule. `recurrence: {…}` adds or changes it.
- Editing the title, notes, project, importance or due time of a recurring task updates the template that future instances are copied from.

#### `DELETE /tasks/{id}`

Deletes the task permanently. There is no trash; completing a task is how it goes to the Logbook. → **204**

#### `POST /tasks/{id}/complete`

Marks the task complete. It is idempotent: completing an already-completed task changes nothing and never creates a second instance.

```json
{
  "task": { …Task, "completed_at": "2026-09-28T13:10:00Z"… },
  "next_task": { …Task… }
}
```

`next_task` is the newly created instance when the task recurs; otherwise it is `null`. The next instance can land in the list you're showing (for example a daily task in Today), so refresh that list, or insert `next_task` yourself.

#### `POST /tasks/{id}/uncomplete`

Moves a task back from the Logbook to the open lists. → **200** Task.

#### `POST /tasks/reorder`

```json
{ "ids": ["task-c", "task-a", "task-b"] }
```

After a drag-and-drop, send the IDs of the list you're showing in their new order. The server gives these tasks the same set of `position` values they already had, re-ordered to match. Tasks not in the list keep their positions. Unknown IDs, or IDs that belong to someone else, are ignored. → **204**

### Search

#### `GET /search?q=<text>&limit=<n>`

Case-insensitive substring match on title **and** notes. `%` and `_` are matched literally. Open tasks come first, then the most recently updated. `limit` is 1–100 and defaults to 20. An empty `q` returns an empty list.

→ **200** `{"tasks": [Task, …]}`

### Reminders

#### `POST /reminders/claim`

Claims every reminder that is **due now and hasn't been delivered yet** (`reminder_at ≤ now`, task not completed) and returns those tasks. Claiming is atomic and happens **exactly once**: a reminder returned here will never be returned again, to this client, another native client, or the web page.

```json
{ "reminders": [ { …Task… }, … ] }
```

The list is empty when nothing is due. This is the endpoint to poll for notifications; see the [recommended strategy](#notifications-recommended-client-strategy).

About delivery channels:

- **Client channel.** This endpoint and the web page's poller share it, so whichever client polls first shows the notification.
- **Pushover channel.** Tracked separately. If the user has Pushover enabled, their phone *also* gets every reminder, whatever the clients do.
- A reminder becomes deliverable again only if its time changes (you edit the date, time or offset).

### Projects

#### `GET /projects`

→ **200** `{"projects": [Project, …]}` in sidebar order.

#### `GET /projects/{id}`

→ **200** Project. **404** if it isn't found.

To get a project's tasks, use `GET /tasks?project_id={id}`.

#### `POST /projects`

```json
{ "name": "Garden", "icon": "leaf" }
```

`icon` is optional. → **201** Project, added to the end of the sidebar.

#### `PATCH /projects/{id}`

```json
{ "name": "Allotment", "icon": null }
```

→ **200** Project.

#### `DELETE /projects/{id}`

Deletes the project. **Its tasks are kept** and lose their project; undated ones go back to the Inbox. → **204**

#### `POST /projects/reorder`

```json
{ "ids": ["proj-2", "proj-1", "proj-3"] }
```

The full sidebar order after a drag. It works the same way as task reorder. → **204**

#### `POST /projects/{id}/move`

```json
{ "direction": "up" }
```

Swaps the project with its neighbour (`"up"` or `"down"`). Nothing happens at either end. → **204**

### Admin: users

These need `is_admin`. A non-admin gets **403** `forbidden`.

#### `GET /admin/users`

→ **200** `{"users": [User, …]}`, oldest first.

#### `GET /admin/users/{id}`

→ **200** User.

#### `POST /admin/users`

```json
{ "email": "sam@example.com", "password": "at-least-8", "name": "Sam", "is_admin": false }
```

→ **201** User. **409** `email_taken`; **422** for an invalid email, an empty name, or a short password.

#### `PATCH /admin/users/{id}`

```json
{ "email": "…", "name": "…", "is_admin": true }
```

All fields are optional. → **200** User.

- **409** `email_taken`
- **422** `cannot_demote_self`: you can't remove your own admin flag.

#### `DELETE /admin/users/{id}`

Deletes the user and **all of their data** (tasks, projects, sessions). → **204**

- **422** `cannot_delete_self`
- **422** `last_admin`: you can't delete the only admin.

#### `POST /admin/users/{id}/reset-password`

```json
{ "new_password": "at-least-8" }
```

→ **204**. The user is signed out everywhere: all their tokens are revoked.

---

## Replicating the web client

What the web UI does, and how to do the same with this API:

| Web UI feature | API |
|---|---|
| Sidebar lists Today / Inbox / Upcoming / Anytime / Logbook | `GET /tasks?view=…` |
| Sidebar projects (drag to reorder) | `GET /projects`, `POST /projects/reorder` |
| Project page | `GET /projects/{id}` + `GET /tasks?project_id={id}` |
| Quick-add ("fire and forget") | `POST /tasks` with just `{"title": …}` |
| Detail panel edits (title, notes, project, date and time chips, reminder, repeat, star) | `PATCH /tasks/{id}`. Send only the fields that changed. |
| Check / uncheck | `POST /tasks/{id}/complete` / `uncomplete` |
| Drag tasks | `POST /tasks/reorder` with the visible list's order |
| Search overlay (`/`) | `GET /search?q=…` |
| Auto-refresh | Poll `GET /sync`; refetch when the token changes |
| Browser reminders | Poll `POST /reminders/claim` and show native notifications |
| Profile, Pushover, password | `GET`/`PATCH /me`, `POST /me/pushover/test`, `POST /me/password` |
| Admin → Users | `/admin/users…` |
| First-run `/setup` | `GET /status` → `POST /setup` |
| Light/dark theme | Client-side only; the server doesn't store it |

### Presentation rules the web client uses

These are formatting rules, so the native client re-implements them. "Today" means the local calendar date.

**Due-date chip** on a task row, from `due_date`:

| Condition | Label |
|---|---|
| today | `Today` |
| ±1 day | `Tomorrow` / `Yesterday` |
| 2–6 days ahead | weekday name, for example `Friday` |
| same year | `3 May` |
| otherwise | `3 May 2027` |

A due time is appended in 12-hour form after a dot, for example `Friday · 3:04 PM`. The chip is rust-red when the task is overdue and not completed, amber when it's due today, and grey otherwise.

**Upcoming group headings**, grouped by `due_date`: `Today` / `Tomorrow` / weekday name (up to 6 days ahead) / `Mon, 2 Jan`, with the year added when it isn't this year (`Mon, 2 Jan 2027`).

**Logbook group headings**, grouped by the local date of `completed_at`: `Today` / `Yesterday` / `Mon, 2 Jan 2006`.

**Project rows** show `project_name` and `project_icon`. Hide them on the project page, where every row belongs to the same project.

**Row indicators:**

- a star when `is_important`
- a repeat icon when `recurrence` isn't null
- a notes glyph when `notes` isn't null

---

## Notifications: recommended client strategy

The reason for this API is reliable desktop notifications, so here is a pattern that fires on time, exactly once, and survives the app being closed:

1. **Poll the claim endpoint.** Run a `QTimer` every 30–60 seconds that calls `POST /reminders/claim` and shows one `QSystemTrayIcon::showMessage` (or a libnotify / D-Bus notification) for each returned task. Suggested content:
   - title: `task.title`
   - body: the due date and time, plus `project_name`
2. **Poll on wake.** Call it once at start-up and again after the system resumes from sleep, so reminders that came due while the app was closed or asleep are delivered straight away. The server keeps unclaimed reminders indefinitely until the task is completed.
3. **Optional: exact-time firing.** From your cached open tasks (`GET /tasks?view=anytime`), find the smallest future `reminder_at`. Set a single-shot `QTimer` for that moment and claim when it fires. You still claim, so the reminder is delivered exactly once.
4. **Click-through.** A notification's task ID lets the app open that task's detail view (`GET /tasks/{id}`).
5. **Don't double up.** If the web page is also open with browser notifications allowed, it competes for the same claim; whichever polls first shows the reminder. Pushover is separate and always delivers to the phone if it's enabled.

Qt notes:

- `QNetworkAccessManager` with a `QNetworkRequest` per call.
- Set the `Authorization` and `Content-Type: application/json` headers yourself.
- Parse responses with `QJsonDocument::fromJson`.
- Treat 401 on any call as "signed out".

---

## Error codes

| HTTP | `code` | Meaning |
|---|---|---|
| 400 | `bad_request` | Malformed JSON, unknown field, bad query parameter |
| 401 | `unauthorized` | No token, or the token is invalid, expired or revoked |
| 401 | `invalid_credentials` | Wrong email or password at login |
| 403 | `forbidden` | Admin-only endpoint |
| 404 | `not_found` | No such resource (or it isn't yours), or no such endpoint |
| 405 | `method_not_allowed` | Wrong HTTP method for this path |
| 409 | `already_set_up` | `POST /setup` after the first user exists |
| 409 | `email_taken` | Email already in use |
| 413 | `body_too_large` | Body over 1 MiB |
| 415 | `unsupported_media_type` | Body isn't `application/json` |
| 422 | `validation_failed` | A field value was rejected; `message` names the field |
| 422 | `incorrect_password` | Wrong current password on `/me/password` |
| 422 | `invalid_pushover_key` | Pushover rejected the key |
| 422 | `cannot_demote_self` / `cannot_delete_self` / `last_admin` | Admin self-protection rules |
| 500 | `internal_error` | Server bug; the details are in the server log |
| 502 | `pushover_error` | Pushover couldn't be reached |
| 503 | `pushover_unavailable` | Server has no `PUSHOVER_APP_TOKEN` |

---

## Endpoint summary

| Method | Path | Auth | Purpose |
|---|---|---|---|
| GET | `/status` | public | Server info, setup state, timezone |
| POST | `/setup` | public | Create the first admin |
| POST | `/auth/login` | public | Get a token |
| POST | `/auth/logout` | ✓ | Revoke this token |
| GET | `/sync` | ✓ | Change-detection token |
| GET | `/me` | ✓ | Own profile |
| PATCH | `/me` | ✓ | Update name / Pushover key |
| POST | `/me/password` | ✓ | Change password |
| POST | `/me/pushover/test` | ✓ | Send a test push |
| GET | `/me/app-passwords` | ✓ | List CalDAV app passwords |
| POST | `/me/app-passwords` | ✓ | Create one (password shown once) |
| DELETE | `/me/app-passwords/{id}` | ✓ | Revoke one |
| GET | `/tasks` | ✓ | Smart list or project list |
| POST | `/tasks` | ✓ | Create task |
| GET | `/tasks/{id}` | ✓ | Get task |
| PATCH | `/tasks/{id}` | ✓ | Update task |
| DELETE | `/tasks/{id}` | ✓ | Delete task |
| POST | `/tasks/{id}/complete` | ✓ | Complete (+ next recurrence) |
| POST | `/tasks/{id}/uncomplete` | ✓ | Restore from Logbook |
| POST | `/tasks/reorder` | ✓ | Persist manual order |
| GET | `/search` | ✓ | Search tasks |
| POST | `/reminders/claim` | ✓ | Claim due reminders |
| GET | `/projects` | ✓ | List projects |
| POST | `/projects` | ✓ | Create project |
| GET | `/projects/{id}` | ✓ | Get project |
| PATCH | `/projects/{id}` | ✓ | Rename / change icon |
| DELETE | `/projects/{id}` | ✓ | Delete project (keeps tasks) |
| POST | `/projects/reorder` | ✓ | Persist sidebar order |
| POST | `/projects/{id}/move` | ✓ | Move up/down one |
| GET | `/admin/users` | admin | List users |
| POST | `/admin/users` | admin | Create user |
| GET | `/admin/users/{id}` | admin | Get user |
| PATCH | `/admin/users/{id}` | admin | Edit email / name / admin flag |
| DELETE | `/admin/users/{id}` | admin | Delete user and all their data |
| POST | `/admin/users/{id}/reset-password` | admin | Reset password, revoke sessions |

---

## CalDAV

Outside `/api/v1`, the server is also a CalDAV server for to-dos, so Apple Reminders (or any CalDAV client) can use TodoStuff as a reminders account. Each project is a VTODO list, and tasks with no project are in a list called **Inbox**.

- **Discovery:** point the client at `host:port`. `/.well-known/caldav` and `PROPFIND /` both lead to `/dav/principals/{user-id}/`.
- **Auth:** HTTP Digest (MD5, `qop=auth`, which is what iOS uses over plain HTTP, because it won't send a Basic password without TLS) or HTTP Basic, with the account email and an [app password](#get-meapp-passwords--post-meapp-passwords--delete-meapp-passwordsid). The account password is refused.
- **Layout:** `/dav/calendars/{user-id}/inbox/` and `/dav/calendars/{user-id}/{project-id}/`, with one `.ics` per task. A collection holds the open tasks plus those completed in the last 30 days.
- **Supported:** `PROPFIND`, `REPORT` (`calendar-multiget`, `calendar-query`), `GET`, `PUT`, `DELETE`, ETags with `If-Match` / `If-None-Match`, and CalendarServer `getctag` for change detection. `sync-collection` isn't offered. Lists can't be created, renamed or deleted over CalDAV: `MKCALENDAR` and `PROPPATCH` are refused.
- **Mapping:** `SUMMARY`↔title, `DESCRIPTION`↔notes, `DUE`↔due date and time (read in any zone and converted to the server's `TZ`; always written as `TZID=<server zone>` with a matching `VTIMEZONE`, because iOS keeps whatever zone a reminder arrives in and would otherwise edit it in UTC), `PRIORITY` 1–4↔important, `STATUS:COMPLETED`↔complete or uncomplete, and `RRULE` FREQ + INTERVAL↔recurrence (anything else, such as `COUNT`, `UNTIL` or several weekdays, gets 403). The first `VALARM` becomes the reminder (written back relative to `DTSTART`, e.g. `TRIGGER:-PT15M`, because iOS displays an absolute UTC trigger as a second time zone), snapped to the nearest allowed offset that isn't later than the alarm. An alarm on an all-day reminder becomes its due time. Properties TodoStuff doesn't model (`X-APPLE-*`, …) are stored and sent back unchanged.
- **Writes** go through the same services as the API, so completing a recurring task over CalDAV generates the next instance and the `/sync` token moves.
- **Debugging:** `CALDAV_TRACE=1` logs every CalDAV request and response body (the `Authorization` header is left out).
