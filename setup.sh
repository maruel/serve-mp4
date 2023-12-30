#!/bin/bash
# Copyright 2017 Marc-Antoine Ruel. All rights reserved.
# Use of this source code is governed under the Apache License, Version 2.0
# that can be found in the LICENSE file.

set -eu

if [ $# != 2 ]; then
  echo "usage: setup.sh <root> <bind>"
  echo ""
  echo "Example:"
  echo "  ./setup.sh ~/Video :7899"
  exit 1
fi

cd "$(dirname $0)"

go install -v ./cmd/...

BIN="$(which serve-mp4)"
CMD="$BIN -root $1 -http $2"

mkdir -p ~/.config/systemd/user
cat > ~/.config/systemd/user/serve-mp4.service <<EOF
# https://github.com/maruel/serve-mp4
[Unit]
Description=Runs serve-mp4 automatically upon boot
Wants=network-online.target
After=network-online.target

[Service]
KillMode=mixed
Restart=always
TimeoutStopSec=600s
ExecStart=${CMD}
Environment=GOTRACEBACK=all
#AmbientCapabilities=CAP_NET_BIND_SERVICE

[Install]
WantedBy=default.target
EOF

systemctl --user daemon-reload
systemctl --user enable serve-mp4.service
systemctl --user restart serve-mp4.service
journalctl --user -f -u serve-mp4.service
