# Proxmox VE (community-scripts)

Scripts in the format of the [community-scripts](https://community-scripts.org) Proxmox VE helpers: one command on the Proxmox host makes a Debian 13 LXC container and installs Mediarium in it, natively (no Docker), as a systemd service.

| File | What it does |
|---|---|
| `ct/mediarium.sh` | Runs on the Proxmox host: makes the container (2 cores, 2 GB RAM, 8 GB disk, unprivileged, amd64 and arm64) and has the `update` entry that installs a newer release |
| `install/mediarium-install.sh` | Runs inside the container: installs par2 and 7-Zip, the latest release from GitHub into `/opt/mediarium`, writes `/opt/mediarium/.env` and the `mediarium` service |
| `json/mediarium.json` | The listing on the community-scripts website |

What it sets up:

- The program in `/opt/mediarium`, settings and database in `/opt/mediarium/config` (kept across updates).
- Media folders under `/mnt/media` (`downloads`, `movies`, `tv`, `music`). Bind-mount your storage there, or change the paths in `/opt/mediarium/.env` and run `systemctl restart mediarium`.
- The web page on port 8264. The first visit creates the administrator account.
- The built-in VPN needs no special container permissions, so the container stays unprivileged.

The listing text describes Mediarium 3 (movies, TV, music, ebooks and audiobooks). The scripts install the latest release, so they need no version change. New scripts go to community-scripts only after a request and only for projects that meet their rules: see [../stores/PLAYBOOK.md](../stores/PLAYBOOK.md).

Status: **prepared, not submitted.** The install steps were run in a plain Debian 13 container (release download, par2 and 7-Zip, the settings file, the program starting from it); the scripts pass shellcheck. They still need a run on a real Proxmox host through the community-scripts tools, which is what their testing repository (DevScripts) is for, before they are submitted.
