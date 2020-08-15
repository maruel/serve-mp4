#!/bin/bash
# Copyright 2017 Marc-Antoine Ruel. All rights reserved.
# Use of this source code is governed under the Apache License, Version 2.0
# that can be found in the LICENSE file.

# See for full info:
# https://trac.ffmpeg.org/wiki/CompilationGuide
# https://trac.ffmpeg.org/wiki/CompilationGuide/Ubuntu

set -eu

cd "$(dirname $0)"

# On non-Raspbian:
# - sudo usermod -a -G video $USER
# - sudo apt install v4l-utils
# - sudo apt-get install yasm
# On Raspbian:
# - sudo raspi-config nonint do_camera 0
function prerequisites {
  sudo apt install \
      autoconf \
      asciidoc \
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

# The function fetches or clone the repository, then cd's into it.
function checkout_or_fetch {
  if [ -d $2 ]; then
    cd $2
    git fetch
  else
    git clone $1 $2
    cd $2
  fi
}

function install_nasm {
  if command -v nasm >/dev/null 2>&1 ; then
    echo "- Found $(nasm -v)"
    #return 0
  fi
  checkout_or_fetch git://repo.or.cz/nasm.git nasm
  # Use the latest release as nasm uses proper git tag.
  # 'nasm-2.13.01' as of this writting.
  git checkout $(git tag | grep '^nasm-' | grep -v rc | sort -h | tail -n 1)
  ./autogen.sh
  ./configure
  make -j all manpages
  # TODO(maruel): Install locally.
  sudo make install
  hash -r
  cd ..
  echo "- Installed $(nasm -v)"
}

function install_x264 {
  if [ -f /usr/local/lib/libx264.so ]; then
    echo "- Found x264"
    #return 0
  fi
  checkout_or_fetch git://git.videolan.org/x264.git x264
  # x264 doesn't use git tag, so ¯\_(ツ)_/¯. 'stable' is a bit old.
  # git log origin/stable..origin/master
  # -b stable
  # Hardcode so the build process is reproducible.
  # 7d0ff22e8 is from Jan 2018.
  git checkout 7d0ff22e8c96de126be9d3de4952edd6d1b75a8c
  ./configure --enable-static --enable-shared
  make -j
  # TODO(maruel): Install locally.
  sudo make install
  sudo ldconfig
  cd ..
  echo "- Installed x264"
}

function install_ffmpeg {
  if command -v ffmpeg >/dev/null 2>&1 ; then
    if command -v ffprobe >/dev/null 2>&1 ; then
      echo "- Found $(ffmpeg -version | head -n 1)"
      #return 0
    fi
  fi
  checkout_or_fetch https://github.com/ffmpeg/FFMpeg FFMpeg
  # Use the latest release as FFMpeg uses proper git tag.
  # 'n4.0.2' as of this writing.
  git checkout $(git tag | grep -v dev | grep '^n' | sort -h | tail -n 1)

  # List of ./configure flags:
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

  # TODO(maruel): Detect Raspbian.
  if false; then
    # On Raspbian, we want to use the OMX encoder for performance and strip the
    # compile as much as possible because it is very slow.
    ./configure --enable-gpl \
      --enable-nonfree \
      --disable-everything \
      --enable-omx \
      --enable-omx-rpi \
      --enable-indev=v4l2 \
      --enable-protocol=pipe \
      --enable-muxer=mp4 \
      --enable-muxer=mpegts \
      --enable-demuxer=mpegts
  else
    ./configure --enable-gpl \
        --enable-nonfree \
        --pkg-config-flags="--static" \
        --disable-ffplay \
        --disable-doc \
        --enable-libx264
    # --disable-network
    # --disable-all
  fi

  # make -j ffmpeg ffprobe ?
  make -j
  # TODO(maruel): Install locally.
  sudo make install
  hash -r
  cd ..
  echo "- Installed FFMpeg"
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

prerequisites
#clean
install_nasm
install_x264
install_ffmpeg
# install_mp4box

echo "- Success!"
ffmpeg -version
