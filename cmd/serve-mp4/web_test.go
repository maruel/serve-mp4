// Copyright 2018 Marc-Antoine Ruel. All rights reserved.
// Use of this source code is governed under the Apache License, Version 2.0
// that can be found in the LICENSE file.

package main

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWeb(t *testing.T) {
	mp4, err := base64.StdEncoding.DecodeString(mp4Base64)
	if err != nil {
		t.Fatal(err)
	}

	d, f := tmpDir(t)
	defer f()
	a := filepath.Join(d, "a")
	if err = os.Mkdir(a, 0700); err != nil {
		t.Fatal(err)
	}
	if err = ioutil.WriteFile(filepath.Join(a, "b.mp4"), mp4, 0700); err != nil {
		t.Fatal(err)
	}

	c, err := NewCatalog(d, d, "fre")
	if err != nil {
		t.Fatal(err)
	}
	crawl, err := NewCrawler(c)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err = crawl.Close(); err != nil {
			t.Fatal(err)
		}
	}()
	tq := NewTranscodingQueue(c)
	s, err := startServer(":0", c, tq)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err = s.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	parts := strings.Split(s.Addr(), ":")
	port := parts[len(parts)-1]

	urls := []string{"/", "/browse/", "/browse/a", "/browse/a/", "/metadata/a/b.mp4", "/raw/a/b.mp4"}
	for _, url := range urls {
		get(t, port, url)
	}

	e := c.LookupEntry("a/b.mp4")
	post(t, port, "/transcode/chromecast/a/b.mp4")
	// Wait for transcoding to finish.
	for e.IsTranscoding() {
		time.Sleep(time.Microsecond)
	}
	get(t, port, "/chromecast/a/b.mp4")
	get(t, port, "/browse/a/")

	post(t, port, "/transcode/chromeos/a/b.mp4")
	// Wait for transcoding to finish.
	for e.IsTranscoding() {
		time.Sleep(time.Microsecond)
	}
	get(t, port, "/chromeos/a/b.mp4")
	get(t, port, "/browse/a/")
}

func get(t *testing.T, port, url string) {
	resp, err := http.DefaultClient.Get(fmt.Sprintf("http://localhost:%s%s", port, url))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ioutil.ReadAll(resp.Body); err != nil {
	}
	if err = resp.Body.Close(); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("%s: %d", url, resp.StatusCode)
	}
}

func post(t *testing.T, port, url string) {
	req, err := http.NewRequest("POST", fmt.Sprintf("http://localhost:%s%s", port, url), &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Referer", fmt.Sprintf("http://localhost:%s/browse/", port))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ioutil.ReadAll(resp.Body); err != nil {
	}
	if err = resp.Body.Close(); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("%s: %d", url, resp.StatusCode)
	}
}

//
// python -c "import base64,urllib;a=base64.b64encode(urllib.urlopen('https://github.com/mathiasbynens/small/raw/master/mp4.mp4').read()); print '\n'.join(a[i:i+70] for i in range(0,len(a),70))"
//
const mp4Base64 = `
AAAAHGZ0eXBpc29tAAACAGlzb21pc28ybXA0MQAAAAhmcmVlAAAAGm1kYXQAAAGzABAHAA
ABthBgUYI9t+8AAAMNbW9vdgAAAGxtdmhkAAAAAMXMvvrFzL76AAAD6AAAACoAAQAAAQAA
AAAAAAAAAAAAAAEAAAAAAAAAAAAAAAAAAAABAAAAAAAAAAAAAAAAAABAAAAAAAAAAAAAAA
AAAAAAAAAAAAAAAAAAAAAAAAAAAgAAABhpb2RzAAAAABCAgIAHAE/////+/wAAAiF0cmFr
AAAAXHRraGQAAAAPxcy++sXMvvoAAAABAAAAAAAAACoAAAAAAAAAAAAAAAAAAAAAAAEAAA
AAAAAAAAAAAAAAAAABAAAAAAAAAAAAAAAAAABAAAAAAAgAAAAIAAAAAAG9bWRpYQAAACBt
ZGhkAAAAAMXMvvrFzL76AAAAGAAAAAEVxwAAAAAALWhkbHIAAAAAAAAAAHZpZGUAAAAAAA
AAAAAAAABWaWRlb0hhbmRsZXIAAAABaG1pbmYAAAAUdm1oZAAAAAEAAAAAAAAAAAAAACRk
aW5mAAAAHGRyZWYAAAAAAAAAAQAAAAx1cmwgAAAAAQAAAShzdGJsAAAAxHN0c2QAAAAAAA
AAAQAAALRtcDR2AAAAAAAAAAEAAAAAAAAAAAAAAAAAAAAAAAgACABIAAAASAAAAAAAAAAB
AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAGP//AAAAXmVzZHMAAAAAA4CAgE
0AAQAEgICAPyARAAAAAAMNQAAAAAAFgICALQAAAbABAAABtYkTAAABAAAAASAAxI2IAMUA
RAEUQwAAAbJMYXZjNTMuMzUuMAaAgIABAgAAABhzdHRzAAAAAAAAAAEAAAABAAAAAQAAAB
xzdHNjAAAAAAAAAAEAAAABAAAAAQAAAAEAAAAUc3RzegAAAAAAAAASAAAAAQAAABRzdGNv
AAAAAAAAAAEAAAAsAAAAYHVkdGEAAABYbWV0YQAAAAAAAAAhaGRscgAAAAAAAAAAbWRpcm
FwcGwAAAAAAAAAAAAAAAAraWxzdAAAACOpdG9vAAAAG2RhdGEAAAABAAAAAExhdmY1My4y
MS4x`
