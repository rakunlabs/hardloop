package hardloop_test

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/rakunlabs/hardloop"
)

func ExampleParse() {
	schedule := "0 7 * * *"

	s, err := hardloop.Parse(schedule)
	if err != nil {
		log.Fatal(err)
	}

	now := time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC)

	fmt.Println("Next 5 times:")
	tmpNow := now
	for range 5 {
		tmpNow = s.Next(tmpNow)
		fmt.Println(tmpNow)
	}

	tmpNow = now
	fmt.Println("Prev 5 times:")
	for range 5 {
		tmpNow = s.Prev(tmpNow)
		fmt.Println(tmpNow)
	}

	// Output:
	// Next 5 times:
	// 2023-01-01 07:00:00 +0000 UTC
	// 2023-01-02 07:00:00 +0000 UTC
	// 2023-01-03 07:00:00 +0000 UTC
	// 2023-01-04 07:00:00 +0000 UTC
	// 2023-01-05 07:00:00 +0000 UTC
	// Prev 5 times:
	// 2022-12-31 07:00:00 +0000 UTC
	// 2022-12-30 07:00:00 +0000 UTC
	// 2022-12-29 07:00:00 +0000 UTC
	// 2022-12-28 07:00:00 +0000 UTC
	// 2022-12-27 07:00:00 +0000 UTC
}

func ExampleParse_timezone() {
	// Timezone of the spec is used for calculation, result is in the location of the given time.
	s, err := hardloop.Parse("CRON_TZ=Europe/Istanbul 0 7 * * 1-5")
	if err != nil {
		log.Fatal(err)
	}

	now := time.Date(2024, 6, 7, 12, 0, 0, 0, time.UTC) // friday

	fmt.Println(s.Next(now))
	fmt.Println(s.Prev(now))

	// Output:
	// 2024-06-10 04:00:00 +0000 UTC
	// 2024-06-07 04:00:00 +0000 UTC
}

func ExampleParse_descriptors() {
	now := time.Date(2024, 6, 7, 12, 34, 56, 0, time.UTC)

	for _, spec := range []string{"@hourly", "@daily", "@weekly", "@monthly", "@yearly", "@every 1h30m"} {
		s, err := hardloop.Parse(spec)
		if err != nil {
			log.Fatal(err)
		}

		fmt.Printf("%-13s %s\n", spec, s.Next(now))
	}

	// Output:
	// @hourly       2024-06-07 13:00:00 +0000 UTC
	// @daily        2024-06-08 00:00:00 +0000 UTC
	// @weekly       2024-06-09 00:00:00 +0000 UTC
	// @monthly      2024-07-01 00:00:00 +0000 UTC
	// @yearly       2025-01-01 00:00:00 +0000 UTC
	// @every 1h30m  2024-06-07 14:04:56 +0000 UTC
}

func ExampleParse_dayOfMonthAndWeek() {
	// Day of month and day of week are combined with AND, this is friday the 13th.
	s, err := hardloop.Parse("0 0 13 * 5")
	if err != nil {
		log.Fatal(err)
	}

	now := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	for range 3 {
		now = s.Next(now)
		fmt.Println(now.Format("2006-01-02 Monday"))
	}

	// Output:
	// 2024-09-13 Friday
	// 2024-12-13 Friday
	// 2025-06-13 Friday
}

func ExampleFindNext() {
	// Multiple schedules, closest one wins.
	weekdays, _ := hardloop.Parse("0 7 * * 1-5")
	saturday, _ := hardloop.Parse("0 9 * * 6")
	schedules := []hardloop.Schedule{weekdays, saturday}

	now := time.Date(2024, 6, 7, 12, 0, 0, 0, time.UTC) // friday

	fmt.Println(hardloop.FindNext(schedules, now))
	fmt.Println(hardloop.FindPrev(schedules, now))

	// Output:
	// 2024-06-08 09:00:00 +0000 UTC
	// 2024-06-07 07:00:00 +0000 UTC
}

func ExampleScheduleGroup_NextStartTime() {
	group, err := hardloop.NewSchedule(
		[]string{"CRON_TZ=UTC 0 7 * * 1-5", "CRON_TZ=UTC 0 9 * * 6"},
		[]string{"CRON_TZ=UTC 0 17 * * 1-5", "CRON_TZ=UTC 0 13 * * 6"},
	)
	if err != nil {
		log.Fatal(err)
	}

	for _, now := range []time.Time{
		time.Date(2024, 6, 7, 12, 0, 0, 0, time.UTC), // friday inside window
		time.Date(2024, 6, 7, 18, 0, 0, 0, time.UTC), // friday after stop
		time.Date(2024, 6, 8, 14, 0, 0, 0, time.UTC), // saturday after stop
	} {
		if next := group.NextStartTime(now); next != nil {
			fmt.Println(now.Format("Mon 15:04"), "-> start at", next.Format("Mon 15:04"))
		} else {
			fmt.Println(now.Format("Mon 15:04"), "-> running now")
		}
	}

	// Output:
	// Fri 12:00 -> running now
	// Fri 18:00 -> start at Sat 09:00
	// Sat 14:00 -> start at Mon 07:00
}

func ExampleNewLoop() {
	// Run the function between 7:00 and 17:00 on weekdays and 9:00 and 13:00 on saturday.
	startSpecs := []string{
		"0 7 * * 1-5",
		"0 9 * * 6",
	}
	stopSpecs := []string{
		"0 17 * * 1-5",
		"0 13 * * 6",
	}

	loop, err := hardloop.NewLoop(startSpecs, stopSpecs, func(ctx context.Context) error {
		// do work until stop time, ctx is canceled at stop time
		<-ctx.Done()

		// return hardloop.ErrCloseLoop to close the loop completely
		return nil
	})
	if err != nil {
		log.Fatal(err)
	}

	loop.SetLogger(nil)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	// blocks until ctx is done or function returns hardloop.ErrCloseLoop
	err = loop.Run(ctx)

	fmt.Println("error:", err)

	// Output:
	// error: context deadline exceeded
}

func ExampleLoop_Run_background() {
	loop, err := hardloop.NewLoop(nil, nil, func(ctx context.Context) error {
		<-ctx.Done()

		return nil
	})
	if err != nil {
		log.Fatal(err)
	}

	loop.SetLogger(nil)

	ctx, cancel := context.WithCancel(context.Background())

	// run in background, wait with a WaitGroup
	var wg sync.WaitGroup
	wg.Go(func() {
		if err := loop.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			log.Println(err)
		}
	})

	// ... do other work, then stop the loop and wait to finish
	cancel()
	wg.Wait()

	fmt.Println("loop running:", loop.IsLoopRunning())

	// Output:
	// loop running: false
}

func ExampleLoop_SetRestartDelay() {
	count := 0

	// Without specs, function runs again after it returns.
	loop, err := hardloop.NewLoop(nil, nil, func(context.Context) error {
		count++
		if count == 3 {
			return hardloop.ErrCloseLoop
		}

		return nil
	})
	if err != nil {
		log.Fatal(err)
	}

	loop.SetLogger(nil)
	// default is 1 second between two starts, wait 10ms instead
	loop.SetRestartDelay(10 * time.Millisecond)

	if err := loop.Run(context.Background()); err != nil {
		log.Fatal(err)
	}

	fmt.Println("run count:", count)

	// Output:
	// run count: 3
}

func ExampleNewLoop_closeLoop() {
	// Without specs function runs immediately, returning ErrCloseLoop finishes the loop.
	loop, err := hardloop.NewLoop(nil, nil, func(context.Context) error {
		fmt.Println("run once")

		return hardloop.ErrCloseLoop
	})
	if err != nil {
		log.Fatal(err)
	}

	loop.SetLogger(nil)
	// returns nil when function returns ErrCloseLoop
	if err := loop.Run(context.Background()); err != nil {
		log.Fatal(err)
	}

	fmt.Println("closed")

	// Output:
	// run once
	// closed
}

func ExampleNewCron() {
	job, err := hardloop.NewCron(
		hardloop.Cron{
			Name:  "report",
			Func:  func(context.Context) error { return nil },
			Specs: []string{"0 7 * * 1-5"}, // every weekday at 7 AM
		},
		hardloop.Cron{
			Name:  "cleanup",
			Func:  func(context.Context) error { return nil },
			Specs: []string{"@hourly", "30 12 * * *"}, // multiple specs
		},
	)
	if err != nil {
		log.Fatal(err)
	}

	job.SetLogger(nil)

	if err := job.Start(context.Background()); err != nil {
		log.Fatal(err)
	}
	fmt.Println("jobs:", len(job.Jobs))

	// cancel jobs and wait running ones to finish
	<-job.Stop().Done()

	// Output:
	// jobs: 2
}
