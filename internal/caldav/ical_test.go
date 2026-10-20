package caldav

import (
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/andyjessop/todostuff/internal/models"
)

func london(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Europe/London")
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

// iosTodo is shaped like what iOS Reminders PUTs for "remind me to call the
// bank at 3pm" (BST, so 14:00Z), with a VTIMEZONE and Apple extras.
const iosTodo = "BEGIN:VCALENDAR\r\n" +
	"VERSION:2.0\r\n" +
	"PRODID:-//Apple Inc.//iOS 18.0//EN\r\n" +
	"CALSCALE:GREGORIAN\r\n" +
	"BEGIN:VTODO\r\n" +
	"CREATED:20261020T101500Z\r\n" +
	"DTSTAMP:20261020T101512Z\r\n" +
	"DTSTART;TZID=Europe/London:20261020T150000\r\n" +
	"DUE;TZID=Europe/London:20261020T150000\r\n" +
	"LAST-MODIFIED:20261020T101512Z\r\n" +
	"PRIORITY:0\r\n" +
	"SEQUENCE:0\r\n" +
	"STATUS:NEEDS-ACTION\r\n" +
	"SUMMARY:Call the bank\r\n" +
	"DESCRIPTION:Ask about fees\r\n" +
	"UID:6A1B2C3D-0000-4000-8000-ABCDEF012345\r\n" +
	"X-APPLE-SORT-ORDER:742000000\r\n" +
	"BEGIN:VALARM\r\n" +
	"ACTION:DISPLAY\r\n" +
	"DESCRIPTION:Reminder\r\n" +
	"TRIGGER;VALUE=DATE-TIME:20261020T140000Z\r\n" +
	"UID:A1\r\n" +
	"X-WR-ALARMUID:A1\r\n" +
	"END:VALARM\r\n" +
	"END:VTODO\r\n" +
	"BEGIN:VTIMEZONE\r\n" +
	"TZID:Europe/London\r\n" +
	"BEGIN:STANDARD\r\n" +
	"DTSTART:19961027T020000\r\n" +
	"TZOFFSETFROM:+0100\r\n" +
	"TZOFFSETTO:+0000\r\n" +
	"END:STANDARD\r\n" +
	"END:VTIMEZONE\r\n" +
	"END:VCALENDAR\r\n"

func TestParseIOSReminder(t *testing.T) {
	td, err := Parse(strings.NewReader(iosTodo), london(t))
	if err != nil {
		t.Fatal(err)
	}
	if td.UID != "6A1B2C3D-0000-4000-8000-ABCDEF012345" || td.Title != "Call the bank" || td.Notes != "Ask about fees" {
		t.Fatalf("identity/text: %+v", td)
	}
	if td.DueDate == nil || td.DueDate.Format("2006-01-02") != "2026-10-20" || td.DueTime != "15:00" {
		t.Fatalf("due: %v %q", td.DueDate, td.DueTime)
	}
	if td.ReminderOffset == nil || *td.ReminderOffset != 0 {
		t.Fatalf("reminder offset: %v", td.ReminderOffset)
	}
	if td.Completed || td.Important || td.Recurrence != nil {
		t.Fatalf("flags: %+v", td)
	}
	if !strings.Contains(td.Extra, "X-APPLE-SORT-ORDER") || strings.Contains(td.Extra, "SUMMARY") {
		t.Fatalf("extra: %s", td.Extra)
	}
}

func TestParseUTCDueConvertsToServerZone(t *testing.T) {
	body := strings.Replace(iosTodo, "DUE;TZID=Europe/London:20261020T150000", "DUE:20261020T140000Z", 1)
	body = strings.Replace(body, "DTSTART;TZID=Europe/London:20261020T150000\r\n", "", 1)
	td, err := Parse(strings.NewReader(body), london(t))
	if err != nil {
		t.Fatal(err)
	}
	if td.DueTime != "15:00" {
		t.Fatalf("due time %q, want 15:00 local", td.DueTime)
	}
}

func TestParseAllDayWithAlarmBecomesTimed(t *testing.T) {
	body := strings.NewReplacer(
		"DTSTART;TZID=Europe/London:20261020T150000", "DTSTART;VALUE=DATE:20261020",
		"DUE;TZID=Europe/London:20261020T150000", "DUE;VALUE=DATE:20261020",
		"TRIGGER;VALUE=DATE-TIME:20261020T140000Z", "TRIGGER;VALUE=DATE-TIME:20261020T080000Z",
	).Replace(iosTodo)
	td, err := Parse(strings.NewReader(body), london(t))
	if err != nil {
		t.Fatal(err)
	}
	if td.DueTime != "09:00" || td.ReminderOffset == nil || *td.ReminderOffset != 0 {
		t.Fatalf("got due %q offset %v", td.DueTime, td.ReminderOffset)
	}
}

func TestParseRelativeTriggerSnaps(t *testing.T) {
	body := strings.Replace(iosTodo, "TRIGGER;VALUE=DATE-TIME:20261020T140000Z", "TRIGGER:-PT20M", 1)
	td, err := Parse(strings.NewReader(body), london(t))
	if err != nil {
		t.Fatal(err)
	}
	if td.ReminderOffset == nil || *td.ReminderOffset != 30 {
		t.Fatalf("20 min before should snap to 30, got %v", td.ReminderOffset)
	}
}

func TestSnapOffset(t *testing.T) {
	for in, want := range map[int]int{-5: 0, 0: 0, 5: 5, 7: 10, 45: 60, 1440: 1440, 3000: 1440} {
		if got := SnapOffset(in); got != want {
			t.Errorf("SnapOffset(%d) = %d, want %d", in, got, want)
		}
	}
}

func TestParseRRule(t *testing.T) {
	due := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC) // a Tuesday
	ok := map[string]Recurrence{
		"FREQ=WEEKLY;INTERVAL=2":         {"weeks", 2},
		"FREQ=DAILY":                     {"days", 1},
		"FREQ=WEEKLY;BYDAY=TU":           {"weeks", 1},
		"FREQ=MONTHLY;BYMONTHDAY=20":     {"months", 1},
		"FREQ=YEARLY;BYMONTH=10;WKST=MO": {"years", 1},
	}
	for rule, want := range ok {
		got, err := parseRRule(rule, &due)
		if err != nil || *got != want {
			t.Errorf("%s: got %+v %v, want %+v", rule, got, err, want)
		}
	}
	for _, rule := range []string{"FREQ=WEEKLY;BYDAY=MO,WE", "FREQ=DAILY;COUNT=3", "FREQ=HOURLY", "INTERVAL=2"} {
		if _, err := parseRRule(rule, &due); err == nil {
			t.Errorf("%s: want error", rule)
		}
	}
}

func TestParseRejectsEvents(t *testing.T) {
	ev := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:x\r\nBEGIN:VEVENT\r\nUID:e\r\nDTSTAMP:20261020T101512Z\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	if _, err := Parse(strings.NewReader(ev), time.UTC); err != ErrNotTodo {
		t.Fatalf("got %v, want ErrNotTodo", err)
	}
}

func TestRenderRoundTrip(t *testing.T) {
	loc := london(t)
	td, err := Parse(strings.NewReader(iosTodo), loc)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 20, 10, 15, 12, 0, time.UTC)
	task := &models.Task{
		ID: "t1", Title: td.Title,
		Notes:                 sql.NullString{String: td.Notes, Valid: true},
		DueDate:               sql.NullTime{Time: *td.DueDate, Valid: true},
		DueTime:               sql.NullString{String: td.DueTime + ":00", Valid: true},
		ReminderAt:            sql.NullTime{Time: time.Date(2026, 10, 20, 14, 0, 0, 0, time.UTC), Valid: true},
		ReminderOffsetMinutes: sql.NullInt64{Int64: 0, Valid: true},
		RecurrenceFrequency:   sql.NullString{String: "weeks", Valid: true},
		RecurrenceInterval:    sql.NullInt64{Int64: 2, Valid: true},
		IsImportant:           true,
		ICalUID:               sql.NullString{String: td.UID, Valid: true},
		ICalExtra:             sql.NullString{String: td.Extra, Valid: true},
		CreatedAt:             now, UpdatedAt: now,
	}
	data, err := Render(task, loc)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	for _, want := range []string{
		"UID:6A1B2C3D-0000-4000-8000-ABCDEF012345", "DUE;TZID=Europe/London:20261020T150000", "PRIORITY:1",
		"RRULE:FREQ=WEEKLY;INTERVAL=2", "TRIGGER:PT0S",
		"X-APPLE-SORT-ORDER:742000000", "STATUS:NEEDS-ACTION",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("render missing %q:\n%s", want, s)
		}
	}

	back, err := Parse(strings.NewReader(s), loc)
	if err != nil {
		t.Fatal(err)
	}
	if back.DueTime != "15:00" || *back.ReminderOffset != 0 || !back.Important ||
		*back.Recurrence != (Recurrence{"weeks", 2}) || back.Extra != td.Extra {
		t.Fatalf("round trip lost data: %+v", back)
	}
	again, _ := Render(task, loc)
	if ETag(again) != ETag(data) {
		t.Fatal("render isn't deterministic")
	}
}

func TestRenderTimedWithoutReminderStillAlarms(t *testing.T) {
	now := time.Now().UTC()
	task := &models.Task{
		ID: "t2", Title: "Stand-up",
		DueDate:   sql.NullTime{Time: time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC), Valid: true},
		DueTime:   sql.NullString{String: "09:30", Valid: true},
		CreatedAt: now, UpdatedAt: now,
	}
	data, err := Render(task, london(t))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "TRIGGER:PT0S") {
		t.Fatalf("want an alarm at the due time:\n%s", data)
	}
}

func TestRenderUsesServerZoneWithRules(t *testing.T) {
	loc := london(t)
	now := time.Now().UTC()
	for _, c := range []struct{ date, due, alarm string }{
		{"2026-10-20", "DUE;TZID=Europe/London:20261020T150000", "TRIGGER:PT0S"}, // BST
		{"2026-12-15", "DUE;TZID=Europe/London:20261215T150000", "TRIGGER:PT0S"}, // GMT
	} {
		d, _ := time.Parse("2006-01-02", c.date)
		task := &models.Task{ID: "tz", Title: "x", CreatedAt: now, UpdatedAt: now,
			DueDate: sql.NullTime{Time: d, Valid: true}, DueTime: sql.NullString{String: "15:00", Valid: true}}
		data, err := Render(task, loc)
		if err != nil {
			t.Fatal(err)
		}
		s := string(data)
		for _, want := range []string{
			c.due, c.alarm, "BEGIN:VTIMEZONE", "TZID:Europe/London",
			"BEGIN:DAYLIGHT", "RRULE:FREQ=YEARLY;BYMONTH=3;BYDAY=-1SU", "TZOFFSETTO:+0100", "TZNAME:BST",
			"BEGIN:STANDARD", "RRULE:FREQ=YEARLY;BYMONTH=10;BYDAY=-1SU", "TZOFFSETTO:+0000", "TZNAME:GMT",
		} {
			if !strings.Contains(s, want) {
				t.Errorf("%s: missing %q:\n%s", c.date, want, s)
			}
		}
		// What iOS sends back must mean the same wall-clock time.
		back, err := Parse(strings.NewReader(s), loc)
		if err != nil || back.DueTime != "15:00" {
			t.Fatalf("%s: round trip %v %v", c.date, back, err)
		}
	}
}

func TestRenderUTCZoneUsesZ(t *testing.T) {
	now := time.Now().UTC()
	task := &models.Task{ID: "u", Title: "x", CreatedAt: now, UpdatedAt: now,
		DueDate: sql.NullTime{Time: time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC), Valid: true},
		DueTime: sql.NullString{String: "15:00", Valid: true}}
	data, err := Render(task, time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "DUE:20261020T150000Z") || strings.Contains(string(data), "VTIMEZONE") {
		t.Fatalf("UTC server:\n%s", data)
	}
}

func TestRenderEarlyAlarmIsRelative(t *testing.T) {
	loc := london(t)
	now := time.Now().UTC()
	task := &models.Task{ID: "d", Title: "Call the dentist", CreatedAt: now, UpdatedAt: now,
		DueDate:               sql.NullTime{Time: time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC), Valid: true},
		DueTime:               sql.NullString{String: "10:00", Valid: true},
		ReminderAt:            sql.NullTime{Time: time.Date(2026, 10, 21, 8, 45, 0, 0, time.UTC), Valid: true},
		ReminderOffsetMinutes: sql.NullInt64{Int64: 15, Valid: true}}
	data, err := Render(task, loc)
	if err != nil {
		t.Fatal(err)
	}
	if s := string(data); !strings.Contains(s, "TRIGGER:-PT15M") || strings.Contains(s, "084500Z") {
		t.Fatalf("want a relative trigger and no UTC instant:\n%s", s)
	}
	back, err := Parse(strings.NewReader(string(data)), loc)
	if err != nil || back.DueTime != "10:00" || *back.ReminderOffset != 15 {
		t.Fatalf("round trip: %+v %v", back, err)
	}
}
