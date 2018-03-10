// Copyright 2017 Marc-Antoine Ruel. All rights reserved.
// Use of this source code is governed under the Apache License, Version 2.0
// that can be found in the LICENSE file.

package main

import (
	"encoding/base64"
	"html/template"
	"io"
	"log"
	"mime"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/kr/pretty"
	"github.com/maruel/serve-dir/loghttp"
	"github.com/maruel/serve-mp4/vid"
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
func startServer(bind string, c Catalog, t TranscodingQueue) (io.Closer, error) {
	ln, err := net.Listen("tcp", bind)
	if err != nil {
		return nil, err
	}
	s := &server{
		c: c,
		t: t,
		h: http.Server{Addr: ln.Addr().String()},
	}

	m := &http.ServeMux{}
	// Static
	m.HandleFunc("/cast.svg", serveStatic(casticon, "image/svg+xml"))
	m.HandleFunc("/chromeos.svg", serveStatic(chromeOSicon, "image/svg+xml"))
	m.HandleFunc("/favicon.ico", serveStatic(favicon, "image/x-icon"))
	m.HandleFunc("/spinner.gif", serveStatic(spinner, "image/gif"))
	m.HandleFunc("/vlc.svg", serveStatic(vlcicon, "image/svg+xml"))
	// Retrieval
	m.HandleFunc("/chromecast/", s.serveChromeCast)
	m.HandleFunc("/chromeos/", s.serveChromeOS)
	m.HandleFunc("/raw/", s.serveRaw)
	m.HandleFunc("/metadata/", s.serveMetadata)
	m.HandleFunc("/browse/", s.serveBrowse)
	m.HandleFunc("/", serveRoot)
	// Action
	m.HandleFunc("/transcode/chromecast/", s.transcodeChromeCast)
	m.HandleFunc("/transcode/chromeos/", s.transcodeChromeOS)

	s.h.Handler = &loghttp.Handler{Handler: m}
	go s.h.Serve(ln)
	log.Printf("Listening on %s", s.h.Addr)
	return s, nil
}

type server struct {
	c Catalog
	t TranscodingQueue
	h http.Server
}

func (s *server) Close() error {
	return s.h.Close()
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

func (s *server) serveBrowse(w http.ResponseWriter, req *http.Request) {
	if req.Method != "GET" {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	const prefix = "/browse/"
	rel := req.URL.Path[len(prefix):]
	d := s.c.LookupDir(rel)
	if d == nil {
		http.Error(w, "Not found", 404)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "private")
	data := struct {
		Title         string
		ShouldRefresh bool
		Directory     *Directory
		Rel           string
	}{
		Title:         "serve-mp4",
		ShouldRefresh: d.StillLoading(),
		Directory:     d,
		Rel:           rel,
	}
	if err := listing.Execute(w, data); err != nil {
		log.Printf("root template: %v", err)
	}
}

func (s *server) serveChromeOS(w http.ResponseWriter, req *http.Request) {
	s.serveTranscoded(w, req, "/chromeos/", vid.ChromeOS)
}

// serveChromeCast handles when the user intents to stream to a ChromeCast.
//
// In practice, we could make a full app? It's very geared towards Android/iOS.
// https://developers.google.com/cast/docs/caf_receiver_overview
//
// It's 5$ to get the ID: https://cast.google.com/publish/#/signup
func (s *server) serveChromeCast(w http.ResponseWriter, req *http.Request) {
	s.serveTranscoded(w, req, "/chromecast/", vid.ChromeCast)
}

func (s *server) serveTranscoded(w http.ResponseWriter, req *http.Request, prefix string, v vid.Device) {
	if req.Method != "GET" {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	rel := req.URL.Path[len(prefix):]
	// The challenge here is that we can't find the item based on the path.
	if filepath.Clean(rel) != rel {
		log.Printf("Invalid path %q", rel)
		http.Error(w, "Invalid path", 400)
	}
	serveFile(w, req, filepath.Join(s.c.CacheDir(), v.String(), rel))
}

func (s *server) serveRaw(w http.ResponseWriter, req *http.Request) {
	if req.Method != "GET" {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	const prefix = "/raw/"
	rel := req.URL.Path[len(prefix):]
	e := s.c.LookupEntry(rel)
	if e == nil {
		log.Printf("no item %s", rel)
		http.Error(w, "Not found", 404)
		return
	}
	serveFile(w, req, e.srcFile())
}

func (s *server) serveMetadata(w http.ResponseWriter, req *http.Request) {
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
	e := s.c.LookupEntry(rel)
	if e == nil {
		log.Printf("no item %s", rel)
		http.Error(w, "Not found", 404)
		return
	}
	v := e.Info()
	if v == nil {
		log.Printf("bad file %q", rel)
		http.Error(w, "Not found", http.StatusUnsupportedMediaType)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=86400") // 24*60*60
	pretty.Fprintf(w, "%# v\n", v)
}

// Action

func (s *server) transcodeChromeCast(w http.ResponseWriter, req *http.Request) {
	s.doTranscode(w, req, "/transcode/chromecast/", vid.ChromeCast)
}

func (s *server) transcodeChromeOS(w http.ResponseWriter, req *http.Request) {
	s.doTranscode(w, req, "/transcode/chromeos/", vid.ChromeOS)
}

func (s *server) doTranscode(w http.ResponseWriter, req *http.Request, prefix string, v vid.Device) {
	if req.Method != "POST" {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	rel := req.URL.Path[len(prefix):]
	e := s.c.LookupEntry(rel)
	if e == nil {
		log.Printf("no item %s", rel)
		http.Error(w, "Not found", 404)
		return
	}
	if e.IsCached(v) {
		log.Printf("no item %s", rel)
		http.Error(w, "Already transcoded", 400)
		return
	}
	if e.IsTranscoding() {
		log.Printf("still transcoding %s", rel)
		http.Error(w, "Already transcoding", 400)
		return
	}
	if v := e.Info(); v == nil {
		log.Printf("Failed to process %q", rel)
		http.Error(w, "Failed to process", 400)
		return
	}

	// At that point, always redirect to the referer.
	defer http.Redirect(w, req, req.Referer(), http.StatusFound)
	s.t.Transcode(v, e)
}

func serveFile(w http.ResponseWriter, req *http.Request, path string) {
	w.Header().Set("Cache-Control", "public, max-age=86400") // 24*60*60
	w.Header().Set("Content-Type", mime.TypeByExtension(filepath.Ext(path)))
	f, err := os.Open(path)
	if err != nil {
		http.Error(w, "broken", 404)
		log.Printf("%q: %v", path, err)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		http.Error(w, "broken", 404)
		log.Printf("%q: %v", path, err)
		return
	}
	http.ServeContent(w, req, filepath.Base(path), info.ModTime(), f)
	log.Printf("done serving %s", path)
}
