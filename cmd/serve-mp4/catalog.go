// Copyright 2017 Marc-Antoine Ruel. All rights reserved.
// Use of this source code is governed under the Apache License, Version 2.0
// that can be found in the LICENSE file.

package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/maruel/interrupt"
	"github.com/maruel/serve-mp4/vid"
	fsnotify "gopkg.in/fsnotify.v1"
)

// Entry is a single video file found.
type Entry struct {
	Rel           string // Relative path to source file.
	preferredLang string // cache of prefered language.
	rootDir       string // cache of root directory.

	// Mutable
	mu          sync.Mutex
	info        *vid.Info
	err         error               // Cached error if Info() failed.
	cached      map[vid.Device]bool // Transcoded paths.
	transcoding bool                // transcoding
	frame       int                 // frame at which transcoding is at
	cold        bool                // cold means that the file disappeared in last refresh
}

func (e *Entry) IsCached(v vid.Device) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.cached[v]
}

func (e *Entry) IsCachedChromeCast() bool {
	return e.IsCached(vid.ChromeCast)
}

func (e *Entry) IsCachedChromeOS() bool {
	return e.IsCached(vid.ChromeOS)
}

func (e *Entry) Path(v vid.Device) string {
	return e.Rel[:len(e.Rel)-len(filepath.Ext(e.Rel))+1] + v.ToContainer()
}

// ChromeCastPath is the path for the ChromeCast version.
//
// It must be prepended by cacheDir and v.String().
func (e *Entry) ChromeCastPath() string {
	return e.Path(vid.ChromeCast)
}

// ChromeOSPath is the path for the ChromeOS version.
//
// It must be prepended by cacheDir and v.String().
func (e *Entry) ChromeOSPath() string {
	return e.Path(vid.ChromeOS)
}

func (e *Entry) IsTranscoding() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.transcoding
}

// Percent returns the percentage at which transcoding is at.
func (e *Entry) Percent() string {
	v := e.Info()
	if v == nil {
		return "N/A"
	}
	e.mu.Lock()
	f := e.frame
	e.mu.Unlock()
	nb, err := strconv.Atoi(v.Raw.Streams[v.VideoIndex].NbFrames)
	if err != nil {
		return "N/A"
	}
	return fmt.Sprintf("%3.1f%%", 100.*float32(f)/float32(nb))
}

// TryInfo returns vid.Info only if it had been loaded already.
func (e *Entry) TryInfo() *vid.Info {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.info
}

// Info lazy loads e.info.
func (e *Entry) Info() *vid.Info {
	s := e.srcFile()
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.info == nil && e.err == nil {
		if e.info, e.err = vid.Identify(s, e.preferredLang); e.err != nil {
			log.Printf("%q:%v", s, e.err)
		}
	}
	return e.info
}

func (e *Entry) srcFile() string {
	return filepath.Join(e.rootDir, e.Rel)
}

//

// Directory is all files in a directory.
type Directory struct {
	Items   map[string]*Entry
	Subdirs map[string]*Directory
}

func (d *Directory) StillLoading() bool {
	for _, e := range d.Items {
		if e.IsTranscoding() {
			return true
		}
	}
	for _, s := range d.Subdirs {
		if s.StillLoading() {
			return true
		}
	}
	return false
}

func (d *Directory) TotalItems() int {
	t := len(d.Items)
	for _, s := range d.Subdirs {
		t += s.TotalItems()
	}
	return t
}

func (d *Directory) lookupDir(dir string) *Directory {
	if dir == "" {
		return d
	}
	i := strings.IndexByte(dir, '/')
	if i == -1 {
		return d.Subdirs[dir]
	}
	b := dir[:i]
	s := d.Subdirs[b]
	if s == nil {
		return nil
	}
	return s.lookupDir(dir[i+1:])
}

func (d *Directory) lookupEntry(rel string) *Entry {
	s := d
	if i := strings.LastIndexByte(rel, '/'); i != -1 {
		if i == 0 {
			return nil
		}
		s = d.lookupDir(rel[:i])
		if s == nil {
			return nil
		}
		rel = rel[i+1:]
	}
	return s.Items[rel]
}

// findEntryToPreload is inefficient but for few thousands it should be fine.
func (d *Directory) findEntryToPreload() *Entry {
	for _, e := range d.Items {
		if e.info == nil {
			return e
		}
	}
	for _, s := range d.Subdirs {
		if e := s.findEntryToPreload(); e != nil {
			return e
		}
	}
	return nil
}

// resetCold tags all entries as cold before reenumerating the directory.
func (d *Directory) resetCold() {
	for _, e := range d.Items {
		e.cold = true
	}
	for _, s := range d.Subdirs {
		s.resetCold()
	}
}

// trimCold removes all entries tagged as cold.
func (d *Directory) trimCold() {
	for name, e := range d.Items {
		if e.cold {
			// File was deleted.
			delete(d.Items, name)
		}
	}
	for name, s := range d.Subdirs {
		s.trimCold()
		if len(s.Items) == 0 && len(s.Subdirs) == 0 {
			delete(d.Subdirs, name)
		}
	}
}

//

type Catalog interface {
	CacheDir() string
	LookupEntry(rel string) *Entry
	LookupDir(rel string) *Directory
}

type catalog struct {
	preferredLang string
	rootDir       string
	cacheDir      string

	// Mutable.
	mu            sync.RWMutex
	tree          Directory
	updatingInfos bool
}

func NewCatalog(rootDir, cacheDir, preferredLang string) (Catalog, error) {
	c := &catalog{
		preferredLang: preferredLang,
		rootDir:       rootDir,
		cacheDir:      cacheDir,
		tree: Directory{
			Items:   map[string]*Entry{},
			Subdirs: map[string]*Directory{},
		},
		updatingInfos: true,
	}
	if i, err := os.Stat(c.cacheDir); err != nil || !i.IsDir() {
		if err := os.Mkdir(c.cacheDir, 0777); err != nil {
			return nil, err
		}
	}
	return c, nil
}

func (c *catalog) CacheDir() string {
	return c.cacheDir
}

func (c *catalog) LookupEntry(rel string) *Entry {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.tree.lookupEntry(rel)
}

// LookupDir returns a copy of one Directory entry.
func (c *catalog) LookupDir(rel string) *Directory {
	c.mu.RLock()
	defer c.mu.RUnlock()
	d := c.tree.lookupDir(rel)
	if d == nil {
		return nil
	}
	// Make a copy so lock doesn't need to be held.
	out := &Directory{
		Items:   make(map[string]*Entry, len(d.Items)),
		Subdirs: make(map[string]*Directory, len(d.Subdirs)),
	}
	for k, v := range d.Items {
		out.Items[k] = v
	}
	for k, v := range d.Subdirs {
		out.Subdirs[k] = v
	}
	return out
}

func (c *catalog) findEntryToPreload() *Entry {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.tree.findEntryToPreload()
}

// addFile is called when a file is enumerated.
func (c *catalog) addFile(rel string) {
	//log.Printf("addFile(%q)", rel)
	c.mu.Lock()
	defer c.mu.Unlock()
	nrel := strings.Replace(rel, string(filepath.Separator), "/", -1)
	d := &c.tree
	base := ""
	rest := nrel
	for {
		parts := strings.SplitN(rest, "/", 2)
		if len(parts) == 1 {
			base = parts[0]
			if e, ok := d.Items[base]; ok {
				// Found.
				e.cold = false
				return
			}
			break
		}
		if _, ok := d.Subdirs[parts[0]]; !ok {
			d.Subdirs[parts[0]] = &Directory{
				Items:   map[string]*Entry{},
				Subdirs: map[string]*Directory{},
			}
		}
		d = d.Subdirs[parts[0]]
		rest = parts[1]
	}

	e := &Entry{
		Rel:           rel,
		preferredLang: c.preferredLang,
		rootDir:       c.rootDir,
		cached:        map[vid.Device]bool{},
	}
	for _, v := range []vid.Device{vid.ChromeCast, vid.ChromeOS} {
		// For now force transcoding so -movflags +faststart is guaranteed.
		p := toCachedPath(rel, v)
		if i, err := os.Stat(filepath.Join(c.cacheDir, p)); err == nil && i.Size() > 0 {
			e.cached[v] = true
		}
	}
	d.Items[base] = e
}

// enumerateEntries enumerates or reenumerates the tree.
//
// Returns all directories enumerated.
func (c *catalog) enumerateEntries() []string {
	// Keep a writer lock for the duration of the enumeration.
	c.mu.Lock()
	c.updatingInfos = true
	c.tree.resetCold()
	c.mu.Unlock()

	found := 0
	prefix := len(c.rootDir) + 1
	var dirs []string
	err := filepath.Walk(c.rootDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || len(path) < prefix {
			return err
		}
		if filepath.Base(path)[0] == '.' {
			return filepath.SkipDir
		}
		rel := path[prefix:]
		if info.IsDir() {
			dirs = append(dirs, rel)
			return nil
		}
		if !isValidExt(filepath.Ext(path)) {
			return err
		}
		found++
		c.addFile(rel)
		return nil
	})
	if err != nil {
		log.Printf("Failed to enumerate files: %v", err)
	}
	log.Printf("Found %d files", found)

	c.mu.Lock()
	c.tree.trimCold()
	c.mu.Unlock()
	return dirs
}

func toCachedPath(rel string, v vid.Device) string {
	path := filepath.Join(v.String(), rel)
	ext := filepath.Ext(path)
	return path[:len(path)-len(ext)] + "." + v.ToContainer()
}

//

type Crawler interface {
	io.Closer
	WatchFiles() error
}

type crawler struct {
	c       *catalog
	watcher *fsnotify.Watcher
	refresh chan bool

	mu            sync.Mutex
	updatingInfos bool
	lastUpdate    time.Time
	watchedDirs   []string // absolute directories
}

func NewCrawler(cat Catalog) (Crawler, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	c := &crawler{
		c:             cat.(*catalog),
		watcher:       watcher,
		refresh:       make(chan bool, 1000),
		updatingInfos: true,
	}
	// Do the first enumeration and starts a routine to update file metadata.
	if err := c.enumerateEntries(); err != nil {
		c.Close()
		return nil, err
	}
	go c.handleRefresh(c.refresh)
	return c, nil
}

func (c *crawler) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	var err error
	for _, d := range c.watchedDirs {
		if err2 := c.watcher.Remove(d); err2 != nil {
			log.Printf("Failed to unwatch %s: %v", d, err2)
			err = err2
		}
	}
	c.watchedDirs = nil
	if err2 := c.watcher.Close(); err == nil {
		err = err2
	}
	return err
}

func (c *crawler) WatchFiles() error {
	exePath, err := os.Executable()
	if err != nil {
		return err
	}
	fi, err := os.Stat(exePath)
	if err != nil {
		return err
	}
	mod0 := fi.ModTime()
	if err = c.watcher.Add(exePath); err != nil {
		return err
	}

	interrupt.HandleCtrlC()
	for {
		select {
		case <-interrupt.Channel:
			return err
		case err = <-c.watcher.Errors:
			return err
		case e := <-c.watcher.Events:
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
				c.refresh <- true
			}
		}
	}
}

// preloadInfos preloads all Info for all Entry.
func (c *crawler) preloadInfos(stamp time.Time) {
	for {
		c.mu.Lock()
		stop := stamp != c.lastUpdate
		c.mu.Unlock()
		if stop {
			log.Printf("A new refresh happened; stopping pre-processing early")
			return
		}
		e := c.c.findEntryToPreload()
		if e == nil {
			break
		}
		e.Info()
	}

	// Done.
	c.mu.Lock()
	c.updatingInfos = false
	c.mu.Unlock()
	log.Printf("Done pre-processing")
}

// enumerateEntries enumerates or reenumerates the tree.
//
// Calls preloadInfos() as a separate asynchronous goroutine.
func (c *crawler) enumerateEntries() error {
	dirs := c.c.enumerateEntries()
	sort.Strings(dirs)
	for i := range dirs {
		dirs[i] = filepath.Join(c.c.rootDir, dirs[i])
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	for i, d := range c.watchedDirs {
		j := sort.SearchStrings(dirs, d)
		if dirs[j] != d {
			c.watchedDirs[i] = ""
			if err := c.watcher.Remove(d); err != nil {
				log.Printf("Failed to unwatch %q: %v", d, err)
			} else {
				log.Printf("Unwatching %q", d)
			}
		}
	}
	sort.Strings(c.watchedDirs)
	i := 0
	for ; i < len(c.watchedDirs) && c.watchedDirs[i] == ""; i++ {
	}
	if i != 0 {
		copy(c.watchedDirs, c.watchedDirs[i:])
	}
	new := 0
	for _, d := range dirs {
		j := sort.SearchStrings(c.watchedDirs, d)
		if dirs[j] != d {
			if err := c.watcher.Add(d); err != nil {
				log.Printf("Failed to watch %q: %v", d, err)
			}
			new++
			c.watchedDirs = append(c.watchedDirs, d)
			sort.Strings(c.watchedDirs)
		}
	}
	if cap(c.watchedDirs) > 2*len(c.watchedDirs) {
		n := make([]string, len(c.watchedDirs))
		copy(n, c.watchedDirs)
		c.watchedDirs = n
	}
	log.Printf("Watching %d new directories", new)
	c.lastUpdate = time.Now()
	go c.preloadInfos(c.lastUpdate)
	return nil
}

// handleRefresh handles the events from refresh that are triggered via
// fsnotify.Watcher.
func (c *crawler) handleRefresh(refresh <-chan bool) {
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
		if err := c.enumerateEntries(); err != nil {
			log.Printf("failed to refresh files: %v", err)
		}
	}
}

//

type TranscodingQueue interface {
	io.Closer
	Transcode(v vid.Device, e *Entry)
}

type transcodingRequest struct {
	v vid.Device
	e *Entry
}

type transcodingQueue struct {
	c     *catalog
	mu    sync.Mutex
	queue chan *transcodingRequest
}

func NewTranscodingQueue(c Catalog) TranscodingQueue {
	t := &transcodingQueue{
		c:     c.(*catalog),
		queue: make(chan *transcodingRequest, 10240),
	}
	go t.run()
	return t
}

func (t *transcodingQueue) Close() error {
	// Flush the pending items in the transcoding queue, wait for the current
	// transcoding to complete, return.
	log.Printf("shutting down")
	for stop := false; !stop; {
		select {
		case <-t.queue:
		default:
			t.queue <- nil
			stop = true
			break
		}
	}
	t.mu.Lock()
	t.mu.Unlock()
	return nil
}

func (t *transcodingQueue) Transcode(v vid.Device, e *Entry) {
	e.mu.Lock()
	e.transcoding = true
	e.mu.Unlock()
	t.queue <- &transcodingRequest{v: v, e: e}
}

func (t *transcodingQueue) run() {
	for r := range t.queue {
		if r == nil {
			break
		}
		p := func(frame int) {
			r.e.mu.Lock()
			r.e.frame = frame
			r.e.mu.Unlock()
		}

		// Keeps the transcoding lock for the whole process so the serve-mp4
		// executable doesn't abruptly interrupt the transcoding but do not keep
		// the Entry lock.
		i := r.e.Info()
		if i == nil {
			log.Printf("Skipping transcoding for %q", r.e.Rel)
		}
		path := filepath.Join(t.c.cacheDir, toCachedPath(r.e.Rel, r.v))
		t.mu.Lock()
		err := r.v.TranscodeMP4(r.e.srcFile(), path, i, p)
		t.mu.Unlock()

		r.e.mu.Lock()
		r.e.transcoding = false
		if err == nil {
			r.e.cached[r.v] = true
		}
		r.e.mu.Unlock()
	}
}
