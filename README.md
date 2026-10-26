# hardloop

[![License](https://img.shields.io/github/license/rakunlabs/hardloop?color=red&style=flat-square)](https://raw.githubusercontent.com/rakunlabs/hardloop/main/LICENSE)
[![Coverage](https://img.shields.io/sonar/coverage/rakunlabs_hardloop?logo=sonarcloud&server=https%3A%2F%2Fsonarcloud.io&style=flat-square)](https://sonarcloud.io/summary/overall?id=rakunlabs_hardloop)
[![GitHub Workflow Status](https://img.shields.io/github/actions/workflow/status/rakunlabs/hardloop/test.yml?branch=main&logo=github&style=flat-square&label=ci)](https://github.com/rakunlabs/hardloop/actions)
[![Go PKG](https://raw.githubusercontent.com/rakunlabs/.github/main/assets/badges/gopkg.svg)](https://pkg.go.dev/github.com/rakunlabs/hardloop)

Hardloop is a cron time-based function runner.

Set start and end times as cron specs, and give function to run between them.

Go 1.25 or later is required. Running windows include their start time and exclude
their stop time. The function must respect context cancellation to stop on time.

```shell
go get github.com/rakunlabs/hardloop
```

![hardloop](./_assets/hardloop.svg)

## Usage

Check the https://crontab.guru/ to explain about cron specs.

> Hardloop different works than _crontab.guru_ in weekdays and day of month selection. We use __and__ operation but that site use __or__ operation when used both of them.

You can give as much as you want start, stop times.  
If stop time is not given, it will run forever.  
If just stop time given, it will restart in the stop times.

If the function returns before the stop time, it is started again, at most once per second. Change it with `SetRestartDelay`.

Use Timezone to set the timezone: `CRON_TZ=Europe/Istanbul 0 7 * * 1,2,3,4,5`

Default timezone is system timezone.

Day of week accepts `0-7` (both `0` and `7` are Sunday) and names like `mon-fri`.

> Some times don't exist in some timezones.
> For example, `CRON_TZ=Europe/Amsterdam 30 2 * * *` doesn't exist on the DST start day, clocks jump from 02:00 to 03:00 so that day is skipped. Avoid using times that don't exist.
>
> When clocks are turned back (DST end), repeated times run only once like standard cron. Schedules without a fixed hour like `*/30 * * * *` still run in both occurrences.

```go
// Set start cron specs.
startSpecs := []string{
    // start at 7:00 in Monday, Tuesday, Wednesday, Thursday, Friday
    "0 7 * * 1,2,3,4,5",
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

// blocks until ctx is done or the function returns hardloop.ErrCloseLoop
// returns nil on ErrCloseLoop, otherwise the context error
err = myFunctionLoop.Run(ctx)

// or run in background
// go myFunctionLoop.Run(ctx)
```

For simple jobs, you can use `hardloop.NewCron` to create a cron job.

```go
// Create a new cron job.
myCronJob, err := hardloop.NewCron(hardloop.Cron{
	Name:  "MyCronJob",
	Func:  MyFunction,
	Specs: []string{"0 7 * * 1-5"}, // Every weekday at 7 AM
})
// ... handle error

myCronJob.Start(ctx) // background run the cron job

// Stop cancels the jobs without waiting, safe to call inside a job.
// Wait the returned context to be sure running jobs are finished.
<-myCronJob.Stop().Done()
```

### Restart Delay

When the function returns inside a running window, it is started again. To prevent a busy loop, there is a minimum duration between two starts, default is `hardloop.DefaultRestartDelay` (1 second).

```go
// start again at most once in 10 seconds
myFunctionLoop.SetRestartDelay(10 * time.Second)
```

This minimum also applies to scheduled starts. If waiting for the delay would
outlast a running window, the loop waits for the next window instead.

### Set Logger

Loop and cron job use `*slog.Logger`, default is `slog.Default()`. Set `nil` to disable logging.

```go
myFunctionLoop.SetLogger(slog.New(slog.NewTextHandler(os.Stdout, nil)))
```

## Development

Test code

```sh
make test
# make coverage html-wsl
```

## License

Licensed under [MIT](LICENSE). See [third-party notices](THIRD_PARTY_NOTICES.md) for adapted code.
