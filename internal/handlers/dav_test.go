package handlers

import (
	"bytes"
	"context"
	"crypto/md5"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/andyjessop/todostuff/internal/auth"
	"github.com/andyjessop/todostuff/internal/caldav"
	"github.com/andyjessop/todostuff/internal/database"
	"github.com/andyjessop/todostuff/internal/models"
	"github.com/andyjessop/todostuff/internal/notifications"
	"github.com/andyjessop/todostuff/internal/services"
	"github.com/andyjessop/todostuff/migrations"
	"github.com/go-chi/chi/v5"
)

type davEnv struct {
	t        *testing.T
	h        http.Handler
	tasks    *services.Tasks
	projects *services.Projects
	user     *models.User
	email    string
	pw       string
}

func newDAVEnv(t *testing.T) *davEnv {
	t.Helper()
	loc, err := time.LoadLocation("Europe/London")
	if err != nil {
		t.Fatal(err)
	}
	prev := time.Local
	time.Local = loc
	t.Cleanup(func() { time.Local = prev })

	db, err := database.Open(filepath.Join(t.TempDir(), "dav.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := database.Migrate(db, migrations.FS); err != nil {
		t.Fatal(err)
	}
	authSvc := auth.NewService(db, false)
	tasks, projects := services.NewTasks(db), services.NewProjects(db)
	ctx := context.Background()
	u, err := authSvc.CreateUser(ctx, auth.CreateUserParams{Email: "andy@example.com", Password: "account-password", Name: "Andy", IsAdmin: true})
	if err != nil {
		t.Fatal(err)
	}
	_, pw, err := authSvc.CreateAppPassword(ctx, u.ID, "iPhone")
	if err != nil {
		t.Fatal(err)
	}
	h := New(authSvc, tasks, projects, notifications.NewPushover(""), nil)
	r := chi.NewRouter()
	dav := h.DAVHandler()
	r.Handle("/dav", dav)
	r.Handle("/dav/*", dav)
	r.Handle("/.well-known/caldav", dav)
	r.Method("PROPFIND", "/", dav)
	return &davEnv{t: t, h: r, tasks: tasks, projects: projects, user: u, email: u.Email, pw: pw}
}

func (e *davEnv) do(method, path, body string, hdr ...string) (int, string, http.Header) {
	e.t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.SetBasicAuth(e.email, e.pw)
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	b, _ := io.ReadAll(rec.Body)
	return rec.Code, string(b), rec.Header()
}

func (e *davEnv) coll(id string) string { return "/dav/calendars/" + e.user.ID + "/" + id + "/" }

func (e *davEnv) task(uid string) *models.Task {
	e.t.Helper()
	tk, err := e.tasks.GetByCalDAVUID(context.Background(), e.user.ID, uid)
	if err != nil {
		e.t.Fatalf("task %s: %v", uid, err)
	}
	return tk
}

func mustContain(t *testing.T, body string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(body, w) {
			t.Fatalf("response missing %q:\n%s", w, body)
		}
	}
}

// vtodo builds an iOS-style reminder body.
func vtodo(uid, summary string, lines ...string) string {
	return "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//Apple Inc.//iOS 18.0//EN\r\nBEGIN:VTODO\r\n" +
		"UID:" + uid + "\r\nDTSTAMP:20261020T101512Z\r\nSUMMARY:" + summary + "\r\n" +
		"X-APPLE-SORT-ORDER:742000000\r\n" +
		strings.Join(lines, "\r\n") + "\r\nEND:VTODO\r\nEND:VCALENDAR\r\n"
}

const propfindAll = `<?xml version="1.0"?><d:propfind xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav" xmlns:cs="http://calendarserver.org/ns/">
<d:prop><d:current-user-principal/><c:calendar-home-set/><d:resourcetype/><d:displayname/><cs:getctag/><d:getetag/><c:supported-calendar-component-set/><d:sync-token/></d:prop></d:propfind>`

func TestDAVAuth(t *testing.T) {
	e := newDAVEnv(t)
	req := httptest.NewRequest("PROPFIND", "/dav/", nil)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	if rec.Code != 401 || !strings.Contains(strings.Join(rec.Header().Values("WWW-Authenticate"), " "), "Basic") {
		t.Fatalf("no auth: %d %v", rec.Code, rec.Header())
	}
	e.pw = "account-password" // the real password is not a CalDAV credential
	if code, _, _ := e.do("PROPFIND", "/dav/", propfindAll); code != 401 {
		t.Fatalf("account password: got %d, want 401", code)
	}
	if code, _, _ := e.do(http.MethodOptions, "/dav/", ""); code != 200 {
		t.Fatalf("OPTIONS should be open: %d", code)
	}
}

func TestDAVDiscovery(t *testing.T) {
	e := newDAVEnv(t)
	p, err := e.projects.Create(context.Background(), e.user.ID, services.CreateProjectParams{Name: "Shopping & Errands"})
	if err != nil {
		t.Fatal(err)
	}

	code, body, _ := e.do("PROPFIND", "/.well-known/caldav", propfindAll, "Depth", "0")
	if code != 207 {
		t.Fatalf("well-known: %d", code)
	}
	mustContain(t, body, "<d:current-user-principal><d:href>/dav/principals/"+e.user.ID+"/</d:href>")

	_, root, _ := e.do("PROPFIND", "/", propfindAll, "Depth", "0")
	mustContain(t, root, "<d:current-user-principal><d:href>/dav/principals/"+e.user.ID+"/</d:href>")

	_, body, _ = e.do("PROPFIND", "/dav/principals/"+e.user.ID+"/", propfindAll, "Depth", "0")
	mustContain(t, body, "<c:calendar-home-set><d:href>/dav/calendars/"+e.user.ID+"/</d:href>")

	_, body, _ = e.do("PROPFIND", "/dav/calendars/"+e.user.ID+"/", propfindAll, "Depth", "1")
	mustContain(t, body,
		"<d:href>"+e.coll("inbox")+"</d:href>",
		"<d:href>"+e.coll(p.ID)+"</d:href>",
		"<d:displayname>Shopping &amp; Errands</d:displayname>",
		`<c:comp name="VTODO"/>`, "<cs:getctag>", "<d:collection/><c:calendar/>",
		"404 Not Found") // sync-token isn't offered

	if code, _, _ := e.do("PROPFIND", "/dav/calendars/someone-else/", propfindAll); code != 403 {
		t.Fatalf("other user's home: %d", code)
	}
}

func TestDAVPutGetCompleteDelete(t *testing.T) {
	e := newDAVEnv(t)
	inbox := e.coll("inbox")
	_, before, _ := e.do("PROPFIND", inbox, propfindAll, "Depth", "0")

	body := vtodo("IOS-1", "Call the bank",
		"DTSTART;TZID=Europe/London:20261020T150000",
		"DUE;TZID=Europe/London:20261020T150000",
		"STATUS:NEEDS-ACTION",
		"BEGIN:VALARM\r\nACTION:DISPLAY\r\nTRIGGER;VALUE=DATE-TIME:20261020T140000Z\r\nEND:VALARM")
	code, _, hdr := e.do(http.MethodPut, inbox+"IOS-1.ics", body, "If-None-Match", "*")
	if code != 201 || hdr.Get("ETag") != "" {
		t.Fatalf("create: %d etag=%q", code, hdr.Get("ETag"))
	}
	tk := e.task("IOS-1")
	if tk.Title != "Call the bank" || tk.ProjectID.Valid || hhmm(tk.DueTime.String) != "15:00" ||
		!tk.ReminderOffsetMinutes.Valid || tk.ReminderOffsetMinutes.Int64 != 0 ||
		!tk.ReminderAt.Time.Equal(time.Date(2026, 10, 20, 14, 0, 0, 0, time.UTC)) {
		t.Fatalf("stored task: %+v", tk)
	}
	if tk.CalDAVName() != "IOS-1.ics" {
		t.Fatalf("href: %s", tk.CalDAVName())
	}

	// Creating it again at the same href is refused.
	if code, _, _ := e.do(http.MethodPut, inbox+"IOS-1.ics", body, "If-None-Match", "*"); code != 412 {
		t.Fatalf("If-None-Match on existing: %d", code)
	}

	_, after, _ := e.do("PROPFIND", inbox, propfindAll, "Depth", "0")
	if ctag(before) == ctag(after) {
		t.Fatal("getctag didn't change after a PUT")
	}

	code, ics, hdr := e.do(http.MethodGet, inbox+"IOS-1.ics", "")
	if code != 200 {
		t.Fatalf("GET: %d", code)
	}
	mustContain(t, ics, "UID:IOS-1", "X-APPLE-SORT-ORDER:742000000", "DUE;TZID=Europe/London:20261020T150000")
	etag := hdr.Get("ETag")

	if code, _, _ := e.do(http.MethodPut, inbox+"IOS-1.ics", body, "If-Match", `"stale"`); code != 412 {
		t.Fatalf("stale If-Match: %d", code)
	}

	done := strings.Replace(body, "STATUS:NEEDS-ACTION", "STATUS:COMPLETED\r\nCOMPLETED:20261020T150500Z", 1)
	if code, _, _ := e.do(http.MethodPut, inbox+"IOS-1.ics", done, "If-Match", etag); code != 204 {
		t.Fatalf("complete: %d", code)
	}
	if !e.task("IOS-1").CompletedAt.Valid {
		t.Fatal("not completed")
	}

	// An open-only query (what a client asks for to skip the logbook)
	// leaves it out; a plain one doesn't.
	open := `<c:calendar-query xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav"><d:prop><d:getetag/></d:prop>
<c:filter><c:comp-filter name="VCALENDAR"><c:comp-filter name="VTODO"><c:prop-filter name="COMPLETED"><c:is-not-defined/></c:prop-filter></c:comp-filter></c:comp-filter></c:filter></c:calendar-query>`
	if _, q, _ := e.do("REPORT", inbox, open, "Depth", "1"); strings.Contains(q, "IOS-1.ics") {
		t.Fatalf("open-only query returned a completed task:\n%s", q)
	}
	all := `<c:calendar-query xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav"><d:prop><d:getetag/></d:prop>
<c:filter><c:comp-filter name="VCALENDAR"><c:comp-filter name="VTODO"/></c:comp-filter></c:filter></c:calendar-query>`
	_, q, _ := e.do("REPORT", inbox, all, "Depth", "1")
	mustContain(t, q, "IOS-1.ics")

	if code, _, _ := e.do(http.MethodDelete, inbox+"IOS-1.ics", ""); code != 204 {
		t.Fatalf("delete: %d", code)
	}
	if _, err := e.tasks.GetByCalDAVUID(context.Background(), e.user.ID, "IOS-1"); err == nil {
		t.Fatal("task survived DELETE")
	}
}

func ctag(body string) string {
	_, rest, _ := strings.Cut(body, "<cs:getctag>")
	v, _, _ := strings.Cut(rest, "<")
	return v
}

func TestDAVExistingTasksAndMultiget(t *testing.T) {
	e := newDAVEnv(t)
	ctx := context.Background()
	p, _ := e.projects.Create(ctx, e.user.ID, services.CreateProjectParams{Name: "Admin"})
	due := time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC)
	tk, err := e.tasks.Create(ctx, e.user.ID, services.CreateTaskParams{Title: "File taxes", ProjectID: p.ID, DueDate: &due, DueTime: "09:30"})
	if err != nil {
		t.Fatal(err)
	}

	_, list, _ := e.do("PROPFIND", e.coll(p.ID), propfindAll, "Depth", "1")
	href := e.coll(p.ID) + tk.ID + ".ics"
	mustContain(t, list, "<d:href>"+href+"</d:href>", "<d:getetag>")

	mg := `<c:calendar-multiget xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav"><d:prop><d:getetag/><c:calendar-data/></d:prop>
<d:href>` + href + `</d:href><d:href>` + e.coll(p.ID) + `missing.ics</d:href></c:calendar-multiget>`
	code, body, _ := e.do("REPORT", e.coll(p.ID), mg, "Depth", "1")
	if code != 207 {
		t.Fatalf("multiget: %d", code)
	}
	mustContain(t, body, "SUMMARY:File taxes", "DUE;TZID=Europe/London:20261021T093000", "TRIGGER:PT0S", "404 Not Found")

	// Not visible in the inbox collection.
	if code, _, _ := e.do(http.MethodGet, e.coll("inbox")+tk.ID+".ics", ""); code != 404 {
		t.Fatalf("task leaked into inbox: %d", code)
	}
}

func TestDAVMoveBetweenLists(t *testing.T) {
	e := newDAVEnv(t)
	p, _ := e.projects.Create(context.Background(), e.user.ID, services.CreateProjectParams{Name: "Home"})
	body := vtodo("IOS-2", "Fix the gate")
	if code, _, _ := e.do(http.MethodPut, e.coll("inbox")+"IOS-2.ics", body); code != 201 {
		t.Fatalf("create: %d", code)
	}
	// iOS moves a reminder by PUTting it into the new list, then deleting
	// the old copy.
	if code, _, _ := e.do(http.MethodPut, e.coll(p.ID)+"IOS-2.ics", body, "If-None-Match", "*"); code != 204 {
		t.Fatalf("move PUT: %d", code)
	}
	if code, _, _ := e.do(http.MethodDelete, e.coll("inbox")+"IOS-2.ics", ""); code != 204 {
		t.Fatalf("old-copy DELETE: %d", code)
	}
	tk := e.task("IOS-2")
	if tk.ProjectID.String != p.ID {
		t.Fatalf("not moved: %+v", tk.ProjectID)
	}
}

func TestDAVRecurringCompletion(t *testing.T) {
	e := newDAVEnv(t)
	inbox := e.coll("inbox")
	weekly := func(due string, extra ...string) string {
		return vtodo("IOS-R", "Bins out", append([]string{
			"DTSTART;VALUE=DATE:" + due, "DUE;VALUE=DATE:" + due, "RRULE:FREQ=WEEKLY;INTERVAL=1"}, extra...)...)
	}
	if code, _, _ := e.do(http.MethodPut, inbox+"IOS-R.ics", weekly("20261020", "STATUS:NEEDS-ACTION")); code != 201 {
		t.Fatalf("create: %d", code)
	}
	if tk := e.task("IOS-R"); tk.RecurrenceFrequency.String != "weeks" {
		t.Fatalf("recurrence not stored: %+v", tk)
	}

	// Apple style: the series advances in place and a completed copy with a
	// new UID records the occurrence.
	if code, _, _ := e.do(http.MethodPut, inbox+"IOS-R.ics", weekly("20261027", "STATUS:NEEDS-ACTION")); code != 204 {
		t.Fatalf("advance: %d", code)
	}
	copyBody := vtodo("IOS-R-DONE", "Bins out", "DUE;VALUE=DATE:20261020", "STATUS:COMPLETED", "COMPLETED:20261020T190000Z")
	if code, _, _ := e.do(http.MethodPut, inbox+"IOS-R-DONE.ics", copyBody); code != 201 {
		t.Fatalf("completed copy: %d", code)
	}
	series, occurrence := e.task("IOS-R"), e.task("IOS-R-DONE")
	if series.CompletedAt.Valid || series.DueDate.Time.Format("2006-01-02") != "2026-10-27" || !series.RecurrenceFrequency.Valid {
		t.Fatalf("series: %+v", series)
	}
	if !occurrence.CompletedAt.Valid || occurrence.RecurrenceRuleID.Valid {
		t.Fatalf("occurrence: %+v", occurrence)
	}

	// Generic style: completing the series itself regenerates the next one
	// server-side, exactly as the app does.
	if code, _, _ := e.do(http.MethodPut, inbox+"IOS-R.ics", weekly("20261027", "STATUS:COMPLETED")); code != 204 {
		t.Fatalf("complete series: %d", code)
	}
	open, err := e.tasks.ListByView(context.Background(), e.user.ID, services.ViewAnytime)
	if err != nil {
		t.Fatal(err)
	}
	if len(open) != 1 || open[0].DueDate.Time.Format("2006-01-02") != "2026-11-03" {
		t.Fatalf("next instance: %+v", open)
	}
}

func TestDAVRejects(t *testing.T) {
	e := newDAVEnv(t)
	ev := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:x\r\nBEGIN:VEVENT\r\nUID:e\r\nDTSTAMP:20261020T101512Z\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	code, body, _ := e.do(http.MethodPut, e.coll("inbox")+"e.ics", ev)
	if code != 403 {
		t.Fatalf("VEVENT: %d", code)
	}
	mustContain(t, body, "supported-calendar-component")

	code, _, _ = e.do(http.MethodPut, e.coll("inbox")+"r.ics", vtodo("r", "x", "DUE;VALUE=DATE:20261020", "RRULE:FREQ=DAILY;COUNT=3"))
	if code != 403 {
		t.Fatalf("unsupported RRULE: %d", code)
	}
	if code, _, _ := e.do(http.MethodPut, e.coll("no-such-list")+"x.ics", vtodo("x", "x")); code != 409 {
		t.Fatalf("unknown list: %d", code)
	}
	if code, _, _ := e.do("MKCALENDAR", e.coll("new"), ""); code != 403 {
		t.Fatalf("MKCALENDAR: %d", code)
	}
}

// digestAuth answers a Digest challenge the way iOS does (qop=auth).
func digestAuth(t *testing.T, challenge, user, pw, method, uri string) string {
	t.Helper()
	_, rest, _ := strings.Cut(challenge, `nonce="`)
	nonce, _, _ := strings.Cut(rest, `"`)
	md5s := func(s string) string { return fmt.Sprintf("%x", md5.Sum([]byte(s))) }
	ha1 := md5s(user + ":TodoStuff:" + pw)
	ha2 := md5s(method + ":" + uri)
	resp := md5s(ha1 + ":" + nonce + ":00000001:abc123:auth:" + ha2)
	return fmt.Sprintf(`Digest username="%s", realm="TodoStuff", nonce="%s", uri="%s", response="%s", algorithm=MD5, qop=auth, nc=00000001, cnonce="abc123"`,
		user, nonce, uri, resp)
}

func TestDAVDigestAuth(t *testing.T) {
	e := newDAVEnv(t)
	req := httptest.NewRequest("PROPFIND", "/.well-known/caldav", strings.NewReader(propfindAll))
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	chs := rec.Result().Header.Values("WWW-Authenticate")
	if rec.Code != 401 || len(chs) != 2 || !strings.HasPrefix(chs[0], "Digest ") || !strings.HasPrefix(chs[1], "Basic ") {
		t.Fatalf("challenge: %d %q", rec.Code, chs)
	}

	try := func(pw string) int {
		req := httptest.NewRequest("PROPFIND", "/.well-known/caldav", strings.NewReader(propfindAll))
		req.Header.Set("Authorization", digestAuth(t, chs[0], e.email, pw, "PROPFIND", "/.well-known/caldav"))
		req.Header.Set("Depth", "0")
		rec := httptest.NewRecorder()
		e.h.ServeHTTP(rec, req)
		return rec.Code
	}
	if code := try(e.pw); code != 207 {
		t.Fatalf("digest with app password: %d", code)
	}
	if code := try("account-password"); code != 401 {
		t.Fatalf("digest with account password: %d", code)
	}
}

// TestDAVRealIOS27 replays request bodies captured from iOS 27.0.1
// (internal/caldav/testdata/ios27) during the Phase 0 sandbox test.
func TestDAVRealIOS27(t *testing.T) {
	e := newDAVEnv(t)
	ctx := context.Background()
	home, _ := e.projects.Create(ctx, e.user.ID, services.CreateProjectParams{Name: "Home"})
	put := func(coll, file string, want int) {
		t.Helper()
		body, err := os.ReadFile(filepath.Join("..", "caldav", "testdata", "ios27", file))
		if err != nil {
			t.Fatal(err)
		}
		td, err := caldav.Parse(bytes.NewReader(body), time.Local)
		if err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		if code, _, _ := e.do(http.MethodPut, e.coll(coll)+td.UID+".ics", string(body)); code != want {
			t.Fatalf("%s: got %d, want %d", file, code, want)
		}
	}

	// "Remind me to test one at 3pm": TZID=Europe/London, alarm at the time.
	put("inbox", "093229-ed362c7e.ics", 201)
	one := e.task("ED362C7E-4EFA-441B-BB16-6D47D8D64369")
	if hhmm(one.DueTime.String) != "15:00" || one.ReminderOffsetMinutes.Int64 != 0 || !one.ReminderOffsetMinutes.Valid {
		t.Fatalf("test one: %+v", one)
	}
	_, ics, _ := e.do(http.MethodGet, e.coll("inbox")+"ED362C7E-4EFA-441B-BB16-6D47D8D64369.ics", "")
	mustContain(t, ics, "DUE;TZID=Europe/London:20261020T150000", "BEGIN:VTIMEZONE")

	// Ticking off a weekly all-day reminder: iOS PUTs a completed copy under
	// a new UID, then advances the series by a week.
	put("inbox", "093229-3aa161ca.ics", 201)
	put("inbox", "093419-4387b8aa.ics", 201)
	put("inbox", "093419-3aa161ca.ics", 204)
	series, done := e.task("3aa161ca-41fd-481b-880b-e2bea36f2a2c"), e.task("4387B8AA-1E38-4154-88FB-7C7012BA92DD")
	if series.CompletedAt.Valid || series.DueDate.Time.Format("2006-01-02") != "2026-10-29" || series.RecurrenceFrequency.String != "weeks" {
		t.Fatalf("series: %+v", series)
	}
	if !done.CompletedAt.Valid || done.DueDate.Time.Format("2006-01-02") != "2026-10-22" {
		t.Fatalf("completed occurrence: %+v", done)
	}

	// Moving between lists: iOS deletes the old copy and creates a new one
	// with a fresh UID.
	put("inbox", "093408-94d023e5.ics", 201)
	if code, _, _ := e.do(http.MethodDelete, e.coll("inbox")+"94D023E5-60CE-4C39-80AC-3CF0D65C41F4.ics", ""); code != 204 {
		t.Fatalf("delete old copy: %d", code)
	}
	moved := strings.Replace(mustRead(t, "093408-94d023e5.ics"), "UID:94D023E5-60CE-4C39-80AC-3CF0D65C41F4", "UID:MOVED-COPY", 1)
	if code, _, _ := e.do(http.MethodPut, e.coll(home.ID)+"MOVED-COPY.ics", moved); code != 201 {
		t.Fatalf("new copy: %d", code)
	}
	if e.task("MOVED-COPY").ProjectID.String != home.ID {
		t.Fatal("moved copy isn't in Home")
	}
}

func mustRead(t *testing.T, file string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "caldav", "testdata", "ios27", file))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
