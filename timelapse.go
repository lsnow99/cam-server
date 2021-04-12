package main

import (
	"bufio"
	"bytes"
	"context"
	"io/ioutil"
	"log"
	"os"
	"path"
	"strconv"
	"sync"
	"time"
)

func TimelapseWorker(ctx context.Context, errCh chan error, wg *sync.WaitGroup, li chan *bytes.Buffer) {
	// This is guaranteed to run as the last thing before this function returns
	defer wg.Done()

	log.Println("Starting timelapse worker")

	if _, err := os.Stat(TimelapseOutputDir); os.IsNotExist(err) {
		if err := os.Mkdir(TimelapseOutputDir, 0755); err != nil {
			errCh <- err
			return
		}
	}

	ticker := time.NewTicker(time.Minute * time.Duration(TimelapseIntervalMins))

FOR:
	for {
		select {
		case <-ctx.Done():
			// Shut down the worker
			break FOR
		case <-ticker.C:
			// Take snapshot

			// Remove stale image
			<-li

			// Get new image
			img := <-li

			now := time.Now()

			filename := strconv.FormatInt(now.Unix(), 10) + ".jpg"

			f, err := os.Create(path.Join(TimelapseOutputDir, filename))
			if err != nil {
				errCh <- err
				break FOR
			}

			w := bufio.NewWriter(f)
			b, err := ioutil.ReadAll(img)
			if err != nil {
				errCh <- err
				break FOR
			}
			_, err = w.Write(b)
			if err != nil {
				errCh <- err
				break FOR
			}

			log.Printf("Recorded timelapse snapshot @ %v", now)
		}
	}

	ticker.Stop()

	log.Println("Stopping timelapse worker")
}
