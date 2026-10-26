package hardloop

import (
	"time"
)

// YearLimit is the maximum number of years to search for a matching time.
var YearLimit = 5

// Schedule describes a recurring time.
type Schedule interface {
	// Next returns the next time this schedule is activated, greater than the given time.
	Next(time.Time) time.Time
	// Prev returns the previous time this schedule is activated, less than the given time.
	Prev(time.Time) time.Time
}

// SpecSchedule specifies a duty cycle (to the second granularity), based on a
// traditional crontab specification. It is computed initially and stored as bit sets.
type SpecSchedule struct {
	ConstantDelaySchedule time.Duration

	Second, Minute, Hour, Dom, Month, Dow uint64

	// Override location for this schedule.
	// time.Local or nil uses the location of the given time.
	Location *time.Location
}

var _ Schedule = &SpecSchedule{}

const (
	// Set the top bit if a star was included in the expression.
	starBit = 1 << 63
)

// Next returns the next time this schedule is activated, greater than the given
// time.  If no time can be found to satisfy the schedule, return the zero time.
//
// When clocks are turned back (DST end), wall clock times are repeated. Schedules
// with a fixed hour run only in the first occurrence, like standard cron.
func (s *SpecSchedule) Next(t time.Time) time.Time {
	for {
		t = s.next(t)
		if t.IsZero() || !s.isRepeated(t) {
			return t
		}
	}
}

// Prev returns the previous time this schedule is activated, less than the given
// time.  If no time can be found to satisfy the schedule, return the zero time.
//
// Repeated wall clock times are skipped like in Next.
func (s *SpecSchedule) Prev(t time.Time) time.Time {
	for {
		t = s.prev(t)
		if t.IsZero() || !s.isRepeated(t) {
			return t
		}
	}
}

// isRepeated reports whether t is the second occurrence of a wall clock time
// after clocks are turned back, for schedules with a fixed hour.
func (s *SpecSchedule) isRepeated(t time.Time) bool {
	if s.ConstantDelaySchedule != 0 || s.Hour&starBit != 0 {
		return false
	}

	if s.Location != nil && s.Location != time.Local {
		t = t.In(s.Location)
	}

	start, _ := t.ZoneBounds()
	if start.IsZero() {
		return false
	}

	_, offset := t.Zone()
	_, prevOffset := start.Add(-time.Second).Zone()
	if prevOffset <= offset {
		return false
	}

	// wall clock times in [start, start+diff) already happened in the previous zone
	return t.Before(start.Add(time.Duration(prevOffset-offset) * time.Second))
}

func (s *SpecSchedule) next(t time.Time) time.Time {
	if s.ConstantDelaySchedule != 0 {
		return t.Add(s.ConstantDelaySchedule - time.Duration(t.Nanosecond())*time.Nanosecond)
	}

	// General approach
	//
	// For Month, Day, Hour, Minute, Second:
	// Check if the time value matches.  If yes, continue to the next field.
	// If the field doesn't match the schedule, then increment the field until it matches.
	// While incrementing the field, a wrap-around brings it back to the beginning
	// of the field list (since it is necessary to re-verify previous field
	// values)

	// Convert the given time into the schedule's timezone, if one is specified.
	// Save the original timezone so we can convert back after we find a time.
	// Note that schedules without a time zone specified (time.Local) are treated
	// as local to the time provided.
	origLocation := t.Location()
	loc := s.Location
	if loc == nil || loc == time.Local {
		loc = t.Location()
	} else {
		t = t.In(loc)
	}

	// Start at the earliest possible time (the upcoming second).
	t = t.Add(1*time.Second - time.Duration(t.Nanosecond())*time.Nanosecond)

	// This flag indicates whether a field has been incremented.
	added := false

	// If no time is found within five years, return zero.
	yearLimit := t.Year() + YearLimit

WRAP:
	if t.Year() > yearLimit {
		return time.Time{}
	}

	// Find the first applicable month.
	// If it's this month, then do nothing.
	for 1<<uint(t.Month())&s.Month == 0 {
		// If we have to add a month, reset the other parts to 0.
		if !added {
			added = true
			// Otherwise, set the date at the beginning (since the current time is irrelevant).
			t = time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, loc)
		}
		t = t.AddDate(0, 1, 0)

		// Wrapped around.
		if t.Month() == time.January {
			goto WRAP
		}
	}

	// Now get a day in that month.
	//
	// NOTE: This causes issues for daylight savings regimes where midnight does
	// not exist.  For example: Sao Paulo has DST that transforms midnight on
	// 11/3 into 1am. Handle that by noticing when the Hour ends up != 0.
	for !dayMatches(s, t) {
		if !added {
			added = true
			t = time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)
		}
		t = t.AddDate(0, 0, 1)
		// Notice if the hour is no longer midnight due to DST.
		// Add an hour if it's 23, subtract an hour if it's 1.
		if t.Hour() != 0 {
			if t.Hour() > 12 {
				t = t.Add(time.Duration(24-t.Hour()) * time.Hour)
			} else {
				t = t.Add(time.Duration(-t.Hour()) * time.Hour)
			}
		}

		if t.Day() == 1 {
			goto WRAP
		}
	}

	for 1<<uint(t.Hour())&s.Hour == 0 {
		if !added {
			added = true
			t = time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), 0, 0, 0, loc)
		}
		t = t.Add(1 * time.Hour)

		if t.Hour() == 0 {
			goto WRAP
		}
	}

	for 1<<uint(t.Minute())&s.Minute == 0 {
		if !added {
			added = true
			t = t.Truncate(time.Minute)
		}
		t = t.Add(1 * time.Minute)

		if t.Minute() == 0 {
			goto WRAP
		}
	}

	for 1<<uint(t.Second())&s.Second == 0 {
		if !added {
			added = true
			t = t.Truncate(time.Second)
		}
		t = t.Add(1 * time.Second)

		if t.Second() == 0 {
			goto WRAP
		}
	}

	return t.In(origLocation)
}

func (s *SpecSchedule) prev(t time.Time) time.Time {
	if s.ConstantDelaySchedule != 0 {
		return t.Add(-s.ConstantDelaySchedule - time.Duration(t.Nanosecond())*time.Nanosecond)
	}

	// General approach
	//
	// For Month, Day, Hour, Minute, Second:
	// Check if the time value matches.  If yes, continue to the next field.
	// If the field doesn't match the schedule, then move to the last second of
	// the previous field value until it matches.
	// While decrementing the field, a wrap-around brings it back to the beginning
	// of the field list (since it is necessary to re-verify previous field
	// values)

	// Convert the given time into the schedule's timezone, if one is specified.
	// Save the original timezone so we can convert back after we find a time.
	// Note that schedules without a time zone specified (time.Local) are treated
	// as local to the time provided.
	origLocation := t.Location()
	loc := s.Location
	if loc == nil || loc == time.Local {
		loc = t.Location()
	} else {
		t = t.In(loc)
	}

	// Start at the latest possible time (the previous whole second).
	if t.Nanosecond() > 0 {
		t = t.Add(-time.Duration(t.Nanosecond()) * time.Nanosecond)
	} else {
		t = t.Add(-time.Second)
	}

	// If no time is found within five years, return zero.
	yearLimit := t.Year() - YearLimit

WRAP:
	if t.Year() < yearLimit {
		return time.Time{}
	}

	for 1<<uint(t.Month())&s.Month == 0 {
		// Last second of the previous month.
		firstDay := time.Date(t.Year(), t.Month(), 1, 12, 0, 0, 0, loc)
		t = startOfDay(firstDay).Add(-time.Second)

		if t.Month() == time.December {
			goto WRAP
		}
	}

	for !dayMatches(s, t) {
		month := t.Month()
		// Last second of the previous day.
		t = startOfDay(t).Add(-time.Second)

		if t.Month() != month {
			goto WRAP
		}
	}

	// Hours, minutes and seconds are decremented with absolute durations to
	// visit repeated wall clock hours in DST changes.
	for 1<<uint(t.Hour())&s.Hour == 0 {
		day := t.Day()
		// Last second of the previous hour.
		t = t.Add(-time.Duration(t.Minute())*time.Minute - time.Duration(t.Second()+1)*time.Second)

		if t.Day() != day {
			goto WRAP
		}
	}

	for 1<<uint(t.Minute())&s.Minute == 0 {
		hour := t.Hour()
		// Last second of the previous minute.
		t = t.Add(-time.Duration(t.Second()+1) * time.Second)

		if t.Hour() != hour {
			goto WRAP
		}
	}

	for 1<<uint(t.Second())&s.Second == 0 {
		minute := t.Minute()
		t = t.Add(-time.Second)

		if t.Minute() != minute {
			goto WRAP
		}
	}

	return t.In(origLocation)
}

// startOfDay returns the first instant of t's local calendar day. Midnight can
// be missing or repeated when the UTC offset changes.
func startOfDay(t time.Time) time.Time {
	year, month, day := t.Date()
	sameDay := func(v time.Time) bool {
		y, m, d := v.Date()
		return y == year && m == month && d == day
	}

	start := time.Date(year, month, day, 0, 0, 0, 0, t.Location())
	if !sameDay(start) {
		// A missing midnight may normalize to the previous day. The end of
		// that UTC-offset interval is the first instant of the requested day.
		_, end := start.ZoneBounds()
		if !end.IsZero() && sameDay(end) {
			start = end
		}
	}

	// If Date chose the second midnight, move back to the first occurrence.
	zoneStart, _ := start.ZoneBounds()
	if !zoneStart.IsZero() {
		_, offset := start.Zone()
		_, prevOffset := zoneStart.Add(-time.Second).Zone()
		if prevOffset > offset {
			earlier := start.Add(time.Duration(offset-prevOffset) * time.Second)
			if sameDay(earlier) {
				start = earlier
			}
		}
	}

	return start
}

// edited function of github.com/robfig/cron/v3 to change day of week and day of month both usage.
func dayMatches(s *SpecSchedule, t time.Time) bool {
	var (
		domMatch = 1<<uint(t.Day())&s.Dom > 0
		dowMatch = 1<<uint(t.Weekday())&s.Dow > 0
		domStar  = s.Dom&starBit > 0
		dowStar  = s.Dow&starBit > 0
	)

	if domStar && dowStar {
		return domMatch || dowMatch
	}

	if !domStar && !dowStar {
		return domMatch && dowMatch
	}

	if !dowStar && dowMatch {
		return dowMatch
	}

	if !domStar && domMatch {
		return domMatch
	}

	return domMatch && dowMatch
}
