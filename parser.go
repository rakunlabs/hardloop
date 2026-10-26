package hardloop

// Parsing logic is adapted from github.com/robfig/cron/v3.
//
// Copyright (C) 2012 Rob Figueiredo
// All Rights Reserved.
// MIT LICENSE

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// bounds provides a range of acceptable values (plus a map of name to value).
type bounds struct {
	min, max uint
	names    map[string]uint
}

// The bounds for each field.
var (
	seconds = bounds{0, 59, nil}
	minutes = bounds{0, 59, nil}
	hours   = bounds{0, 23, nil}
	dom     = bounds{1, 31, nil}
	months  = bounds{1, 12, map[string]uint{
		"jan": 1,
		"feb": 2,
		"mar": 3,
		"apr": 4,
		"may": 5,
		"jun": 6,
		"jul": 7,
		"aug": 8,
		"sep": 9,
		"oct": 10,
		"nov": 11,
		"dec": 12,
	}}
	dowNames = map[string]uint{
		"sun": 0,
		"mon": 1,
		"tue": 2,
		"wed": 3,
		"thu": 4,
		"fri": 5,
		"sat": 6,
	}
	dow = bounds{0, 6, dowNames}
	// dowSpec accepts 7 as sunday like standard cron, it is folded to 0 after parsing.
	dowSpec = bounds{0, 7, dowNames}
)

// Parse returns a new cron schedule for the given spec.
//
// It accepts
//   - Standard crontab specs (minute, hour, day of month, month, day of week), e.g. "* * * * ?"
//   - Descriptors, e.g. "@midnight", "@every 1h30m"
//   - Optional timezone prefix, e.g. "CRON_TZ=Europe/Istanbul 0 7 * * *" or "TZ=UTC @daily"
func Parse(spec string) (*SpecSchedule, error) {
	spec = strings.TrimSpace(spec)
	if len(spec) == 0 {
		return nil, errors.New("empty spec string")
	}

	// Extract timezone if present
	loc := time.Local
	if strings.HasPrefix(spec, "TZ=") || strings.HasPrefix(spec, "CRON_TZ=") {
		var err error
		i := strings.IndexFunc(spec, unicode.IsSpace)
		if i < 0 {
			return nil, fmt.Errorf("missing schedule after location: %s", spec)
		}
		eq := strings.Index(spec, "=")
		if eq+1 == i {
			return nil, fmt.Errorf("empty location: %s", spec)
		}
		if loc, err = time.LoadLocation(spec[eq+1 : i]); err != nil {
			return nil, fmt.Errorf("provided bad location %s: %w", spec[eq+1:i], err)
		}
		spec = strings.TrimSpace(spec[i:])
	}

	// Handle named schedules (descriptors)
	if strings.HasPrefix(spec, "@") {
		return parseDescriptor(spec, loc)
	}

	fields := strings.Fields(spec)
	if len(fields) != 5 { //nolint:mnd // standard cron has 5 fields
		return nil, fmt.Errorf("expected exactly 5 fields, found %d: %s", len(fields), fields)
	}

	var err error
	field := func(field string, r bounds) uint64 {
		if err != nil {
			return 0
		}
		var bits uint64
		bits, err = getField(field, r)
		return bits
	}

	var (
		minute     = field(fields[0], minutes)
		hour       = field(fields[1], hours)
		dayofmonth = field(fields[2], dom)
		month      = field(fields[3], months)
		dayofweek  = field(fields[4], dowSpec)
	)
	if err != nil {
		return nil, err
	}

	// 7 is sunday
	if dayofweek&(1<<7) != 0 {
		dayofweek = dayofweek&^(1<<7) | 1
	}

	return &SpecSchedule{
		Second:   1 << seconds.min,
		Minute:   minute,
		Hour:     hour,
		Dom:      dayofmonth,
		Month:    month,
		Dow:      dayofweek,
		Location: loc,
	}, nil
}

// getField returns an Int with the bits set representing all of the times that
// the field represents or error parsing field value.  A "field" is a comma-separated
// list of "ranges".
func getField(field string, r bounds) (uint64, error) {
	var bits uint64
	for expr := range strings.SplitSeq(field, ",") {
		if expr == "" {
			return 0, fmt.Errorf("empty value in list: %s", field)
		}

		bit, err := getRange(expr, r)
		if err != nil {
			return bits, err
		}
		bits |= bit
	}

	return bits, nil
}

// getRange returns the bits indicated by the given expression:
//
//	number | number "-" number [ "/" number ]
//
// or error parsing range.
func getRange(expr string, r bounds) (uint64, error) {
	var (
		start, end, step uint
		rangeAndStep     = strings.Split(expr, "/")
		lowAndHigh       = strings.Split(rangeAndStep[0], "-")
		singleDigit      = len(lowAndHigh) == 1
		err              error
	)

	var extra uint64
	if lowAndHigh[0] == "*" || lowAndHigh[0] == "?" {
		if len(lowAndHigh) > 1 {
			return 0, fmt.Errorf("range is not allowed with wildcard: %s", expr)
		}

		start = r.min
		end = r.max
		extra = starBit
	} else {
		start, err = parseIntOrName(lowAndHigh[0], r.names)
		if err != nil {
			return 0, err
		}
		switch len(lowAndHigh) {
		case 1:
			end = start
		case 2: //nolint:mnd // low-high
			end, err = parseIntOrName(lowAndHigh[1], r.names)
			if err != nil {
				return 0, err
			}
		default:
			return 0, fmt.Errorf("too many hyphens: %s", expr)
		}
	}

	switch len(rangeAndStep) {
	case 1:
		step = 1
	case 2: //nolint:mnd // range/step
		step, err = mustParseInt(rangeAndStep[1])
		if err != nil {
			return 0, err
		}

		// Special handling: "N/step" means "N-max/step".
		if singleDigit {
			end = r.max
		}
		if step > 1 {
			extra = 0
		}
	default:
		return 0, fmt.Errorf("too many slashes: %s", expr)
	}

	if start < r.min {
		return 0, fmt.Errorf("beginning of range (%d) below minimum (%d): %s", start, r.min, expr)
	}
	if end > r.max {
		return 0, fmt.Errorf("end of range (%d) above maximum (%d): %s", end, r.max, expr)
	}
	if start > end {
		return 0, fmt.Errorf("beginning of range (%d) beyond end of range (%d): %s", start, end, expr)
	}
	if step == 0 {
		return 0, fmt.Errorf("step of range should be a positive number: %s", expr)
	}

	return getBits(start, end, step) | extra, nil
}

// parseIntOrName returns the (possibly-named) integer contained in expr.
func parseIntOrName(expr string, names map[string]uint) (uint, error) {
	if names != nil {
		if namedInt, ok := names[strings.ToLower(expr)]; ok {
			return namedInt, nil
		}
	}

	return mustParseInt(expr)
}

// mustParseInt parses the given expression as an int or returns an error.
func mustParseInt(expr string) (uint, error) {
	num, err := strconv.Atoi(expr)
	if err != nil {
		return 0, fmt.Errorf("failed to parse int from %s: %w", expr, err)
	}
	if num < 0 {
		return 0, fmt.Errorf("negative number (%d) not allowed: %s", num, expr)
	}

	return uint(num), nil
}

// getBits sets all bits in the range [min, max], modulo the given step size.
func getBits(minV, maxV, step uint) uint64 {
	var bits uint64

	// If step is 1, use shifts.
	if step == 1 {
		return ^(math.MaxUint64 << (maxV + 1)) & (math.MaxUint64 << minV)
	}

	// Else, use a simple loop.
	for i := minV; i <= maxV; i += step {
		bits |= 1 << i
	}

	return bits
}

// all returns all bits within the given bounds.  (plus the star bit)
func all(r bounds) uint64 {
	return getBits(r.min, r.max, 1) | starBit
}

// parseDescriptor returns a predefined schedule for the expression, or error if none matches.
func parseDescriptor(descriptor string, loc *time.Location) (*SpecSchedule, error) {
	switch descriptor {
	case "@yearly", "@annually":
		return &SpecSchedule{
			Second:   1 << seconds.min,
			Minute:   1 << minutes.min,
			Hour:     1 << hours.min,
			Dom:      1 << dom.min,
			Month:    1 << months.min,
			Dow:      all(dow),
			Location: loc,
		}, nil

	case "@monthly":
		return &SpecSchedule{
			Second:   1 << seconds.min,
			Minute:   1 << minutes.min,
			Hour:     1 << hours.min,
			Dom:      1 << dom.min,
			Month:    all(months),
			Dow:      all(dow),
			Location: loc,
		}, nil

	case "@weekly":
		return &SpecSchedule{
			Second:   1 << seconds.min,
			Minute:   1 << minutes.min,
			Hour:     1 << hours.min,
			Dom:      all(dom),
			Month:    all(months),
			Dow:      1 << dow.min,
			Location: loc,
		}, nil

	case "@daily", "@midnight":
		return &SpecSchedule{
			Second:   1 << seconds.min,
			Minute:   1 << minutes.min,
			Hour:     1 << hours.min,
			Dom:      all(dom),
			Month:    all(months),
			Dow:      all(dow),
			Location: loc,
		}, nil

	case "@hourly":
		return &SpecSchedule{
			Second:   1 << seconds.min,
			Minute:   1 << minutes.min,
			Hour:     all(hours),
			Dom:      all(dom),
			Month:    all(months),
			Dow:      all(dow),
			Location: loc,
		}, nil
	}

	if fields := strings.Fields(descriptor); len(fields) == 2 && fields[0] == "@every" { //nolint:mnd // @every duration
		duration, err := time.ParseDuration(fields[1])
		if err != nil {
			return nil, fmt.Errorf("failed to parse duration %s: %w", descriptor, err)
		}
		if duration <= 0 {
			return nil, fmt.Errorf("duration should be positive: %s", descriptor)
		}

		return &SpecSchedule{ConstantDelaySchedule: every2Delay(duration)}, nil
	}

	return nil, fmt.Errorf("unrecognized descriptor: %s", descriptor)
}

// every2Delay rounds the duration to the second, minimum one second.
func every2Delay(duration time.Duration) time.Duration {
	if duration < time.Second {
		duration = time.Second
	}

	return duration - time.Duration(duration.Nanoseconds())%time.Second
}
