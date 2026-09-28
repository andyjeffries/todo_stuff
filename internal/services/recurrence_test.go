package services

import (
	"testing"
	"time"

	"github.com/andyjessop/todostuff/internal/models"
)

// withLocal swaps time.Local for the duration of a test.
func withLocal(t *testing.T, loc *time.Location) {
	t.Helper()
	orig := time.Local
	time.Local = loc
	t.Cleanup(func() { time.Local = orig })
}

// Both "today" computations use the server-local calendar date, not UTC.
func TestLocalDateNotUTC(t *testing.T) {
	cases := []struct {
		name  string
		zone  *time.Location
		now   time.Time // UTC instant
		today string    // local date at that instant
	}{
		{"ahead of UTC", time.FixedZone("+14", 14*3600), time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC), "2026-09-28"},
		{"behind UTC", time.FixedZone("-11", -11*3600), time.Date(2026, 9, 28, 5, 0, 0, 0, time.UTC), "2026-09-27"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withLocal(t, tc.zone)

			for _, v := range []View{ViewToday, ViewUpcoming} {
				if _, _, args := viewQuery("u", v, tc.now); args[1] != tc.today {
					t.Errorf("viewQuery(%s) today = %v, want %s", v, args[1], tc.today)
				}
			}

			rule := &models.RecurrenceRule{RegenerationType: "on_complete", Frequency: "days", Interval: 1}
			got := computeNextDueDate(rule, &models.Task{}, tc.now)
			want, _ := time.Parse("2006-01-02", tc.today)
			if want = want.AddDate(0, 0, 1); !got.Equal(want) {
				t.Errorf("on_complete next due = %v, want %v", got, want)
			}
		})
	}
}
