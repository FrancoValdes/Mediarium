# Packaging

| Folder | What | Status |
|---|---|---|
| [`stores/`](./stores/) | Ready-to-submit kits for Unraid, CasaOS/ZimaOS, Umbrel, TrueNAS, Portainer, Synology, Runtipi and Cosmos | Prepared for 3.0.0, not submitted: see [stores/README.md](./stores/README.md) and the step-by-step [stores/PLAYBOOK.md](./stores/PLAYBOOK.md) |
| [`proxmox/`](./proxmox/) | community-scripts style install for Proxmox VE (an LXC container with Mediarium as a native service) | Prepared, not submitted (a "later" item in the playbook): see [proxmox/README.md](./proxmox/README.md) |
| [`systemd/`](./systemd/) | systemd unit and environment file for the native Linux program | **Coming soon.** Draft, not tested |
| `README-INSTALL.txt` | Notes packed into the native release archives | Explains that the native programs are a preview |

To get Mediarium into the app stores and directories, follow [stores/PLAYBOOK.md](./stores/PLAYBOOK.md): it lists the order, what you provide for each one and what is ready. None of the kits has been tried on a real device yet.

The supported install today is Docker: see [docs/INSTALL.md](../docs/INSTALL.md). The native Linux files are kept here so they can be finished and tested; they are not offered to users yet. There are no Windows or macOS versions: on those, run Mediarium in Docker Desktop ([docs/PLATFORMS.md](../docs/PLATFORMS.md)).
