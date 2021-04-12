package main

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"os/exec"
	"strconv"
	"sync"
	"time"
)

type FrameClient struct {
	li chan *bytes.Buffer
}

// HandleRoot simple http handler to serve a welcome message
func HandleRoot(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w,
		`<!DOCTYPE html>
	<html>
	<head>
		<h1>Welcome to cam server %s!</h1>
	</head>
	<body>
		<p>Check out the following links!</p>
		<ol>
			<li><a href="/snap">/snap</a> - view a still image of the camera</li>
			<li><a href="/stream">/stream</a> - view a livestream of the camera</li>
			<li><a href="/timelapse">/timelapse</a> - download a timelapse video</li>
		</ol>
	</body>
	</html>
	`, Version)
}

// HandleStream returns a stream of jpeg frames
func (fc *FrameClient) HandleStream(w http.ResponseWriter, r *http.Request) {
	// Remove stale image
	<-fc.li
	const boundary = `frame`
	w.Header().Set("Content-Type", `multipart/x-mixed-replace;boundary=`+boundary)
	multipartWriter := multipart.NewWriter(w)
	multipartWriter.SetBoundary(boundary)
	for {
		img := <-fc.li
		image := img.Bytes()
		iw, err := multipartWriter.CreatePart(textproto.MIMEHeader{
			"Content-type":   []string{"image/jpeg"},
			"Content-length": []string{strconv.Itoa(len(image))},
		})
		if err != nil {
			log.Println(err)
			return
		}
		_, err = iw.Write(image)
		if err != nil {
			log.Println(err)
			return
		}
	}
}

// HandleSnapshot returns a single jpeg frame
func (fc *FrameClient) HandleSnapshot(w http.ResponseWriter, r *http.Request) {
	// Remove stale image
	<-fc.li

	img := <-fc.li

	w.WriteHeader(http.StatusOK)
	w.Header().Set("Content-Type", "image/jpeg")

	if _, err := w.Write(img.Bytes()); err != nil {
		log.Println(err)
		return
	}
}

func HandleTimelapse(w http.ResponseWriter, r *http.Request) {
	tod := TimelapseOutputDir
	if len(tod) < 0 {
		w.WriteHeader(http.StatusInternalServerError)
		return
	} else if tod[len(tod)-1] == '/' {
		tod = tod[:len(tod)-1]
	}
	filename := strconv.FormatInt(time.Now().Unix(), 10) + ".mkv"
	cmd := exec.Command("sh", "-c", `ffmpeg -framerate 30 -pattern_type glob -i "`+tod+`/*.jpg" -codec copy `+tod+"/"+filename)

	_, err := cmd.CombinedOutput()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	http.ServeFile(w, r, tod+"/"+filename)
}

func ServeHttp(ctx context.Context, errCh chan error, wg *sync.WaitGroup, port string, li chan *bytes.Buffer) {
	// This is guaranteed to run as the last thing before this function returns
	defer wg.Done()

	log.Printf("Starting http server on port %s", port)

	fc := FrameClient{
		li: li,
	}

	/*
		Here we create a new http mux. You can alternatively just use `http.HandleFunc`
		instead of `mux.HandleFunc` but then all http servers started by this program
		will use that handling rule. Instead of using the global http mux, I like to
		make my own for every http server in the program even though it doesn't really
		matter when there is only one http server, as in this case.
	*/
	mux := http.NewServeMux()
	mux.HandleFunc("/", HandleRoot)
	mux.HandleFunc("/stream", fc.HandleStream)
	mux.HandleFunc("/snap", fc.HandleSnapshot)
	mux.HandleFunc("/timelapse", HandleTimelapse)
	server := &http.Server{
		Addr:        ":" + port,
		Handler:     mux,
		ReadTimeout: ReadTimeout,
	}

	// Receives a signal from the done channel to close the server
	go func() {
		<-ctx.Done()
		log.Println("Gracefully shutting down http server")
		// We give the server a few seconds to allow requests to finish and shutdown
		ctx, cancel := context.WithTimeout(context.TODO(), time.Duration(GracefulTimeoutSecs-1))
		server.Shutdown(ctx)
		cancel()
	}()

	/*
		`server.ListenAndServe()` blocks until an error, then returns it. We report
		any errors to the main synchronization goroutine via the `errCh` channel.
		Note that nil errors may return and be sent over the `errCh` channel as
		well as ErrServerClosed which occurs as as result of the above `Shutdown()`
		call. This isn't an issue, however, since `errCh` is buffered and therefore
		this line will never hang. Also, we always want the program to begin its
		shutdown procedure if it hasn't already which is why it makes sense to just
		always send it because it is harmless.
	*/
	errCh <- server.ListenAndServe()

	log.Println("Stopped http server")
}
