# Connecting apps made for Radarr, Sonarr and Lidarr

Many apps were made to work with Radarr, Sonarr and Lidarr: request apps (Overseerr, Jellyseerr), dashboards (Homepage, Homarr) and phone apps (nzb360, LunaSea, Ruddarr). Mediarium answers the same requests, so you can add it in those apps as if it were Radarr (for movies), Sonarr (for TV) or Lidarr (for music).

The page **Settings > Connections > Other apps** shows the addresses to use and makes the key.

## What to fill in

Each app gets its own address, because Radarr and Sonarr use the same paths:

| Add Mediarium as | URL base | Full address (example) |
|---|---|---|
| Radarr (movies) | `/radarr` | `http://192.168.1.20:8264/radarr` |
| Sonarr (TV) | `/sonarr` | `http://192.168.1.20:8264/sonarr` |
| Lidarr (music) | `/lidarr` | `http://192.168.1.20:8264/lidarr` |

- **Host and port** are the ones you open Mediarium at. Inside Docker, use the container name or the NAS address, not `localhost`.
- **URL base** is `/radarr`, `/sonarr` or `/lidarr`. Apps that ask for one full address take the full address instead.
- **API key**: an administrator's Mediarium API key. Press **Make a key for other apps** on the Other apps page, or make one under **Accounts > API keys**. The apps send it as the `X-Api-Key` header or the `apikey` parameter; both work. A key of a basic account is refused.

Each part only answers while its media type is switched on (**Settings > Media types**). Lidarr, for example, answers once music is on.

If Mediarium sits behind a reverse proxy under a sub-path (the `URL_BASE` setting, see [INSTALL.md](./INSTALL.md#running-under-a-sub-path-url-base)), put that path in front: the URL base becomes `/mediarium/radarr` and the full address `https://home.example.com/mediarium/radarr`. The Other apps page shows them that way.

## Overseerr and Jellyseerr

1. In Overseerr go to **Settings > Services > Add Radarr Server**. Fill in the host, port, URL base `/radarr` and the API key, then press **Test**.
2. Pick a **Quality Profile** (Mediarium's quality profiles are listed) and any **Root Folder**. Mediarium decides the folder itself.
3. Do the same under **Add Sonarr Server** with URL base `/sonarr`. Choose **English** as the language profile when it asks.

From then on, an approved request adds the title to your library, with the seasons that were asked for monitored and the others not. When Overseerr asks for a search, Mediarium searches straight away, like **Search now**. Overseerr's tags (for example who asked) become Mediarium tags on the title. Overseerr shows what's downloading from Mediarium's queue, and marks a title available once it's downloaded.

## Dashboards and phone apps

- **Homepage**: add a `radarr`, `sonarr` or `lidarr` widget with `url: http://<mediarium>:8264/radarr` (or `/sonarr`, `/lidarr`) and the key. It shows what's wanted, missing and downloading.
- **Homarr**: add a Radarr, Sonarr or Lidarr integration with the full address and the key. The calendar and the download list work.
- **matcharr** (group movie picking): add Radarr with the address `http://<mediarium>:8264/radarr` and the key. A match is added to your library and searched for; a movie you already have counts as done.
- **nzb360, LunaSea, Ruddarr**: add a Radarr or Sonarr instance with the full address and the key. You can browse the library, add movies and shows, change monitoring and quality profile, search, and follow the download queue and the calendar.

## Tdarr and other apps that change your files

Tdarr and similar apps change a file after it was imported (a new codec, sometimes a new extension) and then tell Radarr or Sonarr to look again, so that the media server finds the new file. Add Mediarium to them like Radarr or Sonarr (the address and key above). When they send **RescanMovie**, **RefreshMovie**, **RescanSeries** or **RefreshSeries**, Mediarium checks each downloaded file of that title: a file that is still there stays as it is, a file that only changed its extension (`.mp4` became `.mkv`, same name) is found under its new name, and then your media servers are asked to scan the folder. A file that can't be found is left alone (it is never marked missing or deleted). Tdarr itself works on the files in your library folders, so mount the same folders into it.

## What works and what doesn't

| Works | Notes |
|---|---|
| Library lists, details, lookup by TMDB id, TVDB id or title | A show's TVDB id is read from TMDB when the show is added or refreshed; older shows get theirs the first time an app lists them. |
| Adding a movie, show or artist | The quality profile is used. Root folder, minimum availability and language profile are accepted and ignored. |
| Monitoring a title, season or episode; quality profile; tags; show type | Also through the "editor" endpoints that change many titles at once. |
| Searching (one movie, a show, a season, episodes, everything missing) | Show and season searches only look for monitored episodes, as in Sonarr. A search for everything missing searches up to 25 titles; the scheduled search does the rest. |
| Removing a title | Files go to the recycle bin when the app asks to delete them. |
| Download queue, queue counts, wanted and missing, calendar | A season pack is one entry per episode, as Sonarr shows it. Removing an entry stops the download. |
| Root folders, disk space, quality profiles, tags | |

Not available: history (an empty list is answered), manual imports, renaming, the app's own settings pages, and changing quality profiles from the app. Anything an app asks that has no meaning in Mediarium (such as a "refresh downloads" command) is accepted and does nothing.

## For scripts

The endpoints are Radarr's and Sonarr's API v3 and Lidarr's API v1 under the prefixes above, for example `GET /radarr/api/v3/movie` or `POST /sonarr/api/v3/command` with `{"name": "SeriesSearch", "seriesId": 3}`. They follow the shapes of the real apps closely enough for the apps listed here. For anything else, Mediarium's own API ([reference/api.md](./reference/api.md)) covers more and is the one to build on.
