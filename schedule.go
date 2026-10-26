package hardloop

import "time"

// ScheduleGroup holds start and stop schedules of a loop.
type ScheduleGroup struct {
	StartSchedules []Schedule
	StopSchedules  []Schedule
}

// NewSchedule parses the start and stop specs into a ScheduleGroup.
func NewSchedule(startSpec, endSpec []string) (*ScheduleGroup, error) {
	startSchedules, err := parseSchedules(startSpec)
	if err != nil {
		return nil, err
	}

	stopSchedules, err := parseSchedules(endSpec)
	if err != nil {
		return nil, err
	}

	return &ScheduleGroup{
		StartSchedules: startSchedules,
		StopSchedules:  stopSchedules,
	}, nil
}

// getStartTime returns the next start time, nil if there is no start time.
func (l *ScheduleGroup) getStartTime(now time.Time) *time.Time {
	nextStart := FindNext(l.StartSchedules, now)
	if nextStart.IsZero() {
		return nil
	}

	return &nextStart
}

// getStopTime returns the next stop time if it is in the running window, otherwise nil.
func (l *ScheduleGroup) getStopTime(now time.Time) *time.Time {
	// Prev is strictly before its argument. Include events at now when deciding
	// whether the running window is open: start is inclusive, stop is exclusive.
	inclusive := now.Add(time.Nanosecond)
	prevStop := FindPrev(l.StopSchedules, inclusive)
	if prevStop.IsZero() {
		return nil
	}

	prevStart := FindPrev(l.StartSchedules, inclusive)

	// start schedules are set but never started
	if prevStart.IsZero() && len(l.StartSchedules) > 0 {
		return nil
	}

	// if prevStop is after prevStart, then we should stop the loop
	if !prevStart.IsZero() && prevStop.After(prevStart) {
		return nil
	}

	nextStop := FindNext(l.StopSchedules, now)
	if nextStop.IsZero() {
		return nil
	}

	return &nextStop
}

// NextStartTime returns the next start time.
//   - If it should be start now than it returns nil.
func (l *ScheduleGroup) NextStartTime(now time.Time) *time.Time {
	if l.getStopTime(now) != nil {
		return nil
	}

	return l.getStartTime(now)
}

func parseSchedules(specs []string) ([]Schedule, error) {
	schedules := make([]Schedule, 0, len(specs))

	for _, spec := range specs {
		schedule, err := Parse(spec)
		if err != nil {
			return nil, err
		}

		schedules = append(schedules, schedule)
	}

	return schedules, nil
}
