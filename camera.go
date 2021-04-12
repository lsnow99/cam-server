package main

import (
	"bytes"
	"context"
	"image"
	"image/jpeg"
	"log"
	"sync"

	"github.com/blackjack/webcam"
)

var (
	V4L2_PIX_FMT_PJPG  webcam.PixelFormat
	V4L2_PIX_FMT_MJPEG webcam.PixelFormat
)

func FourCCToU32(b []byte) uint32 {
	return uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24
}

type FrameSizes []webcam.FrameSize

// For sorting purposes
func (slice FrameSizes) Len() int {
	return len(slice)
}

//For sorting purposes
func (slice FrameSizes) Less(i, j int) bool {
	ls := slice[i].MaxWidth * slice[i].MaxHeight
	rs := slice[j].MaxWidth * slice[j].MaxHeight
	return ls < rs
}

//For sorting purposes
func (slice FrameSizes) Swap(i, j int) {
	slice[i], slice[j] = slice[j], slice[i]
}

func initFormatCodes() {
	pjpg := []byte{
		0: 'P',
		1: 'J',
		2: 'P',
		3: 'G',
	}
	V4L2_PIX_FMT_PJPG = webcam.PixelFormat(FourCCToU32(pjpg))
	mjpeg := []byte{
		0: 'M',
		1: 'J',
		2: 'P',
		3: 'G',
	}
	V4L2_PIX_FMT_MJPEG = webcam.PixelFormat(FourCCToU32(mjpeg))
}

func StreamWorker(ctx context.Context, errCh chan error, wg *sync.WaitGroup, li chan *bytes.Buffer) {
	// This is guaranteed to run as the last thing before this function returns
	defer wg.Done()

	log.Println("Starting camera streaming worker")

	initFormatCodes()

	cam, err := webcam.Open("/dev/video0") // Open webcam
	if err != nil {
		errCh <- err
	}
	defer cam.Close()

	err = cam.StartStreaming()
	if err != nil {
		errCh <- err
	}

	doShutdown := false

	// Register our handler to process shutdown requests
	go func() {
		<-ctx.Done()
		log.Println("Gracefully shutting down camera streamer")
		cam.StopStreaming()
		doShutdown = true
	}()

	// Channels to handle messaging with the image encoder goroutine
	var (
		fi   chan []byte        = make(chan []byte)
		back chan struct{}      = make(chan struct{})
	)

	go encodeToImage(cam, back, fi, li, 1920, 1080, errCh)

	failures := 0
	for !doShutdown {
		err = cam.WaitForFrame(uint32(WebcamFrameTimeoutSecs))

		switch err.(type) {
		case nil:
			failures = 0
		case *webcam.Timeout:
			if failures == WebcamFrameMaxTimeouts {
				errCh <- err
			}
			failures++
			continue
		default:
			errCh <- err
		}

		frame, err := cam.ReadFrame()
		if len(frame) != 0 {
			select {
			case fi <- frame:
				<-back
			default:
			}
		} else if err != nil {
			errCh <- err
		}
	}

	log.Println("Stopped camera streamer")
}

func encodeToImage(wc *webcam.Webcam, back chan struct{}, fi chan []byte, li chan *bytes.Buffer, w, h uint32, errCh chan error) {

	var (
		frame []byte
	)
	for {
		bframe := <-fi
		// copy frame
		if len(frame) < len(bframe) {
			frame = make([]byte, len(bframe))
		}
		copy(frame, bframe)
		back <- struct{}{}

		img, _, err := image.Decode(bytes.NewReader(frame))
		if err != nil {
			errCh <- err
			return
		}
		//convert to jpeg
		buf := &bytes.Buffer{}
		if err := jpeg.Encode(buf, img, nil); err != nil {
			errCh <- err
			return
		}

		const N = 50
		// broadcast image up to N ready clients
		nn := 0
	FOR:
		for ; nn < N; nn++ {
			select {
			case li <- buf:
			default:
				break FOR
			}
		}
		if nn == 0 {
			li <- buf
		}

	}
}
