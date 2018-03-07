// Copyright 2017 Marc-Antoine Ruel. All rights reserved.
// Use of this source code is governed under the Apache License, Version 2.0
// that can be found in the LICENSE file.

package main

import (
	"encoding/base64"
	"html/template"
	"log"
	"mime"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/kr/pretty"
	"github.com/maruel/serve-dir/loghttp"
)

var (
	listing      = template.Must(template.New("listing").Parse(listingRaw))
	favicon      []byte
	spinner      []byte
	casticon     []byte
	chromeOSicon []byte
	vlcicon      []byte
)

func init() {
	var err error
	if favicon, err = base64.StdEncoding.DecodeString(faviconRaw); err != nil {
		panic(err)
	}
	if spinner, err = base64.StdEncoding.DecodeString(spinnerRaw); err != nil {
		panic(err)
	}
	casticon = []byte(castIcon)
	chromeOSicon = []byte(chromeOSIcon)
	vlcicon = []byte(vlcIcon)
}

// startServer starts the web server.
func startServer(bind string) error {
	ln, err := net.Listen("tcp", bind)
	if err != nil {
		return err
	}
	m := &http.ServeMux{}
	// Static
	m.HandleFunc("/cast.svg", serveStatic(casticon, "image/svg+xml"))
	m.HandleFunc("/chromeos.svg", serveStatic(chromeOSicon, "image/svg+xml"))
	m.HandleFunc("/favicon.ico", serveStatic(favicon, "image/x-icon"))
	m.HandleFunc("/spinner.gif", serveStatic(spinner, "image/gif"))
	m.HandleFunc("/vlc.svg", serveStatic(vlcicon, "image/svg+xml"))
	// Retrieval
	m.HandleFunc("/chromecast/", serveChromeCast)
	m.HandleFunc("/chromeos/", serveChromeOS)
	m.HandleFunc("/raw/", serveRaw)
	m.HandleFunc("/metadata/", serveMetadata)
	m.HandleFunc("/browse/", serveBrowse)
	m.HandleFunc("/", serveRoot)
	// Action
	m.HandleFunc("/transcode", serveTranscode)
	s := &http.Server{
		Addr:    ln.Addr().String(),
		Handler: &loghttp.Handler{Handler: m},
	}
	go s.Serve(ln)
	log.Printf("Listening on %s", s.Addr)
	// TODO(maruel): Return io.Closer?
	return nil
}

// Static

func serveStatic(b []byte, t string) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		if req.Method != "GET" {
			http.Error(w, "GET only", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", t)
		w.Header().Set("Cache-Control", "public, max-age=86400") // 24*60*60
		w.Write(b)
	}
}

// Retrieval

func serveBrowse(w http.ResponseWriter, req *http.Request) {
	if req.Method != "GET" {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	//const prefix = "/browse/"
	//p :=  req.URL.Path[len(prefix):]
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

func serveRoot(w http.ResponseWriter, req *http.Request) {
	if req.Method != "GET" {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	if req.URL.Path != "/" {
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}
	// For now just redirect to the browser.
	http.Redirect(w, req, "browse/", http.StatusFound)
}

func serveChromeOS(w http.ResponseWriter, req *http.Request) {
	if req.Method != "GET" {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	const prefix = "/chromecast/"
	rel := req.URL.Path[len(prefix):]
	cat.mu.RLock()
	e := cat.itemsMap[rel]
	if e == nil {
		log.Printf("no item %s", rel)
		http.Error(w, "Not found", 404)
	} else if !e.Cached {
		cat.mu.RUnlock()
		doTranscode(w, req, rel)
		return
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

// serveChromeCast handles when the user intents to stream to a ChromeCast.
//
// In practice, we could make a full app? It's very geared towards Android/iOS.
// https://developers.google.com/cast/docs/caf_receiver_overview
//
// It's 5$ to get the ID: https://cast.google.com/publish/#/signup
func serveChromeCast(w http.ResponseWriter, req *http.Request) {
	if req.Method != "GET" {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	const prefix = "/chromecast/"
	rel := req.URL.Path[len(prefix):]
	cat.mu.RLock()
	e := cat.itemsMap[rel]
	if e == nil {
		log.Printf("no item %s", rel)
		http.Error(w, "Not found", 404)
	} else if !e.Cached {
		cat.mu.RUnlock()
		doTranscode(w, req, rel)
		return
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

func serveRaw(w http.ResponseWriter, req *http.Request) {
	if req.Method != "GET" {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	const prefix = "/raw/"
	rel := req.URL.Path[len(prefix):]
	cat.mu.RLock()
	e := cat.itemsMap[rel]
	cat.mu.RUnlock()
	w.Header().Set("Content-Type", mime.TypeByExtension(filepath.Ext(rel)))
	w.Header().Set("Cache-Control", "public, max-age=86400") // 24*60*60
	f, err := os.Open(e.Src)
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
	http.ServeContent(w, req, filepath.Base(rel), info.ModTime(), f)
	log.Printf("done serving %s", rel)
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

// Action

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
	doTranscode(w, req, rel)
}

func doTranscode(w http.ResponseWriter, req *http.Request, rel string) {
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

	// At that point, always redirect to the referer.
	defer http.Redirect(w, req, req.Referer(), http.StatusFound)
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
