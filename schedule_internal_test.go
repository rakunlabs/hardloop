package hardloop

import (
	"math/rand"
	"testing"
	"time"
)

// refMatch is an independent reference implementation of the schedule semantics.
// Hardloop uses AND between day of month and day of week.
func refMatch(s *SpecSchedule, t time.Time) bool {
	has := func(bits uint64, v int) bool { return bits&(1<<uint(v)) != 0 }

	return t.Second() == 0 &&
		has(s.Minute, t.Minute()) &&
		has(s.Hour, t.Hour()) &&
		has(s.Dom, t.Day()) &&
		has(s.Month, int(t.Month())) &&
		has(s.Dow, int(t.Weekday()))
}

func refNext(s *SpecSchedule, t time.Time, limit time.Time) time.Time {
	c := t.Truncate(time.Minute)
	for ; !c.After(limit); c = c.Add(time.Minute) {
		if c.After(t) && refMatch(s, c) {
			return c
		}
	}

	return time.Time{}
}

func refPrev(s *SpecSchedule, t time.Time, limit time.Time) time.Time {
	c := t.Truncate(time.Minute)
	for ; !c.Before(limit); c = c.Add(-time.Minute) {
		if c.Before(t) && refMatch(s, c) {
			return c
		}
	}

	return time.Time{}
}

var bruteForceSpecs = []string{
	"* * * * *",
	"0 7 * * *",
	"*/7 * * * *",
	"30 */5 * * *",
	"0 9-17 * * 1-5",
	"15 3 1 * *",
	"0 0 31 * *",
	"0 12 13 * 5",     // friday the 13th
	"0 0 * * 0",       // sundays
	"0 6 1,15 * 1",    // 1st or 15th only when monday
	"45 23 28-31 2 *", // end of february, includes leap days
	"0 0 1 1 *",
	"5 4 * 3,6,9,12 2,4",
	"59 23 31 12 *",
}

func TestSchedule_BruteForceUTC(t *testing.T) {
	r := rand.New(rand.NewSource(42))
	start := time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC)
	window := 2 * 365 * 24 * time.Hour

	for _, spec := range bruteForceSpecs {
		t.Run(spec, func(t *testing.T) {
			s, err := Parse("CRON_TZ=UTC " + spec)
			if err != nil {
				t.Fatal(err)
			}

			for i := 0; i < 30; i++ {
				now := start.Add(time.Duration(r.Int63n(int64(window))))
				// randomly use exact minutes, exact seconds and sub-second times
				switch i % 3 {
				case 0:
					now = now.Truncate(time.Minute)
				case 1:
					now = now.Truncate(time.Second)
				}

				limitNext := now.AddDate(YearLimit, 0, 0)
				if want, got := refNext(s, now, limitNext), s.Next(now); !got.Equal(want) {
					t.Fatalf("Next(%s) = %s, want %s", now.Format(time.RFC3339Nano), got, want)
				}

				limitPrev := now.AddDate(-YearLimit, 0, 0)
				if want, got := refPrev(s, now, limitPrev), s.Prev(now); !got.Equal(want) {
					t.Fatalf("Prev(%s) = %s, want %s", now.Format(time.RFC3339Nano), got, want)
				}
			}
		})
	}
}

func TestSchedule_NextPrevChain(t *testing.T) {
	// walking with Next and Prev must visit the same points in both directions
	for _, spec := range bruteForceSpecs {
		t.Run(spec, func(t *testing.T) {
			s, err := Parse("CRON_TZ=UTC " + spec)
			if err != nil {
				t.Fatal(err)
			}

			cur := time.Date(2024, 2, 20, 10, 11, 12, 0, time.UTC)
			forward := make([]time.Time, 0, 20)
			for range 20 {
				cur = s.Next(cur)
				if cur.IsZero() {
					t.Fatal("Next returned zero")
				}
				if !refMatch(s, cur) {
					t.Fatalf("Next returned non matching time %s", cur)
				}
				forward = append(forward, cur)
			}

			for i := len(forward) - 1; i > 0; i-- {
				if got := s.Prev(forward[i]); !got.Equal(forward[i-1]) {
					t.Fatalf("Prev(%s) = %s, want %s", forward[i], got, forward[i-1])
				}
			}
		})
	}
}

func TestSchedule_DayOfMonthAndWeekUseAnd(t *testing.T) {
	// friday the 13th, both fields restricted
	s, err := Parse("CRON_TZ=UTC 0 0 13 * 5")
	if err != nil {
		t.Fatal(err)
	}

	got := s.Next(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC))
	want := time.Date(2024, 9, 13, 0, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("Next() = %s, want %s", got, want)
	}

	got = s.Prev(want)
	want = time.Date(2023, 10, 13, 0, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("Prev() = %s, want %s", got, want)
	}
}

func TestSchedule_Impossible(t *testing.T) {
	s, err := Parse("0 0 30 2 *")
	if err != nil {
		t.Fatal(err)
	}

	now := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	if got := s.Next(now); !got.IsZero() {
		t.Errorf("Next() = %s, want zero", got)
	}
	if got := s.Prev(now); !got.IsZero() {
		t.Errorf("Prev() = %s, want zero", got)
	}
}

func TestSchedule_LeapDay(t *testing.T) {
	s, err := Parse("CRON_TZ=UTC 0 0 29 2 *")
	if err != nil {
		t.Fatal(err)
	}

	now := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	if got, want := s.Next(now), time.Date(2028, 2, 29, 0, 0, 0, 0, time.UTC); !got.Equal(want) {
		t.Errorf("Next() = %s, want %s", got, want)
	}
	if got, want := s.Prev(now), time.Date(2024, 2, 29, 0, 0, 0, 0, time.UTC); !got.Equal(want) {
		t.Errorf("Prev() = %s, want %s", got, want)
	}
}

func TestSchedule_KeepsInputLocation(t *testing.T) {
	ist, err := time.LoadLocation("Europe/Istanbul") // UTC+3, no DST
	if err != nil {
		t.Fatal(err)
	}

	s, err := Parse("CRON_TZ=Europe/Istanbul 0 7 * * *")
	if err != nil {
		t.Fatal(err)
	}

	now := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)

	next := s.Next(now)
	if next.Location() != time.UTC {
		t.Errorf("Next location = %s, want UTC", next.Location())
	}
	if want := time.Date(2024, 6, 1, 7, 0, 0, 0, ist); !next.Equal(want) {
		t.Errorf("Next() = %s, want %s", next, want)
	}

	prev := s.Prev(now)
	if want := time.Date(2024, 5, 31, 7, 0, 0, 0, ist); !prev.Equal(want) {
		t.Errorf("Prev() = %s, want %s", prev, want)
	}

	// schedule without timezone uses the location of the given time
	local, err := Parse("0 7 * * *")
	if err != nil {
		t.Fatal(err)
	}
	nowIST := time.Date(2024, 6, 1, 0, 0, 0, 0, ist)
	if got, want := local.Next(nowIST), time.Date(2024, 6, 1, 7, 0, 0, 0, ist); !got.Equal(want) {
		t.Errorf("Next() = %s, want %s", got, want)
	}
}

func TestSchedule_NilLocation(t *testing.T) {
	s := &SpecSchedule{
		Second: 1,
		Minute: 1,
		Hour:   1 << 7,
		Dom:    all(dom),
		Month:  all(months),
		Dow:    all(dow),
	}

	now := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	if got, want := s.Next(now), time.Date(2024, 1, 1, 7, 0, 0, 0, time.UTC); !got.Equal(want) {
		t.Errorf("Next() = %s, want %s", got, want)
	}
	if got, want := s.Prev(now), time.Date(2023, 12, 31, 7, 0, 0, 0, time.UTC); !got.Equal(want) {
		t.Errorf("Prev() = %s, want %s", got, want)
	}
}

func TestSchedule_DSTFallBackRunsOnce(t *testing.T) {
	ams, err := time.LoadLocation("Europe/Amsterdam")
	if err != nil {
		t.Fatal(err)
	}

	// 2024-10-27 03:00 CEST -> 02:00 CET, 02:00-03:00 happens twice
	cest := time.FixedZone("CEST", 2*60*60)
	cet := time.FixedZone("CET", 1*60*60)

	tests := []struct {
		spec string
		want []time.Time
	}{
		{
			spec: "CRON_TZ=Europe/Amsterdam 30 2 * * *",
			want: []time.Time{
				time.Date(2024, 10, 26, 2, 30, 0, 0, cest),
				time.Date(2024, 10, 27, 2, 30, 0, 0, cest), // first occurrence only
				time.Date(2024, 10, 28, 2, 30, 0, 0, cet),
			},
		},
		{
			spec: "CRON_TZ=Europe/Amsterdam 0,30 2-3 * * *",
			want: []time.Time{
				time.Date(2024, 10, 27, 2, 0, 0, 0, cest),
				time.Date(2024, 10, 27, 2, 30, 0, 0, cest),
				time.Date(2024, 10, 27, 3, 0, 0, 0, cet),
				time.Date(2024, 10, 27, 3, 30, 0, 0, cet),
			},
		},
		{
			// every 30 minutes are different instants, all of them run
			spec: "CRON_TZ=Europe/Amsterdam */30 * * * *",
			want: []time.Time{
				time.Date(2024, 10, 27, 2, 0, 0, 0, cest),
				time.Date(2024, 10, 27, 2, 30, 0, 0, cest),
				time.Date(2024, 10, 27, 2, 0, 0, 0, cet),
				time.Date(2024, 10, 27, 2, 30, 0, 0, cet),
				time.Date(2024, 10, 27, 3, 0, 0, 0, cet),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.spec, func(t *testing.T) {
			s, err := Parse(tt.spec)
			if err != nil {
				t.Fatal(err)
			}

			cur := tt.want[0].Add(-time.Second)
			for i, want := range tt.want {
				cur = s.Next(cur)
				if !cur.Equal(want) {
					t.Fatalf("Next[%d] = %s, want %s", i, cur.In(ams), want)
				}
			}

			cur = tt.want[len(tt.want)-1].Add(time.Second)
			for i := len(tt.want) - 1; i >= 0; i-- {
				cur = s.Prev(cur)
				if !cur.Equal(tt.want[i]) {
					t.Fatalf("Prev[%d] = %s, want %s", i, cur.In(ams), tt.want[i])
				}
			}
		})
	}
}

func TestSchedule_DST(t *testing.T) {
	ams, err := time.LoadLocation("Europe/Amsterdam")
	if err != nil {
		t.Fatal(err)
	}

	s, err := Parse("CRON_TZ=Europe/Amsterdam 0 7 * * *")
	if err != nil {
		t.Fatal(err)
	}

	// spring forward: 2024-03-31 02:00 -> 03:00
	// fall back: 2024-10-27 03:00 -> 02:00
	for _, day := range []int{30, 31} {
		now := time.Date(2024, 3, day, 0, 0, 0, 0, ams)
		if got, want := s.Next(now), time.Date(2024, 3, day, 7, 0, 0, 0, ams); !got.Equal(want) {
			t.Errorf("Next(%s) = %s, want %s", now, got, want)
		}
	}

	now := time.Date(2024, 10, 28, 0, 0, 0, 0, ams)
	if got, want := s.Prev(now), time.Date(2024, 10, 27, 7, 0, 0, 0, ams); !got.Equal(want) {
		t.Errorf("Prev(%s) = %s, want %s", now, got, want)
	}

	now = time.Date(2024, 4, 1, 0, 0, 0, 0, ams)
	if got, want := s.Prev(now), time.Date(2024, 3, 31, 7, 0, 0, 0, ams); !got.Equal(want) {
		t.Errorf("Prev(%s) = %s, want %s", now, got, want)
	}
}

func TestSchedule_PrevAcrossMissingMidnight(t *testing.T) {
	loc, err := time.LoadLocation("America/Sao_Paulo")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		spec string
		now  time.Time
		want time.Time
	}{
		{
			// Midnight on November 4 was skipped, but November 3 at 23:00 exists.
			spec: "0 23 3 11 *",
			now:  time.Date(2018, 11, 4, 12, 0, 0, 0, loc),
			want: time.Date(2018, 11, 3, 23, 0, 0, 0, loc),
		},
		{
			// The same transition at the start of a month must not skip October 31.
			spec: "0 23 31 10 *",
			now:  time.Date(2009, 11, 2, 12, 0, 0, 0, loc),
			want: time.Date(2009, 10, 31, 23, 0, 0, 0, loc),
		},
	}
	for _, tt := range tests {
		t.Run(tt.spec, func(t *testing.T) {
			s, err := Parse("CRON_TZ=America/Sao_Paulo " + tt.spec)
			if err != nil {
				t.Fatal(err)
			}
			if got := s.Prev(tt.now); !got.Equal(tt.want) {
				t.Fatalf("Prev(%s) = %s, want %s", tt.now, got, tt.want)
			}
		})
	}
}

func TestStartOfDay(t *testing.T) {
	tests := []struct {
		zone string
		now  time.Time
		want time.Time
	}{
		{
			zone: "UTC",
			now:  time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC),
			want: time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC),
		},
		{
			zone: "America/Sao_Paulo",
			now:  time.Date(2018, 11, 4, 12, 0, 0, 0, time.UTC),
			want: time.Date(2018, 11, 4, 3, 0, 0, 0, time.UTC),
		},
		{
			zone: "America/Havana",
			now:  time.Date(2024, 11, 3, 12, 0, 0, 0, time.UTC),
			want: time.Date(2024, 11, 3, 4, 0, 0, 0, time.UTC),
		},
	}
	for _, tt := range tests {
		t.Run(tt.zone, func(t *testing.T) {
			loc, err := time.LoadLocation(tt.zone)
			if err != nil {
				t.Fatal(err)
			}
			if got := startOfDay(tt.now.In(loc)); !got.Equal(tt.want) {
				t.Fatalf("startOfDay(%s) = %s, want %s", tt.now.In(loc), got, tt.want)
			}
		})
	}
}

func TestScheduleGroup_WindowBoundaries(t *testing.T) {
	group, err := NewSchedule(
		[]string{"CRON_TZ=UTC 0 7 * * *"},
		[]string{"CRON_TZ=UTC 0 8 * * *"},
	)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2024, 6, 1, 7, 0, 0, 0, time.UTC)
	stop := start.Add(time.Hour)
	for _, tt := range []struct {
		now     time.Time
		running bool
	}{
		{start.Add(-time.Nanosecond), false},
		{start, true},
		{stop.Add(-time.Nanosecond), true},
		{stop, false},
	} {
		t.Run(tt.now.Format(time.RFC3339Nano), func(t *testing.T) {
			got := group.getStopTime(tt.now)
			if (got != nil) != tt.running {
				t.Fatalf("running = %v, want %v", got != nil, tt.running)
			}
			if got != nil && !got.Equal(stop) {
				t.Fatalf("stop time = %s, want %s", got, stop)
			}
		})
	}
}

func TestFindNextPrev(t *testing.T) {
	a, _ := Parse("CRON_TZ=UTC 0 7 * * *")
	b, _ := Parse("CRON_TZ=UTC 0 9 * * *")
	never, _ := Parse("CRON_TZ=UTC 0 0 30 2 *")

	now := time.Date(2024, 1, 1, 8, 0, 0, 0, time.UTC)

	if got, want := FindNext([]Schedule{a, b, never}, now), time.Date(2024, 1, 1, 9, 0, 0, 0, time.UTC); !got.Equal(want) {
		t.Errorf("FindNext() = %s, want %s", got, want)
	}
	if got, want := FindPrev([]Schedule{never, b, a}, now), time.Date(2024, 1, 1, 7, 0, 0, 0, time.UTC); !got.Equal(want) {
		t.Errorf("FindPrev() = %s, want %s", got, want)
	}
	if got := FindNext([]Schedule{never}, now); !got.IsZero() {
		t.Errorf("FindNext() = %s, want zero", got)
	}
	if got := FindPrev(nil, now); !got.IsZero() {
		t.Errorf("FindPrev() = %s, want zero", got)
	}
}
