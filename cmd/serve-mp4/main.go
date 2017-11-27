// Copyright 2017 Marc-Antoine Ruel. All rights reserved.
// Use of this source code is governed under the Apache License, Version 2.0
// that can be found in the LICENSE file.

// serve-mp4 serves a directory of video files over HTTP and transcodes on the
// fly.
package main

import (
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"html/template"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/maruel/interrupt"
	"github.com/maruel/serve-dir/loghttp"
	"github.com/maruel/serve-mp4/vid"
)

var (
	listing  = template.Must(template.New("listing").Parse(listingRaw))
	favicon  []byte
	spinner  []byte
	validExt = []string{".avi", ".m4v", ".mkv", ".mp4", ".mpeg", ".mpg", ".mov", ".wmv"}

	mu         sync.RWMutex
	itemsMap   = map[string]*entry{}
	items      = []*entry{}
	queue      = make(chan *entry, 10240)
	processing = true
)

type entry struct {
	Display string // Display name.
	Actual  string // Absolute path to cached file.
	Src     string // Absolute path to source file.
	lang    string

	// Mutable
	Info        *vid.Info
	Cached      bool
	Transcoding bool
}

func init() {
	var err error
	if favicon, err = base64.StdEncoding.DecodeString(faviconRaw); err != nil {
		panic(err)
	}
	if spinner, err = base64.StdEncoding.DecodeString(spinnerRaw); err != nil {
		panic(err)
	}
}

func getWd() string {
	wd, _ := os.Getwd()
	return wd
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
	data := struct {
		Title         string
		ShouldRefresh bool
		Files         []*entry
	}{
		Title: "serve-mp4",
		Files: items,
	}
	mu.RLock()
	defer mu.RUnlock()
	if processing {
		// Still loading metadata.
		data.ShouldRefresh = true
	} else {
		for _, v := range items {
			if v.Transcoding {
				// Refresh the page every few seconds until there's no transcoding
				// happening.
				data.ShouldRefresh = true
				break
			}
		}
	}
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
	root := "/"

	mu.RLock()
	e := itemsMap[rel]
	need := false
	if e != nil {
		need = !e.Cached && !e.Transcoding
	}
	mu.RUnlock()

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

	v, err := vid.Identify(e.Src, e.lang)
	if err != nil {
		log.Printf("%v", err)
		return
	}

	mu.Lock()
	if e.Transcoding == true || e.Cached == true {
		// Oops.
		mu.Unlock()
		return
	}
	e.Transcoding = true
	e.Info = v
	mu.Unlock()

	queue <- e
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
	mu.RLock()
	e := itemsMap[rel]
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
		mu.RUnlock()
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
	mu.RUnlock()
}

func enumerate(root, cache string, lang string) error {
	mu.Lock()
	defer mu.Unlock()
	l := len(root) + 1
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if len(path) <= l {
			return nil
		}
		src := path[l:]
		if src[0] == '.' {
			return filepath.SkipDir
		}
		if info.IsDir() {
			return nil
		}
		ext := filepath.Ext(src)
		found := false
		for _, i := range validExt {
			if ext == i {
				found = true
				break
			}
		}
		if !found {
			return nil
		}
		display := src[:len(src)-len(ext)]
		rel := display + ".mp4"
		e := &entry{Display: display, Actual: filepath.Join(cache, rel), Src: path, lang: lang}
		// For now force transcoding so -movflags +faststart is guaranteed.
		//if rel == src {
		if i, err := os.Stat(e.Actual); err == nil && i.Size() > 0 {
			e.Cached = true
		}
		items = append(items, e)
		itemsMap[rel] = e
		return nil
	})
	sort.Slice(items, func(i, j int) bool {
		return items[i].Display < items[j].Display
	})
	return err
}

func mainImpl() error {
	bind := flag.String("http", ":8010", "port and host to bind to")
	rootDir := flag.String("root", getWd(), "root directory")
	cacheDir := flag.String("cache", "", "cache directory, defaults to <root>/.cache")
	lang := flag.String("lang", "fre", "preferred language")
	timeout := flag.Int("timeout", 24*60*60, "write timeout in seconds; default 24h")
	maxSize := flag.Int("max_size", 256*1024*1024*1024, "max transfer size; default 256gb")
	log.SetFlags(log.Lmicroseconds)
	flag.Parse()
	if flag.NArg() != 0 {
		return errors.New("unexpected argument")
	}

	// Web server.
	ln, err := net.Listen("tcp", *bind)
	if err != nil {
		return err
	}
	m := &http.ServeMux{}
	m.HandleFunc("/favicon.ico", serveFavicon)
	m.HandleFunc("/spinner.gif", serveSpinner)
	m.HandleFunc("/transcode", serveTranscode)
	m.HandleFunc("/get/", serveMP4)
	m.HandleFunc("/", serveRoot)
	s := &http.Server{
		Addr:           ln.Addr().String(),
		Handler:        &loghttp.Handler{Handler: m},
		ReadTimeout:    10. * time.Second,
		WriteTimeout:   time.Duration(*timeout) * time.Second,
		MaxHeaderBytes: *maxSize,
	}

	// Files.
	root, err := filepath.Abs(*rootDir)
	if err != nil {
		return err
	}
	if *cacheDir == "" {
		*cacheDir = filepath.Join(root, ".cache")
	}
	if i, err := os.Stat(*cacheDir); err != nil || !i.IsDir() {
		if err := os.Mkdir(*cacheDir, 0777); err != nil {
			return err
		}
	}
	if err = enumerate(root, *cacheDir, *lang); err != nil {
		return err
	}
	log.Printf("Found %d files", len(items))

	// Preload Info for all files.
	go func() {
		for _, e := range items {
			v, err := vid.Identify(e.Src, *lang)
			if err != nil {
				log.Printf("%v", err)
				continue
			}
			mu.Lock()
			e.Info = v
			mu.Unlock()
		}
		mu.Lock()
		processing = false
		mu.Unlock()
		log.Printf("Done pre-processing")
	}()

	var muTranscode sync.Mutex
	go func() {
		for e := range queue {
			if e == nil {
				break
			}
			muTranscode.Lock()
			err := vid.Transcode(e.Src, e.Actual, e.Info, "")
			muTranscode.Unlock()

			mu.Lock()
			e.Transcoding = false
			e.Cached = err == nil
			mu.Unlock()
		}
	}()
	go s.Serve(ln)
	log.Printf("Listening on %s", s.Addr)

	interrupt.HandleCtrlC()
	err = WatchFile()
	log.Printf("shutting down")
	stop := false
	for !stop {
		select {
		case <-queue:
		default:
			queue <- nil
			stop = true
			break
		}
	}
	muTranscode.Lock()
	muTranscode.Unlock()
	return err
}

func main() {
	if err := mainImpl(); err != nil {
		fmt.Fprintf(os.Stderr, "serve-mp4: %s\n", err)
		os.Exit(1)
	}
}
