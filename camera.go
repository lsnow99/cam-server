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

/*
	This file defines a worker to stream and process frames from the camera.
	Note: code is heavily borrowed from https://github.com/blackjack/webcam/blob/master/examples/http_mjpeg_streamer/webcam.go
*/

var (
	V4L2_PIX_FMT_PJPG  webcam.PixelFormat
	V4L2_PIX_FMT_MJPEG webcam.PixelFormat
)

func fourCCToU32(b []byte) uint32 {
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
	V4L2_PIX_FMT_PJPG = webcam.PixelFormat(fourCCToU32(pjpg))
	mjpeg := []byte{
		0: 'M',
		1: 'J',
		2: 'P',
		3: 'G',
	}
	V4L2_PIX_FMT_MJPEG = webcam.PixelFormat(fourCCToU32(mjpeg))
}

// StreamWorker main loop to grab raw frame data from the camera
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
		fi   chan []byte   = make(chan []byte)
		back chan struct{} = make(chan struct{})
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

/*
	encodeToImage This function is supposed to take the raw bytes output by the camera streamer on the fi channel and convert 
	them to jpeg, and then forward the jpeg bytes onto the li channel. It turns out the bytes we are getting are already jpeg 
	so most of this should can probably be removed (TODO: verify this). When a new frame is pushed to fi, it is picked up by
	this function and processed. The processed jpeg is pushed onto li, and up to N client goroutines can grab the frame from
	li before a new frame is grabbed from fi. At least one client goroutine must draw a frame from li before the loop
	continues and a fresh frame is grabbed. Consequently, it is advised to pop a frame off of li before grabbing a fresh one
	in a client goroutine if it does not know how recently the last frame was requested.
*/
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
		// Signal to the streamer worker that it should grab the next frame. Meanwhile we will be broadcasting this frame
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
			/*
				This select statement will check if anyone is waiting on data to come in through `li`, and if so, it will
				send the data to that client. If no one is currently waiting, then the default case runs, and the for loop
				breaks. The purpose of this is so that we can have N goroutine clients concurrently receive the same frame
				allowing for concurrent access to the stream. (Otherwise, each client goroutine would receive unique
				frames, which could quickly slow down their streams)
			*/
			select {
			case li <- buf:
			default:
				break FOR
			}
		}

		/*
			If the number of clients we successfully broadcast the frame to was zero, this means no goroutine is currently
			waiting on a frame. Instead of grabbing more frames to process that no one wants, we will wait here for the
			next goroutine to request a frame. NOTE: this implementation has the side effect that the next frame will be
			stale if significant time passes before a goroutine requests a frame. Therefore, it is advised to perform a
			`<-li` in the client goroutine before requesting another frame to clear the stale one.
		*/
		if nn == 0 {
			li <- buf
		}

	}
}
