#!/bin/bash
# Copyright 2017 Marc-Antoine Ruel. All rights reserved.
# Use of this source code is governed under the Apache License, Version 2.0
# that can be found in the LICENSE file.

# Builds the minimum needed ffmpeg to be able to use in this project.

set -eu

# On non-Raspbian:
# - sudo usermod -a -G video $USER
# - sudo apt install v4l-utils
# - sudo apt-get install yasm
# On Raspbian:
# - sudo raspi-config nonint do_camera 0

function install_ffmpeg() {
  VERSION=n3.3.2
  echo "Installing ffmpeg $VERSION to leverage Raspberry Pi's OMX h.264 hardware encoder"
  if [ ! -d FFMpeg ]; then
    git clone https://github.com/ffmpeg/FFMpeg -b $VERSION --depth 1
  fi
  cd FFMpeg
  time ./configure --enable-gpl --enable-nonfree --disable-everything \
    --enable-omx --enable-omx-rpi \
    --enable-indev=v4l2 --enable-protocol=pipe \
    --enable-muxer=mp4 --enable-muxer=mpegts --enable-demuxer=mpegts
  #  --enable-demuxer=h264
  # Optional: --enable-mmal
  # --disable-encoders
  # --disable-decoders  --enable-decoder=h264
  # --disable-muxers
  # --disable-demuxers
  # --disable-parsers
  # --disable-bsf
  # --disable-protocols
  # --disable-indevs / --disable-outdevs / --disable-devices
  # --disable-filters --enable-filter=edgedetect

  # Use -j4 on RPi2+
  # - Trimmed RPi Zero  30min
  # - Full    RPi Zero 200min
  time make -j 1 ffmpeg ffprobe
  cd ..
}

function install_mp4box() {
  VERSION=v0.7.1
  echo "Installing MP4box $VERSION to split the MPEG2TS stream into 1 second chunks"
  git clone https://github.com/gpac/gpac -b $VERSION --depth 1
  cd gpac
  time ./configure --disable-opengl --use-js=no --use-ft=no --use-jpeg=no \
    --use-png=no --use-faad=no --use-mad=no --use-xvid=no --use-ffmpeg=no \
    --use-ogg=no --use-vorbis=no --use-theora=no --use-openjpeg=no \
    --static-mp4box
  # RPi Zero: 62min
  time make bin/gcc/MP4Box
  cd ..
}

install_ffmpeg
# install_mp4box
