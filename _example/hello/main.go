package main

import (
	"context"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"time"

	"github.com/rakunlabs/hardloop"
)

func MyFunction(ctx context.Context) error {
	// ctx is canceled when the stop time comes.
	log.Println("Hello hardloop!")
	log.Println("Waiting 12 secs...")

	timer := time.NewTimer(12 * time.Second)

	select {
	case <-timer.C:
		log.Println("Done!")
	case <-ctx.Done():
		log.Println("Canceled!")
		timer.Stop()
	}

	return nil
}

func main() {
	// Set start cron specs.
	startSpecs := []string{
		// start at 7:00 in Monday, Tuesday, Wednesday, Thursday, Friday
		"CRON_TZ=Europe/Amsterdam 0 7 * * 1,2,3,4,5",
		// start at 9:00 in Saturday
		"0 9 * * 6",
	}
	// Set stop cron specs.
	stopSpecs := []string{
		// stop at 17:00 in Monday, Tuesday, Wednesday, Thursday, Friday
		"0 17 * * 1,2,3,4,5",
		// stop at 13:00 in Saturday
		"0 13 * * 6",
	}

	// Create a new schedule.
	myFunctionLoop, err := hardloop.NewLoop(startSpecs, stopSpecs, MyFunction)
	if err != nil {
		// wrong cron specs
		log.Fatal(err)
	}

	myFunctionLoop.SetLogger(slog.New(slog.NewTextHandler(os.Stdout, nil)))

	// stop with Ctrl+C
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	// blocks until ctx is done or the function returns hardloop.ErrCloseLoop
	if err := myFunctionLoop.Run(ctx); err != nil {
		log.Println(err)
	}
}
