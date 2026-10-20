// Package caldav holds the protocol halves of TodoStuff's CalDAV endpoint
// that don't touch the database: translating tasks to and from iCalendar
// VTODOs, and the WebDAV XML. The HTTP handler that ties them to the
// services lives in internal/handlers/dav.go.
package caldav

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/andyjessop/todostuff/internal/models"
	"github.com/emersion/go-ical"
)

const prodID = "-//TodoStuff//CalDAV//EN"

// RenderVersion goes into every collection's getctag. Bump it whenever
// Render's output changes for unchanged data, or clients holding the old
// rendering will never re-fetch it.
const RenderVersion = "3"

// ReminderOffsets are the reminder offsets TodoStuff accepts, in minutes,
// ascending. A client's alarm is snapped onto one of these.
var ReminderOffsets = []int{0, 5, 10, 15, 30, 60, 120, 240, 480, 720, 1440}

// ErrNotTodo means the body held no VTODO (an event, say). The handler turns
// it into the supported-calendar-component precondition.
var ErrNotTodo = errors.New("caldav: no VTODO in calendar data")

// Recurrence is the subset of RRULE TodoStuff can store.
type Recurrence struct {
	Frequency string // days | weeks | months | years
	Interval  int
}

// Todo is a VTODO reduced to the fields TodoStuff models.
type Todo struct {
	UID       string
	Title     string
	Notes     string
	DueDate   *time.Time // UTC midnight of the server-local date
	DueTime   string     // "HH:MM" server-local wall clock, "" when all-day
	Important bool
	Completed bool

	// ReminderOffset is minutes before the due time; nil = no reminder.
	ReminderOffset *int
	Recurrence     *Recurrence

	// Extra is the JSON array of properties TodoStuff doesn't model, stored
	// in tasks.ical_extra and re-emitted verbatim.
	Extra string
}

// modelled are the VTODO properties Parse consumes and Render regenerates.
// Everything else goes to Todo.Extra.
var modelled = map[string]bool{
	"UID": true, "DTSTAMP": true, "CREATED": true, "LAST-MODIFIED": true,
	"SUMMARY": true, "DESCRIPTION": true, "DUE": true, "DTSTART": true,
	"DURATION": true, "STATUS": true, "COMPLETED": true,
	"PERCENT-COMPLETE": true, "PRIORITY": true, "RRULE": true, "SEQUENCE": true,
}

type extraProp struct {
	Name   string              `json:"name"`
	Params map[string][]string `json:"params,omitempty"`
	Value  string              `json:"value"`
}

// Parse reads one VTODO from an iCalendar body. Dates are converted into
// loc, the server's zone, because TodoStuff stores due dates and times as
// server-local wall clock.
func Parse(r io.Reader, loc *time.Location) (*Todo, error) {
	cal, err := ical.NewDecoder(r).Decode()
	if err != nil {
		return nil, fmt.Errorf("caldav: parse calendar: %w", err)
	}
	var vtodo *ical.Component
	for _, c := range cal.Children {
		switch c.Name {
		case ical.CompToDo:
			if vtodo != nil {
				// Overridden instances of a recurring VTODO; TodoStuff has
				// no per-instance overrides.
				return nil, errors.New("caldav: more than one VTODO (recurrence overrides aren't supported)")
			}
			vtodo = c
		case ical.CompEvent, ical.CompJournal:
			return nil, ErrNotTodo
		}
	}
	if vtodo == nil {
		return nil, ErrNotTodo
	}
	p := vtodo.Props

	t := &Todo{}
	if t.UID, err = p.Text(ical.PropUID); err != nil || t.UID == "" {
		return nil, errors.New("caldav: VTODO has no UID")
	}
	t.Title, _ = p.Text(ical.PropSummary)
	t.Title = strings.TrimSpace(t.Title)
	if desc, _ := p.Text(ical.PropDescription); desc != "" {
		t.Notes = strings.TrimSpace(desc)
	}

	status := strings.ToUpper(propValue(p, ical.PropStatus))
	t.Completed = status == "COMPLETED" || (status == "" && p.Get(ical.PropCompleted) != nil)

	if pr := p.Get(ical.PropPriority); pr != nil {
		if n, err := pr.Int(); err == nil && n >= 1 && n <= 4 {
			t.Important = true
		}
	}

	// Due: DUE, falling back to DTSTART (some clients only set a start).
	dueProp := p.Get(ical.PropDue)
	if dueProp == nil {
		dueProp = p.Get(ical.PropDateTimeStart)
	}
	var dueAt time.Time // zero unless the due has a time of day
	if dueProp != nil {
		d, timed, err := propDateTime(dueProp, loc)
		if err != nil {
			return nil, fmt.Errorf("caldav: bad DUE: %w", err)
		}
		date := time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, time.UTC)
		t.DueDate = &date
		if timed {
			dueAt = d
			t.DueTime = d.Format("15:04")
		}
	}

	if at, ok := alarmTime(vtodo, dueProp, loc); ok {
		at = at.In(loc)
		if dueAt.IsZero() {
			// TodoStuff anchors a reminder on a due time, so an alarm on an
			// all-day (or undated) reminder becomes the due time itself.
			date := time.Date(at.Year(), at.Month(), at.Day(), 0, 0, 0, 0, time.UTC)
			t.DueDate = &date
			t.DueTime = at.Format("15:04")
			zero := 0
			t.ReminderOffset = &zero
		} else {
			off := SnapOffset(int(dueAt.Sub(at).Round(time.Minute) / time.Minute))
			t.ReminderOffset = &off
		}
	}

	if rr := propValue(p, ical.PropRecurrenceRule); rr != "" {
		rec, err := parseRRule(rr, t.DueDate)
		if err != nil {
			return nil, err
		}
		t.Recurrence = rec
	}

	var extra []extraProp
	for name, props := range p {
		if modelled[name] {
			continue
		}
		for _, pr := range props {
			extra = append(extra, extraProp{Name: pr.Name, Params: pr.Params, Value: pr.Value})
		}
	}
	if len(extra) > 0 {
		b, _ := json.Marshal(extra)
		t.Extra = string(b)
	}
	return t, nil
}

// SnapOffset maps an arbitrary alarm offset (minutes before due) onto the
// nearest allowed offset that alerts no later than the client asked for.
// Alarms after the due time become "at the time".
func SnapOffset(minutes int) int {
	if minutes <= 0 {
		return 0
	}
	for _, o := range ReminderOffsets {
		if o >= minutes {
			return o
		}
	}
	return ReminderOffsets[len(ReminderOffsets)-1]
}

// alarmTime returns the instant of the VTODO's first VALARM, resolving
// relative triggers against DUE (RELATED=END) or DTSTART (the default).
func alarmTime(vtodo *ical.Component, dueProp *ical.Prop, loc *time.Location) (time.Time, bool) {
	for _, c := range vtodo.Children {
		if c.Name != ical.CompAlarm {
			continue
		}
		trig := c.Props.Get(ical.PropTrigger)
		if trig == nil {
			continue
		}
		if strings.EqualFold(trig.Params.Get(ical.ParamValue), "DATE-TIME") {
			at, err := trig.DateTime(time.UTC)
			if err != nil {
				continue
			}
			return at, true
		}
		dur, err := trig.Duration()
		if err != nil {
			continue
		}
		anchor := vtodo.Props.Get(ical.PropDateTimeStart)
		if strings.EqualFold(trig.Params.Get(ical.ParamRelated), "END") || anchor == nil {
			anchor = dueProp
		}
		if anchor == nil {
			continue
		}
		base, _, err := propDateTime(anchor, loc)
		if err != nil {
			continue
		}
		return base.Add(dur), true
	}
	return time.Time{}, false
}

// propDateTime parses a DATE or DATE-TIME property into loc. timed is false
// for a DATE (all-day) value, which comes back as midnight in loc.
func propDateTime(p *ical.Prop, loc *time.Location) (t time.Time, timed bool, err error) {
	if strings.EqualFold(p.Params.Get(ical.ParamValue), "DATE") || len(p.Value) == 8 {
		d, err := time.ParseInLocation("20060102", p.Value, loc)
		return d, false, err
	}
	// A TZID go-ical can't load (Outlook's "GMT Standard Time", say) is
	// treated as floating, i.e. server-local, rather than failing the PUT.
	t, err = p.DateTime(loc)
	if err != nil && p.Params.Get(ical.ParamTimezoneID) != "" {
		t, err = time.ParseInLocation("20060102T150405", p.Value, loc)
	}
	if err != nil {
		return time.Time{}, false, err
	}
	return t.In(loc), true, nil
}

var rruleFreq = map[string]string{"DAILY": "days", "WEEKLY": "weeks", "MONTHLY": "months", "YEARLY": "years"}

// parseRRule accepts FREQ + INTERVAL, plus BY* parts that only restate the
// due date (Apple sends BYDAY/BYMONTHDAY that way). Anything that would
// change the schedule (COUNT, UNTIL, several weekdays) is rejected rather
// than silently dropped.
func parseRRule(rule string, due *time.Time) (*Recurrence, error) {
	rec := &Recurrence{Interval: 1}
	for _, part := range strings.Split(rule, ";") {
		k, v, _ := strings.Cut(part, "=")
		k, v = strings.ToUpper(strings.TrimSpace(k)), strings.ToUpper(strings.TrimSpace(v))
		switch k {
		case "FREQ":
			f, ok := rruleFreq[v]
			if !ok {
				return nil, fmt.Errorf("caldav: unsupported repeat frequency %q", v)
			}
			rec.Frequency = f
		case "INTERVAL":
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 {
				return nil, fmt.Errorf("caldav: bad repeat interval %q", v)
			}
			rec.Interval = n
		case "WKST", "":
		case "BYDAY":
			if due == nil || v != strings.ToUpper(due.Weekday().String()[:2]) {
				return nil, fmt.Errorf("caldav: unsupported repeat rule %q", rule)
			}
		case "BYMONTHDAY":
			if due == nil || v != strconv.Itoa(due.Day()) {
				return nil, fmt.Errorf("caldav: unsupported repeat rule %q", rule)
			}
		case "BYMONTH":
			if due == nil || v != strconv.Itoa(int(due.Month())) {
				return nil, fmt.Errorf("caldav: unsupported repeat rule %q", rule)
			}
		default:
			return nil, fmt.Errorf("caldav: unsupported repeat rule %q", rule)
		}
	}
	if rec.Frequency == "" {
		return nil, errors.New("caldav: repeat rule has no FREQ")
	}
	return rec, nil
}

func propValue(p ical.Props, name string) string {
	if pr := p.Get(name); pr != nil {
		return strings.TrimSpace(pr.Value)
	}
	return ""
}

// Render writes a task as a VCALENDAR holding one VTODO. Due times go out as
// wall clock in loc (the server's zone, which must be a named location such
// as Europe/London) with a VTIMEZONE; see tz.go for why not UTC.
func Render(t *models.Task, loc *time.Location) ([]byte, error) {
	todo := ical.NewComponent(ical.CompToDo)
	p := todo.Props
	p.SetText(ical.PropUID, t.CalDAVUID())
	p.SetDateTime(ical.PropDateTimeStamp, t.UpdatedAt.UTC())
	p.SetDateTime(ical.PropCreated, t.CreatedAt.UTC())
	p.SetDateTime(ical.PropLastModified, t.UpdatedAt.UTC())
	p.SetText(ical.PropSummary, t.Title)
	if t.Notes.Valid && t.Notes.String != "" {
		p.SetText(ical.PropDescription, t.Notes.String)
	}

	var dueAt time.Time
	if t.DueDate.Valid {
		y, m, d := t.DueDate.Time.Date()
		if t.DueTime.Valid {
			hh, mm := 0, 0
			fmt.Sscanf(t.DueTime.String, "%d:%d", &hh, &mm)
			dueAt = time.Date(y, m, d, hh, mm, 0, 0, loc)
			setLocalDateTime(p, ical.PropDue, dueAt, loc)
			setLocalDateTime(p, ical.PropDateTimeStart, dueAt, loc)
		} else {
			date := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
			p.SetDate(ical.PropDue, date)
			p.SetDate(ical.PropDateTimeStart, date)
		}
	}

	if t.CompletedAt.Valid {
		p.SetText(ical.PropStatus, "COMPLETED")
		p.SetDateTime(ical.PropCompleted, t.CompletedAt.Time.UTC())
		setRaw(p, ical.PropPercentComplete, "100")
	} else {
		p.SetText(ical.PropStatus, "NEEDS-ACTION")
	}
	if t.IsImportant {
		setRaw(p, ical.PropPriority, "1")
	}
	if t.RecurrenceFrequency.Valid {
		for f, ours := range rruleFreq {
			if ours == t.RecurrenceFrequency.String {
				n := t.RecurrenceInterval.Int64
				if n < 1 {
					n = 1
				}
				setRaw(p, ical.PropRecurrenceRule, fmt.Sprintf("FREQ=%s;INTERVAL=%d", f, n))
			}
		}
	}

	// One alarm, at reminder_at; a timed task without a reminder still gets
	// one at its due time, which is when the Linux client alerts too. It's
	// written relative to DTSTART (= DUE), never as an absolute UTC instant:
	// iOS shows the alarm as the reminder's time, and a UTC trigger makes it
	// display "09:45 (08:45 GMT)".
	if !dueAt.IsZero() {
		before := 0
		if t.ReminderAt.Valid {
			before = int(dueAt.Sub(t.ReminderAt.Time).Round(time.Minute) / time.Minute)
		}
		trigger := "PT0S"
		if before > 0 {
			trigger = fmt.Sprintf("-PT%dM", before)
		}
		alarm := ical.NewComponent(ical.CompAlarm)
		alarm.Props.SetText(ical.PropAction, "DISPLAY")
		alarm.Props.SetText(ical.PropDescription, "Reminder")
		setRaw(alarm.Props, ical.PropTrigger, trigger)
		setRaw(alarm.Props, "X-WR-ALARMUID", t.ID+"-alarm")
		alarm.Props.SetText(ical.PropUID, t.ID+"-alarm")
		todo.Children = append(todo.Children, alarm)
	}

	if t.ICalExtra.Valid {
		var extra []extraProp
		if err := json.Unmarshal([]byte(t.ICalExtra.String), &extra); err == nil {
			for _, e := range extra {
				if modelled[strings.ToUpper(e.Name)] {
					continue
				}
				pr := ical.NewProp(e.Name)
				pr.Value = e.Value
				for k, vs := range e.Params {
					for _, v := range vs {
						pr.Params.Add(k, v)
					}
				}
				p.Add(pr)
			}
		}
	}

	cal := ical.NewCalendar()
	cal.Props.SetText(ical.PropProductID, prodID)
	cal.Props.SetText(ical.PropVersion, "2.0")
	if !dueAt.IsZero() && !isUTC(loc) {
		cal.Children = append(cal.Children, vtimezone(loc, dueAt.Year()))
	}
	cal.Children = append(cal.Children, todo)

	var buf bytes.Buffer
	if err := ical.NewEncoder(&buf).Encode(cal); err != nil {
		return nil, fmt.Errorf("caldav: render task %s: %w", t.ID, err)
	}
	return buf.Bytes(), nil
}

// setRaw sets a property's value verbatim. Props.SetText would escape the
// semicolons in an RRULE and tag non-text properties VALUE=TEXT.
func setRaw(p ical.Props, name, value string) {
	pr := ical.NewProp(name)
	pr.Value = value
	p.Set(pr)
}

// ETag is the strong entity tag for a rendered object.
func ETag(data []byte) string {
	sum := sha256.Sum256(data)
	return `"` + hex.EncodeToString(sum[:16]) + `"`
}
