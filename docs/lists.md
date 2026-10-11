# Import lists

An import list is a list somewhere else (on Trakt, TMDB, IMDb, MDBList or Letterboxd) that Mediarium reads on a schedule. Whatever is new on it is added to your library, with the quality profile, folder, monitoring and tags you chose for that list. It is the same idea as Lists in Radarr and Sonarr.

You find it under **Settings > Info, lists and subtitles > Import lists**. It is for administrators. Nothing is added until you add a list yourself.

## The rules every list follows

- **Nothing is ever removed.** If a title leaves the list, it stays in your library.
- **A title is added once.** If you remove it from your library later, the list does not put it back. **Start this list over** (under the list) makes the list forget what it has seen, so it treats everything on it as new again.
- **Your Not interested list is respected.** A title you marked as not interested (Discover) is skipped. If you take it off that list, the next read adds it.
- **Titles already in your library are skipped**, in any quality.
- **A few at a time.** **Add at most this many at a time** (5 to 100, 10 to start) limits how many titles one read adds, so a list of 250 movies is not searched for all at once. The rest wait for the next read, and the list's result says how many.
- **Searching for what it adds.** With **Start searching for each one as it is added** on, the titles a read added are searched for right after, the same way the scheduled searches do it: no more than 10 automatic downloads an hour, the delay profile and the size limits apply, a movie that is not out yet is left for later, and an indexer that is paused is not asked. Whatever is not found right away is picked up by the next scheduled search.
- **Movies and shows.** Choose **Movies and shows**, **Movies only** or **Shows only** for each list. Letterboxd only has movies.
- **Only the ones you want.** **Rated at least** (TMDB's rating, 0 to 10) and **From this year on** hold back the rest. They use TMDB's rating and year whichever service the list came from, so the same number means the same thing everywhere. A title nobody has rated yet does not pass a minimum rating.
- **Leave what is on the list now alone.** Tick this when you add a list and only titles added to it afterwards are brought into your library.

Each list has an **On / Off** switch, **Run now**, and says when it was last read and what it did: how many titles it found, how many it added and why the others were skipped. How often it is read is up to you, from every hour to every week (every day to start). Mediarium checks which lists are due every ten minutes. **Check the list** in the form reads the list and shows how many movies and shows are on it and a few names, without adding anything.

Mediarium is polite to the services. It asks as itself (`Mediarium/<version>`), reads each list with as few requests as it can, spaces out its lookups, reads one list at a time, and when a service says to slow down it stops and tries again at the next read.

## Where the lists come from

### Trakt

Needs the Trakt client ID from the **Trakt** card at the bottom of this page (the official build has one built in). Only public things can be read: Mediarium does not sign in to Trakt, so a private list or a private profile gives "That Trakt list or profile is private".

- **Trakt list.** Paste the address of a list, like `https://trakt.tv/users/name/lists/list-name` or `https://trakt.tv/lists/2142754`.
- **Trakt watchlist.** Paste someone's watchlist address or just their Trakt user name. Their profile has to be public.
- **Trending, popular or anticipated.** Trakt's own charts, for movies, shows or both. **How many titles to look at** is up to 100.

### TMDB

Uses the TMDB key you already have.

- **TMDB list.** Paste the address of a public list on themoviedb.org (`https://www.themoviedb.org/list/8504994`) or just its number. Lists can hold movies and shows.
- **Trending, popular, top rated or upcoming.** TMDB's charts. Popular and top rated can be narrowed to a **genre** (when you add movies only or shows only) and an **original language**, for example top rated Korean shows. Trending and upcoming cannot be narrowed.

### IMDb (a CSV file)

IMDb does not let programs download its lists: its export address answers a program with a check meant for people, and Mediarium does not try to get past that. So you export the list yourself:

1. Open the list (or your watchlist) on imdb.com in your browser and choose **Export**.
2. In Mediarium, add an IMDb list and **Upload the file**.

The titles are read from the file (movies, TV series and mini series; episodes are left out) and matched by their IMDb number. The file stays with the list, so the list keeps working, but it only knows what was in that file. To bring in newer titles, edit the list and upload a newer export; titles already handled are not added twice.

You can also give an address that serves the CSV (a file on your own server, say). The address of an IMDb list page (`https://www.imdb.com/list/ls055592025/`) is accepted too, but IMDb will most likely refuse it, and the list then says so.

### MDBList

Needs your own MDBList API key (you make one at mdblist.com/preferences, free). Paste a list address like `https://mdblist.com/lists/name/list-name`. The key is kept encrypted, is only sent to MDBList and is never shown again. MDBList's own daily limit for your key applies; a list is read in one or a few requests.

### Letterboxd (a CSV file)

Letterboxd does not let programs read its pages (it shows them a check meant for people), so, as with IMDb, you export the file yourself: your watchlist from **Settings > Import & Export** on letterboxd.com, or a single list exported as CSV, and upload it. Letterboxd files do not carry TMDB numbers, so Mediarium finds each movie by its name and year. A title is only taken when the name matches and the year is within one year; a few titles may not be found, and the list's result says how many. Movies only.

### Plex watchlists

The Plex watchlists accounts have connected (**Settings > Account & Access > Linked services > Plex watchlist**) show in the same view, each with **Check now**. They work as before: every 20 minutes, new titles are added, or asked for when the account has to ask. They are managed by the account that connected them.

## When a list fails

The result under the list says why, in words. The usual ones:

- *Add your TMDB API key first*: every list needs it to find and add titles.
- *That Trakt list or profile is private*: make it public on Trakt.
- *MDBList doesn't accept that API key*: copy the key again.
- *That site only lets people in with a browser*: the address you gave is guarded. Export the file in your browser and upload it.
- *TMDB has no public list with that number*: check the number. Lists made on the TMDB website recently may not be readable by programs.

The list is tried again at its next read.

## For developers

Everything below is for administrators.

- `GET /api/import-lists` returns `{"lists": [...], "sources": [...], "plexWatchlists": [...]}`. A list never returns its file or its key: `hasCsv` and `hasKey` say they are set.
- `POST /api/import-lists` and `PUT /api/import-lists/{id}` take `name`, `source` (`trakt-list`, `trakt-watchlist`, `trakt-chart`, `tmdb-list`, `tmdb-chart`, `imdb`, `mdblist`, `letterboxd`), `media` (`movie`, `tv`, `both`), `config` (`url`, `chart`, `count`, `genre`, `language`, `csv`), `key` (MDBList; empty keeps the saved one), `enabled`, `intervalHours` (1 to 168), `profileId` (0 = default), `movieRoot`, `tvRoot`, `monitored`, `searchNow`, `tags`, `minRating`, `minYear`, `maxPerRun` (1 to 100) and `startFresh`. A new list is read at once.
- `PUT /api/import-lists/{id}/enabled`, `DELETE /api/import-lists/{id}`, `POST /api/import-lists/{id}/run` (answers `202`; the list shows `running` while it is read), `POST /api/import-lists/{id}/forget`, `POST /api/import-lists/preview` (the same body as create, plus an optional `id`) and `POST /api/import-lists/plex/{userId}/run`.
- Tables: `import_lists` and `import_list_items` (what each list has handled: added, in-library, existing, excluded, filtered, no-match or failed).
