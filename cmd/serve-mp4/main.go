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

	"github.com/kr/pretty"
	"github.com/maruel/interrupt"
	"github.com/maruel/serve-dir/loghttp"
	"github.com/maruel/serve-mp4/vid"
	fsnotify "gopkg.in/fsnotify.v1"
)

var (
	listing  = template.Must(template.New("listing").Parse(listingRaw))
	favicon  []byte
	spinner  []byte
	validExt = []string{".avi", ".m4v", ".mkv", ".mp4", ".mpeg", ".mpg", ".mov", ".wmv"}

	mu sync.RWMutex
	// Shared between web server and file crawler:
	itemsMap      = map[string]*entry{}
	buckets       []*bucket
	queue         = make(chan *entry, 10240)
	updatingInfos = true
	// For file crawler only:
	lastUpdate  time.Time
	watchedDirs []string
)

// entry is a single video file found.
type entry struct {
	Rel    string // Resource name.
	Base   string // Display name without relpath.
	Actual string // Absolute path to cached file.
	Src    string // Absolute path to source file.
	lang   string // cache of prefered language.

	// Mutable
	Info        *vid.Info
	Cached      bool
	Transcoding bool
	cold        bool
}

// getInfo lazy loads e.Info.
func (e *entry) getInfo() (*vid.Info, error) {
	mu.RLock()
	v := e.Info
	mu.RUnlock()
	if v != nil {
		return v, nil
	}
	v, err := vid.Identify(e.Src, e.lang)
	if err != nil {
		// TODO(maruel): Signal to not repeatedly analyze the file.
		return nil, err
	}
	mu.Lock()
	e.Info = v
	mu.Unlock()
	return v, nil
}

type bucket struct {
	Dir   string
	Items []*entry
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

// shouldRefresh returns if the file list should auto-refresh.
//
// Must be called with mu.RLock().
func shouldRefresh() bool {
	if updatingInfos {
		// Still loading metadata.
		return true
	}
	for _, b := range buckets {
		for _, v := range b.Items {
			if v.Transcoding {
				// Refresh the page every few seconds until there's no transcoding
				// happening.
				return true
			}
		}
	}
	return false
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
		Buckets       []*bucket
	}{
		Title:   "serve-mp4",
		Buckets: buckets,
	}
	mu.RLock()
	defer mu.RUnlock()
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

	if _, err := e.getInfo(); err != nil {
		// TODO(maruel): Return a failure.
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
	mu.RLock()
	e := itemsMap[rel]
	mu.RUnlock()
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
	return nil
}

//

func isValidExt(ext string) bool {
	for _, i := range validExt {
		if ext == i {
			return true
		}
	}
	return false
}

// preloadInfos preloads all Info for all entry.
func preloadInfos(stamp time.Time) {
	i := 0
	j := -1
	for {
		mu.RLock()
		if stamp != lastUpdate {
			mu.RUnlock()
			log.Printf("A new refresh happened; stopping pre-processing early")
			return
		}
		for i < len(buckets) {
			j++
			if j < len(buckets[i].Items) {
				break
			}
			j = -1
			i++
		}
		if i == len(buckets) {
			mu.RUnlock()
			break
		}
		e := buckets[i].Items[j]
		mu.RUnlock()

		if _, err := e.getInfo(); err != nil {
			log.Printf("%v", err)
		}
	}
	mu.Lock()
	updatingInfos = false
	mu.Unlock()
	log.Printf("Done pre-processing")
}

// handleFile is called from os.Walk(root) from enumerateEntries.
func handleFile(prefix int, cache, lang, path string, info os.FileInfo, err error) error {
	if err != nil || len(path) <= prefix {
		return err
	}
	src := path[prefix:]
	if src[0] == '.' {
		return filepath.SkipDir
	}
	ext := filepath.Ext(src)
	if info.IsDir() || !isValidExt(ext) {
		return nil
	}
	display := src[:len(src)-len(ext)]
	rel := strings.Replace(display+".mp4", string(filepath.Separator), "/", -1)
	if e, ok := itemsMap[rel]; ok {
		e.cold = false
		return nil
	}

	e := &entry{
		Rel:    rel,
		Base:   filepath.Base(display),
		Actual: filepath.Join(cache, display+".mp4"),
		Src:    path,
		lang:   lang,
	}
	// For now force transcoding so -movflags +faststart is guaranteed.
	// TODO(maruel): In practice we'd want to identify if if already with
	// faststart, do not copy.
	if i, err := os.Stat(e.Actual); err == nil && i.Size() > 0 {
		e.Cached = true
	}
	itemsMap[e.Rel] = e
	return nil
}

// enumerateEntries enumerates or reenumerates the tree.
//
// Calls preloadInfos() as a separate asynchronous goroutine.
func enumerateEntries(watcher *fsnotify.Watcher, root, cache string, lang string) error {
	// Keep a writer lock for the duration of the enumeration.
	mu.Lock()
	defer mu.Unlock()
	updatingInfos = true
	prefix := len(root) + 1
	for _, e := range itemsMap {
		e.cold = true
	}
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		return handleFile(prefix, cache, lang, path, info, err)
	})

	newBuckets := map[string][]*entry{}
	for name, e := range itemsMap {
		if e.cold {
			// File was deleted.
			delete(itemsMap, name)
		}
		newBuckets[filepath.Dir(e.Rel)] = append(newBuckets[filepath.Dir(e.Rel)], e)
	}
	buckets = nil
	// Split into buckets.
	dirs := map[string]bool{}
	for _, d := range watchedDirs {
		dirs[d] = false
	}
	for name, items := range newBuckets {
		if name != "" {
			name += "/"
		}
		dirs[filepath.Dir(items[0].Src)] = true
		buckets = append(buckets, &bucket{Dir: name, Items: items})
		sort.Slice(items, func(i, j int) bool {
			return items[i].Rel < items[j].Rel
		})
	}
	sort.Slice(buckets, func(i, j int) bool {
		return buckets[i].Dir < buckets[j].Dir
	})
	log.Printf("Found %d files", len(itemsMap))

	// Compare dirs with watchedDirs. Removes deleted directory, watch new ones.
	// This is done with the mu lock.
	watchedDirs = nil
	for d, w := range dirs {
		if w {
			if err = watcher.Add(d); err != nil {
				return err
			}
			log.Printf("Watching %s", d)
			watchedDirs = append(watchedDirs, d)
		} else {
			if err = watcher.Remove(d); err != nil {
				return err
			}
			log.Printf("Unwatching %s", d)
		}
	}

	lastUpdate = time.Now()
	if err != nil {
		updatingInfos = false
	} else {
		go preloadInfos(lastUpdate)
	}
	return err
}

// handleRefresh handles the events from refresh that are triggered via
// fsnotify.Watcher.
func handleRefresh(refresh <-chan bool, watcher *fsnotify.Watcher, root, cache, lang string) {
	for {
		<-refresh
		log.Printf("Will refresh in 10s")
		delay := time.After(10 * time.Second)
		for {
			select {
			case <-refresh:
			case <-delay:
				break
			}
		}
		if err := enumerateEntries(watcher, root, cache, lang); err != nil {
			// TODO(maruel): dirs.
			log.Printf("failed to refresh files")
		}
	}
}

// setupFiles do the first enumeration and starts a routine to update file
// metadata.
func setupFiles(watcher *fsnotify.Watcher, root, cache, lang string) (chan<- bool, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if cache == "" {
		cache = filepath.Join(root, ".cache")
	}
	if i, err := os.Stat(cache); err != nil || !i.IsDir() {
		if err := os.Mkdir(cache, 0777); err != nil {
			return nil, err
		}
	}

	if err := enumerateEntries(watcher, root, cache, lang); err != nil {
		return nil, err
	}

	refresh := make(chan bool, 1000)
	go handleRefresh(refresh, watcher, root, cache, lang)
	return refresh, nil
}

//

type transcodingQueue struct {
	mu sync.Mutex
}

func (t *transcodingQueue) run() {
	for e := range queue {
		if e == nil {
			break
		}
		t.mu.Lock()
		err := vid.Transcode(e.Src, e.Actual, e.Info, "")
		t.mu.Unlock()

		mu.Lock()
		e.Transcoding = false
		e.Cached = err == nil
		mu.Unlock()
	}
}

func (t *transcodingQueue) stop() {
	// Flush the pending items in the transcoding queue, wait for the current
	// transcoding to complete, return.
	log.Printf("shutting down")
	for stop := false; !stop; {
		select {
		case <-queue:
		default:
			queue <- nil
			stop = true
			break
		}
	}
	t.mu.Lock()
	t.mu.Unlock()
}

//

func watchFiles(watcher *fsnotify.Watcher, refresh chan<- bool) error {
	exePath, err := os.Executable()
	if err != nil {
		return err
	}
	fi, err := os.Stat(exePath)
	if err != nil {
		return err
	}
	mod0 := fi.ModTime()
	if err = watcher.Add(exePath); err != nil {
		return err
	}
	for {
		select {
		case <-interrupt.Channel:
			return err
		case err = <-watcher.Errors:
			return err
		case e := <-watcher.Events:
			log.Printf("fsnotify: %s %s", e.Name, e.Op)
			if e.Name == exePath {
				if fi, err = os.Stat(exePath); err == nil && !fi.ModTime().Equal(mod0) {
					// Time to restart.
					return nil
				}
				continue
			}
			refresh <- true
		}
	}
}

//

func mainImpl() error {
	bind := flag.String("http", ":8010", "port and host to bind to")
	rootDir := flag.String("root", getWd(), "root directory")
	cacheDir := flag.String("cache", "", "cache directory, defaults to <root>/.cache")
	lang := flag.String("lang", "fre", "preferred language")
	log.SetFlags(log.Lmicroseconds)
	flag.Parse()
	if flag.NArg() != 0 {
		return errors.New("unexpected argument")
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer watcher.Close()

	refresh, err := setupFiles(watcher, *rootDir, *cacheDir, *lang)
	if err != nil {
		return err
	}

	var t transcodingQueue
	go t.run()
	defer t.stop()

	if err = startServer(*bind); err != nil {
		return err
	}

	interrupt.HandleCtrlC()
	return watchFiles(watcher, refresh)
}

func main() {
	if err := mainImpl(); err != nil {
		fmt.Fprintf(os.Stderr, "serve-mp4: %s\n", err)
		os.Exit(1)
	}
}
