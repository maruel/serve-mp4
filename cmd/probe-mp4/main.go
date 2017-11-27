// Copyright 2017 Marc-Antoine Ruel. All rights reserved.
// Use of this source code is governed under the Apache License, Version 2.0
// that can be found in the LICENSE file.

// prove-mp4 probes a file.
package main

import (
	"errors"
	"flag"
	"fmt"
	"html/template"
	"io/ioutil"
	"log"
	"os"

	"github.com/maruel/serve-mp4/vid"
)

const defaultFmt = "Fmt: {{.Container}}\nDur: {{.Duration}}\nVid: {{.VideoCodec}}\nAud: {{.AudioCodec}}\nLng: {{.AudioLang}}"

func mainImpl() error {
	lang := flag.String("lang", "fre", "preferred language")
	format := flag.String("fmt", defaultFmt, "format to use; an instance vid.Info")
	verbose := flag.Bool("v", false, "verbose")
	log.SetFlags(log.Lmicroseconds)
	flag.Parse()
	if !*verbose {
		log.SetOutput(ioutil.Discard)
	}
	if flag.NArg() != 1 {
		return errors.New("expected a single file")
	}

	v, err := vid.Identify(flag.Args()[0], *lang)
	if err != nil {
		return err
	}
	t, err := template.New("").Parse(*format + "\n")
	if err != nil {
		return err
	}
	return t.Execute(os.Stdout, v)
}

func main() {
	if err := mainImpl(); err != nil {
		fmt.Fprintf(os.Stderr, "probe-mp4: %s\n", err)
		os.Exit(1)
	}
}
