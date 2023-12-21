// Copyright 2018 Marc-Antoine Ruel. All rights reserved.
// Use of this source code is governed under the Apache License, Version 2.0
// that can be found in the LICENSE file.

package main

import (
	"flag"
	"io"
	"log"
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	flag.Parse()
	if !testing.Verbose() {
		log.SetOutput(io.Discard)
	}
	os.Exit(m.Run())
}

func tmpDir(t *testing.T) (string, func()) {
	d, err := os.MkdirTemp("", "serve-mp4")
	if err != nil {
		t.Fatal(err)
	}
	return d, func() {
		if err := os.RemoveAll(d); err != nil {
			t.Fatal(err)
		}
	}
}
