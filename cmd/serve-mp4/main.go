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
)

var validExt = []string{".avi", ".m4v", ".mkv", ".mp4", ".mpeg", ".mpg", ".mov", ".wmv"}

func isValidExt(ext string) bool {
	for _, i := range validExt {
		if ext == i {
			return true
		}
	}
	return false
}

func getWd() string {
	wd, _ := os.Getwd()
	return wd
}

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

	root, err := filepath.Abs(*rootDir)
	if err != nil {
		return err
	}
	cache := *cacheDir
	if cache == "" {
		cache = filepath.Join(root, ".cache")
	}
	cat, err := NewCatalog(root, cache, *lang)
	if err != nil {
		return err
	}
	crawl, err := NewCrawler(cat)
	if err != nil {
		return err
	}
	defer crawl.Close()

	t := NewTranscodingQueue(cat)
	defer t.Close()

	s, err := startServer(*bind, cat, t)
	if err != nil {
		return err
	}
	defer s.Close()

	return crawl.WatchFiles()
}

func main() {
	if err := mainImpl(); err != nil {
		fmt.Fprintf(os.Stderr, "serve-mp4: %s\n", err)
		os.Exit(1)
	}
}
