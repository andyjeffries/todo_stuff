package handlers

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/xml"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/andyjessop/todostuff/internal/auth"
	"github.com/andyjessop/todostuff/internal/caldav"
	"github.com/andyjessop/todostuff/internal/models"
	"github.com/andyjessop/todostuff/internal/services"
	"github.com/go-chi/chi/v5"
)

// CalDAV endpoint: every project is a VTODO collection, plus "inbox" for
// tasks with no project, so Apple Reminders (or any CalDAV client) can use
// TodoStuff as a reminders account. Auth is HTTP Digest (what iOS uses over
// plain HTTP) or Basic, with the account email and an app password
// (auth.AppPassword), never the real password.
//
//	/dav/                                  root (discovery)
//	/dav/principals/{user}/                principal
//	/dav/calendars/{user}/                 calendar home
//	/dav/calendars/{user}/{inbox|project}/ collection
//	/dav/calendars/{user}/{coll}/{name}    one task as a VTODO
//
// sync-collection isn't advertised, so clients fall back to getctag + ETag
// listing and no deletion tombstones are needed.

const (
	davPrefix   = "/dav/"
	davInbox    = "inbox"
	davMaxBody  = 1 << 20
	davTodoType = "text/calendar; charset=utf-8; component=VTODO"
)

func init() {
	for _, m := range []string{"PROPFIND", "PROPPATCH", "REPORT", "MKCALENDAR", "MKCOL"} {
		chi.RegisterMethod(m)
	}
}

type davKind int

const (
	davRoot davKind = iota
	davPrincipal
	davHome
	davCollection
	davObject
)

type davPath struct {
	kind   davKind
	userID string
	coll   string
	name   string
}

// parseDAVPath splits a request path under /dav/. Trailing slashes on
// collections are optional.
func parseDAVPath(p string) (davPath, bool) {
	rest, ok := strings.CutPrefix(p, "/dav")
	if !ok {
		return davPath{}, false
	}
	parts := strings.FieldsFunc(rest, func(r rune) bool { return r == '/' })
	switch {
	case len(parts) == 0:
		return davPath{kind: davRoot}, true
	case parts[0] == "principals" && len(parts) == 2:
		return davPath{kind: davPrincipal, userID: parts[1]}, true
	case parts[0] == "calendars" && len(parts) == 2:
		return davPath{kind: davHome, userID: parts[1]}, true
	case parts[0] == "calendars" && len(parts) == 3:
		return davPath{kind: davCollection, userID: parts[1], coll: parts[2]}, true
	case parts[0] == "calendars" && len(parts) == 4:
		return davPath{kind: davObject, userID: parts[1], coll: parts[2], name: parts[3]}, true
	}
	return davPath{}, false
}

func principalHref(u *models.User) string { return davPrefix + "principals/" + u.ID + "/" }
func homeHref(u *models.User) string      { return davPrefix + "calendars/" + u.ID + "/" }
func collHref(u *models.User, coll string) string {
	return homeHref(u) + url.PathEscape(coll) + "/"
}
func objectHref(u *models.User, coll string, t *models.Task) string {
	return collHref(u, coll) + url.PathEscape(t.CalDAVName())
}

// taskColl is the collection a task lives in.
func taskColl(t *models.Task) string {
	if t.ProjectID.Valid {
		return t.ProjectID.String
	}
	return davInbox
}

// DAVHandler serves /dav/*, /.well-known/caldav and PROPFIND on /.
func (h *Handlers) DAVHandler() http.Handler {
	var handler http.Handler = http.HandlerFunc(h.serveDAV)
	if os.Getenv("CALDAV_TRACE") != "" {
		handler = davTrace(handler)
	}
	return handler
}

func (h *Handlers) serveDAV(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("DAV", "1, 3, calendar-access")
	if r.Method == http.MethodOptions {
		w.Header().Set("Allow", "OPTIONS, GET, HEAD, PUT, DELETE, PROPFIND, PROPPATCH, REPORT")
		w.WriteHeader(http.StatusOK)
		return
	}

	user, stale := h.davAuth(r)
	if user == nil {
		// Digest first: iOS won't send a Basic password over plain HTTP.
		w.Header().Add("WWW-Authenticate", auth.DigestChallenge(stale))
		w.Header().Add("WWW-Authenticate", `Basic realm="`+auth.DigestRealm+`", charset="UTF-8"`)
		http.Error(w, "Use your email and a TodoStuff app password.", http.StatusUnauthorized)
		return
	}
	ctx := auth.WithUser(r.Context(), user)
	r = r.WithContext(ctx)

	// Discovery: clients given just "host:port" PROPFIND the well-known URL
	// or the site root for current-user-principal.
	if strings.HasPrefix(r.URL.Path, "/.well-known/caldav") || r.URL.Path == "/" {
		if r.Method != "PROPFIND" {
			http.Redirect(w, r, davPrefix, http.StatusMovedPermanently)
			return
		}
		h.davPropfind(w, r, user, davPath{kind: davRoot})
		return
	}

	p, ok := parseDAVPath(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if p.kind != davRoot && p.userID != user.ID {
		http.Error(w, "Not your calendars.", http.StatusForbidden)
		return
	}

	switch r.Method {
	case "PROPFIND":
		h.davPropfind(w, r, user, p)
	case "REPORT":
		h.davReport(w, r, user, p)
	case http.MethodGet, http.MethodHead:
		h.davGet(w, r, user, p)
	case http.MethodPut:
		h.davPut(w, r, user, p)
	case http.MethodDelete:
		h.davDelete(w, r, user, p)
	case "PROPPATCH":
		// Lists are managed in TodoStuff; refuse every property cleanly so a
		// client reordering or recolouring lists gets a well-formed answer.
		body, _ := caldav.ParseXML(io.LimitReader(r.Body, davMaxBody))
		var names []xml.Name
		for _, prop := range body.Find(caldav.NSDAV, "prop") {
			for _, c := range prop.Children {
				names = append(names, c.Name)
			}
		}
		writeDAV(w, http.StatusMultiStatus, caldav.PropstatForbidden(r.URL.Path, names))
	default:
		// MKCALENDAR / MKCOL: lists are TodoStuff projects.
		http.Error(w, "Create lists in TodoStuff.", http.StatusForbidden)
	}
}

// davAuth accepts HTTP Digest or Basic with an app password. stale means
// the Digest credentials matched but the nonce expired.
func (h *Handlers) davAuth(r *http.Request) (user *models.User, stale bool) {
	var err error
	hdr := r.Header.Get("Authorization")
	switch {
	case hdr == "":
		return nil, false
	case strings.HasPrefix(strings.ToLower(hdr), "digest "):
		user, err = h.Auth.AuthenticateDigest(r.Context(), r.Method, r.RequestURI, hdr)
	default:
		email, pw, ok := r.BasicAuth()
		if !ok {
			return nil, false
		}
		user, err = h.Auth.AuthenticateAppPassword(r.Context(), email, pw)
	}
	switch {
	case errors.Is(err, auth.ErrStaleNonce):
		return nil, true
	case errors.Is(err, auth.ErrInvalidCredentials):
		slog.Warn("caldav: rejected credentials", "remote", r.RemoteAddr, "path", r.URL.Path)
		return nil, false
	case err != nil:
		slog.Error("caldav auth", "err", err)
		return nil, false
	}
	return user, false
}

// davList is one list as a client sees it.
type davList struct {
	id        string // "inbox" or the project id
	projectID string // "" for the inbox
	name      string
	color     string
	order     int
}

// davPalette colours projects that have no colour of their own, by position.
var davPalette = []string{"#D97706", "#2563EB", "#059669", "#DC2626", "#7C3AED", "#DB2777", "#0D9488", "#EA580C"}

func (h *Handlers) davCollections(ctx context.Context, user *models.User) ([]davList, error) {
	projects, err := h.Projects.List(ctx, user.ID)
	if err != nil {
		return nil, err
	}
	out := []davList{{id: davInbox, name: "Inbox", color: "#78716C", order: 0}}
	for i, p := range projects {
		out = append(out, davProjectCollection(&p, i))
	}
	return out, nil
}

func davProjectCollection(p *models.Project, i int) davList {
	color := davPalette[i%len(davPalette)]
	if p.Color.Valid && strings.HasPrefix(p.Color.String, "#") {
		color = p.Color.String
	}
	return davList{id: p.ID, projectID: p.ID, name: p.Name, color: color, order: i + 1}
}

// davResolveColl finds the collection named in a path, or nil.
func (h *Handlers) davResolveColl(ctx context.Context, user *models.User, id string) (*davList, error) {
	if id == davInbox {
		return &davList{id: davInbox, name: "Inbox", color: "#78716C"}, nil
	}
	cols, err := h.davCollections(ctx, user)
	if err != nil {
		return nil, err
	}
	for _, c := range cols {
		if c.id == id {
			return &c, nil
		}
	}
	return nil, nil
}

// ------------------------------------------------------------- PROPFIND ---

func (h *Handlers) davPropfind(w http.ResponseWriter, r *http.Request, user *models.User, p davPath) {
	body, err := caldav.ParseXML(io.LimitReader(r.Body, davMaxBody))
	if err != nil {
		http.Error(w, "Malformed XML.", http.StatusBadRequest)
		return
	}
	names, allprop := caldav.RequestedProps(body)
	depth1 := r.Header.Get("Depth") != "0"
	ctx := r.Context()

	var out []caldav.Response
	switch p.kind {
	case davRoot:
		out = append(out, pick(r.URL.Path, h.davRootProps(user), names, allprop))
	case davPrincipal:
		out = append(out, pick(principalHref(user), h.davPrincipalProps(user), names, allprop))
	case davHome:
		out = append(out, pick(homeHref(user), h.davHomeProps(user), names, allprop))
		if depth1 {
			cols, err := h.davCollections(ctx, user)
			if err != nil {
				davInternal(w, "list collections", err)
				return
			}
			for _, c := range cols {
				props, err := h.davCollectionProps(ctx, user, &c)
				if err != nil {
					davInternal(w, "collection props", err)
					return
				}
				out = append(out, pick(collHref(user, c.id), props, names, allprop))
			}
		}
	case davCollection:
		c, err := h.davResolveColl(ctx, user, p.coll)
		if err != nil {
			davInternal(w, "resolve collection", err)
			return
		}
		if c == nil {
			http.NotFound(w, r)
			return
		}
		props, err := h.davCollectionProps(ctx, user, c)
		if err != nil {
			davInternal(w, "collection props", err)
			return
		}
		out = append(out, pick(collHref(user, c.id), props, names, allprop))
		if depth1 {
			tasks, err := h.Tasks.ListForCalDAV(ctx, user.ID, c.projectID)
			if err != nil {
				davInternal(w, "list tasks", err)
				return
			}
			for i := range tasks {
				props, err := davObjectProps(&tasks[i], wantsCalendarData(names))
				if err != nil {
					davInternal(w, "render task", err)
					return
				}
				out = append(out, pick(objectHref(user, c.id, &tasks[i]), props, names, allprop))
			}
		}
	case davObject:
		t, err := h.davFindObject(ctx, user, p)
		if err != nil {
			davInternal(w, "find object", err)
			return
		}
		if t == nil {
			http.NotFound(w, r)
			return
		}
		props, err := davObjectProps(t, wantsCalendarData(names))
		if err != nil {
			davInternal(w, "render task", err)
			return
		}
		out = append(out, pick(objectHref(user, p.coll, t), props, names, allprop))
	}
	writeDAV(w, http.StatusMultiStatus, caldav.Multistatus(out))
}

// davProps maps property name → serialised value for one resource.
type davProps map[xml.Name]string

func dn(local string) xml.Name { return xml.Name{Space: caldav.NSDAV, Local: local} }
func cn(local string) xml.Name { return xml.Name{Space: caldav.NSCalDAV, Local: local} }

// pick answers a PROPFIND for one resource: the requested properties it has,
// and a 404 propstat for the rest. allprop returns everything except
// calendar-data, which clients only get by asking.
func pick(href string, have davProps, names []xml.Name, allprop bool) caldav.Response {
	resp := caldav.Response{Href: href}
	if allprop {
		for n, v := range have {
			if n == cn("calendar-data") {
				continue
			}
			resp.Found = append(resp.Found, caldav.Prop{Name: n, Inner: v})
		}
		return resp
	}
	for _, n := range names {
		if v, ok := have[n]; ok {
			resp.Found = append(resp.Found, caldav.Prop{Name: n, Inner: v})
		} else {
			resp.NotFound = append(resp.NotFound, n)
		}
	}
	return resp
}

func wantsCalendarData(names []xml.Name) bool {
	for _, n := range names {
		if n == cn("calendar-data") {
			return true
		}
	}
	return false
}

func (h *Handlers) davRootProps(user *models.User) davProps {
	return davProps{
		dn("resourcetype"):             "<d:collection/>",
		dn("current-user-principal"):   caldav.Href(principalHref(user)),
		dn("principal-collection-set"): caldav.Href(davPrefix + "principals/"),
		dn("displayname"):              "TodoStuff",
	}
}

func (h *Handlers) davPrincipalProps(user *models.User) davProps {
	return davProps{
		dn("resourcetype"):               "<d:principal/>",
		dn("displayname"):                caldav.Escape(user.Name),
		dn("current-user-principal"):     caldav.Href(principalHref(user)),
		dn("principal-URL"):              caldav.Href(principalHref(user)),
		dn("principal-collection-set"):   caldav.Href(davPrefix + "principals/"),
		cn("calendar-home-set"):          caldav.Href(homeHref(user)),
		cn("calendar-user-address-set"):  caldav.Href("mailto:"+user.Email) + caldav.Href(principalHref(user)),
		dn("current-user-privilege-set"): privileges("read"),
		dn("supported-report-set"):       "",
		cn("calendar-user-type"):         "INDIVIDUAL",
		dn("owner"):                      caldav.Href(principalHref(user)),
	}
}

func (h *Handlers) davHomeProps(user *models.User) davProps {
	return davProps{
		dn("resourcetype"):               "<d:collection/>",
		dn("displayname"):                "TodoStuff",
		dn("current-user-principal"):     caldav.Href(principalHref(user)),
		dn("owner"):                      caldav.Href(principalHref(user)),
		dn("current-user-privilege-set"): privileges("read", "read-current-user-privilege-set"),
	}
}

func (h *Handlers) davCollectionProps(ctx context.Context, user *models.User, c *davList) (davProps, error) {
	ctag, err := h.Tasks.CollectionCTag(ctx, user.ID, c.projectID)
	if err != nil {
		return nil, err
	}
	return davProps{
		dn("resourcetype"): "<d:collection/><c:calendar/>",
		dn("displayname"):  caldav.Escape(c.name),
		xml.Name{Space: caldav.NSCS, Local: "getctag"}: caldav.Escape(ctag + "-v" + caldav.RenderVersion),
		cn("supported-calendar-component-set"):         `<c:comp name="VTODO"/>`,
		dn("current-user-privilege-set"): privileges("read", "read-current-user-privilege-set",
			"write-content", "bind", "unbind"),
		dn("current-user-principal"): caldav.Href(principalHref(user)),
		dn("owner"):                  caldav.Href(principalHref(user)),
		dn("supported-report-set"): "<d:supported-report><d:report><c:calendar-multiget/></d:report></d:supported-report>" +
			"<d:supported-report><d:report><c:calendar-query/></d:report></d:supported-report>",
		xml.Name{Space: caldav.NSApple, Local: "calendar-color"}: caldav.Escape(c.color + "FF"),
		xml.Name{Space: caldav.NSApple, Local: "calendar-order"}: strconv.Itoa(c.order),
		cn("max-resource-size"):                                  strconv.Itoa(davMaxBody),
	}, nil
}

func davObjectProps(t *models.Task, withData bool) (davProps, error) {
	data, err := caldav.Render(t, davLoc())
	if err != nil {
		return nil, err
	}
	props := davProps{
		dn("resourcetype"):     "",
		dn("getetag"):          caldav.Escape(caldav.ETag(data)),
		dn("getcontenttype"):   davTodoType,
		dn("getcontentlength"): strconv.Itoa(len(data)),
		dn("getlastmodified"):  t.UpdatedAt.UTC().Format(http.TimeFormat),
	}
	if withData {
		props[cn("calendar-data")] = caldav.Escape(string(data))
	}
	return props, nil
}

func privileges(names ...string) string {
	var b strings.Builder
	for _, n := range names {
		b.WriteString("<d:privilege><d:" + n + "/></d:privilege>")
	}
	return b.String()
}

// --------------------------------------------------------------- REPORT ---

func (h *Handlers) davReport(w http.ResponseWriter, r *http.Request, user *models.User, p davPath) {
	body, err := caldav.ParseXML(io.LimitReader(r.Body, davMaxBody))
	if err != nil || body == nil {
		http.Error(w, "Malformed XML.", http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	names, _ := caldav.RequestedProps(body)
	withData := wantsCalendarData(names)

	switch {
	case body.Is(caldav.NSCalDAV, "calendar-multiget"):
		var out []caldav.Response
		for _, hn := range body.Find(caldav.NSDAV, "href") {
			href := strings.TrimSpace(hn.Text)
			hp, ok := parseDAVPath(davUnescape(href))
			if !ok || hp.kind != davObject || hp.userID != user.ID {
				out = append(out, caldav.Response{Href: href, Status: http.StatusNotFound})
				continue
			}
			t, err := h.davFindObject(ctx, user, hp)
			if err != nil {
				davInternal(w, "multiget", err)
				return
			}
			if t == nil {
				out = append(out, caldav.Response{Href: href, Status: http.StatusNotFound})
				continue
			}
			props, err := davObjectProps(t, withData)
			if err != nil {
				davInternal(w, "render task", err)
				return
			}
			out = append(out, pick(href, props, names, false))
		}
		writeDAV(w, http.StatusMultiStatus, caldav.Multistatus(out))

	case body.Is(caldav.NSCalDAV, "calendar-query"):
		if p.kind != davCollection {
			writeDAV(w, http.StatusMultiStatus, caldav.Multistatus(nil))
			return
		}
		c, err := h.davResolveColl(ctx, user, p.coll)
		if err != nil {
			davInternal(w, "resolve collection", err)
			return
		}
		if c == nil {
			http.NotFound(w, r)
			return
		}
		match := davQueryFilter(body)
		tasks, err := h.Tasks.ListForCalDAV(ctx, user.ID, c.projectID)
		if err != nil {
			davInternal(w, "list tasks", err)
			return
		}
		var out []caldav.Response
		for i := range tasks {
			if !match(&tasks[i]) {
				continue
			}
			props, err := davObjectProps(&tasks[i], withData)
			if err != nil {
				davInternal(w, "render task", err)
				return
			}
			out = append(out, pick(objectHref(user, c.id, &tasks[i]), props, names, false))
		}
		writeDAV(w, http.StatusMultiStatus, caldav.Multistatus(out))

	case body.Is(caldav.NSDAV, "sync-collection"):
		writeDAV(w, http.StatusForbidden, caldav.Error(caldav.NSDAV, "supported-report"))

	default:
		// principal-property-search, expand-property and friends: nothing
		// to report, but a well-formed empty answer keeps clients happy.
		writeDAV(w, http.StatusMultiStatus, caldav.Multistatus(nil))
	}
}

// davQueryFilter interprets the part of a calendar-query filter clients
// actually send for reminders: VEVENT-only queries match nothing, and
// "COMPLETED is-not-defined" or "STATUS != COMPLETED" mean open tasks only.
// Time ranges aren't applied; returning extra VTODOs is harmless.
func davQueryFilter(body *caldav.Node) func(*models.Task) bool {
	filter := body.Child(caldav.NSCalDAV, "filter")
	all := func(*models.Task) bool { return true }
	if filter == nil {
		return all
	}
	for _, cf := range filter.Find(caldav.NSCalDAV, "comp-filter") {
		if name := cf.AttrValue("name"); name == "VEVENT" || name == "VJOURNAL" {
			return func(*models.Task) bool { return false }
		}
	}
	openOnly := false
	for _, pf := range filter.Find(caldav.NSCalDAV, "prop-filter") {
		switch strings.ToUpper(pf.AttrValue("name")) {
		case "COMPLETED":
			if pf.Child(caldav.NSCalDAV, "is-not-defined") != nil {
				openOnly = true
			}
		case "STATUS":
			if tm := pf.Child(caldav.NSCalDAV, "text-match"); tm != nil &&
				strings.EqualFold(tm.AttrValue("negate-condition"), "yes") &&
				strings.EqualFold(strings.TrimSpace(tm.Text), "COMPLETED") {
				openOnly = true
			}
		}
	}
	if openOnly {
		return func(t *models.Task) bool { return !t.CompletedAt.Valid }
	}
	return all
}

// ------------------------------------------------------------------ GET ---

func (h *Handlers) davGet(w http.ResponseWriter, r *http.Request, user *models.User, p davPath) {
	if p.kind != davObject {
		http.Error(w, "Not a calendar object.", http.StatusMethodNotAllowed)
		return
	}
	t, err := h.davFindObject(r.Context(), user, p)
	if err != nil {
		davInternal(w, "find object", err)
		return
	}
	if t == nil {
		http.NotFound(w, r)
		return
	}
	data, err := caldav.Render(t, davLoc())
	if err != nil {
		davInternal(w, "render task", err)
		return
	}
	w.Header().Set("Content-Type", davTodoType)
	w.Header().Set("ETag", caldav.ETag(data))
	w.Header().Set("Last-Modified", t.UpdatedAt.UTC().Format(http.TimeFormat))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

// davFindObject returns the task at a collection/name path, or nil when the
// name is unknown or the task now lives in another collection.
func (h *Handlers) davFindObject(ctx context.Context, user *models.User, p davPath) (*models.Task, error) {
	t, err := h.Tasks.GetByCalDAVName(ctx, user.ID, p.name)
	if errors.Is(err, services.ErrTaskNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if taskColl(t) != p.coll {
		return nil, nil
	}
	return t, nil
}

// davCurrentETag is the ETag a client would hold for t, for If-Match.
func davCurrentETag(t *models.Task) string {
	data, err := caldav.Render(t, davLoc())
	if err != nil {
		return ""
	}
	return caldav.ETag(data)
}

// ------------------------------------------------------------------ PUT ---

func (h *Handlers) davPut(w http.ResponseWriter, r *http.Request, user *models.User, p davPath) {
	if p.kind != davObject {
		http.Error(w, "PUT a calendar object into a list.", http.StatusMethodNotAllowed)
		return
	}
	ctx := r.Context()
	c, err := h.davResolveColl(ctx, user, p.coll)
	if err != nil {
		davInternal(w, "resolve collection", err)
		return
	}
	if c == nil {
		http.Error(w, "No such list.", http.StatusConflict)
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, davMaxBody+1))
	if err != nil {
		http.Error(w, "Couldn't read body.", http.StatusBadRequest)
		return
	}
	if len(raw) > davMaxBody {
		writeDAV(w, http.StatusForbidden, caldav.Error(caldav.NSCalDAV, "max-resource-size"))
		return
	}
	todo, err := caldav.Parse(bytes.NewReader(raw), davLoc())
	if errors.Is(err, caldav.ErrNotTodo) {
		writeDAV(w, http.StatusForbidden, caldav.Error(caldav.NSCalDAV, "supported-calendar-component"))
		return
	}
	if err != nil {
		slog.Warn("caldav: rejected PUT", "path", r.URL.Path, "err", err)
		writeDAV(w, http.StatusForbidden, caldav.Error(caldav.NSCalDAV, "valid-calendar-data"))
		return
	}

	// The task at this href, if any, takes precedence; otherwise a task with
	// the same UID elsewhere is being moved into this list.
	atHref, err := h.davFindObject(ctx, user, p)
	if err != nil {
		davInternal(w, "find object", err)
		return
	}
	if inm := r.Header.Get("If-None-Match"); inm == "*" && atHref != nil {
		w.WriteHeader(http.StatusPreconditionFailed)
		return
	}
	if im := r.Header.Get("If-Match"); im != "" {
		if atHref == nil || (im != "*" && im != davCurrentETag(atHref)) {
			w.WriteHeader(http.StatusPreconditionFailed)
			return
		}
	}
	target := atHref
	if target == nil {
		t, err := h.Tasks.GetByCalDAVUID(ctx, user.ID, todo.UID)
		if err != nil && !errors.Is(err, services.ErrTaskNotFound) {
			davInternal(w, "find by uid", err)
			return
		}
		target = t
	}

	status := http.StatusNoContent
	if target == nil {
		params := services.CreateTaskParams{
			Title:       davTitle(todo.Title),
			Notes:       todo.Notes,
			ProjectID:   c.projectID,
			IsImportant: todo.Important,
			DueDate:     todo.DueDate,
			DueTime:     todo.DueTime,
		}
		target, err = h.Tasks.Create(ctx, user.ID, params)
		if err != nil {
			davInternal(w, "create task", err)
			return
		}
		status = http.StatusCreated
	}
	if err := h.davApply(ctx, user.ID, target, todo, c.projectID, p.name); err != nil {
		davInternal(w, "apply todo", err)
		return
	}
	// No ETag: the stored object is normalised, so the client must re-fetch
	// rather than assume its bytes are what the server now holds.
	w.WriteHeader(status)
}

func davTitle(s string) string {
	if strings.TrimSpace(s) == "" {
		return "New Reminder"
	}
	return s
}

// davApply makes task match todo: fields, list, reminder, recurrence and
// completion, through the same services the API uses, then records the
// client's UID, resource name and unmodelled properties.
func (h *Handlers) davApply(ctx context.Context, userID string, task *models.Task, todo *caldav.Todo, projectID, name string) error {
	var patch services.TaskPatch
	templateChanged := false

	if title := davTitle(todo.Title); title != task.Title {
		patch.Title = &title
		templateChanged = true
	}
	if todo.Notes != task.Notes.String {
		patch.Notes = &sql.NullString{String: todo.Notes, Valid: todo.Notes != ""}
		templateChanged = true
	}
	if projectID != task.ProjectID.String {
		patch.ProjectID = &sql.NullString{String: projectID, Valid: projectID != ""}
		templateChanged = true
	}
	if todo.Important != task.IsImportant {
		patch.IsImportant = &todo.Important
		templateChanged = true
	}

	oldDate, oldTime := "", ""
	if task.DueDate.Valid {
		oldDate = task.DueDate.Time.Format("2006-01-02")
	}
	if task.DueTime.Valid {
		oldTime = hhmm(task.DueTime.String)
	}
	newDate, newTime := "", todo.DueTime
	if todo.DueDate != nil {
		newDate = todo.DueDate.Format("2006-01-02")
	}
	if newDate != oldDate {
		if todo.DueDate == nil {
			patch.DueDate = &sql.NullTime{} // clears time and reminder too
		} else {
			patch.DueDate = &sql.NullTime{Time: *todo.DueDate, Valid: true}
		}
	}
	if newDate != "" && newTime != oldTime {
		patch.DueTime = &sql.NullString{String: newTime, Valid: newTime != ""}
		templateChanged = true
	}

	haveDue := newDate != "" && newTime != ""
	switch {
	case todo.ReminderOffset == nil:
		if haveDue && task.ReminderOffsetMinutes.Valid {
			patch.ReminderAt = &sql.NullTime{}
			patch.ReminderOffsetMinutes = &sql.NullInt64{}
		}
	case haveDue:
		off := sql.NullInt64{Int64: int64(*todo.ReminderOffset), Valid: true}
		at, err := reminderTime(newDate, newTime, *todo.ReminderOffset)
		if err != nil {
			return err
		}
		// Only when it moves: rewriting reminder_at re-arms a delivered
		// reminder.
		if !task.ReminderAt.Valid || !task.ReminderAt.Time.Equal(at) || task.ReminderOffsetMinutes != off {
			patch.ReminderAt = &sql.NullTime{Time: at, Valid: true}
			patch.ReminderOffsetMinutes = &off
		}
	}

	if _, err := h.Tasks.Update(ctx, userID, task.ID, patch); err != nil {
		return err
	}

	switch {
	case todo.Recurrence == nil && task.RecurrenceFrequency.Valid:
		if err := h.Tasks.SetRecurrenceForTask(ctx, userID, task.ID, services.RecurrenceParams{}); err != nil {
			return err
		}
	case todo.Recurrence != nil:
		regen := "on_create"
		if task.RecurrenceType.Valid {
			regen = task.RecurrenceType.String
		}
		changed := !task.RecurrenceFrequency.Valid ||
			task.RecurrenceFrequency.String != todo.Recurrence.Frequency ||
			int(task.RecurrenceInterval.Int64) != todo.Recurrence.Interval
		if changed || templateChanged {
			err := h.Tasks.SetRecurrenceForTask(ctx, userID, task.ID, services.RecurrenceParams{
				Frequency: todo.Recurrence.Frequency, Interval: todo.Recurrence.Interval, RegenerationType: regen,
			})
			if err != nil {
				return err
			}
		}
	}

	switch {
	case todo.Completed && !task.CompletedAt.Valid:
		if _, _, err := h.Tasks.Complete(ctx, userID, task.ID); err != nil {
			return err
		}
	case !todo.Completed && task.CompletedAt.Valid:
		if _, err := h.Tasks.Uncomplete(ctx, userID, task.ID); err != nil {
			return err
		}
	}

	return h.Tasks.SetCalDAVMeta(ctx, userID, task.ID, todo.UID, name, todo.Extra)
}

// --------------------------------------------------------------- DELETE ---

func (h *Handlers) davDelete(w http.ResponseWriter, r *http.Request, user *models.User, p davPath) {
	if p.kind != davObject {
		http.Error(w, "Delete lists in TodoStuff.", http.StatusForbidden)
		return
	}
	ctx := r.Context()
	t, err := h.Tasks.GetByCalDAVName(ctx, user.ID, p.name)
	if errors.Is(err, services.ErrTaskNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		davInternal(w, "find object", err)
		return
	}
	if taskColl(t) != p.coll {
		// A move: the client PUT the task into its new list first and is now
		// removing the old copy, which no longer exists here.
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if im := r.Header.Get("If-Match"); im != "" && im != "*" && im != davCurrentETag(t) {
		w.WriteHeader(http.StatusPreconditionFailed)
		return
	}
	if err := h.Tasks.Delete(ctx, user.ID, t.ID); err != nil {
		davInternal(w, "delete task", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// -------------------------------------------------------------- helpers ---

// davLoc is the server's zone as a named location: time.Local can be called
// just "Local", which is no use as a TZID. Falls back to time.Local (written
// as UTC) if the name can't be loaded.
func davLoc() *time.Location {
	if time.Local.String() != "Local" {
		return time.Local
	}
	davLocOnce.Do(func() {
		davLocNamed = time.Local
		if loc, err := time.LoadLocation(serverTimezone()); err == nil {
			davLocNamed = loc
		}
	})
	return davLocNamed
}

var (
	davLocOnce  sync.Once
	davLocNamed *time.Location
)

func writeDAV(w http.ResponseWriter, status int, body []byte) {
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

func davInternal(w http.ResponseWriter, what string, err error) {
	slog.Error("caldav: "+what, "err", err)
	http.Error(w, "Internal server error.", http.StatusInternalServerError)
}

func davUnescape(href string) string {
	if u, err := url.Parse(href); err == nil {
		return u.Path
	}
	return href
}

// davTrace logs every CalDAV exchange (bodies included, auth header
// excluded) when CALDAV_TRACE is set: the way to see exactly what a client
// like iOS sends.
func davTrace(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqBody, _ := io.ReadAll(io.LimitReader(r.Body, davMaxBody))
		r.Body = io.NopCloser(bytes.NewReader(reqBody))
		rec := &traceWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		hdr := r.Header.Clone()
		hdr.Del("Authorization")
		slog.Info("caldav trace",
			"method", r.Method, "path", r.URL.Path, "status", rec.status,
			"headers", hdr, "request", string(reqBody), "response", rec.body.String())
	})
}

type traceWriter struct {
	http.ResponseWriter
	status int
	body   bytes.Buffer
}

func (t *traceWriter) WriteHeader(code int) {
	t.status = code
	t.ResponseWriter.WriteHeader(code)
}

func (t *traceWriter) Write(b []byte) (int, error) {
	t.body.Write(b)
	return t.ResponseWriter.Write(b)
}
