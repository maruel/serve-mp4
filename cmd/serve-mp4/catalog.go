// Copyright 2017 Marc-Antoine Ruel. All rights reserved.
// Use of this source code is governed under the Apache License, Version 2.0
// that can be found in the LICENSE file.

package main

import (
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/maruel/serve-mp4/vid"
)

// entry is a single video file found.
type entry struct {
	Rel    string // Resource name.
	Base   string // Display name without relpath.
	Actual string // Absolute path to cached file.
	Src    string // Absolute path to source file.
	lang   string // cache of prefered language.

	// Mutable
	mu          sync.Mutex
	Info        *vid.Info
	Cached      bool // Transcoded; TODO(maruel): One per device type?
	Transcoding bool // Transcoding
	Frame       int  // Frame at which transcoding is at
	cold        bool
}

// Percent returns the percentage at which transcoding is at.
func (e *entry) Percent() string {
	v, err := e.getInfo()
	if err != nil {
		return "N/A"
	}
	e.mu.Lock()
	f := e.Frame
	c := e.Cached
	e.mu.Unlock()
	if c {
		return "100%"
	}
	nb, err := strconv.Atoi(v.Raw.Streams[v.VideoIndex].NbFrames)
	if err != nil {
		return "N/A"
	}
	return fmt.Sprintf("%3.1f%%", 100.*float32(f)/float32(nb))
}

// getInfo lazy loads e.Info.
func (e *entry) getInfo() (*vid.Info, error) {
	e.mu.Lock()
	v := e.Info
	e.mu.Unlock()
	if v != nil {
		return v, nil
	}
	v, err := vid.Identify(e.Src, e.lang)
	if err != nil {
		// TODO(maruel): Signal to not repeatedly analyze the file.
		return nil, err
	}
	e.mu.Lock()
	e.Info = v
	e.mu.Unlock()
	return v, nil
}

// bucket is all files in a directory.
type bucket struct {
	Dir     string
	Items   []*entry
	Subdirs []string
}

type catalog struct {
	mu sync.RWMutex
	// Shared between web server and file crawler:
	itemsMap      map[string]*entry
	buckets       []*bucket
	queue         chan *entry
	updatingInfos bool
	// For file crawler only:
	lastUpdate  time.Time
	watchedDirs []string
}

var cat = catalog{
	itemsMap:      map[string]*entry{},
	queue:         make(chan *entry, 10240),
	updatingInfos: true,
}
