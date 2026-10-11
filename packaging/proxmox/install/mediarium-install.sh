#!/usr/bin/env bash

# Copyright (c) 2021-2026 community-scripts ORG
# Author: Mediarium
# License: MIT | https://github.com/community-scripts/DevScripts/raw/main/LICENSE
# Source: https://mediarium.app | Github: https://github.com/rdborg/Mediarium

source /dev/stdin <<<"$FUNCTIONS_FILE_PATH"
color
verb_ip6
catch_errors
setting_up_container
network_check
update_os

msg_info "Installing Dependencies"
$STD apt install -y \
  par2 \
  p7zip-full
msg_ok "Installed Dependencies"

fetch_and_deploy_gh_release "mediarium" "rdborg/Mediarium" "prebuild" "latest" "/opt/mediarium" "mediarium_*_linux_$(arch_resolve "amd64" "arm64").tar.gz"

msg_info "Configuring Mediarium"
mkdir -p /opt/mediarium/config /mnt/media/{downloads,movies,tv,music}
cat <<EOF >/opt/mediarium/.env
CONFIG_DIR=/opt/mediarium/config
DOWNLOADS_DIR=/mnt/media/downloads
MOVIES_DIR=/mnt/media/movies
TV_DIR=/mnt/media/tv
MUSIC_DIR=/mnt/media/music
APP_PORT=8264
EOF
msg_ok "Configured Mediarium"

msg_info "Creating Service"
cat <<EOF >/etc/systemd/system/mediarium.service
[Unit]
Description=Mediarium Service
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=root
WorkingDirectory=/opt/mediarium
EnvironmentFile=/opt/mediarium/.env
ExecStart=/opt/mediarium/mediarium
Restart=always
RestartSec=5
TimeoutStopSec=20

[Install]
WantedBy=multi-user.target
EOF
systemctl enable -q --now mediarium
msg_ok "Created Service"

motd_ssh
customize
cleanup_lxc
