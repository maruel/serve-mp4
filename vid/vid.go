// Copyright 2017 Marc-Antoine Ruel. All rights reserved.
// Use of this source code is governed under the Apache License, Version 2.0
// that can be found in the LICENSE file.

// vid identifies and transcodes video files via ffprobe and ffmpeg.
package vid

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Info contains the important information about a video.
type Info struct {
	Container  string
	Duration   time.Duration
	VideoIndex int
	VideoCodec string
	AudioIndex int
	AudioCodec string
	AudioLang  string
	Raw        FfprobeResult
}

// FfprobeStream is one stream in the video container as output by ffprobe.
type FfprobeStream struct {
	// Both
	Index          int
	CodecName      string `json:"codec_name"`
	CodecLongName  string `json:"codec_long_name"`
	Profile        string
	CodecType      string `json:"codec_type"`
	CodecTimeBase  string `json:"codec_time_base"`
	CodecTagString string `json:"codec_tag_string"`
	CodecTag       string `json:"codec_tag"`
	RFrameRate     string `json:"r_frame_rate"`
	AvgFrameRate   string `json:"avg_frame_rate"`
	TimeBase       string `json:"time_base"`
	StartPts       int    `json:"start_pts"`
	StartTime      string `json:"start_time"`
	DurationTs     int    `json:"duration_ts"`
	Duration       string
	BitRate        string `json:"bit_rate"`
	NbFrames       string `json:"nb_frames"`
	Disposition    map[string]int
	Tags           map[string]string

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
	MaxBitRate    string `json:"max_bit_rate"`
}

// FfprobeFormat is the detected file format as output by ffprobe.
type FfprobeFormat struct {
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

// FfprobeResult is the raw output of ffprobe.
type FfprobeResult struct {
	Streams []FfprobeStream
	Format  FfprobeFormat
}

/* In case we need to debug the output.
func IdentifyVeryRaw(src string) (map[string]interface{}, error) {
	out := map[string]interface{}{}
	c := exec.Command("ffprobe", "-v", "quiet", "-print_format", "json", "-show_format", "-show_streams", src)
	raw, err := c.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("IdentifyRaw(%s): %v\n%s", src, err, raw)
	}
	if err = json.Unmarshal(raw, out); err != nil {
		return nil, fmt.Errorf("IdentifyRaw(%s): %v", src, err)
	}
	return out, nil
}
*/

// Identify runs ffprobe on a file and analyzes its output.
//
// lang shall be the preferred language, e.g. "eng" or "fre".
func Identify(src string, lang string) (*Info, error) {
	out := &Info{}
	c := exec.Command("ffprobe", "-v", "quiet", "-print_format", "json", "-show_format", "-show_streams", src)
	raw, err := c.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("Identify(%s): %v\n%s", src, err, raw)
	}
	if err = json.Unmarshal(raw, &out.Raw); err != nil {
		return nil, fmt.Errorf("Identify(%s): %v", src, err)
	}
	out.Container = out.Raw.Format.FormatName
	if out.Raw.Format.Duration != "" {
		d, err := time.ParseDuration(out.Raw.Format.Duration + "s")
		if err != nil {
			return nil, err
		}
		out.Duration = d.Round(time.Second)
	}
	var audios, videos []int
	for i, s := range out.Raw.Streams {
		switch s.CodecType {
		case "video":
			videos = append(videos, i)
		case "audio":
			if s.CodecName != "" {
				// Do not add audio tracks without a codec. It seems to happen.
				audios = append(audios, i)
			}
		case "data", "subtitle":
		default:
			return nil, fmt.Errorf("Identify(%s): unknown stream %q", src, s.CodecType)
		}
	}
	// Choose the prefered stream based on preferences.
	if len(videos) > 1 {
		return nil, fmt.Errorf("Identify(%s): too many video streams", src)
	}
	out.VideoIndex = out.Raw.Streams[videos[0]].Index
	out.VideoCodec = out.Raw.Streams[videos[0]].CodecName
	for _, i := range audios {
		if out.AudioLang == lang {
			continue
		}
		out.AudioIndex = out.Raw.Streams[i].Index
		out.AudioCodec = out.Raw.Streams[i].CodecName
		out.AudioLang = out.Raw.Streams[i].Tags["language"]
	}
	return out, nil
}

// Transcode transcodes a video file for playback on a ChromeCast.
//
// TODO(maruel): Need to assert all the file formats.
func Transcode(src, dst string, v *Info, progressURL string) error {
	args := []string{
		"-hide_banner",
		"-i", src,
		"-f", "mp4",
		"-movflags", "+faststart",
		// TODO(maruel): Confirm.
		"-map", fmt.Sprintf("0:%d", v.VideoIndex),
		"-map", fmt.Sprintf("0:%d", v.AudioIndex),
		// TODO(maruel): (?) "-threads", "16",
	}

	switch v.VideoCodec {
	// TODO(maruel): Confirm: "vp8"
	case "mpeg1video", "mpeg2video", "h264":
		// Video Copy
		args = append(args, "-c:v", "copy")
	default:
		// Video Transcode
		// https://trac.ffmpeg.org/wiki/Encode/H.264
		// https://trac.ffmpeg.org/wiki/Encode/H.265; only works with ChromeCast Ultra.
		// https://trac.ffmpeg.org/wiki/HWAccelIntro; on nvidia, use h264_nvenc and h264_cuvid
		// On Raspbian, use: h264_omx
		// mpeg4, msmpeg4v3, svq3, wmv1
		args = append(args, "-c:v", "h264", "-preset", "slow", "-crf", "20")
	}

	switch v.AudioCodec {
	// TODO(maruel): Confirm they all work.
	case "ac3", "aac", "dts", "mp2", "mp3":
		// Audio copy
		args = append(args, "-c:a", "copy")
	default:
		// Audio Transcode
		// pcm_u8, wmav2
		args = append(args, "-c:a", "aac")
	}

	switch v.AudioLang {
	case "", "und":
	default:
		// TODO(maruel): Doesn't work.
		args = append(args, "-metadata:s:a:0", fmt.Sprintf("language=%s", v.AudioLang))
	}

	if progressURL != "" {
		args = append(args, "-progress", progressURL)
	}

	args = append(args, dst)

	d := filepath.Dir(dst)
	if i, err := os.Stat(d); err != nil || !i.IsDir() {
		if err := os.MkdirAll(d, 0777); err != nil {
			return fmt.Errorf("Transcode(%s, %s): %v", src, dst, err)
		}
	}
	log.Printf("Transcode(%s) running: ffmpeg %s", src, strings.Join(args, " "))
	if out, err := exec.Command("ffmpeg", args...).CombinedOutput(); err != nil {
		log.Printf("Transcode(%s) = %v\n%s", src, err, out)
		os.Remove(dst)
		return fmt.Errorf("Transcode(%s, %s): %v", src, dst, err)
	}
	log.Printf("Transcode(%s) done", src)
	return nil
}
