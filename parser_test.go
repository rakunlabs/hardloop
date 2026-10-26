package hardloop

import (
	"strings"
	"testing"
	"time"
)

func TestParse_Fields(t *testing.T) {
	tests := []struct {
		name string
		spec string
		want SpecSchedule
	}{
		{
			name: "all stars",
			spec: "* * * * *",
			want: SpecSchedule{
				Second: 1,
				Minute: all(minutes),
				Hour:   all(hours),
				Dom:    all(dom),
				Month:  all(months),
				Dow:    all(dow),
			},
		},
		{
			name: "question mark is star",
			spec: "0 0 ? * ?",
			want: SpecSchedule{
				Second: 1,
				Minute: 1,
				Hour:   1,
				Dom:    all(dom),
				Month:  all(months),
				Dow:    all(dow),
			},
		},
		{
			name: "list range and step",
			spec: "1,2,30 8-10 */10 1-12/6 mon-fri",
			want: SpecSchedule{
				Second: 1,
				Minute: 1<<1 | 1<<2 | 1<<30,
				Hour:   1<<8 | 1<<9 | 1<<10,
				Dom:    1<<1 | 1<<11 | 1<<21 | 1<<31,
				Month:  1<<1 | 1<<7,
				Dow:    1<<1 | 1<<2 | 1<<3 | 1<<4 | 1<<5,
			},
		},
		{
			name: "step from single number",
			spec: "50/5 0 1 jan sun",
			want: SpecSchedule{
				Second: 1,
				Minute: 1<<50 | 1<<55,
				Hour:   1,
				Dom:    1 << 1,
				Month:  1 << 1,
				Dow:    1,
			},
		},
		{
			name: "names are case insensitive",
			spec: "0 0 1 DEC Sat",
			want: SpecSchedule{
				Second: 1,
				Minute: 1,
				Hour:   1,
				Dom:    1 << 1,
				Month:  1 << 12,
				Dow:    1 << 6,
			},
		},
		{
			name: "star with step 1 keeps star bit",
			spec: "*/1 * * * *",
			want: SpecSchedule{
				Second: 1,
				Minute: all(minutes),
				Hour:   all(hours),
				Dom:    all(dom),
				Month:  all(months),
				Dow:    all(dow),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse(tt.spec)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			assertBits(t, "second", got.Second, tt.want.Second)
			assertBits(t, "minute", got.Minute, tt.want.Minute)
			assertBits(t, "hour", got.Hour, tt.want.Hour)
			assertBits(t, "dom", got.Dom, tt.want.Dom)
			assertBits(t, "month", got.Month, tt.want.Month)
			assertBits(t, "dow", got.Dow, tt.want.Dow)

			if got.Location != time.Local {
				t.Errorf("location = %v, want Local", got.Location)
			}
			if got.ConstantDelaySchedule != 0 {
				t.Errorf("constant delay = %v, want 0", got.ConstantDelaySchedule)
			}
		})
	}
}

func TestParse_SundayAsSeven(t *testing.T) {
	tests := []struct {
		spec string
		want uint64
	}{
		{"0 0 * * 7", 1},
		{"0 0 * * 0,7", 1},
		{"0 0 * * 5-7", 1 | 1<<5 | 1<<6},
		{"0 0 * * sat-7", 1 | 1<<6},
		{"0 0 * * */7", 1},
	}

	for _, tt := range tests {
		t.Run(tt.spec, func(t *testing.T) {
			s, err := Parse(tt.spec)
			if err != nil {
				t.Fatal(err)
			}

			assertBits(t, "dow", s.Dow, tt.want)
		})
	}

	s, err := Parse("0 0 * * *")
	if err != nil {
		t.Fatal(err)
	}
	assertBits(t, "dow", s.Dow, all(dow))
}

func TestParse_WhitespaceAfterTimezone(t *testing.T) {
	s, err := Parse("CRON_TZ=UTC\t0 7 * * *")
	if err != nil {
		t.Fatal(err)
	}
	if s.Location != time.UTC || s.Hour != 1<<7 {
		t.Errorf("location = %s, hour bits = %b", s.Location, s.Hour)
	}

	s, err = Parse("TZ=UTC  @every\t5m")
	if err != nil {
		t.Fatal(err)
	}
	if s.ConstantDelaySchedule != 5*time.Minute {
		t.Errorf("delay = %s, want 5m", s.ConstantDelaySchedule)
	}
}

func TestParse_TrimsSpace(t *testing.T) {
	for _, spec := range []string{" @daily ", "\t0 7 * * *\n", "  CRON_TZ=UTC 0 7 * * *"} {
		if _, err := Parse(spec); err != nil {
			t.Errorf("%q: %v", spec, err)
		}
	}

	if _, err := Parse("   "); err == nil {
		t.Error("expected error for blank spec")
	}
}

func TestParse_StepRemovesStarBit(t *testing.T) {
	s, err := Parse("0 0 */2 * *")
	if err != nil {
		t.Fatal(err)
	}

	if s.Dom&starBit != 0 {
		t.Errorf("dom with step must not have star bit")
	}
	if s.Dow&starBit == 0 {
		t.Errorf("dow must have star bit")
	}
}

func TestParse_Timezone(t *testing.T) {
	for _, prefix := range []string{"TZ=", "CRON_TZ="} {
		s, err := Parse(prefix + "Europe/Istanbul   0 7 * * *")
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", prefix, err)
		}

		if s.Location.String() != "Europe/Istanbul" {
			t.Errorf("%s: location = %s", prefix, s.Location)
		}
		if s.Hour != 1<<7 {
			t.Errorf("%s: hour bits = %b", prefix, s.Hour)
		}
	}

	s, err := Parse("CRON_TZ=UTC @daily")
	if err != nil {
		t.Fatal(err)
	}
	if s.Location != time.UTC {
		t.Errorf("descriptor location = %v, want UTC", s.Location)
	}
}

func TestParse_Descriptors(t *testing.T) {
	base := time.Date(2024, 5, 15, 10, 30, 45, 0, time.UTC) // Wednesday

	tests := []struct {
		spec string
		next time.Time
	}{
		{"@yearly", time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)},
		{"@annually", time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)},
		{"@monthly", time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)},
		{"@weekly", time.Date(2024, 5, 19, 0, 0, 0, 0, time.UTC)},
		{"@daily", time.Date(2024, 5, 16, 0, 0, 0, 0, time.UTC)},
		{"@midnight", time.Date(2024, 5, 16, 0, 0, 0, 0, time.UTC)},
		{"@hourly", time.Date(2024, 5, 15, 11, 0, 0, 0, time.UTC)},
	}

	for _, tt := range tests {
		t.Run(tt.spec, func(t *testing.T) {
			s, err := Parse(tt.spec)
			if err != nil {
				t.Fatal(err)
			}

			if got := s.Next(base); !got.Equal(tt.next) {
				t.Errorf("Next() = %v, want %v", got, tt.next)
			}
		})
	}
}

func TestParse_Every(t *testing.T) {
	tests := []struct {
		spec string
		want time.Duration
	}{
		{"@every 1h30m", 90 * time.Minute},
		{"@every 1500ms", time.Second},
		{"@every 10ms", time.Second},
		{"@every 2s", 2 * time.Second},
	}

	for _, tt := range tests {
		t.Run(tt.spec, func(t *testing.T) {
			s, err := Parse(tt.spec)
			if err != nil {
				t.Fatal(err)
			}

			if s.ConstantDelaySchedule != tt.want {
				t.Errorf("delay = %v, want %v", s.ConstantDelaySchedule, tt.want)
			}

			now := time.Date(2024, 1, 1, 0, 0, 0, 300, time.UTC)
			want := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
			if got := s.Next(now); !got.Equal(want.Add(tt.want)) {
				t.Errorf("Next() = %v, want %v", got, want.Add(tt.want))
			}
			if got := s.Prev(now); !got.Equal(want.Add(-tt.want)) {
				t.Errorf("Prev() = %v, want %v", got, want.Add(-tt.want))
			}
		})
	}
}

func TestParse_Errors(t *testing.T) {
	tests := []struct {
		spec    string
		wantErr string
	}{
		{"", "empty spec"},
		{"* * * *", "expected exactly 5 fields"},
		{"* * * * * *", "expected exactly 5 fields"},
		{"60 * * * *", "above maximum"},
		{"* 24 * * *", "above maximum"},
		{"* * 0 * *", "below minimum"},
		{"* * 32 * *", "above maximum"},
		{"* * * 13 *", "above maximum"},
		{"* * * * 8", "above maximum"},
		{"5-1 * * * *", "beyond end of range"},
		{"*/0 * * * *", "positive number"},
		{"1-2-3 * * * *", "too many hyphens"},
		{"*/2/3 * * * *", "too many slashes"},
		{"a * * * *", "failed to parse int"},
		{"-1 * * * *", "failed to parse int"},
		{"* * * foo *", "failed to parse int"},
		{"CRON_TZ=Nowhere/City 0 7 * * *", "provided bad location"},
		{"CRON_TZ=UTC", "missing schedule after location"},
		{"TZ= 0 7 * * *", "empty location"},
		{"CRON_TZ= @daily", "empty location"},
		{"@foo", "unrecognized descriptor"},
		{"@every", "unrecognized descriptor"},
		{"@every abc", "failed to parse duration"},
		{"@every 5m 10s", "unrecognized descriptor"},
		{"*-5 * * * *", "range is not allowed with wildcard"},
		{"?-3 * * * *", "range is not allowed with wildcard"},
		{"0 , * * *", "empty value in list"},
		{"0 7 1,,2 * *", "empty value in list"},
		{"0 7 * * mon,", "empty value in list"},
		{"@every -5m", "duration should be positive"},
		{"@every 0s", "duration should be positive"},
	}

	for _, tt := range tests {
		t.Run(tt.spec, func(t *testing.T) {
			s, err := Parse(tt.spec)
			if err == nil {
				t.Fatalf("expected error, got %+v", s)
			}

			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %q, want contains %q", err, tt.wantErr)
			}
		})
	}
}

func assertBits(t *testing.T, name string, got, want uint64) {
	t.Helper()

	if got != want {
		t.Errorf("%s bits = %064b, want %064b", name, got, want)
	}
}

func FuzzParse(f *testing.F) {
	for _, spec := range []string{
		"", "* * * * *", "*/5 9-17 * * mon-fri", "0 0 13 * 5",
		"0 0 30 2 *", "0 0 29 2 *", "0 0 * * 7", "0 0 * * 5-7",
		"@daily", "@every 1h30m", "@every 1ms", "@every -1s",
		"CRON_TZ=Europe/Amsterdam 30 2 * * *",
		"TZ=America/Sao_Paulo 0 23 3 11 *",
		"TZ=UTC\t0 7 * * *", "TZ= 0 7 * * *", "1,,2 * * * *",
		"*/0 * * * *", "*-5 * * * *", "0 0 * * */9223372036854775807",
	} {
		f.Add(spec)
	}

	f.Fuzz(func(t *testing.T, spec string) {
		s, err := Parse(spec)
		if err != nil {
			if s != nil {
				t.Fatal("Parse returned both a schedule and an error")
			}

			return
		}
		if s == nil {
			t.Fatal("Parse succeeded without a schedule")
		}

		now := time.Date(2024, 10, 27, 1, 15, 20, 123456789, time.UTC)
		for _, direction := range []struct {
			name string
			get  func(time.Time) time.Time
		}{
			{"Next", s.Next},
			{"Prev", s.Prev},
		} {
			got := direction.get(now)
			if got.IsZero() {
				continue
			}
			if direction.name == "Next" && !got.After(now) || direction.name == "Prev" && !got.Before(now) {
				t.Fatalf("%s(%s) = %s, not strictly in the requested direction", direction.name, now, got)
			}
			if got.Nanosecond() != 0 {
				t.Fatalf("%s returned a fractional second: %s", direction.name, got)
			}
			if s.ConstantDelaySchedule == 0 {
				loc := s.Location
				if loc == nil || loc == time.Local {
					loc = now.Location()
				}
				if !refMatch(s, got.In(loc)) {
					t.Fatalf("%s returned a time that does not match the schedule: %s", direction.name, got)
				}
			}
		}
	})
}
