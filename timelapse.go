package main

import (
	"bufio"
	"bytes"
	"context"
	"io/ioutil"
	"log"
	"math"
	"os"
	"os/exec"
	"path"
	"strconv"
	"sync"
	"time"
)

// TimelapseWorker loop to take timelapse snapshots and stitch together the timelapse video
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
			// Take snapshot & encode new timelapse video

			// Remove stale image
			<-li

			// Get new image
			img := <-li

			now := time.Now()

			snapFilename := strconv.FormatInt(now.Unix(), 10) + ".jpg"

			f, err := os.Create(path.Join(TimelapseOutputDir, snapFilename))
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

			encodeStarted := time.Now()
			vidFilename := path.Join(TimelapseOutputDir, "timelapse.mp4")
			jpgGlob := path.Join(TimelapseOutputDir, "*.jpg")
			cmd := exec.Command("sh", "-c", `ffmpeg -framerate 30 -pattern_type glob -i "`+jpgGlob+`" -y `+vidFilename)
			_, err = cmd.CombinedOutput()
			if err != nil {
				log.Println(err)
				return
			}
			encodeDur := time.Since(encodeStarted)
			minStr := strconv.FormatFloat(math.Floor(encodeDur.Minutes()), 'f', 0, 64)
			secStr := strconv.FormatFloat(math.Floor(encodeDur.Seconds()), 'f', 0, 64)
			if encodeDur >= time.Duration(TimelapseIntervalMins)*time.Minute {
				log.Printf("ERROR: timelapse encoding took %sm%ss, greater than the timelapse capture interval of %dm", minStr, secStr, TimelapseIntervalMins)
			} else if encodeDur >= (time.Duration(TimelapseIntervalMins)*time.Minute)/2 {
				log.Printf("WARN: timelapse encoding took %sm%ss, greater than half of the timelapse capture interval of %dm", minStr, secStr, TimelapseIntervalMins)
			} else {
				log.Printf("INFO: timelapse encoding took %sm%ss", minStr, secStr)
			}
		}
	}

	ticker.Stop()

	log.Println("Stopping timelapse worker")
}
