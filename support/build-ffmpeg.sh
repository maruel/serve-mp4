#!/bin/bash
# Copyright 2017 Marc-Antoine Ruel. All rights reserved.
# Use of this source code is governed under the Apache License, Version 2.0
# that can be found in the LICENSE file.

# See for full info:
# https://trac.ffmpeg.org/wiki/CompilationGuide
# https://trac.ffmpeg.org/wiki/CompilationGuide/Ubuntu
# Took inspiration from
# https://github.com/alicemara/ffmpegcompileqsvav1/blob/main/ffmpegcompileqsvav1.sh

set -eu

cd "$(dirname $0)"

OUT="$HOME/ffmpeg_build"

# On non-Raspbian:
# - sudo usermod -a -G video $USER
# - sudo apt install v4l-utils
# - sudo apt install libvpl-dev
# - sudo apt-get install yasm
# On Raspbian:
# - sudo raspi-config nonint do_camera 0
function prerequisites {
  sudo apt remove ffmpeg

  sudo apt install \
    --no-install-recommends \
    autoconf \
    asciidoc \
    automake \
    build-essential \
    cmake \
    meson \
    nasm \
    ninja-build \
    pkg-config \
    texinfo \
    wget \
    yasm

  # These are always certainly too old but let's roll with it for now.
  #sudo apt -y remove libx264-dev nasm

  sudo apt install \
    --no-install-recommends \
    libaom-dev \
    libass-dev \
    libdav1d-dev \
    libfdk-aac-dev \
    libfreetype6-dev \
    libgnutls28-dev \
    libmp3lame-dev \
    libnuma-dev \
    libopus-dev \
    libsdl2-dev \
    libtool \
    libunistring-dev \
    libva-dev \
    libvdpau-dev \
    libvorbis-dev \
    libvpl2 \
    libvpl-dev \
    libvpx-dev \
    libx264-dev \
    libx265-dev \
    libxcb-shm0-dev \
    libxcb-xfixes0-dev \
    libxcb1-dev \
    zlib1g-dev
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
  ./configure \
    --bindir="$HOME/bin" \
    --prefix="$OUT" \
    --pkg-config-flags="--static" \
    --extra-cflags="-I$OUT/include" \
    --extra-ldflags="-L$OUT/lib"
  make -j all manpages
  make install
  hash -r
  cd ..
  echo "- Installed $(nasm -v)"
}

function install_x264 {
  if [ -f /usr/local/lib/libx264.so ]; then
    echo "- Found x264"
    #return 0
  fi
  checkout_or_fetch https://code.videolan.org/videolan/x264.git x264
  # x264 doesn't use git tag, so ¯\_(ツ)_/¯. 'stable' is a bit old.
  # git log origin/stable..origin/master
  # Hardcode so the build process is reproducible.
  # This is branch "stable" as of 2023-12-30:
  git checkout -b 31e19f92f00c7003fa115047ce50978bc98c3a0d
  ./configure \
    --bindir="$HOME/bin" \
    --prefix="$OUT" \
    --pkg-config-flags="--static" \
    --extra-cflags="-I$OUT/include" \
    --extra-ldflags="-L$OUT/lib" \
    --enable-static \
    --enable-shared
  make -j
  make install
  cd ..
  echo "- Installed x264"
}

function install_aom {
  checkout_or_fetch https://aomedia.googlesource.com/aom aom
  mkdir -p aom_build
  cd aom_build
  PATH="$HOME/bin:$PATH" cmake -G "Unix Makefiles" \
    -DCMAKE_INSTALL_PREFIX="$OUT" -DENABLE_TESTS=OFF \
    -DENABLE_NASM=on ../aom
  PATH="$HOME/bin:$PATH" make && make install
  cd ..
  echo "- Installed aom"
}

function install_av1 {
  checkout_or_fetch https://gitlab.com/AOMediaCodec/SVT-AV1.git SVT-AV1
  # Recent tag as of 2013-12.
  git checkout v1.8.0
  mkdir -p build
  cd build
  PATH="$HOME/bin:$PATH" cmake -G "Unix Makefiles" \
    -DCMAKE_INSTALL_PREFIX="$OUT" -DCMAKE_BUILD_TYPE=Release \
    -DBUILD_DEC=OFF -DBUILD_SHARED_LIBS=OFF ..
  PATH="$HOME/bin:$PATH" make
  make install
  cd ..
  echo "- Installed av1"
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
  # 'n6.1' as of this writing.
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
    ./configure \
      --bindir="$HOME/bin" \
      --prefix="$OUT" \
      --enable-gpl \
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
    # Don't enable too much to improve performance.
    ./configure \
      --bindir="$HOME/bin" \
      --prefix="$OUT" \
      --pkg-config-flags="--static" \
      --extra-cflags="-I$OUT/include" \
      --extra-ldflags="-L$OUT/lib" \
      --extra-libs="-lpthread -lm" \
      --ld="g++" \
      --disable-ffplay \
      --disable-doc \
      --enable-gnutls \
      --enable-gpl \
      --enable-libaom \
      --enable-libass \
      --enable-libdav1d \
      --enable-libfdk-aac \
      --enable-libfreetype \
      --enable-libmp3lame \
      --enable-libopus \
      --enable-libvorbis \
      --enable-libvpl \
      --enable-libvpx \
      --enable-libx264 \
      --enable-libx265 \
      --enable-nonfree
      # TODO(maruel): Fix
      #--enable-libsvtav1
    # --disable-network
    # --disable-all
  fi

  # make -j ffmpeg ffprobe ?
  make -j
  make install
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
#install_nasm
#install_x264
#install_aom
install_av1
install_ffmpeg
# install_mp4box

echo "- Success!"
ffmpeg -version
