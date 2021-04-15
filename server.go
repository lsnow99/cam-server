package main

import (
	"bytes"
	"context"
	"log"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"path"
	"strconv"
	"sync"
	"text/template"
	"time"
)

type FrameClient struct {
	li chan *bytes.Buffer
}

type route struct {
	Path        string
	Description string
	handler     http.HandlerFunc
}

type RouteTable struct {
	Routes []route
}

type IndexInfo struct {
	RouteTable
	Version string
}

// HandleRoot simple http handler to render our index.html sitemap
func (rt *RouteTable) HandleRoot(w http.ResponseWriter, r *http.Request) {

	ii := IndexInfo{}
	ii.Routes = rt.Routes
	ii.Version = Version

	t, err := template.ParseFiles("./templates/index.html")
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		log.Println(err)
		return
	}

	if err := t.Execute(w, ii); err != nil {
		log.Println(err)
	}
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

// ServeHttp start the http server
func ServeHttp(ctx context.Context, errCh chan error, wg *sync.WaitGroup, port string, li chan *bytes.Buffer) {
	// This is guaranteed to run as the last thing before this function returns
	defer wg.Done()

	log.Printf("Starting http server on port %s", port)

	fc := FrameClient{
		li: li,
	}

	/*
		Register some routes with our global routes table. This is just a neat way to
		update our index.html without modifying the template, so it generates the
		sitemap at runtime. I added this because I wanted a way for index.html to
		automatically add new routes to the sitemap without me having to edit anything.
		Structure is subject to change.
	*/
	rt := RouteTable{
		Routes: []route{
			{
				Path:        "/snap",
				Description: "view a still image of the camera",
				handler:     fc.HandleSnapshot,
			},
			{
				Path:        "/stream",
				Description: "view a livestream of the camera",
				handler:     fc.HandleStream,
			},
			{
				Path:        "/timelapse",
				Description: "view a timelapse video",
				handler: func(w http.ResponseWriter, r *http.Request) {
					http.ServeFile(w, r, path.Join(TimelapseOutputDir, "timelapse.mp4"))
				},
			},
		},
	}

	/*
		Here we create a new http mux. You can alternatively just use `http.HandleFunc`
		instead of `mux.HandleFunc` but then all http servers started by this program
		will use that handling rule. Instead of using the global http mux, I like to
		make my own for every http server in the program even though it doesn't really
		matter when there is only one http server, as in this case.
	*/
	mux := http.NewServeMux()
	mux.HandleFunc("/", rt.HandleRoot)
	for _, route := range rt.Routes {
		mux.HandleFunc(route.Path, route.handler)
	}

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
