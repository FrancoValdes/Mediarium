# Keep your settings in a file

If you like your setup in git, or you set up Mediarium from a script, you can keep the settings in one YAML file and apply it. This is a command you run yourself. Mediarium doesn't read the file when it starts, so nothing changes behind your back, and you can still change anything on the Settings pages.

The command talks to your running Mediarium through its API, so every value is checked exactly as it is on the Settings pages. A value Mediarium refuses is reported, and nothing is written for that block.

## What the file can hold

One block for each Settings page that holds a single set of values:

| Block | Where it is in Mediarium |
|---|---|
| `settings` | General, Library, Downloading, Info, lists and subtitles, and the rest of Settings that is one set of values (folders, naming, schedules, languages, subtitles, retention...) |
| `downloadClient` | Settings > Downloading > Torrents > **Use your own torrent client** (qBittorrent, Transmission, RDT-Client, Decypharr) |
| `debrid` | Settings > Downloading > Torrents > **Debrid service** |
| `proxy` | The SOCKS5 or HTTP proxy for Usenet, indexers and torrents ([vpn.md](./vpn.md#a-socks5-or-http-proxy-instead)) |
| `systemOptions` | The update options under Settings > System (daily check, overnight install, restart when stuck) |

Things that are lists you add to (indexers, Usenet servers, quality profiles, custom formats, notifications, media servers, accounts) are not in the file yet. They stay on their Settings pages.

## Start from what you have

```bash
docker exec -e MEDIARIUM_API_KEY=your-key mediarium /app/app config export -o /config/mediarium.yml
```

An administrator makes the API key on the **Profile** page. The file is written inside the container, so use a folder that is mounted (`/config` is). Passwords and keys are never exported.

Open the file and **delete every line you don't want to control**. A line that is not in the file is left alone. A short file is a good file:

```yaml
settings:
  huntIntervalHours: 12
  trashDays: 14
  automationEnabled: true
  unsafeExtensions: [.exe, .scr, .bat]

downloadClient:
  kind: transmission
  url: http://transmission:9091
  username: admin
  password: ${TRANSMISSION_PASSWORD}
  category: mediarium

debrid:
  preferCached: true
```

## Secrets

Write a password or a key as `${NAME}`. It is replaced with the environment variable `NAME` when the file is applied, so the secret never has to be in the file or in git. `${NAME:-fallback}` uses `fallback` when `NAME` isn't set. A `${NAME}` with no value and no fallback stops the command before anything is changed.

These are the values Mediarium accepts but never shows back: in `settings` `tmdbApiKey`, `openSubtitlesApiKey`, `openSubtitlesUsername`, `openSubtitlesPassword`, `subdlApiKey` and `traktClientId`; `password` in `downloadClient` and `proxy`; `apiKey` in `debrid`. Because they can't be compared, they are sent again every time you apply.

## See what would change, then apply

```bash
docker exec -e MEDIARIUM_API_KEY=your-key -e TRANSMISSION_PASSWORD=... mediarium /app/app config diff /config/mediarium.yml
docker exec -e MEDIARIUM_API_KEY=your-key -e TRANSMISSION_PASSWORD=... mediarium /app/app config apply /config/mediarium.yml
```

`diff` (or `apply -n`) prints one line per difference, for example `settings.huntIntervalHours: 6 -> 12`, and changes nothing. `apply` makes the changes and prints what it did. Run it again and it says there is nothing to change.

- A name that isn't one of Mediarium's settings (a typo) stops the command before anything is changed, and the name is listed.
- A block Mediarium refuses is reported, and the other blocks are still applied. The exit code is then 1, so a script can tell.
- A few things can't be done with an API key, for security (for example switching on pushed updates, `systemOptions.allowPush`). Mediarium says so; do those on the Settings page.

Options for all three commands: `--url` (the address of your Mediarium; the default is `http://127.0.0.1:8264`, or `$MEDIARIUM_URL`) and `--key` (the API key; the default is `$MEDIARIUM_API_KEY`). Options go before the file name.

Outside Docker, the same command is `mediarium config apply mediarium.yml` (the program file is the `app` binary).

## Idea or missing block

This is a first version, started from what people asked for. If a setting you want in git is not in one of these blocks, say which on [GitHub](https://github.com/rdborg/Mediarium/issues) and it will be added.
