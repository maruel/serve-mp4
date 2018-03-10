// Copyright 2018 Marc-Antoine Ruel. All rights reserved.
// Use of this source code is governed under the Apache License, Version 2.0
// that can be found in the LICENSE file.

package main

import (
	"reflect"
	"testing"
)

func TestCatalog(t *testing.T) {
	c := catalog{
		tree: Directory{
			Items: map[string]*Entry{
				"x": &Entry{},
			},
			Subdirs: map[string]*Directory{
				"a": &Directory{
					Items: map[string]*Entry{},
					Subdirs: map[string]*Directory{
						"b": &Directory{
							Items: map[string]*Entry{
								"c": &Entry{},
							},
							Subdirs: map[string]*Directory{},
						},
					},
				},
			},
		},
	}

	successes := []string{"", "a", "a/b", "a/", "a/b/"}
	for _, line := range successes {
		if c.LookupDir(line) == nil {
			t.Fatalf("expected dir %q", line)
		}
	}
	failures := []string{"unknown", "/a", "a//b", "a/b/c"}
	for _, line := range failures {
		if c.LookupDir(line) != nil {
			t.Fatalf("unexpected dir %q", line)
		}
	}

	successes = []string{"x", "a/b/c"}
	for _, line := range successes {
		if c.LookupEntry(line) == nil {
			t.Fatalf("expected entry %q", line)
		}
	}
	failures = []string{"unknown", "/a/b/c", "a/b/c/", "a//b/c", "/x"}
	for _, line := range failures {
		if c.LookupEntry(line) != nil {
			t.Fatalf("unexpected entry %q", line)
		}
	}

	if v := c.LookupDir("a/b"); !reflect.DeepEqual(v, c.tree.Subdirs["a"].Subdirs["b"]) {
		t.Fatalf("expected dir a/b, got %#v", v)
	}
	if v := c.LookupEntry("a/b/c"); !reflect.DeepEqual(v, c.tree.Subdirs["a"].Subdirs["b"].Items["c"]) {
		t.Fatalf("expected dir a/b/c, got %#v", v)
	}
}

func TestCatalog_addFile(t *testing.T) {
	d, f := tmpDir(t)
	defer f()
	cat, err := NewCatalog(d, d, "")
	if err != nil {
		t.Fatal(err)
	}
	c := cat.(*catalog)
	c.addFile("foo/bar.mp4")
	if c.LookupEntry("foo/bar.mp4") == nil {
		t.Fatalf("expected foo/bar.mp4; %#v", c.tree)
	}
}
