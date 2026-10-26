package hardloop

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"
)

// DefaultRestartDelay is the default minimum duration between two starts of the function.
const DefaultRestartDelay = time.Second

var (
	// ErrCloseLoop is returned when the loop should be closed.
	ErrCloseLoop = errors.New("close loop")
	// ErrNilFunc is returned when the function to run is nil.
	ErrNilFunc = errors.New("function is nil")
	// ErrLoopAlreadyRunning is returned when Run is called on a running loop.
	ErrLoopAlreadyRunning = errors.New("loop already running")
)

// Loop runs a function between start and stop cron specs, create it with NewLoop.
type Loop struct {
	scheduleGroup *ScheduleGroup
	scheduleMx    sync.RWMutex

	isLoopRunning     bool
	isFunctionRunning bool
	fn                func(ctx context.Context) error
	mx                sync.RWMutex
	cancelFn          context.CancelFunc
	cancelLoop        context.CancelCauseFunc
	lastStart         time.Time
	restartWait       time.Duration
	exited            chan struct{}
	startDuration     chan time.Duration
	stopDuration      chan *time.Duration
	log               *slog.Logger
}

// NewLoop returns a new Loop with the given start and end cron specs and function.
//   - Standard crontab specs, e.g. "* * * * ?"
//   - Descriptors, e.g. "@midnight", "@every 1h30m"
func NewLoop(startSpec, endSpec []string, fn func(ctx context.Context) error) (*Loop, error) {
	if fn == nil {
		return nil, ErrNilFunc
	}

	scheduleGroup, err := NewSchedule(startSpec, endSpec)
	if err != nil {
		return nil, err
	}

	return &Loop{
		scheduleGroup: scheduleGroup,
		fn:            fn,
		restartWait:   DefaultRestartDelay,
		log:           slog.Default(),
	}, nil
}

// SetLogger sets the logger for the loop.
//   - If not set, it uses the default slog logger.
//   - Set to nil to disable logging.
func (l *Loop) SetLogger(log *slog.Logger) {
	l.mx.Lock()
	defer l.mx.Unlock()

	l.log = loggerOrDiscard(log)
}

func (l *Loop) logger() *slog.Logger {
	l.mx.RLock()
	defer l.mx.RUnlock()

	if l.log == nil {
		return slog.Default()
	}

	return l.log
}

// SetRestartDelay sets the minimum duration between two starts of the function.
// It prevents a busy loop when the function returns immediately inside a running window.
// Scheduled starts also respect this delay. If the wait ends outside a running
// window, the function waits for the next window instead.
//   - Default is DefaultRestartDelay (1 second).
//   - Zero or negative disables the wait, function is started again immediately.
func (l *Loop) SetRestartDelay(d time.Duration) {
	l.mx.Lock()
	defer l.mx.Unlock()

	l.restartWait = max(d, 0)
}

// ChangeStartSchedules sets the start cron specs.
// Not effects immediately!
func (l *Loop) ChangeStartSchedules(startSpecs []string) error {
	startSchedules, err := parseSchedules(startSpecs)
	if err != nil {
		return err
	}

	l.scheduleMx.Lock()
	defer l.scheduleMx.Unlock()

	l.initSchedules().StartSchedules = startSchedules

	return nil
}

// ChangeStopSchedules sets the end cron specs.
// Not effects immediately!
func (l *Loop) ChangeStopSchedules(stopSpecs []string) error {
	stopSchedules, err := parseSchedules(stopSpecs)
	if err != nil {
		return err
	}

	l.scheduleMx.Lock()
	defer l.scheduleMx.Unlock()

	l.initSchedules().StopSchedules = stopSchedules

	return nil
}

// IsLoopRunning returns true if the loop is running.
func (l *Loop) IsLoopRunning() bool {
	l.mx.RLock()
	defer l.mx.RUnlock()

	return l.isLoopRunning
}

// IsFunctionRunning returns true if the function is running.
func (l *Loop) IsFunctionRunning() bool {
	l.mx.RLock()
	defer l.mx.RUnlock()

	return l.isFunctionRunning
}

// Run runs the loop and blocks until it is closed, use `go loop.Run(ctx)` to run in background.
// It returns after the running function is finished.
//   - Returns nil when the function returns ErrCloseLoop.
//   - Returns the context error when ctx is done.
//   - Returns ErrLoopAlreadyRunning if the loop is already running.
//   - Returns ErrNilFunc if the loop has no function.
func (l *Loop) Run(ctx context.Context) error {
	if l.fn == nil {
		return ErrNilFunc
	}

	l.mx.Lock()
	if l.isLoopRunning {
		l.mx.Unlock()

		return ErrLoopAlreadyRunning
	}
	if err := ctx.Err(); err != nil {
		l.mx.Unlock()

		return err
	}

	l.isLoopRunning = true
	l.lastStart = time.Time{}

	// fresh channels for every run, previous run's signals must not leak
	l.exited = make(chan struct{}, 1)
	l.startDuration = make(chan time.Duration, 1)
	l.stopDuration = make(chan *time.Duration, 1)

	var ctxLoop context.Context
	ctxLoop, l.cancelLoop = context.WithCancelCause(ctx)
	l.mx.Unlock()

	// internal goroutines, loop is marked as stopped after all of them finished
	inner := &sync.WaitGroup{}

	// listen function exit
	inner.Add(1)
	go func() {
		defer inner.Done()

		for {
			select {
			case <-ctxLoop.Done():
				return
			case <-l.exited:
				now := time.Now()
				if l.shouldRunNow(now) {
					// wait before running again if the function just started, then check again
					if delay := l.restartDelay(); delay > 0 {
						if !sleep(ctxLoop, delay) {
							return
						}

						l.triggerExited()

						continue
					}

					l.runFunction(ctxLoop, inner)

					continue
				}

				// check next time to start again
				startTime := l.startTime(now)
				if startTime == nil {
					// start specs never match again, wait until closed
					l.logger().Warn("No next start time, function will not run again")

					continue
				}

				// set next start time
				l.logger().Info("Next start time", "time", startTime)
				send(ctxLoop, l.startDuration, startTime.Sub(now))
			}
		}
	}()

	// listen start time
	inner.Add(1)
	go func() {
		defer inner.Done()

		var chStartDuration <-chan time.Time
		var startTimer *time.Timer

		for {
			select {
			case <-ctxLoop.Done():
				if startTimer != nil {
					startTimer.Stop()
				}

				return
			case startDuration := <-l.startDuration:
				if startTimer != nil {
					startTimer.Stop()
				}

				// set next start time
				startTimer = time.NewTimer(startDuration)
				chStartDuration = startTimer.C
			case <-chStartDuration:
				// A scheduled start must respect the restart delay too.
				stopTime := l.stopTime(time.Now())
				if delay := l.restartDelay(); delay > 0 {
					if !sleep(ctxLoop, delay) {
						return
					}

					// The wait may have outlasted this running window.
					if stopTime != nil && !l.shouldRunNow(time.Now()) {
						l.triggerExited()

						continue
					}
				}

				// run function
				l.runFunction(ctxLoop, inner)
			}
		}
	}()

	// listen stop time
	inner.Add(1)
	go func() {
		defer inner.Done()

		var chStopDuration <-chan time.Time
		var stopTimer *time.Timer

		for {
			select {
			case <-ctxLoop.Done():
				if stopTimer != nil {
					stopTimer.Stop()
				}

				return
			case stopDuration := <-l.stopDuration:
				if stopTimer != nil {
					stopTimer.Stop()
					stopTimer = nil
				}

				if stopDuration == nil {
					// disable
					chStopDuration = nil

					continue
				}

				// set next stop time
				stopTimer = time.NewTimer(*stopDuration)
				chStopDuration = stopTimer.C
			case <-chStopDuration:
				// time to stop function
				l.stopFunction()
			}
		}
	}()

	// first initialize
	l.triggerExited()

	<-ctxLoop.Done()
	inner.Wait()

	l.mx.Lock()
	l.isLoopRunning = false
	l.mx.Unlock()

	if errors.Is(context.Cause(ctxLoop), ErrCloseLoop) {
		return nil
	}

	return ctx.Err()
}

func (l *Loop) runFunction(ctx context.Context, wg *sync.WaitGroup) {
	l.mx.Lock()
	if l.isFunctionRunning || ctx.Err() != nil {
		l.mx.Unlock()

		return
	}

	l.isFunctionRunning = true
	l.lastStart = time.Now()

	ctxInFunc, cancel := context.WithCancel(ctx)
	l.cancelFn = cancel
	l.mx.Unlock()

	wg.Add(1)
	go func() {
		defer wg.Done()
		defer cancel()

		err := ctxInFunc.Err()
		if err == nil {
			err = l.fn(ctxInFunc)
		}

		// set running to false
		l.mx.Lock()
		closeLoop := errors.Is(err, ErrCloseLoop)
		if closeLoop {
			// Cancel before allowing another function start.
			l.cancelLoop(ErrCloseLoop)
		}
		l.isFunctionRunning = false
		l.mx.Unlock()

		if closeLoop {
			return
		}

		l.triggerExited()
	}()

	// set next stop time
	now := time.Now()
	stopTime := l.stopTime(now)
	if stopTime == nil {
		// disable next stop time
		l.logger().Info("Next stop time disabled")
		send(ctx, l.stopDuration, nil)

		return
	}

	l.logger().Info("Next stop time", "time", stopTime)

	stopDuration := time.Until(*stopTime)
	send(ctx, l.stopDuration, &stopDuration)
}

func (l *Loop) stopFunction() {
	l.mx.Lock()

	// if function is not running, trigger exited to get the next start time
	if !l.isFunctionRunning {
		l.mx.Unlock()
		l.triggerExited()

		return
	}

	// running flag is cleared when the function returns, to not run it twice at the same time
	cancel := l.cancelFn
	l.mx.Unlock()

	cancel()
}

// restartDelay returns the remaining wait to respect the restart delay after the last start of the function.
func (l *Loop) restartDelay() time.Duration {
	l.mx.RLock()
	lastStart, wait := l.lastStart, l.restartWait
	l.mx.RUnlock()

	if lastStart.IsZero() || wait == 0 {
		return 0
	}

	return max(wait-time.Since(lastStart), 0)
}

// triggerExited signals to calculate the next start time, multiple signals are merged.
func (l *Loop) triggerExited() {
	select {
	case l.exited <- struct{}{}:
	default:
	}
}

func (l *Loop) startTime(now time.Time) *time.Time {
	l.scheduleMx.RLock()
	defer l.scheduleMx.RUnlock()

	return l.scheduleGroup.getStartTime(now)
}

// shouldRunNow reports whether now is in a running window, without start specs it always runs.
func (l *Loop) shouldRunNow(now time.Time) bool {
	l.scheduleMx.RLock()
	defer l.scheduleMx.RUnlock()

	return len(l.scheduleGroup.StartSchedules) == 0 || l.scheduleGroup.getStopTime(now) != nil
}

func (l *Loop) stopTime(now time.Time) *time.Time {
	l.scheduleMx.RLock()
	defer l.scheduleMx.RUnlock()

	return l.scheduleGroup.getStopTime(now)
}

// initSchedules creates the schedule group if needed, it must be called with scheduleMx write lock.
func (l *Loop) initSchedules() *ScheduleGroup {
	if l.scheduleGroup == nil {
		l.scheduleGroup = &ScheduleGroup{}
	}

	return l.scheduleGroup
}

// sleep waits for the duration, returns false if the context is done.
func sleep(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}

// send sends the value to the channel or returns when the context is done.
func send[T any](ctx context.Context, ch chan<- T, v T) {
	select {
	case ch <- v:
	case <-ctx.Done():
	}
}

func loggerOrDiscard(log *slog.Logger) *slog.Logger {
	if log == nil {
		return slog.New(slog.DiscardHandler)
	}

	return log
}
