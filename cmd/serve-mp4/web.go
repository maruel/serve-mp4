// Copyright 2017 Marc-Antoine Ruel. All rights reserved.
// Use of this source code is governed under the Apache License, Version 2.0
// that can be found in the LICENSE file.

package main

import (
	"encoding/base64"
	"html/template"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kr/pretty"
	"github.com/maruel/serve-dir/loghttp"
)

var (
	listing = template.Must(template.New("listing").Parse(listingRaw))
	favicon []byte
	spinner []byte
)

func init() {
	var err error
	if favicon, err = base64.StdEncoding.DecodeString(faviconRaw); err != nil {
		panic(err)
	}
	if spinner, err = base64.StdEncoding.DecodeString(spinnerRaw); err != nil {
		panic(err)
	}
}

// startServer starts the web server.
func startServer(bind string) error {
	ln, err := net.Listen("tcp", bind)
	if err != nil {
		return err
	}
	m := &http.ServeMux{}
	m.HandleFunc("/favicon.ico", serveFavicon)
	m.HandleFunc("/spinner.gif", serveSpinner)
	m.HandleFunc("/transcode", serveTranscode)
	m.HandleFunc("/get/", serveMP4)
	m.HandleFunc("/metadata/", serveMetadata)
	m.HandleFunc("/", serveRoot)
	s := &http.Server{
		Addr:           ln.Addr().String(),
		Handler:        &loghttp.Handler{Handler: m},
		ReadTimeout:    10. * time.Second,
		WriteTimeout:   24 * 60 * 60 * time.Second,
		MaxHeaderBytes: 256 * 1024 * 1024 * 1024,
	}
	go s.Serve(ln)
	log.Printf("Listening on %s", s.Addr)
	// TODO(maruel): Return io.Closer?
	return nil
}

func serveStatic(w http.ResponseWriter, req *http.Request, b []byte, t string) {
	if req.Method != "GET" {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", t)
	w.Header().Set("Cache-Control", "public, max-age=86400") // 24*60*60
	w.Write(b)
}

func serveFavicon(w http.ResponseWriter, req *http.Request) {
	serveStatic(w, req, favicon, "image/x-icon")
}

func serveSpinner(w http.ResponseWriter, req *http.Request) {
	serveStatic(w, req, spinner, "image/gif")
}

func serveRoot(w http.ResponseWriter, req *http.Request) {
	if req.Method != "GET" {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "private")
	cat.mu.RLock()
	defer cat.mu.RUnlock()
	data := struct {
		Title         string
		ShouldRefresh bool
		Buckets       []*bucket
	}{
		Title:   "serve-mp4",
		Buckets: cat.buckets,
	}
	data.ShouldRefresh = shouldRefresh()
	if err := listing.Execute(w, data); err != nil {
		log.Printf("root template: %v", err)
	}
}

func serveTranscode(w http.ResponseWriter, req *http.Request) {
	if req.Method != "POST" {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	rel := req.FormValue("file")
	root := req.Referer()
	if root == "" {
		http.Error(w, "Need a referred", http.StatusBadRequest)
		return
	}

	cat.mu.RLock()
	e := cat.itemsMap[rel]
	need := false
	if e != nil {
		need = !e.Cached && !e.Transcoding
	}
	cat.mu.RUnlock()

	if e == nil {
		log.Printf("no item %s", rel)
		http.Error(w, "Not found", 404)
		return
	}

	// At that point, always redirect to root.
	defer http.Redirect(w, req, root, http.StatusFound)
	if !need {
		return
	}

	if _, err := e.getInfo(); err != nil {
		// TODO(maruel): Return a failure.
		log.Printf("%v", err)
		return
	}

	cat.mu.Lock()
	if e.Transcoding == true || e.Cached == true {
		// Oops.
		cat.mu.Unlock()
		return
	}
	e.Transcoding = true
	cat.mu.Unlock()

	cat.queue <- e
}

func serveMP4(w http.ResponseWriter, req *http.Request) {
	if req.Method != "GET" {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	if !strings.HasPrefix(req.URL.Path, "/get/") {
		log.Printf("root")
		http.Error(w, "Not found", 404)
		return
	}
	rel := req.URL.Path[len("/get/"):]
	cat.mu.RLock()
	e := cat.itemsMap[rel]
	if e == nil {
		log.Printf("no item %s", rel)
		http.Error(w, "Not found", 404)
	} else if !e.Cached {
		log.Printf("not cached %s", rel)
		http.Error(w, "not cached", 404)
	} else if e.Transcoding {
		log.Printf("still transcoding %s", rel)
		http.Error(w, "still transcoding", 404)
	} else {
		cat.mu.RUnlock()
		w.Header().Set("Content-Type", "video/mp4")
		w.Header().Set("Cache-Control", "public, max-age=86400") // 24*60*60
		f, err := os.Open(e.Actual)
		if err != nil {
			http.Error(w, "broken", 404)
			log.Printf("%v", err)
			return
		}
		defer f.Close()
		info, err := f.Stat()
		if err != nil {
			http.Error(w, "broken", 404)
			log.Printf("%v", err)
			return
		}
		http.ServeContent(w, req, filepath.Base(e.Actual), info.ModTime(), f)
		log.Printf("done serving %s", e.Actual)
		return
	}
	cat.mu.RUnlock()
}

func serveMetadata(w http.ResponseWriter, req *http.Request) {
	if req.Method != "GET" {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	if !strings.HasPrefix(req.URL.Path, "/metadata/") {
		log.Printf("root")
		http.Error(w, "Not found", 404)
		return
	}
	rel := req.URL.Path[len("/metadata/"):]
	cat.mu.RLock()
	e := cat.itemsMap[rel]
	cat.mu.RUnlock()
	if e == nil {
		log.Printf("no item %s", rel)
		http.Error(w, "Not found", 404)
		return
	}
	v, err := e.getInfo()
	if err != nil {
		log.Printf("bad file %v", err)
		http.Error(w, "Not found", http.StatusUnsupportedMediaType)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=86400") // 24*60*60
	pretty.Fprintf(w, "%# v\n", v)
}
