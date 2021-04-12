package main

import (
	"bytes"
	"context"
	"log"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"
)

// Version flag set during compilation
var Version string

// Port the default port to be used. Can be overrided with APP_PORT env var
var Port = "7676"

// ReadTimeout total time for reading the entire request including body
var ReadTimeout = time.Second * 5

// GracefulTimeoutSecs number of seconds before timing out during graceful shutdown
var GracefulTimeoutSecs = 5

// WebcamFrameTimeoutSecs seconds before webcam frame times out
var WebcamFrameTimeoutSecs = 5

// WebcamFrameMaxTimeouts maximum consecutive timeouts for grabbing webcam frame before shutting down
var WebcamFrameMaxTimeouts = 10

// TimelapseIntervalMins minutes interval between timelapse stills, override with APP_TIMELAPSE_INT_MINS
var TimelapseIntervalMins = 5

// TimelapseOutputDir directory for timelapse output files
var TimelapseOutputDir = "tl_output"

func main() {

	if Version == "" {
		log.Println("Starting cam server unknown version")
	} else {
		log.Printf("Starting cam server %s", Version)
	}

	/*
		Parse configuration options or use defaults and warn
	*/
	if p := os.Getenv("APP_PORT"); p != "" {
		if _, err := strconv.Atoi(p); err != nil {
			log.Fatal("failed to parse env var APP_PORT as integer")
		}
		Port = p
	} else {
		log.Printf("No port provided (APP_PORT), using %s as default", Port)
	}

	if tim := os.Getenv("APP_TIMELAPSE_INT_MINS"); tim != "" {
		temp, err := strconv.Atoi(tim)
		if err != nil {
			log.Fatal("failed to parse env var APP_TIMELAPSE_INT_MINS as integer")
		}
		TimelapseIntervalMins = temp
	} else {
		log.Printf("No timelapse interval provided (APP_TIMELAPSE_INT_MINS), using %d minutes as default", TimelapseIntervalMins)
	}

	if td := os.Getenv("APP_TIMELAPSE_DIR"); td != "" {
		TimelapseOutputDir = td
	} else {
		log.Printf("No timelapse output dir provided provided (APP_TIMELAPSE_DIR), using %s as default", TimelapseOutputDir)
	}

	/*
		Here we are defining our context, which is like a grouping of goroutines.
		We can cancel the context and it will signal all the goroutines who are
		listening within that context to shut down.
	*/
	ctx := context.Background()
	ctx, cancel := context.WithCancel(ctx)

	/*
		Here we define the WaitGroup which is basically a semaphore type mechanism.
		Calling `wg.Add(delta)` increments the counter. Calling wg.Done() decrements
		it. `wg.Wait()` blocks until the counter hits zero. WaitGroups are commonly
		used to ensure multiple goroutines have all completed.
	*/
	wg := sync.WaitGroup{}

	/*
		Here we define a channel. It is of type `error`, since we will be sending errors
		over this channel from the worker jobs to this main goroutine when they encounter
		fatal errors. Sending an error over this channel will cause the system to cancel
		the context we defined earlier, thereby shutting down all other worker goroutines
		gracefully. Another thing to note is that it has a size of 1000, this effectively
		makes it a buffered channel. Without the 1000 parameter, all attempts to put new
		data on the channel will block until the channel is "clear", meaning a receiver
		has received the piece of data on it. You can think of regular channels of having
		a buffer size of one. Here, we set it to some large number that is much greater
		than the number of workers, thereby preventing a scenario where two workers try
		to push an error onto the channel, and since we only ever pull a maximum of one
		error from the channel before beginning the shutdown process, the second worker
		to try pushing a value will hang indefinitely. Having a buffer prevents this.
	*/
	errCh := make(chan error, 1000)

	li := make(chan *bytes.Buffer)

	// Here we add to the waitgroup a delta equal to the number of workers we are spawning
	wg.Add(3)
	go StreamWorker(ctx, errCh, &wg, li)
	go TimelapseWorker(ctx, errCh, &wg, li)
	go ServeHttp(ctx, errCh, &wg, Port, li)

	/*
		Define a buffered channel to listen to stop signals and handle the first one gracefully
	*/
	sigs := make(chan os.Signal, 2)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM, syscall.SIGQUIT)

	/*
		Here we use a select statement which blocks until either one of the channel statements in the
		case statements activate. The program proceeds after executing the case block. In this case,
		we are waiting for 1 of two events. Either one of our workers throws a fatal error, or we get
		an interrupt/stop signal. In either case, we log a message and then call `cancel()` a few lines
		later which actually signals the workers to shut down.
	*/
	select {
	case e := <-errCh:
		log.Printf("Received fatal error from worker, attempting graceful shutdown of other workers: %s", e.Error())
	case <-sigs:
		log.Println("Received signal, attempting graceful shutdown of workers")
	}

	log.Printf("Waiting %d seconds, then force exiting. Alternatively, use Ctrl+C to force exit.", GracefulTimeoutSecs)
	// Actually signal workers to shut down
	cancel()

	// `time.After` returns a channel that gets signalled after the given duration
	timeout := time.After(time.Second * time.Duration(GracefulTimeoutSecs))

	/*
		Here we define a channel `wgCh` that we push a signal on once wg.Wait() finishes. Basically, we are just
		converting `wg.Wait()` into a channel that can be combined with other channels in a select statement later.
		This channel is of type `struct{}` which can be ignored. We won't actually be sending any data over it, we
		just want to use the channel to send a signal.
	*/
	wgCh := make(chan struct{})
	go func() {
		wg.Wait()
		// Closing a channel will cause it to unblock for receivers
		close(wgCh)
	}()

	/*
		Here we use another select statement that waits for one of three cases:
		1. our workers successfully shut down within `GracefulTimeoutSecs` seconds (signalled by the `wgCh` channel)
		2. our timeout of `GracefulTimeoutSecs` is triggered
		3. another stop signal comes in
	*/
	select {
	case <-timeout:
		// Exit with a status code of 1 and error message since we failed to gracefully shut down in time
		log.Fatalf("workers failed to shut down after %d seconds, forcing shutdown with status 1", GracefulTimeoutSecs)
	case <-wgCh:
		// This is the good case! All workers shut down within the time limit
		os.Exit(0)
	case <-sigs:
		log.Fatal("received second shutdown signal, goodbye")
	}

	// Execution will never reach here
}
