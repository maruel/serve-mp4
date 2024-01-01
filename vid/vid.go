// Copyright 2017 Marc-Antoine Ruel. All rights reserved.
// Use of this source code is governed under the Apache License, Version 2.0
// that can be found in the LICENSE file.

// Package vid identifies and transcodes video files via ffprobe and ffmpeg.
package vid

//go:generate go install golang.org/x/tools/cmd/stringer@latest
//go:generate stringer --type Device

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/maruel/serve-mp4/vid/ffmpeg"
)

// Info contains the analyzed information about a video.
type Info struct {
	Container  string // Copy of .Raw.Format.FormatName
	Duration   string // Rounded user readable duration.
	VideoIndex int
	VideoCodec string
	AudioIndex int
	AudioCodec string
	AudioLang  string
	Raw        ffmpeg.ProbeResult
}

// Identify runs ffprobe on a file and analyzes its output.
//
// lang shall be the preferred language, e.g. "eng" or "fre".
func Identify(src string, lang string) (*Info, error) {
	out := &Info{}
	if err := ffmpeg.Probe(src, &out.Raw); err != nil {
		return nil, err
	}
	out.Container = out.Raw.Format.FormatName
	if out.Raw.Format.Duration != "" {
		d, err := time.ParseDuration(out.Raw.Format.Duration + "s")
		if err != nil {
			return nil, err
		}
		// Round with only two units.
		if d > time.Hour {
			out.Duration = d.Round(time.Minute).String()
		} else if d > time.Minute {
			out.Duration = d.Round(time.Second).String()
		} else {
			out.Duration = d.Round(time.Millisecond).String()
		}
		out.Duration = strings.Replace(out.Duration, "m0s", "m", 1)
		out.Duration = strings.Replace(out.Duration, "h0m", "h", 1)
	}
	var audios, videos []int
	for i, s := range out.Raw.Streams {
		switch s.CodecType {
		case "video":
			if s.Tags["mimetype"] == "image/jpeg" {
				// Likely a cover.jpeg.
				continue
			}
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
	// Choose the preferred stream based on preferences.
	if len(videos) > 1 {
		return nil, fmt.Errorf("Identify(%s): too many video streams", src)
	}
	if len(videos) == 0 {
		return nil, fmt.Errorf("Identify(%s): no video stream found", src)
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

// Device is a type of device to target.
type Device int

const (
	// ChromeCast supports AC3 passthrough, or can decodes AAC.
	//
	// https://developers.google.com/cast/docs/media
	//
	// It doesn't support older formats like MPEG4 and awkwards ones like XVID.
	//
	// Containers:        AAC,MP3,MP4,WAV,WebM
	// Video:             H264,VP8
	// Audio:             AAC,FLAC,MP3,Opus,Vorbis,WAV
	// Audio passthrough: AC3
	ChromeCast Device = iota + 1

	// ChromeCastUltra supports h265.
	//
	// https://developers.google.com/cast/docs/media
	//
	// It doesn't support older formats like MPEG4 and awkwards ones like XVID.
	//
	// Containers:        AAC,MP3,MP4,WAV,WebM
	// Video:             H264,H265,VP8,VP9
	// Audio:             AAC,FLAC,MP3,Opus,Vorbis,WAV
	// Audio passthrough: AC3
	ChromeCastUltra

	// ChromeOS decodes AAC and awkward formats like XVID, but doesn't support
	// AC3 at all.
	//
	// https://support.google.com/chromebook/answer/183093
	//
	//  Container | Video Codec     | Audio Codec
	//  ogv       | Theora          | --
	//  webm      | VP8,VP9         | Opus,Vorbis
	//  mp4       | H264,MPEG4      | --
	//  mov       | H264,MPEG4      | --
	//  avi       | DVIX,MPEG4,XVID | MP3
	//  3gp       | H264,MPEG4      | AAC,AMR-NB
	ChromeOS

	// WEBPWebPreview generates a web preview of the video in WEBP
	WEBPWebPreview
)

// supportedVideo returns true if this device supports this video codec.
func (d Device) supportedVideo(codec string) bool {
	// WEBPWebPreview always returns false for video since it's going to be transcoded.
	if d == WEBPWebPreview {
		return false
	}
	switch codec {
	case "mpeg1video", "mpeg2video", "h264":
		return true
	case "vp8":
		// TODO(maruel): Depends on the container.
		return true
	case "h265":
		return d == ChromeCastUltra
	default:
		// mpeg4, msmpeg4v3, svq3, wmv1
		return false
	}
}

// supportedAudio returns true if this device supports this audio codec.
func (d Device) supportedAudio(codec string) bool {
	// WEBPWebPreview always returns true for audio since it's ignored.
	if d == WEBPWebPreview {
		return true
	}
	switch codec {
	case "ac3":
		// ChromeOS doesn't support this, Cast does passthrough, which is fine
		// since all TVs can decode it.
		return d != ChromeOS
	// TODO(maruel): Confirm they all work.
	case "aac", "mp2", "mp3":
		return true
	default:
		// pcm_u8, wmav2
		// Seems like ChromeCast doesn't support "dts".
		return false
	}
}

func (d Device) ToContainer() string {
	if d == WEBPWebPreview {
		return "webp"
	}
	// TODO(maruel): Implement in the case of ChromeOS.
	return "mp4"
}

// Transcode transcodes a video file for playback on the device as MP4.
//
// The generated file is a mp4 file with 'faststart' for fast seeking.
//
// The src file must have been analyzed via Identify() first.
//
// progress will be updated with progress information.
func (d Device) Transcode(src, dst string, v *Info, progress func(frame int)) error {
	c := d.ToContainer()
	args := []string{
		"-i", src,
		"-f", c,
	}
	if c == "mp4" {
		// https://trac.ffmpeg.org/wiki/Encode/AAC#ProgressiveDownload
		args = append(args, "-movflags", "+faststart")
		// TODO(maruel): Confirm.
		args = append(args, "-map", fmt.Sprintf("0:%d", v.VideoIndex))
		args = append(args, "-map", fmt.Sprintf("0:%d", v.AudioIndex))
	}

	if d == WEBPWebPreview {
		args = append(args,
			"-itsoffset", "1:00",
			"-itsscale", "2",
			"-t", "30",
			"-vcodec", "libwebp",
			"-filter:v", "fps=fps=2",
			"-lossless", "0", "-compression_level", "3",
			"-loop", "1",
			"-s", "320:-1")
		// "-preset", "default",
		// "-vsync", "0",
	} else if d.supportedVideo(v.VideoCodec) {
		// Video Copy.
		args = append(args, "-c:v", "copy")
	} else {
		// Video Transcode.
		// https://trac.ffmpeg.org/wiki/Encode/H.264
		// https://trac.ffmpeg.org/wiki/Encode/H.265; only works with ChromeCast Ultra.
		// https://trac.ffmpeg.org/wiki/HWAccelIntro; on nvidia, use h264_nvenc and h264_cuvid
		// On Raspbian, use: h264_omx
		args = append(args, "-c:v", "h264")
		switch d {
		case ChromeCast, ChromeCastUltra:
			// Transcode very fast. This creates large files but we don't care much
			// here. We want to limit the bitrate.
			args = append(args,
				"-preset", "faster",
				"-crf", "21",
				"-level", "4.1",
				// Make sure we don't use yuv420p10le / High 10.
				"-pix_fmt", "yuv420p",
				//"-x264opts", "vbv-bufsize=50000:vbv-maxrate=50000:nal-hrd=vb",
				//"-b:v", "8M",
				//"-maxrate", "8M",
				//"-bufsize", "21M",
			)
		case ChromeOS:
			// The file is meant to be stored on a device. Keep it small.
			args = append(args, "-preset", "slow", "-crf", "21")
		}
	}

	if d == WEBPWebPreview {
		// No audio.
		args = append(args, "-an")
	} else if d.supportedAudio(v.AudioCodec) {
		// Audio copy.
		args = append(args, "-c:a", "copy")
	} else {
		// Audio Transcode.
		// https://trac.ffmpeg.org/wiki/Encode/AAC
		args = append(args, "-c:a", "aac")
		// TODO(maruel): Complained -vbr is unrecognized.
		//args = append(args, "-c:a", "libfdk_aac", "-vbr", "4")
	}

	switch v.AudioLang {
	case "", "und":
	default:
		// TODO(maruel): Doesn't seem to work.
		args = append(args, "-metadata:s:a:0", fmt.Sprintf("language=%s", v.AudioLang))
	}

	args = append(args, dst)
	dir := filepath.Dir(dst)
	if i, err := os.Stat(dir); err != nil || !i.IsDir() {
		if err := os.MkdirAll(dir, 0o777); err != nil {
			return fmt.Errorf("Transcode(%s, %s): %v", src, dst, err)
		}
	}
	log.Printf("Transcode(%s) running: ffmpeg %s", src, strings.Join(args, " "))
	if out, err := ffmpeg.Transcode(args, progress); err != nil {
		log.Printf("Transcode(%s) = %v\n%s", src, err, out)
		os.Remove(dst)
		return fmt.Errorf("Transcode(%s, %s): %v", src, dst, err)
	}
	log.Printf("Transcode(%s) done", src)
	return nil
}
