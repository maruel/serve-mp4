// Copyright 2017 Marc-Antoine Ruel. All rights reserved.
// Use of this source code is governed under the Apache License, Version 2.0
// that can be found in the LICENSE file.

// Package ffmpeg contains ffmpeg specific types.
package ffmpeg

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
)

// Stream is one stream in the video container as output by ffprobe.
type Stream struct {
	// Both
	Index          int
	CodecName      string `json:"codec_name"`
	CodecLongName  string `json:"codec_long_name"`
	Profile        string
	CodecType      string `json:"codec_type"`
	CodecTimeBase  string `json:"codec_time_base"`
	CodecTagString string `json:"codec_tag_string"`
	CodecTag       string `json:"codec_tag"`

	RFrameRate   string `json:"r_frame_rate"`
	AvgFrameRate string `json:"avg_frame_rate"`
	TimeBase     string `json:"time_base"`
	StartPts     int    `json:"start_pts"`
	StartTime    string `json:"start_time"`
	DurationTs   int    `json:"duration_ts"`
	Duration     string
	BitRate      string `json:"bit_rate"`
	NbFrames     string `json:"nb_frames"`
	Disposition  map[string]int
	Tags         map[string]string

	// Video
	Width              int
	Height             int
	CodecWidth         int    `json:"codec_width"`
	CodecHeight        int    `json:"codec_height"`
	HasBFrames         int    `json:"has_b_frames"`
	SampleAspectRatio  string `json:"sample_aspect_ratio"`
	DisplayAspectRatio string `json:"display_aspect_ratio"`
	PixFmt             string `json:"pix_fmt"`
	Level              int
	ChromaLocation     string `json:"chroma_location"`
	FieldOrder         string `json:"field_order"`
	Refs               int
	IsAvc              string `json:"is_avc"`
	NalLengthSize      string `json:"nal_length_size"`
	BitsPerRawSample   string `json:"bits_per_raw_sample"`

	// Audio
	SampleFmt     string `json:"sample_fmt"`
	SampleRate    string `json:"sample_rate"`
	Channels      int
	ChannelLayout string `json:"channel_layout"`
	BitsPerSample int    `json:"bits_per_sample"`
	DmixMode      string `json:"dmix_mode"`
	LtrtCmixlev   string `json:"ltrt_cmixlev"`
	LtrtSurmixlev string `json:"ltrt_surmixlev"`
	LoroCmixlev   string `json:"loro_cmixlev"`
	LoroSurmixlev string `json:"loro_surmixlev"`
	MaxBitRate    string `json:"max_bit_rate"`
}

// Format is the detected file format as output by ffprobe.
type Format struct {
	Filename       string
	NbStreams      int    `json:"nb_streams"`
	NbPrograms     int    `json:"nb_programs"`
	FormatName     string `json:"format_name"`
	FormatLongName string `json:"format_long_name"`
	StartTime      string `json:"start_time"`
	Duration       string
	Size           string
	BitRate        string `json:"bit_rate"`
	ProbeScore     int    `json:"probe_score"`
	Tags           map[string]string
}

// Chapter is a chapter description as output by ffprobe.
type Chapter struct {
	ID        int
	TimeBase  string `json:"time_base"`
	Start     int
	StartTime string `json:"start_time"`
	End       int
	EndTime   string `json:"end_time"`
	Tags      map[string]string
}

// ProbeResult is the raw output of ffprobe.
type ProbeResult struct {
	Streams  []Stream
	Chapters []Chapter
	Format   Format
}

// Probe runs ffprobe on a file and returns the typed output.
func Probe(src string, r *ProbeResult) error {
	c := exec.Command("ffprobe", "-v", "quiet", "-print_format", "json", "-show_format", "-show_streams", "-show_chapters", src)
	raw, err := c.CombinedOutput()
	if err != nil {
		return fmt.Errorf("Probe(%s): %v\n%s", src, err, raw)
	}
	if err = json.Unmarshal(raw, r); err != nil {
		return fmt.Errorf("Probe(%s): %v", src, err)
	}
	return err
}

// ProbeRaw runs ffprobe on a file and returns the untyped output.
func ProbeRaw(src string) (map[string]interface{}, error) {
	out := map[string]interface{}{}
	c := exec.Command("ffprobe", "-v", "quiet", "-print_format", "json", "-show_format", "-show_streams", src)
	raw, err := c.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("ProbeRaw(%s): %v\n%s", src, err, raw)
	}
	if err = json.Unmarshal(raw, out); err != nil {
		return nil, fmt.Errorf("ProbeRaw(%s): %v", src, err)
	}
	return out, nil
}

// Transcode calls ffmpeg with the specified arguments, calls back into
// progress with progress information.
func Transcode(args []string, progress func(frame int)) ([]byte, error) {
	cmd := []string{
		"-hide_banner",
	}
	if progress != nil {
		// Starts a mini web server.
		ln, err := net.Listen("tcp", "localhost:0")
		if err != nil {
			return nil, err
		}
		s := &http.Server{Addr: ln.Addr().String(), Handler: progressHandler(progress)}
		go s.Serve(ln)
		cmd = append(cmd, "-progress", fmt.Sprintf("http://%s/progress", ln.Addr().String()))
		defer s.Close()
	}
	return exec.Command("ffmpeg", append(cmd, args...)...).CombinedOutput()
}

//

type progressHandler func(frame int)

func (p progressHandler) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	defer req.Body.Close()
	defer w.WriteHeader(200)
	l := ""
	b := make([]byte, 1)
	for {
		if n, err := req.Body.Read(b); n != 1 || err != nil {
			// Done.
			break
		}
		if b[0] != '\n' {
			l += string(b[0])
			continue
		}
		parts := strings.SplitN(l, "=", 2)
		if len(parts) == 2 {
			switch parts[0] {
			case "frame":
				if i, err := strconv.Atoi(parts[1]); err == nil {
					p(i)
				} else {
					log.Printf("%s: %v", l, err)
				}
			case "fps", "stream_0_0_q", "bitrate", "total_size", "out_time_ms", "out_time", "dup_frames", "drop_frames", "speed", "progress":
				// Known lines. We could handle these if desired.
			default:
				// Unknown line. Not a big deal.
			}
		}
		l = ""
	}
}
