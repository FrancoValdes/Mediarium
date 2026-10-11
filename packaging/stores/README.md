# One-click store kits (kept for later)

> **Plan:** Mediarium ships one Docker image for every platform and the docs explain the install for each, so none of these listings is needed for launch. The kits stay here, ready, in case a store is wanted later; nothing in them is promised to users.


Ready-to-submit files for app stores and one-click installers, written for Mediarium 3.0.0. **Nothing here has been submitted**: every store needs a public repository, a published image and the maintainer's own accounts. Until they are live, the docs list these as "coming soon" ([docs/PLATFORMS.md](../../docs/PLATFORMS.md)).

**The order to do things in, and what each store needs from you, is in [PLAYBOOK.md](./PLAYBOOK.md).**

| Store | Folder | How it gets listed | Tested |
|---|---|---|---|
| Unraid Community Applications | [`unraid/`](./unraid/) | Template repo + submit at ca.unraid.net | XML well formed; not loaded on Unraid |
| CasaOS / ZimaOS App Store | [`casaos/`](./casaos/) | Own store repo first (pull request to IceWhaleTech/CasaOS-AppStore is the other route) | YAML parses and `docker compose config` accepts it; not on a device |
| Umbrel | [`umbrel/`](./umbrel/) | Own community store repo (later: pull request to getumbrel/umbrel-apps) | YAML parses; not on a device |
| TrueNAS SCALE | [`truenas/`](./truenas/) | Issue + pull request to truenas/apps (community train), later | YAML parses. The 2.1.0 version was rendered with the catalog's validator and run with a local image; the 3.0.0 changes (new optional UMASK field) were not rendered again; not on TrueNAS |
| Portainer | [`portainer/`](./portainer/) | Users add the template URL; nothing to submit | JSON parses and follows Portainer's documented template format (version 2); not in Portainer |
| Synology | [`synology/`](./synology/) | No store for Docker apps; Container Manager project | Same compose as the tested install |
| Runtipi (optional) | [`runtipi/`](./runtipi/) | Own app store repo | JSON/YAML parse; not on Runtipi |
| Cosmos Cloud (optional) | [`cosmos/`](./cosmos/) | Pull request to azukaar/cosmos-servapps-official | JSON parses; not on Cosmos |
| Proxmox VE | [`../proxmox/`](../proxmox/) | community-scripts, later | Install steps run in a plain Debian 13 container; not on a Proxmox host |

None of these kits has been tried on a real device. They were checked for syntax only (and the TrueNAS one earlier rendered, see above).

## Do these first (needed by every store)

1. **The GitHub repository must be public** (the icon, screenshots, templates and stack files are fetched from `raw.githubusercontent.com/rdborg/Mediarium/main/...`).
2. **Set the release secrets** if official images should ship with the built-in TMDB, OpenSubtitles and Trakt keys: repository secrets `TMDB_API_KEY`, `OPENSUBTITLES_API_KEY`, `TRAKT_CLIENT_ID` (see `.github/workflows/release.yml`). Without them users are asked for their own keys in the setup wizard.
3. **Set up Docker Hub** (recommended, many people search there first): create the public repository `<user>/mediarium`, make an access token, and add the secrets `DOCKERHUB_USERNAME` and `DOCKERHUB_TOKEN`. The steps are in [PLAYBOOK.md](./PLAYBOOK.md). Without the secrets the release goes to ghcr.io only.
4. **Push the `v3.0.0` tag** (after `VERSION` says `3.0.0` and the changelog has its section) so the release workflow publishes `ghcr.io/rdborg/mediarium:3.0.0`, `:3.0` and `:latest` (and `:3.0.0-full`, `:3.0-full` and `:latest-full`) for `linux/amd64` and `linux/arm64`, plus the same tags on Docker Hub when its secrets are set. Check the workflow run is green. The kits use the plain image, not `-full`.
5. **Make the container package public**: GitHub → Packages → `mediarium` → Package settings → Change visibility → Public. (New GHCR packages start private, and every store pulls anonymously.)
6. **Check from a machine that is not logged in to GitHub**:
   ```bash
   docker pull ghcr.io/rdborg/mediarium:latest
   docker buildx imagetools inspect ghcr.io/rdborg/mediarium:3.0.0   # shows amd64 + arm64, and the digest
   ```
7. **Pin digests** where a store requires it (Umbrel, TrueNAS; recommended elsewhere): use the `Digest:` from step 6 as `3.0.0@sha256:...`. The Umbrel and TrueNAS kits have a TODO comment where it goes.

## Variables in the kits

The container variables are listed in [docs/reference/environment.md](../../docs/reference/environment.md) and [docs/INSTALL.md](../../docs/INSTALL.md). The kits set the folders, `PUID`, `PGID` and `TZ`, and offer these as optional extras where the store has a place for them:

- `MUSIC_DIR`, `EBOOKS_DIR`, `AUDIOBOOKS_DIR`: the normal optional folders. Left empty, the app uses its own defaults (`/music`, `/ebooks`, `/audiobooks`).
- `UMASK`: the permissions new files get, for example `022` or `002`. Empty (the default) leaves the container as it is. Added to Unraid, CasaOS, Umbrel, Runtipi, TrueNAS (an optional text field), Portainer and Cosmos.

`URL_BASE` is deliberately not in the kits: the stores' own proxies take care of the address. Any variable can also be given as `NAME_FILE` (a path to a file holding the value); the kits do not use that.

## For every new release

Stores that pin a version (CasaOS, Umbrel, TrueNAS, Runtipi) need their version, image tag and digest bumped, usually by a small pull request to the store. Unraid, Portainer, Synology, Cosmos and Proxmox follow `latest` and need nothing.

## When a store goes live

Update `docs/PLATFORMS.md` (move the row from "Coming soon" to "Available now"), the table in `docs/INSTALL.md` (Using a Docker app), the README's platform table, and `CHANGELOG.md`.
