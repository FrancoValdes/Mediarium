# Cross-seeding

Cross-seeding is seeding the same files on more than one tracker. You downloaded a release from tracker A; the same files are also on tracker B; you add tracker B's `.torrent` to your client pointing at the files you already have, the client checks them, and you seed on both trackers from one copy on the disk. On private trackers it is a good way to build ratio without downloading anything again.

**Mediarium doesn't do cross-seeding itself.** It would need your logins on every tracker, a match by size and file layout that must be exact (a wrong match makes the client re-download or report a bad hash, and some trackers punish that), and each tracker's own rules about it. Programs made only for this already do it well, and they work next to Mediarium when you use a torrent client of your own (see [Download clients](downloads.md#download-clients-qbittorrent-transmission-rdt-client-decypharr)). This page says how to set that up so the two don't get in each other's way.

## What you need

- **A download client you run yourself** (qBittorrent is what the cross-seed programs know best; Transmission and Deluge work with some of them). Torrents in Mediarium's built-in torrent client can't be cross-seeded by another program.
- **A cross-seed program**, run next to Mediarium and the client. Two good ones:
  - [Qui](https://github.com/autobrr/qui), a web page for qBittorrent that includes cross-seeding.
  - [cross-seed](https://www.cross-seed.org/), a program that checks finished torrents against your indexers (it can use the same Prowlarr or Jackett you give Mediarium).
- **The same folder for all of them**, and the same path in each container. Cross-seeding only works when the client, the cross-seed program and Mediarium all see the files at the paths the client uses.

## Setting it up with Mediarium

1. In Mediarium, set up your torrent client under **Settings > Downloading > Torrents > Use your own torrent client**, with a category, and a **path mapping** if the client sees its folder under another path.
2. Keep **Remove from the client after import** switched **off**. The torrent has to stay in the client, seeding, for a cross-seed to be added next to it.
3. Let the library import be a **hardlink** (the default when the library and the client's folder are on the same disk, see [Why one /data folder?](INSTALL.md#why-one-data-folder-hardlinks)). The library gets its own name for the file, the client keeps seeding the original, and no space is used twice. If the import has to **copy**, the copy is yours to keep and the cross-seed still works, but it uses space twice.
4. In the cross-seed program, give it your client (its address and login), your trackers or your Prowlarr/Jackett address, and the folder the client downloads to. Choose to add matches to the client **paused or with a recheck**, so a bad match is caught before it seeds. In cross-seed, the options are `action: "inject"` and `linkDirs` for the links it makes; with Qui, the cross-seed page.
5. Don't let the program rename files. The cross-seeded torrent has to point at the same files the first one does, under the same names; Mediarium's own renaming only touches the **library's copy** (the hardlink), not the client's file, so it doesn't break seeding.

## Things to keep in mind

- **Check the rules of each tracker.** Some don't allow cross-seeding, some only with a minimum seed time, some want the torrent to be unmodified. The program can't know; you do.
- **A release that Mediarium upgrades.** When a better version replaces a movie file, the old file goes to the recycle bin, but the client's copy stays. If you seed it, that is the hardlink's other name, still there. If you remove the torrent from the client, the data goes with it, unless it is also linked from somewhere else.
- **Removing a movie from Mediarium** with "also delete the files" removes the library's name for the file. With a hardlink, the client's copy remains and keeps seeding.
- **Privacy.** The cross-seed program talks to your trackers with your account. Mediarium never sees those logins.

## Will Mediarium do it itself later?

It is on the list of things that could come, only if enough people ask for it: a small, careful version for qBittorrent, off by default, that tells you what it would match before adding anything. If you want it, say so on [GitHub](https://github.com/rdborg/Mediarium/issues) and say which client and trackers you use. Until then, Qui or cross-seed next to Mediarium is the better way.
