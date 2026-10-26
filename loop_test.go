package hardloop_test

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/rakunlabs/hardloop"
)

// synctest bubble starts at 2000-01-01 00:00:00 UTC (Saturday).

type recorder struct {
	mx     sync.Mutex
	starts []time.Time
	stops  []time.Time
}

func (r *recorder) fn(ctx context.Context) error {
	r.mx.Lock()
	r.starts = append(r.starts, time.Now().UTC())
	r.mx.Unlock()

	<-ctx.Done()

	r.mx.Lock()
	r.stops = append(r.stops, time.Now().UTC())
	r.mx.Unlock()

	return nil
}

func (r *recorder) get() ([]time.Time, []time.Time) {
	r.mx.Lock()
	defer r.mx.Unlock()

	return append([]time.Time(nil), r.starts...), append([]time.Time(nil), r.stops...)
}

func at(day, hour, minute int) time.Time {
	return time.Date(2000, 1, day, hour, minute, 0, 0, time.UTC)
}

func assertTimes(t *testing.T, name string, got, want []time.Time) {
	t.Helper()

	if len(got) != len(want) {
		t.Fatalf("%s = %v, want %v", name, got, want)
	}

	for i := range got {
		if !got[i].Equal(want[i]) {
			t.Fatalf("%s[%d] = %s, want %s", name, i, got[i], want[i])
		}
	}
}

// startLoop runs the loop in background, returned function cancels it and returns the result of Run.
func startLoop(t *testing.T, l *hardloop.Loop) func() error {
	t.Helper()

	ctx, cancel := context.WithCancel(t.Context())
	errCh := make(chan error, 1)
	go func() { errCh <- l.Run(ctx) }()

	return func() error {
		cancel()

		return <-errCh
	}
}

func runLoop(t *testing.T, l *hardloop.Loop, d time.Duration) {
	t.Helper()

	stop := startLoop(t, l)
	time.Sleep(d)

	if err := stop(); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() error = %v, want context.Canceled", err)
	}
}

func TestLoop_StartStopWindow(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := &recorder{}
		l, err := hardloop.NewLoop(
			[]string{"CRON_TZ=UTC 0 7 * * *"},
			[]string{"CRON_TZ=UTC 0 17 * * *"},
			r.fn,
		)
		if err != nil {
			t.Fatal(err)
		}
		l.SetLogger(nil)

		runLoop(t, l, 3*24*time.Hour)

		starts, stops := r.get()
		assertTimes(t, "starts", starts, []time.Time{at(1, 7, 0), at(2, 7, 0), at(3, 7, 0)})
		assertTimes(t, "stops", stops, []time.Time{at(1, 17, 0), at(2, 17, 0), at(3, 17, 0)})
	})
}

func TestLoop_StartsImmediatelyInsideWindow(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := &recorder{}
		l, err := hardloop.NewLoop(
			[]string{"CRON_TZ=UTC 0 22 * * *"}, // previous start at 1999-12-31 22:00
			[]string{"CRON_TZ=UTC 0 2 * * *"},
			r.fn,
		)
		if err != nil {
			t.Fatal(err)
		}
		l.SetLogger(nil)

		runLoop(t, l, 23*time.Hour)

		starts, stops := r.get()
		assertTimes(t, "starts", starts, []time.Time{at(1, 0, 0), at(1, 22, 0)})
		assertTimes(t, "stops", stops, []time.Time{at(1, 2, 0), at(1, 23, 0)})
	})
}

func TestLoop_MultipleSpecs(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := &recorder{}
		l, err := hardloop.NewLoop(
			[]string{
				"CRON_TZ=UTC 0 9 * * 6",   // saturday 9:00
				"CRON_TZ=UTC 0 7 * * 1-5", // weekdays 7:00
			},
			[]string{
				"CRON_TZ=UTC 0 13 * * 6",   // saturday 13:00
				"CRON_TZ=UTC 0 17 * * 1-5", // weekdays 17:00
			},
			r.fn,
		)
		if err != nil {
			t.Fatal(err)
		}
		l.SetLogger(nil)

		// saturday 1st, sunday 2nd, monday 3rd
		runLoop(t, l, 3*24*time.Hour)

		starts, stops := r.get()
		assertTimes(t, "starts", starts, []time.Time{at(1, 9, 0), at(3, 7, 0)})
		assertTimes(t, "stops", stops, []time.Time{at(1, 13, 0), at(3, 17, 0)})
	})
}

func TestLoop_NoStopRunsForever(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := &recorder{}
		l, err := hardloop.NewLoop([]string{"CRON_TZ=UTC 0 7 * * *"}, nil, r.fn)
		if err != nil {
			t.Fatal(err)
		}
		l.SetLogger(nil)

		stop := startLoop(t, l)

		time.Sleep(48 * time.Hour)
		synctest.Wait()

		if !l.IsFunctionRunning() {
			t.Fatal("function should be running")
		}

		_ = stop()

		starts, stops := r.get()
		assertTimes(t, "starts", starts, []time.Time{at(1, 7, 0)})
		assertTimes(t, "stops", stops, []time.Time{at(3, 0, 0)})
	})
}

func TestLoop_OnlyStopRestarts(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := &recorder{}
		l, err := hardloop.NewLoop(nil, []string{"CRON_TZ=UTC 0 3 * * *"}, r.fn)
		if err != nil {
			t.Fatal(err)
		}
		l.SetLogger(nil)

		runLoop(t, l, 2*24*time.Hour-time.Hour)

		starts, stops := r.get()
		assertTimes(t, "starts", starts, []time.Time{at(1, 0, 0), at(1, 3, 0), at(2, 3, 0)})
		assertTimes(t, "stops", stops, []time.Time{at(1, 3, 0), at(2, 3, 0), at(2, 23, 0)})
	})
}

func TestLoop_FunctionReturnsInsideWindowRestarts(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var count atomic.Int32
		l, err := hardloop.NewLoop(
			[]string{"CRON_TZ=UTC 0 7 * * *"},
			[]string{"CRON_TZ=UTC 0 8 * * *"},
			func(ctx context.Context) error {
				count.Add(1)
				select {
				case <-time.After(20 * time.Minute):
				case <-ctx.Done():
				}

				return nil
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		l.SetLogger(nil)

		runLoop(t, l, 10*time.Hour)

		// 7:00, 7:20, 7:40 and canceled at 8:00
		if got := count.Load(); got != 3 {
			t.Fatalf("run count = %d, want 3", got)
		}
	})
}

func TestLoop_ErrCloseLoop(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var count atomic.Int32
		l, err := hardloop.NewLoop(
			[]string{"CRON_TZ=UTC 0 7 * * *"},
			[]string{"CRON_TZ=UTC 0 8 * * *"},
			func(context.Context) error {
				count.Add(1)

				return hardloop.ErrCloseLoop
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		l.SetLogger(nil)

		if l.IsLoopRunning() {
			t.Fatal("loop should not be running before Run")
		}

		// Run must return by itself without error
		if err := l.Run(t.Context()); err != nil {
			t.Fatalf("Run() error = %v, want nil", err)
		}

		if got := count.Load(); got != 1 {
			t.Fatalf("run count = %d, want 1", got)
		}
		if l.IsLoopRunning() || l.IsFunctionRunning() {
			t.Fatal("loop and function should be stopped")
		}
		if now := time.Now().UTC(); !now.Equal(at(1, 7, 0)) {
			t.Fatalf("closed at %s, want %s", now, at(1, 7, 0))
		}
	})
}

func TestLoop_RunTwice(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var count atomic.Int32
		l, err := hardloop.NewLoop(nil, nil, func(ctx context.Context) error {
			count.Add(1)
			<-ctx.Done()

			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		l.SetLogger(nil)

		stop := startLoop(t, l)
		synctest.Wait()

		if !l.IsLoopRunning() || !l.IsFunctionRunning() {
			t.Fatal("loop and function should be running")
		}

		if err := l.Run(t.Context()); !errors.Is(err, hardloop.ErrLoopAlreadyRunning) {
			t.Fatalf("second Run() error = %v, want ErrLoopAlreadyRunning", err)
		}

		if err := stop(); !errors.Is(err, context.Canceled) {
			t.Fatalf("Run() error = %v, want context.Canceled", err)
		}

		if got := count.Load(); got != 1 {
			t.Fatalf("run count = %d, want 1", got)
		}
		if l.IsLoopRunning() {
			t.Fatal("loop should be stopped")
		}
	})
}

func TestLoop_ChangeSchedules(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := &recorder{}
		l, err := hardloop.NewLoop(
			[]string{"CRON_TZ=UTC 0 7 * * *"},
			[]string{"CRON_TZ=UTC 0 8 * * *"},
			r.fn,
		)
		if err != nil {
			t.Fatal(err)
		}
		l.SetLogger(nil)

		if err := l.ChangeStartSchedules([]string{"bad"}); err == nil {
			t.Fatal("expected error")
		}
		if err := l.ChangeStopSchedules([]string{"bad"}); err == nil {
			t.Fatal("expected error")
		}

		stop := startLoop(t, l)

		// change while running, effects after the current window
		time.Sleep(7*time.Hour + 30*time.Minute)
		if err := l.ChangeStartSchedules([]string{"CRON_TZ=UTC 0 10 * * *"}); err != nil {
			t.Fatal(err)
		}
		if err := l.ChangeStopSchedules([]string{"CRON_TZ=UTC 0 11 * * *"}); err != nil {
			t.Fatal(err)
		}

		time.Sleep(24 * time.Hour)
		_ = stop()

		starts, stops := r.get()
		assertTimes(t, "starts", starts, []time.Time{at(1, 7, 0), at(1, 10, 0)})
		assertTimes(t, "stops", stops, []time.Time{at(1, 8, 0), at(1, 11, 0)})
	})
}

func TestLoop_ImpossibleStartNeverRuns(t *testing.T) {
	for _, stop := range [][]string{nil, {"CRON_TZ=UTC 0 17 * * *"}} {
		synctest.Test(t, func(t *testing.T) {
			r := &recorder{}
			l, err := hardloop.NewLoop([]string{"0 0 30 2 *"}, stop, r.fn)
			if err != nil {
				t.Fatal(err)
			}
			l.SetLogger(nil)

			runLoop(t, l, 3*24*time.Hour)

			if starts, _ := r.get(); len(starts) != 0 {
				t.Fatalf("stop %v: starts = %v, want none", stop, starts)
			}
		})
	}
}

func TestLoop_RunAgainAfterStop(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := &recorder{}
		l, err := hardloop.NewLoop(
			[]string{"CRON_TZ=UTC 0 7 * * *"},
			[]string{"CRON_TZ=UTC 0 8 * * *"},
			r.fn,
		)
		if err != nil {
			t.Fatal(err)
		}
		l.SetLogger(nil)

		// first run ends at 06:00, second run covers 7:00 and is canceled at 7:30
		runLoop(t, l, 6*time.Hour)
		if l.IsLoopRunning() {
			t.Fatal("loop should be stopped")
		}
		runLoop(t, l, 90*time.Minute)

		starts, stops := r.get()
		assertTimes(t, "starts", starts, []time.Time{at(1, 7, 0)})
		assertTimes(t, "stops", stops, []time.Time{at(1, 7, 30)})
	})
}

func TestLoop_RestartDelay(t *testing.T) {
	tests := []struct {
		name  string
		start []string
		stop  []string
		delay *time.Duration
		run   time.Duration
		want  int32
	}{
		{name: "no specs", run: 10*time.Second - time.Millisecond, want: 10},
		{name: "custom delay", delay: ptr(5 * time.Second), run: 30*time.Second - time.Millisecond, want: 6},
		{
			name:  "custom delay inside window",
			start: []string{"CRON_TZ=UTC 0 7 * * *"},
			stop:  []string{"CRON_TZ=UTC 1 7 * * *"},
			delay: ptr(20 * time.Second),
			run:   9 * time.Hour,
			want:  3, // 7:00:00, 7:00:20, 7:00:40
		},
		{name: "only stop", stop: []string{"CRON_TZ=UTC 0 3 * * *"}, run: 10*time.Second - time.Millisecond, want: 10},
		{
			name:  "inside window",
			start: []string{"CRON_TZ=UTC 0 7 * * *"},
			stop:  []string{"CRON_TZ=UTC 1 7 * * *"},
			run:   9 * time.Hour,
			want:  60, // 7:00:00 - 7:00:59
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				var count atomic.Int32
				l, err := hardloop.NewLoop(tt.start, tt.stop, func(context.Context) error {
					count.Add(1)

					return nil // returns immediately
				})
				if err != nil {
					t.Fatal(err)
				}
				l.SetLogger(nil)
				if tt.delay != nil {
					l.SetRestartDelay(*tt.delay)
				}

				runLoop(t, l, tt.run)

				if got := count.Load(); got != tt.want {
					t.Fatalf("run count = %d, want %d", got, tt.want)
				}
			})
		})
	}
}

func TestLoop_RestartDelayDisabled(t *testing.T) {
	for _, d := range []time.Duration{0, -time.Second} {
		synctest.Test(t, func(t *testing.T) {
			var count atomic.Int32
			l, err := hardloop.NewLoop(nil, nil, func(context.Context) error {
				if count.Add(1) == 1000 {
					return hardloop.ErrCloseLoop
				}

				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			l.SetLogger(nil)
			l.SetRestartDelay(d)

			// without delay all runs happen without the fake clock moving
			if err := l.Run(t.Context()); err != nil {
				t.Fatal(err)
			}

			if got := count.Load(); got != 1000 {
				t.Fatalf("delay %s: run count = %d, want 1000", d, got)
			}
			if now := time.Now().UTC(); !now.Equal(at(1, 0, 0)) {
				t.Fatalf("delay %s: finished at %s, want no time passed", d, now)
			}
		})
	}
}

func ptr[T any](v T) *T { return &v }

func TestNewLoop_NilFunc(t *testing.T) {
	if _, err := hardloop.NewLoop(nil, nil, nil); !errors.Is(err, hardloop.ErrNilFunc) {
		t.Fatalf("error = %v, want ErrNilFunc", err)
	}
}

func TestNewLoop_InvalidSpec(t *testing.T) {
	fn := func(context.Context) error { return nil }

	if _, err := hardloop.NewLoop([]string{"* * *"}, nil, fn); err == nil {
		t.Fatal("expected error for start spec")
	}
	if _, err := hardloop.NewLoop(nil, []string{"* * *"}, fn); err == nil {
		t.Fatal("expected error for stop spec")
	}
}

func TestCron_Run(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var (
			mx    sync.Mutex
			runs  = map[string][]time.Time{}
			track = func(name string) func(context.Context) error {
				return func(context.Context) error {
					mx.Lock()
					runs[name] = append(runs[name], time.Now().UTC())
					mx.Unlock()

					if name == "failing" {
						return errors.New("failed")
					}

					return nil
				}
			}
		)

		c, err := hardloop.NewCron(
			hardloop.Cron{Name: "hourly", Func: track("hourly"), Specs: []string{"CRON_TZ=UTC 0 * * * *"}},
			hardloop.Cron{Name: "multi", Func: track("multi"), Specs: []string{"CRON_TZ=UTC 30 1 * * *", "CRON_TZ=UTC 15 2 * * *"}},
			hardloop.Cron{Name: "failing", Func: track("failing"), Specs: []string{"CRON_TZ=UTC 0 1 * * *"}},
			hardloop.Cron{Name: "every", Func: track("every"), Specs: []string{"@every 50m"}},
			hardloop.Cron{Name: "empty", Func: track("empty")}, // no specs, skipped
		)
		if err != nil {
			t.Fatal(err)
		}
		c.SetLogger(slog.New(slog.DiscardHandler))

		if len(c.Jobs) != 4 {
			t.Fatalf("jobs = %d, want 4 (job without specs is skipped)", len(c.Jobs))
		}

		if err := c.Start(t.Context()); err != nil {
			t.Fatal(err)
		}
		if err := c.Start(t.Context()); !errors.Is(err, hardloop.ErrCronAlreadyRunning) {
			t.Fatalf("second start error = %v", err)
		}

		time.Sleep(3*time.Hour + time.Minute)
		<-c.Stop().Done()
		<-c.Stop().Done() // second stop is noop

		mx.Lock()
		defer mx.Unlock()

		assertTimes(t, "hourly", runs["hourly"], []time.Time{at(1, 1, 0), at(1, 2, 0), at(1, 3, 0)})
		assertTimes(t, "multi", runs["multi"], []time.Time{at(1, 1, 30), at(1, 2, 15)})
		assertTimes(t, "failing", runs["failing"], []time.Time{at(1, 1, 0)})
		assertTimes(t, "every", runs["every"], []time.Time{at(1, 0, 50), at(1, 1, 40), at(1, 2, 30)})
		if len(runs["empty"]) != 0 {
			t.Fatal("empty job should not run")
		}
	})
}

func TestCron_ImpossibleScheduleStops(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var count atomic.Int32
		c, err := hardloop.NewCron(hardloop.Cron{
			Name:  "never",
			Func:  func(context.Context) error { count.Add(1); return nil },
			Specs: []string{"0 0 30 2 *"},
		})
		if err != nil {
			t.Fatal(err)
		}
		c.SetLogger(nil)

		if err := c.Start(t.Context()); err != nil {
			t.Fatal(err)
		}

		time.Sleep(time.Hour)
		<-c.Stop().Done()

		if got := count.Load(); got != 0 {
			t.Fatalf("run count = %d, want 0", got)
		}
	})
}

func TestCron_RestartAfterStop(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var count atomic.Int32
		c, err := hardloop.NewCron(hardloop.Cron{
			Name:  "minute",
			Func:  func(context.Context) error { count.Add(1); return nil },
			Specs: []string{"* * * * *"},
		})
		if err != nil {
			t.Fatal(err)
		}
		c.SetLogger(nil)

		for range 2 {
			if err := c.Start(t.Context()); err != nil {
				t.Fatal(err)
			}
			time.Sleep(2*time.Minute + time.Second)
			<-c.Stop().Done()
		}

		if got := count.Load(); got != 4 {
			t.Fatalf("run count = %d, want 4", got)
		}
	})
}

func TestNewCron_InvalidSpec(t *testing.T) {
	fn := func(context.Context) error { return nil }

	if _, err := hardloop.NewCron(hardloop.Cron{Func: fn, Specs: []string{"bad"}}); err == nil {
		t.Fatal("expected error")
	}
}

func TestNewCron_NilFunc(t *testing.T) {
	if _, err := hardloop.NewCron(hardloop.Cron{Name: "x", Specs: []string{"* * * * *"}}); !errors.Is(err, hardloop.ErrCronNilFunc) {
		t.Fatalf("error = %v, want ErrCronNilFunc", err)
	}
}

func TestLoop_SetLoggerWhileRunning(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l, err := hardloop.NewLoop(nil, []string{"CRON_TZ=UTC * * * * *"}, func(ctx context.Context) error {
			<-ctx.Done()

			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		l.SetLogger(nil)

		stop := startLoop(t, l)

		for range 3 {
			time.Sleep(90 * time.Second)
			l.SetLogger(slog.New(slog.DiscardHandler))
		}

		_ = stop()
	})
}

func TestCron_SetLoggerWhileRunning(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, err := hardloop.NewCron(hardloop.Cron{
			Name:  "minute",
			Func:  func(context.Context) error { return nil },
			Specs: []string{"* * * * *"},
		})
		if err != nil {
			t.Fatal(err)
		}
		c.SetLogger(nil)

		if err := c.Start(t.Context()); err != nil {
			t.Fatal(err)
		}

		for range 3 {
			time.Sleep(90 * time.Second)
			c.SetLogger(slog.New(slog.DiscardHandler))
		}

		<-c.Stop().Done()
	})
}

func TestCron_StopNeverStarted(t *testing.T) {
	c, err := hardloop.NewCron()
	if err != nil {
		t.Fatal(err)
	}

	select {
	case <-c.Stop().Done():
	default:
		t.Fatal("stop of never started cron should be done")
	}
}

func TestCron_StopInsideJob(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var (
			c     *hardloop.CronJob
			count atomic.Int32
		)

		c, err := hardloop.NewCron(hardloop.Cron{
			Name:  "self stop",
			Specs: []string{"* * * * *"},
			Func: func(ctx context.Context) error {
				count.Add(1)
				done := c.Stop()

				// own context is canceled, done waits this function to return
				if ctx.Err() == nil {
					t.Error("job context should be canceled")
				}
				select {
				case <-done.Done():
					t.Error("done should wait running job")
				default:
				}

				return nil
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		c.SetLogger(nil)

		if err := c.Start(t.Context()); err != nil {
			t.Fatal(err)
		}

		time.Sleep(5 * time.Minute)
		<-c.Stop().Done()

		if got := count.Load(); got != 1 {
			t.Fatalf("run count = %d, want 1", got)
		}
	})
}

func TestCron_StopWaitsRunningJob(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var finished atomic.Bool
		c, err := hardloop.NewCron(hardloop.Cron{
			Name:  "slow",
			Specs: []string{"* * * * *"},
			Func: func(ctx context.Context) error {
				<-ctx.Done()
				time.Sleep(10 * time.Second) // cleanup after cancel
				finished.Store(true)

				return nil
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		c.SetLogger(nil)

		if err := c.Start(t.Context()); err != nil {
			t.Fatal(err)
		}

		time.Sleep(90 * time.Second)

		done := c.Stop()
		if finished.Load() {
			t.Fatal("stop should not wait")
		}

		<-done.Done()
		if !finished.Load() {
			t.Fatal("done should wait running job")
		}
		if now := time.Now().UTC(); !now.Equal(at(1, 0, 1).Add(40 * time.Second)) {
			t.Fatalf("done at %s", now)
		}
	})
}

func TestCron_ZeroValue(t *testing.T) {
	c := &hardloop.CronJob{}

	if err := c.Start(t.Context()); err != nil {
		t.Fatal(err)
	}

	<-c.Stop().Done()
}

func TestLoop_RunReturnsContextError(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := &recorder{}
		l, err := hardloop.NewLoop(nil, nil, r.fn)
		if err != nil {
			t.Fatal(err)
		}
		l.SetLogger(nil)

		ctx, cancel := context.WithTimeout(t.Context(), time.Hour)
		defer cancel()

		if err := l.Run(ctx); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Run() error = %v, want context.DeadlineExceeded", err)
		}

		// Run returns after the function is finished
		if l.IsLoopRunning() || l.IsFunctionRunning() {
			t.Fatal("loop and function should be stopped")
		}

		_, stops := r.get()
		assertTimes(t, "stops", stops, []time.Time{at(1, 1, 0)})
	})
}

func TestLoop_ZeroValue(t *testing.T) {
	var l hardloop.Loop
	l.SetLogger(nil)

	// must not panic, a loop without NewLoop has no function to run
	if err := l.ChangeStartSchedules([]string{"0 7 * * *"}); err != nil {
		t.Fatal(err)
	}
	if err := l.ChangeStopSchedules([]string{"0 8 * * *"}); err != nil {
		t.Fatal(err)
	}

	if err := l.Run(t.Context()); !errors.Is(err, hardloop.ErrNilFunc) {
		t.Fatalf("Run() error = %v, want ErrNilFunc", err)
	}
}

func TestLoop_RunCanceledContextDoesNotCallFunction(t *testing.T) {
	var count atomic.Int32
	l, err := hardloop.NewLoop(nil, nil, func(context.Context) error {
		count.Add(1)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	l.SetLogger(nil)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	for range 100 {
		if err := l.Run(ctx); !errors.Is(err, context.Canceled) {
			t.Fatalf("Run() error = %v, want context.Canceled", err)
		}
	}

	if got := count.Load(); got != 0 {
		t.Fatalf("called function %d times with an already canceled context", got)
	}
}

func TestLoop_RunWaitsForCleanup(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var finished atomic.Bool
		l, err := hardloop.NewLoop(nil, nil, func(ctx context.Context) error {
			<-ctx.Done()
			time.Sleep(10 * time.Second)
			finished.Store(true)

			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		l.SetLogger(nil)

		ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
		defer cancel()
		if err := l.Run(ctx); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Run() error = %v, want context.DeadlineExceeded", err)
		}
		if !finished.Load() || l.IsLoopRunning() || l.IsFunctionRunning() {
			t.Fatal("Run returned before cleanup finished")
		}
		if want := at(1, 0, 1).Add(10 * time.Second); !time.Now().Equal(want) {
			t.Fatalf("Run returned at %s, want %s", time.Now(), want)
		}
	})
}

func TestLoop_RunAgainAfterWrappedClose(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var count atomic.Int32
		l, err := hardloop.NewLoop(nil, nil, func(context.Context) error {
			count.Add(1)

			return errors.Join(errors.New("finished"), hardloop.ErrCloseLoop)
		})
		if err != nil {
			t.Fatal(err)
		}
		l.SetLogger(nil)

		for range 10 {
			if err := l.Run(t.Context()); err != nil {
				t.Fatalf("Run() error = %v, want nil", err)
			}
			if l.IsLoopRunning() || l.IsFunctionRunning() {
				t.Fatal("loop and function should be stopped")
			}
		}
		if got := count.Load(); got != 10 {
			t.Fatalf("run count = %d, want 10", got)
		}
	})
}

func TestLoop_RestartDelayAcrossWindows(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := &recorder{}
		l, err := hardloop.NewLoop(
			[]string{"CRON_TZ=UTC 0 * * * *"},
			[]string{"CRON_TZ=UTC 30 * * * *"},
			r.fn,
		)
		if err != nil {
			t.Fatal(err)
		}
		l.SetLogger(nil)
		// The 01:00 start must wait until 01:45. That window is closed,
		// so the next run must be at 02:00, not 01:00 or 01:45.
		l.SetRestartDelay(105 * time.Minute)

		runLoop(t, l, 4*time.Hour-time.Minute)

		starts, stops := r.get()
		assertTimes(t, "starts", starts, []time.Time{at(1, 0, 0), at(1, 2, 0)})
		assertTimes(t, "stops", stops, []time.Time{at(1, 0, 30), at(1, 2, 30)})
	})
}

func TestLoop_RestartDelayWithoutStop(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var mx sync.Mutex
		var starts []time.Time
		l, err := hardloop.NewLoop([]string{"CRON_TZ=UTC 0 * * * *"}, nil, func(context.Context) error {
			mx.Lock()
			starts = append(starts, time.Now().UTC())
			mx.Unlock()

			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		l.SetLogger(nil)
		l.SetRestartDelay(105 * time.Minute)

		// The second run is delayed from 02:00 to 02:45. Cancellation
		// at 03:30 must interrupt the wait for the third run at 04:30.
		runLoop(t, l, 210*time.Minute)

		mx.Lock()
		defer mx.Unlock()
		assertTimes(t, "starts", starts, []time.Time{at(1, 1, 0), at(1, 2, 45)})
		if want := at(1, 3, 30); !time.Now().Equal(want) {
			t.Fatalf("Run returned at %s, want %s", time.Now(), want)
		}
	})
}

func TestLoop_RunNearStopBoundary(t *testing.T) {
	for _, offset := range []time.Duration{-time.Second, -500 * time.Millisecond, 0} {
		t.Run(offset.String(), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				time.Sleep(8*time.Hour + offset)
				r := &recorder{}
				l, err := hardloop.NewLoop(
					[]string{"CRON_TZ=UTC 0 7 * * *"},
					[]string{"CRON_TZ=UTC 0 8 * * *"},
					r.fn,
				)
				if err != nil {
					t.Fatal(err)
				}
				l.SetLogger(nil)

				runLoop(t, l, time.Minute)

				starts, stops := r.get()
				if offset == 0 {
					assertTimes(t, "starts", starts, nil)
					assertTimes(t, "stops", stops, nil)
				} else {
					assertTimes(t, "starts", starts, []time.Time{at(1, 8, 0).Add(offset)})
					assertTimes(t, "stops", stops, []time.Time{at(1, 8, 0)})
				}
			})
		})
	}
}
