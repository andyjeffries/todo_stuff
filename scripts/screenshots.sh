#!/usr/bin/env bash
# Regenerate the README screenshots in docs/screenshots/.
#
# Stands up an isolated TodoStuff instance (its own port + temp DB), seeds a
# realistic-looking demo dataset, runs Playwright to capture each view, then
# tears the whole thing down. Idempotent — safe to re-run.
#
# Why isolated? Andy uses the live :8636 instance for real to-dos; we don't
# want to leak personal data into screenshots, and we don't want to step on
# his running server.
#
# Usage:
#   scripts/screenshots.sh
#   make screenshots
#
# Requirements: Go toolchain, Node + npm, sqlite3, curl. The first run
# downloads Playwright + a Chromium build into scripts/.cache/ (gitignored).

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
PORT=18636
DB="/tmp/todostuff-screenshots.db"
BIN="/tmp/todostuff-screenshots-bin"
COOKIES="/tmp/todostuff-screenshots.cookies"
PW_CACHE="$ROOT/scripts/.cache"
BASE="http://localhost:$PORT"

# --- credentials for the seed admin user ---
EMAIL="demo@todostuff.local"
PASSWORD="demoseed12345"
NAME="Alex"

# These IDs get inserted directly so the playwright script can deep-link
# to a known project URL without scraping the sidebar for it.
PROJ_WORK="11111111-1111-1111-1111-111111111111"
PROJ_HOME="22222222-2222-2222-2222-222222222222"
PROJ_READ="33333333-3333-3333-3333-333333333333"

SERVER_PID=""

cleanup() {
  local code=$?
  if [[ -n "$SERVER_PID" ]] && kill -0 "$SERVER_PID" 2>/dev/null; then
    kill "$SERVER_PID" 2>/dev/null || true
    wait "$SERVER_PID" 2>/dev/null || true
  fi
  rm -f "$BIN" "$DB" "$DB-shm" "$DB-wal" "$COOKIES"
  exit "$code"
}
trap cleanup EXIT INT TERM

note() { printf '\033[36m›\033[0m %s\n' "$*"; }

# ---------------------------------------------------------------------------
# 1. Build the binary against the current source tree.
# ---------------------------------------------------------------------------
note "building isolated binary"
( cd "$ROOT" && go build -o "$BIN" ./cmd/todostuff )

# Make sure CSS is current too — the binary serves it from the filesystem.
note "building CSS"
( cd "$ROOT" && npx --yes @tailwindcss/cli \
    -i web/static/css/input.css \
    -o web/static/css/app.css --minify >/dev/null 2>&1 )

# ---------------------------------------------------------------------------
# 2. Start the server. Wait for /health.
# ---------------------------------------------------------------------------
note "starting server on :$PORT"
rm -f "$DB" "$DB-shm" "$DB-wal" "$COOKIES"
DATABASE_PATH="$DB" PORT="$PORT" "$BIN" >/dev/null 2>&1 &
SERVER_PID=$!

for _ in {1..40}; do
  if curl -fs "$BASE/health" >/dev/null 2>&1; then break; fi
  sleep 0.1
done
if ! curl -fs "$BASE/health" >/dev/null 2>&1; then
  echo "server failed to come up on :$PORT" >&2
  exit 1
fi

# ---------------------------------------------------------------------------
# 3. Seed admin + projects + tasks. Curl handles cookies + form-encoding;
#    sqlite3 handles the few fields the API doesn't expose (project IDs we
#    pin in advance, and completed_at backdating for the Logbook).
# ---------------------------------------------------------------------------
note "creating admin user"
# /setup returns 302 with the session cookie. Don't follow the redirect
# (curl with -X POST would re-POST to /today and hit 405); the Set-Cookie
# from the 302 response itself is what we need.
status=$(curl -fs -o /dev/null -w '%{http_code}' \
  -X POST "$BASE/setup" \
  -d "email=$EMAIL" -d "password=$PASSWORD" -d "name=$NAME" \
  -c "$COOKIES" || true)
if [[ "$status" != "302" ]]; then
  echo "setup returned $status (expected 302)" >&2
  exit 1
fi

note "seeding projects"
sqlite3 "$DB" <<SQL
DELETE FROM projects;
INSERT INTO projects (id, user_id, name, icon, position) VALUES
  ('$PROJ_WORK', (SELECT id FROM users LIMIT 1), 'Work',    'briefcase', 1),
  ('$PROJ_HOME', (SELECT id FROM users LIMIT 1), 'Home',    'home',      2),
  ('$PROJ_READ', (SELECT id FROM users LIMIT 1), 'Reading', 'bookmark',  3);
SQL

mk_task() {
  local title="$1" project_id="$2" due="$3" important="$4" notes="${5:-}"
  local args=(-d "title=$title")
  [[ -n "$project_id" ]] && args+=(-d "project_id=$project_id")
  [[ -n "$due"        ]] && args+=(-d "due_date=$due")
  [[ "$important" == "1" ]] && args+=(-d "is_important=1")
  [[ -n "$notes"      ]] && args+=(--data-urlencode "notes=$notes")
  curl -fs -X POST "$BASE/tasks" -b "$COOKIES" "${args[@]}" -o /dev/null
}

TODAY=$(date -I)
TOMORROW=$(date -I -d "+1 day")
PLUS4=$(date -I -d "+4 days")
PLUS8=$(date -I -d "+8 days")
PLUS14=$(date -I -d "+14 days")
PLUS22=$(date -I -d "+22 days")
YESTERDAY=$(date -I -d "yesterday")

note "seeding active tasks"
# Today (due today / overdue / no-date)
mk_task "Review pull request from Sam"            ""           "$TODAY"     0 ""
mk_task "Prep slides for Friday all-hands"        "$PROJ_WORK" "$TODAY"     0 ""
mk_task "Call dentist about next checkup"         ""           ""           1 ""
mk_task "Pick up dry cleaning"                    "$PROJ_HOME" "$TODAY"     0 ""

READING_NOTES='## Reading goals
- Chapter 4: Encoding and Evolution
- Make notes on backwards/forwards compatibility
- Compare Avro vs Protobuf takeaways

> Stop at section 4.3 if running short on time.'
mk_task "Finish chapter 4 of Designing Data-Intensive Applications" \
                                                  "$PROJ_READ" "$TODAY"     0 "$READING_NOTES"

mk_task "Send invoice to Acme"                    "$PROJ_WORK" "$YESTERDAY" 1 ""

# Inbox (no project, no date) — also surfaces in Today by GTD semantics.
mk_task "Look into that podcast Maria mentioned"  ""           ""           0 ""
mk_task "Check warranty on the lawn mower"        ""           ""           0 ""
mk_task "Try the new pasta place on 4th"          ""           ""           0 ""

# Upcoming
mk_task "Plant the tomatoes"                      "$PROJ_HOME" "$TOMORROW"  0 ""
mk_task "Quarterly retro sync"                    "$PROJ_WORK" "$PLUS4"     0 ""
mk_task "Pick up library hold"                    "$PROJ_READ" "$PLUS8"     0 ""
mk_task "Performance reviews due"                 "$PROJ_WORK" "$PLUS14"    1 ""
mk_task "Renew car insurance"                     "$PROJ_HOME" "$PLUS22"    0 ""

note "seeding logbook (completed tasks)"
seed_completed() {
  local title="$1" project="$2" days_ago="$3"
  mk_task "$title" "$project" "" 0 ""
  # SQLite has no $$ literals; double-up apostrophes in the title so the
  # SQL string survives "Reply to Hannah's email" et al.
  local sql_title="${title//\'/\'\'}"
  sqlite3 "$DB" \
    "UPDATE tasks SET completed_at = datetime('now', '-${days_ago} day') \
     WHERE id = (SELECT id FROM tasks \
                  WHERE title = '${sql_title}' AND completed_at IS NULL \
                  ORDER BY created_at DESC LIMIT 1);"
}
seed_completed "Submit expenses for April"          "$PROJ_WORK" 0
seed_completed "Order birthday present for Mum"     "$PROJ_HOME" 0
seed_completed "Reply to Hannah's email"            ""           1
seed_completed "Book table for Saturday"            "$PROJ_HOME" 1
seed_completed "Read Patterns of Distributed Systems chapter 7" \
                                                    "$PROJ_READ" 1
seed_completed "Onboard new contractor"             "$PROJ_WORK" 2
seed_completed "Renew domain registration"          ""           2
seed_completed "Take recycling out"                 "$PROJ_HOME" 3
seed_completed "Skim the Cloudflare blog backlog"   "$PROJ_READ" 3

# ---------------------------------------------------------------------------
# 4. Capture screenshots via Playwright. Install on first run only.
# ---------------------------------------------------------------------------
mkdir -p "$PW_CACHE"
if [[ ! -d "$PW_CACHE/node_modules/playwright" ]]; then
  note "first-run: installing Playwright into scripts/.cache (~150MB, takes a minute)"
  ( cd "$PW_CACHE" \
    && [[ -f package.json ]] || echo '{"name":"todostuff-screenshots","private":true,"type":"module"}' > package.json \
    && npm install --silent --no-audit --no-fund playwright >/dev/null )
  ( cd "$PW_CACHE" && npx --yes playwright install chromium >/dev/null )
fi

note "capturing screenshots"
# Node's ESM resolver looks up bare imports relative to the running file's
# directory — NODE_PATH doesn't apply. Copy the script into the cache dir
# so `import 'playwright'` finds the local install.
cp "$ROOT/scripts/screenshots.mjs" "$PW_CACHE/screenshots.mjs"
(
  cd "$PW_CACHE" && \
  TODOSTUFF_BASE="$BASE" \
  TODOSTUFF_EMAIL="$EMAIL" \
  TODOSTUFF_PASSWORD="$PASSWORD" \
  TODOSTUFF_PROJ_READ="$PROJ_READ" \
  TODOSTUFF_OUT="$ROOT/docs/screenshots" \
    node ./screenshots.mjs
)

note "done — see docs/screenshots/"
