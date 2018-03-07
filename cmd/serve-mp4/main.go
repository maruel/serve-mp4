// Copyright 2017 Marc-Antoine Ruel. All rights reserved.
// Use of this source code is governed under the Apache License, Version 2.0
// that can be found in the LICENSE file.

// serve-mp4 serves a directory of video files over HTTP and transcodes on the
// fly.
package main

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/maruel/interrupt"
	"github.com/maruel/serve-mp4/vid"
	fsnotify "gopkg.in/fsnotify.v1"
)

var validExt = []string{".avi", ".m4v", ".mkv", ".mp4", ".mpeg", ".mpg", ".mov", ".wmv"}

// shouldRefresh returns if the file list should auto-refresh.
//
// Must be called with mu.RLock().
func shouldRefresh() bool {
	if cat.updatingInfos {
		// Still loading metadata.
		return true
	}
	for _, b := range cat.buckets {
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
		cat.mu.RLock()
		if stamp != cat.lastUpdate {
			cat.mu.RUnlock()
			log.Printf("A new refresh happened; stopping pre-processing early")
			return
		}
		for i < len(cat.buckets) {
			j++
			if j < len(cat.buckets[i].Items) {
				break
			}
			j = -1
			i++
		}
		if i == len(cat.buckets) {
			cat.mu.RUnlock()
			break
		}
		e := cat.buckets[i].Items[j]
		cat.mu.RUnlock()

		if _, err := e.getInfo(); err != nil {
			log.Printf("%v", err)
		}
	}
	cat.mu.Lock()
	cat.updatingInfos = false
	cat.mu.Unlock()
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
	if e, ok := cat.itemsMap[rel]; ok {
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
	cat.itemsMap[e.Rel] = e
	return nil
}

// enumerateEntries enumerates or reenumerates the tree.
//
// Calls preloadInfos() as a separate asynchronous goroutine.
func enumerateEntries(watcher *fsnotify.Watcher, root, cache string, lang string) error {
	// Keep a writer lock for the duration of the enumeration.
	cat.mu.Lock()
	defer cat.mu.Unlock()
	cat.updatingInfos = true
	prefix := len(root) + 1
	for _, e := range cat.itemsMap {
		e.cold = true
	}
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		return handleFile(prefix, cache, lang, path, info, err)
	})

	newBuckets := map[string][]*entry{}
	for name, e := range cat.itemsMap {
		if e.cold {
			// File was deleted.
			delete(cat.itemsMap, name)
		}
		newBuckets[filepath.Dir(e.Rel)] = append(newBuckets[filepath.Dir(e.Rel)], e)
	}
	cat.buckets = nil
	// Split into buckets.
	dirs := map[string]bool{}
	for _, d := range cat.watchedDirs {
		dirs[d] = false
	}
	for name, items := range newBuckets {
		if name != "" {
			name += "/"
		}
		dirs[filepath.Dir(items[0].Src)] = true
		cat.buckets = append(cat.buckets, &bucket{Dir: name, Items: items})
		sort.Slice(items, func(i, j int) bool {
			return items[i].Rel < items[j].Rel
		})
	}
	sort.Slice(cat.buckets, func(i, j int) bool {
		return cat.buckets[i].Dir < cat.buckets[j].Dir
	})
	log.Printf("Found %d files", len(cat.itemsMap))

	// TODO(maruel): Populate subdirs

	// Compare dirs with cat.watchedDirs. Removes deleted directory, watch new
	// ones.  This is done with the mu lock.
	cat.watchedDirs = nil
	for d, w := range dirs {
		if w {
			if err = watcher.Add(d); err != nil {
				return err
			}
			cat.watchedDirs = append(cat.watchedDirs, d)
		} else {
			if err = watcher.Remove(d); err != nil {
				return err
			}
			log.Printf("Unwatching %s", d)
		}
	}
	log.Printf("Watching %d new directories", len(cat.watchedDirs))

	cat.lastUpdate = time.Now()
	if err != nil {
		cat.updatingInfos = false
	} else {
		go preloadInfos(cat.lastUpdate)
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
	for e := range cat.queue {
		if e == nil {
			break
		}
		p := func(frame int) {
			cat.mu.Lock()
			e.Frame = frame
			cat.mu.Unlock()
		}

		// Keeps the lock for the whole process so the serve-mp4 executable doesn't
		// abruptly interrupt the transcoding.
		t.mu.Lock()
		//err := vid.ChromeCast.TranscodeMP4(e.Src, e.Actual, e.Info, p)
		err := vid.ChromeOS.TranscodeMP4(e.Src, e.Actual, e.Info, p)
		t.mu.Unlock()

		cat.mu.Lock()
		e.Transcoding = false
		e.Cached = err == nil
		cat.mu.Unlock()
	}
}

func (t *transcodingQueue) stop() {
	// Flush the pending items in the transcoding queue, wait for the current
	// transcoding to complete, return.
	log.Printf("shutting down")
	for stop := false; !stop; {
		select {
		case <-cat.queue:
		default:
			cat.queue <- nil
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
			// TODO(maruel): Ignore streams.
			if e.Op != fsnotify.Write {
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
