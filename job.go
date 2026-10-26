package hardloop

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

var (
	// ErrCronAlreadyRunning is returned when Start is called on a running cron job.
	ErrCronAlreadyRunning = errors.New("cron already running")
	// ErrCronNilFunc is returned when a cron has no function.
	ErrCronNilFunc = errors.New("cron function is nil")
)

// CronJob runs functions on their cron schedules, create it with NewCron.
type CronJob struct {
	Jobs []Cron

	started bool
	m       sync.Mutex
	cancel  context.CancelFunc
	// done is canceled when all jobs of the last run are finished.
	done  context.Context
	logMx sync.RWMutex
	log   *slog.Logger
}

// Cron is a function with its cron specs.
type Cron struct {
	// Name for logging and identification purposes.
	Name  string
	Func  func(ctx context.Context) error
	Specs []string

	schedules []Schedule
}

// NewCron returns a new CronJob with the given crons.
// Crons without specs are skipped, invalid specs or nil function returns an error.
func NewCron(crons ...Cron) (*CronJob, error) {
	jobs := make([]Cron, 0, len(crons))
	for _, cron := range crons {
		if cron.Func == nil {
			return nil, fmt.Errorf("%w: %s", ErrCronNilFunc, cron.Name)
		}

		schedules, err := parseSchedules(cron.Specs)
		if err != nil {
			return nil, err
		}

		if len(schedules) == 0 {
			continue
		}

		jobs = append(jobs, Cron{
			Name:      cron.Name,
			Func:      cron.Func,
			Specs:     cron.Specs,
			schedules: schedules,
		})
	}

	return &CronJob{
		Jobs: jobs,
		log:  slog.Default(),
	}, nil
}

// SetLogger sets the logger for the cron job.
//   - If not set, it uses the default slog logger.
//   - Set to nil to disable logging.
func (c *CronJob) SetLogger(log *slog.Logger) {
	c.logMx.Lock()
	defer c.logMx.Unlock()

	c.log = loggerOrDiscard(log)
}

func (c *CronJob) logger() *slog.Logger {
	c.logMx.RLock()
	defer c.logMx.RUnlock()

	if c.log == nil {
		return slog.Default()
	}

	return c.log
}

// Start starts the cron job, running each job according to its schedule.
//   - If the cron job is already running, it returns an error.
func (c *CronJob) Start(ctx context.Context) error {
	c.m.Lock()
	defer c.m.Unlock()

	if c.started {
		return ErrCronAlreadyRunning
	}
	c.started = true

	ctx, cancel := context.WithCancel(ctx)
	c.cancel = cancel

	done, doneCancel := context.WithCancel(context.Background())
	c.done = done

	wg := &sync.WaitGroup{}
	for _, job := range c.Jobs {
		c.logger().Info("add cron job", "job", job.Name, "specs", job.Specs)

		wg.Go(func() {
			c.runJob(ctx, job)
		})
	}

	go func() {
		wg.Wait()
		doneCancel()
	}()

	return nil
}

func (c *CronJob) runJob(ctx context.Context, job Cron) {
	var nextTime time.Time
	for {
		now := time.Now()
		if !nextTime.IsZero() && nextTime.After(now) {
			now = nextTime
		}

		nextTime = FindNext(job.schedules, now)
		if nextTime.IsZero() {
			c.logger().Warn("no next run time, stopping cron job", "job", job.Name, "specs", job.Specs)

			return
		}

		until := time.Until(nextTime)
		if until <= 0 {
			until = time.Second // Ensure we wait at least a second before running the job again
		}

		c.logger().Info("starting cron job", "job", job.Name, "specs", job.Specs, "next_run", nextTime, "remaining", until)

		timer := time.NewTimer(until)

		select {
		case <-ctx.Done():
			timer.Stop()
			c.logger().Info("stopping cron job", "job", job.Name)

			return
		case <-timer.C:
			c.logger().Info("running cron job", "job", job.Name)

			if err := job.Func(ctx); err != nil {
				c.logger().Error("error running cron job", "job", job.Name, "error", err)
			}
		}
	}
}

// Stop cancels the context of the jobs and returns without waiting them.
// Returned context is done when all running jobs are finished.
//
//	<-job.Stop().Done() // wait for running jobs
//
// It is safe to call Stop inside a job function or multiple times.
func (c *CronJob) Stop() context.Context {
	c.m.Lock()
	defer c.m.Unlock()

	if c.started {
		c.started = false
		c.cancel()
	}

	if c.done == nil {
		// never started
		done, cancel := context.WithCancel(context.Background())
		cancel()

		return done
	}

	return c.done
}
