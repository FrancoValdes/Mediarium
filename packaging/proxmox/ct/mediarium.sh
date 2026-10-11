#!/usr/bin/env bash
_CS_DEFAULT_URL="https://raw.githubusercontent.com/community-scripts/DevScripts/main"
_cs_boot="${COMMUNITY_SCRIPTS_CORE_DIR:-$(dirname "${BASH_SOURCE[0]}")/../../core}/core/build.func"
source "$_cs_boot" 2>/dev/null || source <(curl -fsSL "${COMMUNITY_SCRIPTS_CORE_URL:-https://raw.githubusercontent.com/community-scripts/core/main}/core/build.func")

# Copyright (c) 2021-2026 community-scripts ORG
# Author: Mediarium
# License: MIT | https://github.com/community-scripts/DevScripts/raw/main/LICENSE
# Source: https://mediarium.app | Github: https://github.com/rdborg/Mediarium

APP="Mediarium"
var_tags="${var_tags:-arr;media;usenet;torrent}"
var_cpu="${var_cpu:-2}"
var_ram="${var_ram:-2048}"
var_disk="${var_disk:-8}"
var_os="${var_os:-debian}"
var_version="${var_version:-13}"
var_arm64="${var_arm64:-yes}"
var_unprivileged="${var_unprivileged:-1}"

header_info "$APP"
variables
color
catch_errors

function update_script() {
  header_info
  check_container_storage
  check_container_resources

  if [[ ! -f /opt/mediarium/mediarium ]]; then
    msg_error "No Mediarium Installation Found!"
    exit
  fi

  if check_for_gh_release "mediarium" "rdborg/Mediarium"; then
    msg_info "Stopping Mediarium"
    systemctl stop mediarium
    msg_ok "Stopped Mediarium"

    create_backup /opt/mediarium/.env

    CLEAN_INSTALL=1 CLEAN_INSTALL_KEEP="config" fetch_and_deploy_gh_release "mediarium" "rdborg/Mediarium" "prebuild" "latest" "/opt/mediarium" "mediarium_*_linux_$(arch_resolve "amd64" "arm64").tar.gz"

    restore_backup

    msg_info "Starting Mediarium"
    systemctl start mediarium
    msg_ok "Started Mediarium"
    msg_ok "Updated successfully!"
  fi
  exit
}

start
build_container
description

msg_ok "Completed successfully!\n"
echo -e "${CREATING}${GN}${APP} setup has been successfully initialized!${CL}"
echo -e "${INFO}${YW}Access it using the following URL:${CL}"
echo -e "${GATEWAY}${BGN}http://${IP}:8264${CL}"
