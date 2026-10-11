# Maintainer playbook: getting Mediarium into app stores and directories

A step-by-step list, in the order I would do it, for Mediarium 3.0.0 (released Sunday 2026-10-11). The research behind it was done on 2026-10-09; store rules change, so read each store's own page again before you submit.

The kits it refers to are in the folders next to this file. The common setup is in [README.md](./README.md) ("Do these first"). Nothing below has been done yet.

## What has been tested

Honest summary: **no kit has been tried on a real device.** Every kit was checked for syntax only.

| Kit | What was checked | Not checked |
|---|---|---|
| Unraid template | XML parses | Loading it on Unraid, the CA validator |
| CasaOS / ZimaOS | YAML parses, `docker compose config` accepts it | Installing on a device, the store's build script |
| Umbrel | YAML parses | Installing on umbrelOS, the store's lint |
| TrueNAS | YAML parses. The 2.1.0 version was rendered with the catalog's validator and run with a local image | Rendering the 3.0.0 version with the new UMASK field, the catalog's `ci.py`, a real TrueNAS |
| Portainer | JSON parses, matches Portainer's documented template format (version 2, type 3 stack), every variable in the stack file has a form field, `docker compose config` accepts the stack | Adding the URL in a real Portainer |
| Runtipi | JSON and YAML parse, `docker compose config` accepts the compose file | Installing in Runtipi, the template's schema check |
| Cosmos | JSON parses | The market's `npm run validate`, a real Cosmos server |
| Synology | `docker compose config` accepts it | A real DSM (the guide in docs/synology.md is the route) |
| Proxmox | Install steps ran in a plain Debian 13 container; shellcheck passed earlier | A real Proxmox host through the community-scripts tools |

## Before any store: the release itself

1. `VERSION` says `3.0.0`, `CHANGELOG.md` has its section, the tag `v3.0.0` is pushed ([docs/RELEASING.md](../../docs/RELEASING.md)).
2. The release run is green and publishes `ghcr.io/rdborg/mediarium:3.0.0`, `:3.0` and `:latest` for amd64 and arm64.
3. The GitHub repository and the GHCR package are public. Check from a machine that is not signed in: `docker pull ghcr.io/rdborg/mediarium:3.0.0`.
4. Write down the multi-arch digest (`docker buildx imagetools inspect ghcr.io/rdborg/mediarium:3.0.0`, the `Digest:` line). Umbrel and TrueNAS want `3.0.0@sha256:...`; the kits have a TODO where it goes.

Every store below is optional. Docker and the compose file in [docs/INSTALL.md](../../docs/INSTALL.md) keep working whatever happens here.

## The order

| # | What | When |
|---|---|---|
| 1 | Docker Hub | At launch, before the release if you can |
| 2 | Portainer template | At launch (nothing to submit) |
| 3 | Unraid Community Applications | At launch or the week after |
| 4 | Umbrel community store | At launch or soon after |
| 5 | CasaOS / ZimaOS own store | Soon after |
| 6 | Runtipi custom store | Soon after, if anyone asks |
| 7 | Cosmos pull request | Soon after |
| 8 | Directories: AlternativeTo, noted.lol, selfh.st | At launch |
| 9 | Synology | Docs only (already there) |
| later | Umbrel official store, TrueNAS, Proxmox, awesome-selfhosted, a Synology package, AUR | See each section |
| no | Helm / Artifact Hub, Flatpak, Snap, Homebrew, winget | Not planned |

After any store goes live, the same four places need a line: `docs/PLATFORMS.md` (move the row from "Coming soon" to "Available now"), `docs/INSTALL.md` (the "Using a Docker app" table), the platform table in the top-level `README.md`, and `CHANGELOG.md`. Each section below only adds what is special for it.

---

## 1. Docker Hub

**Why:** many people search Docker Hub first, and some tools only look there.

**You provide:**
1. A Docker Hub account, and the public repository `<user>/mediarium` (Create repository, visibility Public). The free plan allows unlimited public repositories.
2. An access token: Account settings > Personal access tokens > Generate new token, permission **Read, Write, Delete** (Delete is needed so the workflow can refresh the page text).
3. In the GitHub repository (Settings > Secrets and variables > Actions): the secret `DOCKERHUB_TOKEN` (the token) and the secret `DOCKERHUB_USERNAME` (the user name). For an organisation, also set the repository variable `DOCKERHUB_IMAGE` to `<org>/mediarium`.

**Ready in this repo:** the release workflow ([`.github/workflows/release.yml`](../../.github/workflows/release.yml)) pushes the same tags to Docker Hub when both secrets exist, and on a stable release refreshes the Docker Hub page text. Without the secrets nothing changes.

**Notes:** the Docker-Sponsored Open Source programme needs public source, public images and no path to commercialisation. Mediarium has no affiliate links and no paid tier, so it may qualify; apply at docker.com/community/open-source/application once the Hub repository has its first image.

**After it is live:** pull `docker.io/<user>/mediarium:3.0.0` from a signed-out machine; mention Docker Hub as a second place to pull the image in `docs/INSTALL.md` and `docs/PLATFORMS.md`.

## 2. Portainer

**How it works:** there is no store to submit to. Users paste a template file URL into Settings > App Templates.

**You provide:** the repository being public, and (optional) a copy of `templates.json` on the website. The raw GitHub URL works too: `https://raw.githubusercontent.com/rdborg/Mediarium/main/packaging/stores/portainer/templates.json`.

**Ready:** [`portainer/templates.json`](./portainer/templates.json) (format version 2, one type 3 stack entry) and the stack file [`portainer/docker-compose.yml`](./portainer/docker-compose.yml). They follow `latest`, so they need no change per release. Optional folders and `UMASK` are in the form.

**Before announcing:** try it once in a Portainer 2.x install (Settings > App Templates, then Templates > Application > Mediarium). Note that this URL replaces Portainer's own template list; [portainer/README.md](./portainer/README.md) shows the custom-template route that does not.

**After:** one line in `docs/INSTALL.md` (Using a Docker app) with the URL, plus the usual four places.

## 3. Unraid Community Applications

**You provide:**
1. A **separate public repo** made from the starter [github.com/unraid/unraid-community-apps-starter](https://github.com/unraid/unraid-community-apps-starter) (the kit assumes the name `rdborg/unraid-templates`; if you pick another, change `<TemplateURL>` in the template).
2. Your own icon in it. **The starter's placeholder icon is rejected.** The kit already points at `branding/mediarium-icon-512.png`; check the URL opens in a private window.
3. A CA account to submit.
4. A support thread in the Unraid forum (Docker Containers section, "[Support] Mediarium"). Recommended, not required. If you make one, put its URL in `<Support>` and add `<Forum>` to `ca_profile.xml`.

**Ready:** [`unraid/templates/mediarium.xml`](./unraid/templates/mediarium.xml) and [`unraid/ca_profile.xml`](./unraid/ca_profile.xml), with the music, ebooks, audiobooks and UMASK variables in Advanced View. Follows `latest`.

**Steps:** copy the two files into the new repo (with an OSI `LICENSE`), test on a real Unraid server ([unraid/README.md](./unraid/README.md) says how), then validate and submit at [ca.unraid.net/submit/new](https://ca.unraid.net/submit/new).

**After it is live:** `docs/unraid.md` (replace "coming soon" in the first paragraph with "Apps tab, search Mediarium") and the usual four places.

## 4. Umbrel: community store first, official store later

**Community store (do this first):**
- You provide: a public repo made from [getumbrel/umbrel-community-app-store](https://github.com/getumbrel/umbrel-community-app-store) (the kit assumes `rdborg/umbrel-app-store`), the image pinned by digest, and a test on a real umbrelOS device.
- Ready: [`umbrel/`](./umbrel/) laid out as the repo root (`umbrel-app-store.yml`, `rdborg-mediarium/umbrel-app.yml`, compose file with `app_proxy`). Version is `3.0.0`.
- Users add it with App Store > the three dots (top right) > Community App Stores > paste the repo URL.
- After: the store URL in `docs/INSTALL.md` and `docs/PLATFORMS.md`, plus the usual four places.

**Official store (later):** a pull request to [getumbrel/umbrel-apps](https://github.com/getumbrel/umbrel-apps) once the community store has had real users. It needs the pinned digest and a test on a real umbrelOS; the steps are in [umbrel/README.md](./umbrel/README.md). Every release then needs a small update pull request.

## 5. CasaOS / ZimaOS

**What is known:** there is no confirmed submission route for the official ZimaOS store, so start with your own store.

**You provide:** a public store repo in the v2 format (apps in `Apps/<Name>/`), built into the distributable form with `./scripts/build_dist.sh` or with the GitHub Action [IceWhaleTech/build-appstore-action](https://github.com/IceWhaleTech/build-appstore-action), and published on static HTTPS (GitHub Pages works). Then test on a real CasaOS or ZimaOS device.

**Ready:** [`casaos/Apps/Mediarium/`](./casaos/Apps/Mediarium/) (compose file with the `x-casaos` block, icon, thumbnail, three screenshots), version `3.0.0`, `update_at` set to the release date. The icon and screenshot URLs in it point at the official store's CDN path; for your own store, change them to your store's address.

**After:** the store URL in `docs/INSTALL.md` and `docs/PLATFORMS.md`, plus the usual four places. For each release, bump `image`, `version` and `update_at`.

## 6. Runtipi (optional)

**What is known:** the official Runtipi store no longer accepts new apps. Use a custom store.

**You provide:** a store repo made from [runtipi/example-appstore](https://github.com/runtipi/example-appstore), and a test on a Runtipi server.

**Ready:** [`runtipi/apps/mediarium/`](./runtipi/apps/mediarium/), version `3.0.0`. Set `created_at` and `updated_at` to the real publish time. For each release bump `version`, `tipi_version` (+1), `updated_at` and the image tag.

**After:** the store URL in the docs, plus the usual four places.

## 7. Cosmos Cloud (optional)

**You provide:** a pull request to [azukaar/cosmos-servapps-official](https://github.com/azukaar/cosmos-servapps-official) with the `Mediarium` folder, after running `npm run validate` in your fork, and a test on a Cosmos server.

**Ready:** [`cosmos/servapps/Mediarium/`](./cosmos/servapps/Mediarium/). Follows `latest`. The kit follows the format as it was when prepared: compare with a current app (for example Sonarr) first.

**After:** the usual four places.

## 8. Directories (at launch)

None of these needs a build, only your own words and links.
- **AlternativeTo:** suggest a new application, and mark it as an alternative to Radarr, Sonarr, Prowlarr and SABnzbd.
- **noted.lol** and **selfh.st:** contact the maintainers (each has a contact or submission page); give them the repo, the website and one screenshot.
- Keep the replies in your own voice ("I", everyday wording).

## 9. Synology

**Fact:** Package Center only installs `.spk` files. There is no store for Docker apps, so the Container Manager project in [docs/synology.md](../../docs/synology.md) is the route, and it is already documented. Nothing to submit.

**Later, only if there is demand:**
- **SynoCommunity** builds native packages with [spksrc](https://github.com/SynoCommunity/spksrc). A Go package is a small Makefile like `spk/syncthing`. One package per pull request, tested on real hardware, and ask on their Discord first.
- **A package source of our own** means a web endpoint that answers Package Center's requests (parameters `arch`, `major`, `minor`, `build`, `package_update_channel`, `language`, `timezone`) with JSON `{packages:[...], keyrings:[...]}`, plus signed `.spk` files, the way SynoCommunity's spkrepo and [jdel/sspks](https://github.com/jdel/sspks) do.
- Both need a tested native build for each Synology CPU family, so both are **later**.

## Later

**TrueNAS (later, and strict).** An issue or draft pull request to [truenas/apps](https://github.com/truenas/apps). You run their `ci.py` test, the app must run as the non-root user 568, and the icon and screenshots are uploaded by the reviewer. You provide the pinned digest and a TrueNAS test. Ready: [`truenas/`](./truenas/) (the 3.0.0 version with the optional UMASK field has not been rendered with their validator yet). The catalog wants a unique default web port; steps in [truenas/README.md](./truenas/README.md). Until then TrueNAS users paste the compose file (Install via YAML).

**Proxmox (later).** New scripts go to community-scripts/DevScripts after a request on the ProxmoxVE Discussions page. They ask for a project with 600 or more stars that is older than six months and has release tarballs. Ready: [`../proxmox/`](../proxmox/) (scripts and listing JSON). You provide the request and a test on a real Proxmox host.

**awesome-selfhosted (about February 2027 at the earliest).** The project needs a tagged release at least four months old, and you must write the pull request yourself (the file is `software/mediarium.yml` in the awesome-selfhosted-data repository). With a 3.0.0 release in October 2026, that is February 2027.

**AUR `mediarium-bin` (optional).** A package for Arch users built from the release tarball. Only if people ask.

## Not planned

- **Helm chart / Artifact Hub:** not planned.
- **Flatpak, Snap, Homebrew, winget:** no. Mediarium is a server program for Docker; there are no Windows or macOS versions.

## The parts only the maintainer can do

- Accounts and secrets: Docker Hub, the GitHub secrets, a CA login, forum posts, store repos under your GitHub account.
- Testing on real devices (Unraid, Umbrel, CasaOS/ZimaOS, TrueNAS, Runtipi, Cosmos, Portainer, Proxmox, Synology).
- Pinning the digests after the release and pasting them into the Umbrel and TrueNAS kits.
- Writing your own pull requests and replies to directories and reviewers.
