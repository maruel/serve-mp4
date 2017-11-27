#!/bin/bash
# Copyright 2017 Marc-Antoine Ruel. All rights reserved.
# Use of this source code is governed under the Apache License, Version 2.0
# that can be found in the LICENSE file.

set -eu

if [ $# != 2 ]; then
  echo "usage: setup.sh <root> <bind>"
  exit 1
fi

cd "$(dirname $0)"

go install -v ./cmd/...

AS_USER=${USER}
BIN="$(which serve-mp4)"
CMD="$BIN -root $1 -http $2"

sudo tee /etc/systemd/system/serve-mp4.service > /dev/null <<EOF
# https://github.com/maruel/serve-mp4
[Unit]
Description=Runs serve-mp4 automatically upon boot
Wants=network-online.target
After=network-online.target

[Service]
User=${AS_USER}
Group=${AS_USER}
KillMode=mixed
Restart=always
TimeoutStopSec=600s
ExecStart=${CMD}
Environment=GOTRACEBACK=all
# Systemd 229:
AmbientCapabilities=CAP_NET_BIND_SERVICE
# Systemd 228 and below:
#SecureBits=keep-caps
#Capabilities=cap_net_bind_service+pie
# Older systemd:
#PermissionsStartOnly=true
#ExecStartPre=/sbin/setcap 'cap_net_bind_service=+ep' /home/${AS_USER}/go/bin/dlibox
# High priority stuff:
# Nice=-20
# IOSchedulingClass=realtime
# IOSchedulingPriority=0
# CPUSchedulingPolicy=rr
# CPUSchedulingPriority=99
# CPUSchedulingResetOnFork=true

[Install]
WantedBy=default.target
EOF

sudo systemctl daemon-reload
sudo systemctl enable serve-mp4.service
sudo systemctl start serve-mp4.service
