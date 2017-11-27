#!/bin/bash
# Copyright 2017 Marc-Antoine Ruel. All rights reserved.
# Use of this source code is governed under the Apache License, Version 2.0
# that can be found in the LICENSE file.

# See for full info:
# https://trac.ffmpeg.org/wiki/CompilationGuide
# https://trac.ffmpeg.org/wiki/CompilationGuide/Ubuntu

set -eu

cd "$(dirname $0)"

function prerequisites {
  sudo apt install autoconf \
      automake \
      build-essential \
      cmake \
      yasm
  # These are always certainly too old.
  sudo apt -y remove libx264-dev nasm
}

function clean {
  rm -rf FFMpeg nasm x264
}

function install_nasm {
  if command -v nasm >/dev/null 2>&1 ; then
    echo "- Found $(nasm -v)"
    return 0
  fi
  git clone git://repo.or.cz/nasm.git
  cd nasm
  # Use the latest release as nasm uses proper git tag.
  # 'nasm-2.13.01' as of this writting.
  git checkout $(git tag | grep '^nasm-' | grep -v rc | sort -h | tail -n 1)
  ./autogen.sh
  ./configure
  make -j
  sudo make install
  hash -r
  cd ..
  echo "- Installed $(nasm -v)"
}

function install_x264 {
  if [ -f /usr/local/lib/libx264.so ]; then
    echo "- Found x264"
    return 0
  fi
  git clone git://git.videolan.org/x264.git
  cd x264
  # x264 doesn't use git tag, so ¯\_(ツ)_/¯. 'stable' is a bit old.
  # git log origin/stable..origin/master
  # -b stable
  # This checkout dates from June 2017 and seems to work. Hardcode so the build
  # process is reproducible.
  git checkout ba24899b0bf23345921da022f7a51e0c57dbe73d
  ./configure --enable-static --enable-shared
  make -j
  sudo make install
  sudo ldconfig
  cd ..
  echo "- Installed x264"
}

function install_ffmpeg {
  if command -v ffmpeg >/dev/null 2>&1 ; then
    if command -v ffprobe >/dev/null 2>&1 ; then
      echo "- Found FFMpeg"
      return 0
    fi
  fi
  git clone https://github.com/ffmpeg/FFMpeg
  cd FFMpeg
  # Use the latest release as FFMpeg uses proper git tag.
  # 'n3.4' as of this writting.
  git checkout $(git tag | grep -v dev | grep '^n' | sort -h | tail -n 1)

  # TODO(maruel): On Raspbian, we want to use the OMX encoder for performance
  # and strip the compile as much as possible because it is very slow.
  #--disable-everything \
  #--enable-omx --enable-omx-rpi \
  #--enable-indev=v4l2 --enable-protocol=pipe \
  #--enable-muxer=mp4 --enable-muxer=mpegts --enable-demuxer=mpegts

  ./configure --enable-gpl \
      --enable-nonfree \
      --pkg-config-flags="--static" \
      --disable-ffplay \
      --disable-ffserver \
      --disable-doc \
      --enable-libx264
  # --disable-network
  # --disable-all

  # --list-decoders          show all available decoders
  # --list-encoders          show all available encoders
  # --list-hwaccels          show all available hardware accelerators
  # --list-demuxers          show all available demuxers
  # --list-muxers            show all available muxers
  # --list-parsers           show all available parsers
  # --list-protocols         show all available protocols
  # --list-bsfs              show all available bitstream filters
  # --list-indevs            show all available input devices
  # --list-outdevs           show all available output devices
  # --list-filters           show all available filters
  make -j
  sudo make install
  hash -r
  cd ..
  echo "- Installed FFMpeg"
}

#prerequisites
#clean
install_nasm
install_x264
install_ffmpeg

echo "- Success!"
ffmpeg -version
