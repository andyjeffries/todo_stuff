package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/andyjessop/todostuff/internal/auth"
	"github.com/andyjessop/todostuff/internal/database"
	"github.com/andyjessop/todostuff/internal/notifications"
	"github.com/andyjessop/todostuff/internal/services"
	"github.com/andyjessop/todostuff/migrations"
	"github.com/go-chi/chi/v5"
)

// newAPIServer wires the /api/v1 router against a throwaway SQLite file.
func newAPIServer(t *testing.T) http.Handler {
	t.Helper()
	db, err := database.Open(filepath.Join(t.TempDir(), "api.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := database.Migrate(db, migrations.FS); err != nil {
		t.Fatal(err)
	}
	h := New(auth.NewService(db, false), services.NewTasks(db), services.NewProjects(db),
		notifications.NewPushover(""), nil)
	r := chi.NewRouter()
	r.Mount("/api/v1", h.APIRouter())
	return r
}

type obj = map[string]any

type apiClient struct {
	t     *testing.T
	h     http.Handler
	token string
}

// do sends a request and decodes any JSON response body into a map.
func (c *apiClient) do(method, path string, body any) (int, obj) {
	c.t.Helper()
	var rdr *bytes.Reader
	switch b := body.(type) {
	case nil:
		rdr = bytes.NewReader(nil)
	case string:
		rdr = bytes.NewReader([]byte(b))
	default:
		raw, _ := json.Marshal(b)
		rdr = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, "/api/v1"+path, rdr)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	rec := httptest.NewRecorder()
	c.h.ServeHTTP(rec, req)
	var out obj
	if rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			c.t.Fatalf("%s %s: non-JSON response %q", method, path, rec.Body.String())
		}
	}
	return rec.Code, out
}

func (c *apiClient) must(want int, method, path string, body any) obj {
	c.t.Helper()
	code, out := c.do(method, path, body)
	if code != want {
		c.t.Fatalf("%s %s: status %d, want %d; body %v", method, path, code, want, out)
	}
	return out
}

func errCode(o obj) string {
	e, _ := o["error"].(obj)
	s, _ := e["code"].(string)
	return s
}

func ids(o obj, key string) []string {
	var out []string
	for _, v := range o[key].([]any) {
		out = append(out, v.(obj)["id"].(string))
	}
	return out
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

// setupAdmin runs first-run setup and returns an authenticated client.
func setupAdmin(t *testing.T, h http.Handler) *apiClient {
	c := &apiClient{t: t, h: h}
	out := c.must(201, "POST", "/setup", obj{"email": "admin@example.com", "password": "password1", "name": "Admin"})
	c.token = out["token"].(string)
	return c
}

func TestAPIAuthAndSetup(t *testing.T) {
	h := newAPIServer(t)
	anon := &apiClient{t: t, h: h}

	if st := anon.must(200, "GET", "/status", nil); st["needs_setup"] != true {
		t.Fatalf("needs_setup = %v, want true", st["needs_setup"])
	}
	if _, out := anon.do("GET", "/me", nil); errCode(out) != "unauthorized" {
		t.Fatalf("anon /me: %v", out)
	}

	admin := setupAdmin(t, h)
	if st := anon.must(200, "GET", "/status", nil); st["needs_setup"] != false {
		t.Fatal("needs_setup still true after setup")
	}
	if code, _ := anon.do("POST", "/setup", obj{"email": "x@y.z", "password": "password1", "name": "X"}); code != 409 {
		t.Fatalf("second setup: %d, want 409", code)
	}
	if me := admin.must(200, "GET", "/me", nil); me["is_admin"] != true || me["pushover_user_key"] != nil {
		t.Fatalf("me: %v", me)
	}

	if code, _ := anon.do("POST", "/auth/login", obj{"email": "admin@example.com", "password": "wrong"}); code != 401 {
		t.Fatalf("bad login: %d", code)
	}
	login := anon.must(200, "POST", "/auth/login", obj{"email": "ADMIN@example.com", "password": "password1"})
	second := &apiClient{t: t, h: h, token: login["token"].(string)}
	exp, _ := time.Parse(time.RFC3339, login["expires_at"].(string))
	if time.Until(exp) < 300*24*time.Hour {
		t.Fatalf("default login should be long-lived, expires %v", exp)
	}
	second.must(204, "POST", "/auth/logout", nil)
	if code, _ := second.do("GET", "/me", nil); code != 401 {
		t.Fatalf("revoked token still works: %d", code)
	}
	if code, _ := admin.do("GET", "/me", nil); code != 200 {
		t.Fatal("logout of one token revoked another")
	}

	bogus := &apiClient{t: t, h: h, token: "nope"}
	if code, _ := bogus.do("GET", "/me", nil); code != 401 {
		t.Fatalf("bogus token: %d", code)
	}

	// Profile + password.
	me := admin.must(200, "PATCH", "/me", obj{"name": "Boss", "pushover_user_key": "ukey"})
	if me["name"] != "Boss" || me["pushover_enabled"] != true || me["pushover_user_key"] != "ukey" {
		t.Fatalf("patch me: %v", me)
	}
	me = admin.must(200, "PATCH", "/me", obj{"pushover_user_key": nil})
	if me["name"] != "Boss" || me["pushover_enabled"] != false || me["pushover_user_key"] != nil {
		t.Fatalf("clear pushover: %v", me)
	}
	if _, out := admin.do("POST", "/me/password", obj{"current_password": "wrong", "new_password": "password2"}); errCode(out) != "incorrect_password" {
		t.Fatalf("wrong current password: %v", out)
	}
	admin.must(204, "POST", "/me/password", obj{"current_password": "password1", "new_password": "password2"})
	anon.must(200, "POST", "/auth/login", obj{"email": "admin@example.com", "password": "password2"})
	if _, out := admin.do("POST", "/me/pushover/test", nil); errCode(out) != "pushover_unavailable" {
		t.Fatalf("pushover test without server token: %v", out)
	}
}

func TestAPIRequestValidation(t *testing.T) {
	h := newAPIServer(t)
	c := setupAdmin(t, h)

	if _, out := c.do("GET", "/nope", nil); errCode(out) != "not_found" {
		t.Fatalf("unknown endpoint: %v", out)
	}
	if code, out := c.do("PUT", "/tasks", obj{}); code != 405 || errCode(out) != "method_not_allowed" {
		t.Fatalf("wrong method: %d %v", code, out)
	}

	req := httptest.NewRequest("POST", "/api/v1/tasks", strings.NewReader(`title=x`))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Authorization", "Bearer "+c.token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 415 {
		t.Fatalf("form body: %d, want 415", rec.Code)
	}

	if code, _ := c.do("POST", "/tasks", obj{"title": "x", "titel": "typo"}); code != 400 {
		t.Fatalf("unknown field: %d", code)
	}
	if code, _ := c.do("POST", "/tasks", obj{"title": 42}); code != 422 {
		t.Fatalf("wrong type: %d", code)
	}
	if code, _ := c.do("POST", "/tasks", obj{}); code != 422 {
		t.Fatalf("missing title: %d", code)
	}
	for _, bad := range []obj{
		{"title": "x", "due_date": "28/09/2026"},
		{"title": "x", "due_time": "09:00"},
		{"title": "x", "due_date": "2026-09-28", "due_time": "9am"},
		{"title": "x", "due_date": "2026-09-28", "reminder_offset_minutes": 15},
		{"title": "x", "due_date": "2026-09-28", "due_time": "09:00", "reminder_offset_minutes": 7},
		{"title": "x", "recurrence": obj{"frequency": "fortnights"}},
		{"title": "x", "project_id": "not-a-project"},
	} {
		if code, out := c.do("POST", "/tasks", bad); code != 422 {
			t.Fatalf("%v: %d %v, want 422", bad, code, out)
		}
	}
	if code, _ := c.do("GET", "/tasks?view=someday", nil); code != 400 {
		t.Fatalf("bad view: %d", code)
	}
}

func TestAPITasksAndProjects(t *testing.T) {
	h := newAPIServer(t)
	c := setupAdmin(t, h)
	// Smart lists bucket by the UTC date (services.viewQuery), so use that
	// to keep the view assertions stable in any TZ.
	now := time.Now().UTC()
	today := now.Format("2006-01-02")
	tomorrow := now.AddDate(0, 0, 1).Format("2006-01-02")

	// Projects.
	work := c.must(201, "POST", "/projects", obj{"name": "Work", "icon": "briefcase"})
	home := c.must(201, "POST", "/projects", obj{"name": "Home"})
	workID, homeID := work["id"].(string), home["id"].(string)
	if home["icon"] != nil {
		t.Fatalf("icon should be null: %v", home)
	}
	home = c.must(200, "PATCH", "/projects/"+homeID, obj{"icon": "home"})
	if home["icon"] != "home" || home["name"] != "Home" {
		t.Fatalf("patch project: %v", home)
	}
	c.must(204, "POST", "/projects/reorder", obj{"ids": []string{homeID, workID}})
	if got := ids(c.must(200, "GET", "/projects", nil), "projects"); got[0] != homeID {
		t.Fatalf("reorder: %v", got)
	}
	c.must(204, "POST", "/projects/"+homeID+"/move", obj{"direction": "down"})
	if got := ids(c.must(200, "GET", "/projects", nil), "projects"); got[0] != workID {
		t.Fatalf("move: %v", got)
	}

	// Tasks land in the same smart lists as the web UI.
	inbox := c.must(201, "POST", "/tasks", obj{"title": "Capture"})
	dated := c.must(201, "POST", "/tasks", obj{
		"title": "Call", "notes": "**bold**", "project_id": workID, "is_important": true,
		"due_date": tomorrow, "due_time": "09:30", "reminder_offset_minutes": 15,
	})
	inboxID, datedID := inbox["id"].(string), dated["id"].(string)
	if dated["project_name"] != "Work" || dated["project_icon"] != "briefcase" ||
		dated["due_time"] != "09:30" || dated["reminder_offset_minutes"] != float64(15) ||
		!strings.Contains(dated["notes_html"].(string), "<strong>bold</strong>") {
		t.Fatalf("created task: %v", dated)
	}
	due, _ := time.ParseInLocation("2006-01-02 15:04", tomorrow+" 09:30", time.Local)
	if at, _ := time.Parse(time.RFC3339, dated["reminder_at"].(string)); !at.Equal(due.Add(-15 * time.Minute)) {
		t.Fatalf("reminder_at %v, want %v", at, due.Add(-15*time.Minute))
	}

	listed := func(q string) []string { return ids(c.must(200, "GET", "/tasks?"+q, nil), "tasks") }
	if l := listed("view=inbox"); !contains(l, inboxID) || contains(l, datedID) {
		t.Fatalf("inbox: %v", l)
	}
	if l := listed("view=upcoming"); !contains(l, datedID) {
		t.Fatalf("upcoming: %v", l)
	}
	if l := listed("project_id=" + workID); len(l) != 1 || l[0] != datedID {
		t.Fatalf("project list: %v", l)
	}

	// Moving the due date moves the reminder; clearing the offset drops it.
	moved := c.must(200, "PATCH", "/tasks/"+datedID, obj{"due_date": today, "due_time": "10:00"})
	due, _ = time.ParseInLocation("2006-01-02 15:04", today+" 10:00", time.Local)
	if at, _ := time.Parse(time.RFC3339, moved["reminder_at"].(string)); !at.Equal(due.Add(-15 * time.Minute)) {
		t.Fatalf("moved reminder_at %v", moved["reminder_at"])
	}
	if moved["title"] != "Call" || moved["is_important"] != true {
		t.Fatalf("patch touched other fields: %v", moved)
	}
	if l := listed("view=today"); !contains(l, datedID) {
		t.Fatalf("today: %v", l)
	}
	cleared := c.must(200, "PATCH", "/tasks/"+datedID, obj{"reminder_offset_minutes": nil, "notes": nil, "project_id": nil})
	if cleared["reminder_at"] != nil || cleared["reminder_offset_minutes"] != nil ||
		cleared["notes"] != nil || cleared["project_id"] != nil || cleared["due_time"] != "10:00" {
		t.Fatalf("cleared: %v", cleared)
	}
	undated := c.must(200, "PATCH", "/tasks/"+datedID, obj{"due_date": nil})
	if undated["due_date"] != nil || undated["due_time"] != nil {
		t.Fatalf("clearing date should clear time: %v", undated)
	}

	// Recurrence: completing regenerates the next instance.
	rec := c.must(201, "POST", "/tasks", obj{
		"title": "Water plants", "due_date": today,
		"recurrence": obj{"frequency": "weeks", "interval": 1, "regeneration_type": "on_create"},
	})
	recID := rec["id"].(string)
	if r := rec["recurrence"].(obj); r["frequency"] != "weeks" || r["interval"] != float64(1) {
		t.Fatalf("recurrence: %v", rec["recurrence"])
	}
	// Renaming a recurring task carries over to future instances.
	c.must(200, "PATCH", "/tasks/"+recID, obj{"title": "Water all plants"})
	done := c.must(200, "POST", "/tasks/"+recID+"/complete", nil)
	next, _ := done["next_task"].(obj)
	if done["task"].(obj)["completed_at"] == nil || next == nil ||
		next["title"] != "Water all plants" || next["due_date"] != now.AddDate(0, 0, 7).Format("2006-01-02") {
		t.Fatalf("complete recurring: %v", done)
	}
	if again := c.must(200, "POST", "/tasks/"+recID+"/complete", nil); again["next_task"] != nil {
		t.Fatal("completing twice regenerated twice")
	}
	if l := listed("view=logbook"); !contains(l, recID) {
		t.Fatalf("logbook: %v", l)
	}
	if u := c.must(200, "POST", "/tasks/"+recID+"/uncomplete", nil); u["completed_at"] != nil {
		t.Fatalf("uncomplete: %v", u)
	}
	stopped := c.must(200, "PATCH", "/tasks/"+next["id"].(string), obj{"recurrence": nil})
	if stopped["recurrence"] != nil {
		t.Fatalf("remove recurrence: %v", stopped)
	}

	// Search, reorder, delete.
	if l := ids(c.must(200, "GET", "/search?q=plants", nil), "tasks"); len(l) != 2 {
		t.Fatalf("search: %v", l)
	}
	c.must(204, "POST", "/tasks/reorder", obj{"ids": []string{datedID, inboxID}})
	c.must(204, "DELETE", "/tasks/"+inboxID, nil)
	if code, out := c.do("GET", "/tasks/"+inboxID, nil); code != 404 || errCode(out) != "not_found" {
		t.Fatalf("deleted task: %d %v", code, out)
	}

	// Deleting a project keeps its tasks.
	c.must(200, "PATCH", "/tasks/"+datedID, obj{"project_id": homeID})
	c.must(204, "DELETE", "/projects/"+homeID, nil)
	if t2 := c.must(200, "GET", "/tasks/"+datedID, nil); t2["project_id"] != nil {
		t.Fatalf("task after project delete: %v", t2)
	}

	// Users can't see or use each other's data.
	c.must(201, "POST", "/admin/users", obj{"email": "b@example.com", "password": "password1", "name": "B"})
	other := &apiClient{t: t, h: h}
	other.token = other.must(200, "POST", "/auth/login", obj{"email": "b@example.com", "password": "password1"})["token"].(string)
	if code, _ := other.do("GET", "/tasks/"+datedID, nil); code != 404 {
		t.Fatalf("cross-user task read: %d", code)
	}
	if code, _ := other.do("POST", "/tasks", obj{"title": "x", "project_id": workID}); code != 422 {
		t.Fatalf("cross-user project assignment: %d", code)
	}
}

func TestAPIRemindersAndSync(t *testing.T) {
	h := newAPIServer(t)
	c := setupAdmin(t, h)
	past := time.Now().AddDate(0, 0, -2).Format("2006-01-02")

	tok1 := c.must(200, "GET", "/sync", nil)["sync_token"]
	task := c.must(201, "POST", "/tasks", obj{
		"title": "Overdue", "due_date": past, "due_time": "12:00", "reminder_offset_minutes": 0,
	})
	tok2 := c.must(200, "GET", "/sync", nil)["sync_token"]
	if tok1 == tok2 {
		t.Fatal("sync token unchanged after create")
	}
	if tok3 := c.must(200, "GET", "/sync", nil)["sync_token"]; tok3 != tok2 {
		t.Fatal("sync token changed without a write")
	}

	claimed := c.must(200, "POST", "/reminders/claim", nil)["reminders"].([]any)
	if len(claimed) != 1 || claimed[0].(obj)["id"] != task["id"] {
		t.Fatalf("claim: %v", claimed)
	}
	if again := c.must(200, "POST", "/reminders/claim", nil)["reminders"].([]any); len(again) != 0 {
		t.Fatalf("reminder delivered twice: %v", again)
	}

	c.must(204, "DELETE", "/tasks/"+task["id"].(string), nil)
	if tok4 := c.must(200, "GET", "/sync", nil)["sync_token"]; tok4 == tok2 {
		t.Fatal("sync token unchanged after delete")
	}
}

func TestAPIAdmin(t *testing.T) {
	h := newAPIServer(t)
	admin := setupAdmin(t, h)
	me := admin.must(200, "GET", "/me", nil)
	adminID := me["id"].(string)

	u := admin.must(201, "POST", "/admin/users", obj{"email": "u@example.com", "password": "password1", "name": "U"})
	uID := u["id"].(string)
	if _, ok := u["pushover_user_key"]; ok {
		t.Fatal("admin view leaks pushover key")
	}
	if _, out := admin.do("POST", "/admin/users", obj{"email": "u@example.com", "password": "password1", "name": "U2"}); errCode(out) != "email_taken" {
		t.Fatalf("duplicate email: %v", out)
	}

	user := &apiClient{t: t, h: h}
	user.token = user.must(200, "POST", "/auth/login", obj{"email": "u@example.com", "password": "password1"})["token"].(string)
	if _, out := user.do("GET", "/admin/users", nil); errCode(out) != "forbidden" {
		t.Fatalf("non-admin: %v", out)
	}

	if l := ids(admin.must(200, "GET", "/admin/users", nil), "users"); len(l) != 2 {
		t.Fatalf("list users: %v", l)
	}
	if p := admin.must(200, "PATCH", "/admin/users/"+uID, obj{"name": "Ursula"}); p["name"] != "Ursula" || p["email"] != "u@example.com" {
		t.Fatalf("patch user: %v", p)
	}
	if _, out := admin.do("PATCH", "/admin/users/"+adminID, obj{"is_admin": false}); errCode(out) != "cannot_demote_self" {
		t.Fatalf("self demote: %v", out)
	}
	if _, out := admin.do("DELETE", "/admin/users/"+adminID, nil); errCode(out) != "cannot_delete_self" {
		t.Fatalf("self delete: %v", out)
	}

	admin.must(204, "POST", "/admin/users/"+uID+"/reset-password", obj{"new_password": "password9"})
	if code, _ := user.do("GET", "/me", nil); code != 401 {
		t.Fatal("password reset should revoke the user's tokens")
	}
	admin.must(204, "DELETE", "/admin/users/"+uID, nil)
	if code, _ := admin.do("GET", "/admin/users/"+uID, nil); code != 404 {
		t.Fatal("deleted user still readable")
	}
}
