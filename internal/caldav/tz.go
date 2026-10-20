package caldav

import (
	"fmt"
	"time"

	"github.com/emersion/go-ical"
)

// Times go out as TZID=<server zone> with a matching VTIMEZONE, not as UTC:
// iOS keeps whatever zone a reminder arrives in, so a UTC reminder shows a
// second time zone and the next edit is made in UTC (picking "4pm" stores
// 16:00Z, which is 17:00 BST).

// isUTC reports whether loc can be written with the plain Z suffix.
func isUTC(loc *time.Location) bool {
	if loc == time.UTC {
		return true
	}
	name := loc.String()
	return name == "UTC" || name == "Etc/UTC" || name == ""
}

// transition is one change of UTC offset.
type transition struct {
	at       time.Time // the instant, UTC
	from, to int       // offsets in seconds
	name     string    // abbreviation after the change ("BST")
	dst      bool
}

// transitions finds every offset change in [start, end) by stepping a day at
// a time and bisecting to the second.
func transitions(loc *time.Location, start, end time.Time) []transition {
	var out []transition
	_, prevOff := start.In(loc).Zone()
	for t := start; t.Before(end); t = t.Add(24 * time.Hour) {
		next := t.Add(24 * time.Hour)
		if _, off := next.In(loc).Zone(); off == prevOff {
			continue
		}
		lo, hi := t, next
		for hi.Sub(lo) > time.Second {
			mid := lo.Add(hi.Sub(lo) / 2)
			if _, off := mid.In(loc).Zone(); off == prevOff {
				lo = mid
			} else {
				hi = mid
			}
		}
		name, off := hi.In(loc).Zone()
		out = append(out, transition{at: hi.UTC(), from: prevOff, to: off, name: name, dst: hi.In(loc).IsDST()})
		prevOff = off
	}
	return out
}

// byDayRule describes a transition as "the nth (or last) weekday of the
// month", which is how almost every DST rule is written.
func byDayRule(tr transition) string {
	local := tr.at.Add(time.Duration(tr.from) * time.Second)
	wd := [...]string{"SU", "MO", "TU", "WE", "TH", "FR", "SA"}[local.Weekday()]
	n := (local.Day()-1)/7 + 1
	if local.AddDate(0, 0, 7).Month() != local.Month() {
		n = -1
	}
	return fmt.Sprintf("FREQ=YEARLY;BYMONTH=%d;BYDAY=%d%s", int(local.Month()), n, wd)
}

func fmtOffset(sec int) string {
	sign := "+"
	if sec < 0 {
		sign, sec = "-", -sec
	}
	return fmt.Sprintf("%s%02d%02d", sign, sec/3600, sec%3600/60)
}

func localStamp(tr transition) string {
	return tr.at.Add(time.Duration(tr.from) * time.Second).Format("20060102T150405")
}

// vtimezone builds the VTIMEZONE for loc around year: one STANDARD and one
// DAYLIGHT sub-component with an RRULE when the zone follows a fixed yearly
// rule (checked over several years), otherwise explicit RDATEs.
func vtimezone(loc *time.Location, year int) *ical.Component {
	tz := ical.NewComponent(ical.CompTimezone)
	setRaw(tz.Props, ical.PropTimezoneID, loc.String())

	start := time.Date(year-1, 1, 1, 0, 0, 0, 0, time.UTC)
	trs := transitions(loc, start, start.AddDate(6, 0, 0))
	if len(trs) == 0 {
		name, off := start.In(loc).Zone()
		std := ical.NewComponent(ical.CompTimezoneStandard)
		setRaw(std.Props, ical.PropDateTimeStart, "19700101T000000")
		setRaw(std.Props, ical.PropTimezoneOffsetFrom, fmtOffset(off))
		setRaw(std.Props, ical.PropTimezoneOffsetTo, fmtOffset(off))
		setRaw(std.Props, ical.PropTimezoneName, name)
		tz.Children = append(tz.Children, std)
		return tz
	}

	// Group by kind (and offsets, in case a zone changed its rules).
	type key struct {
		dst      bool
		from, to int
	}
	groups := map[key][]transition{}
	var order []key
	for _, tr := range trs {
		k := key{tr.dst, tr.from, tr.to}
		if _, ok := groups[k]; !ok {
			order = append(order, k)
		}
		groups[k] = append(groups[k], tr)
	}
	for _, k := range order {
		g := groups[k]
		name := ical.CompTimezoneStandard
		if k.dst {
			name = ical.CompTimezoneDaylight
		}
		c := ical.NewComponent(name)
		setRaw(c.Props, ical.PropDateTimeStart, localStamp(g[0]))
		setRaw(c.Props, ical.PropTimezoneOffsetFrom, fmtOffset(k.from))
		setRaw(c.Props, ical.PropTimezoneOffsetTo, fmtOffset(k.to))
		setRaw(c.Props, ical.PropTimezoneName, g[0].name)

		rule := byDayRule(g[0])
		regular := len(g) > 1
		for _, tr := range g[1:] {
			if byDayRule(tr) != rule || localStamp(tr)[8:] != localStamp(g[0])[8:] {
				regular = false
			}
		}
		if regular {
			setRaw(c.Props, ical.PropRecurrenceRule, rule)
		} else {
			for _, tr := range g[1:] {
				c.Props.Add(&ical.Prop{Name: ical.PropRecurrenceDates, Params: ical.Params{}, Value: localStamp(tr)})
			}
		}
		tz.Children = append(tz.Children, c)
	}
	return tz
}

// setLocalDateTime writes a DATE-TIME as wall clock in loc with its TZID, or
// with Z when loc is UTC.
func setLocalDateTime(p ical.Props, name string, t time.Time, loc *time.Location) {
	pr := ical.NewProp(name)
	if isUTC(loc) {
		pr.Value = t.UTC().Format("20060102T150405Z")
	} else {
		pr.Params.Set(ical.ParamTimezoneID, loc.String())
		pr.Value = t.In(loc).Format("20060102T150405")
	}
	p.Set(pr)
}
