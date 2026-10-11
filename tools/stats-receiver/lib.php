<?php
// The receiving side of Mediarium's optional anonymous usage numbers.
// What an install sends is described in internal/usagestats/usagestats.go and in
// docs/usage-stats.md. This file checks every field against the same fixed lists,
// keeps one row per install per day, and makes the public totals.
//
// What it never keeps: the address a message came from, or anything outside the
// fixed lists below. The only thing made from the address is a one-way scrambled
// value (with a salt that changes every day) that limits how many different
// installs one address may report in a day; it is deleted after a day.

declare(strict_types=1);

const SCHEMA = 1;
const MAX_BODY_BYTES = 2048;
const MAX_IDS_PER_ADDRESS_PER_DAY = 40;
const MAX_IDS_PER_DAY = 200000;
const KEEP_DAYS = 120;
const SHOW_AT_LEAST = 10; // no group smaller than this is ever shown

const MEDIA = ['movies', 'tv', 'music', 'ebooks', 'audiobooks'];
const FEATURES = ['usenet', 'torrents', 'vpn', 'proxy', 'debrid', 'download-client', 'irc', 'import-lists', 'requests', 'sso', 'passkeys', 'kobo', 'notifications', 'subtitles'];
const SERVERS = ['plex', 'jellyfin', 'emby', 'audiobookshelf', 'kavita', 'silo', 'webhook'];
const ARCHES = ['amd64', 'arm64', 'other'];
const CORES = ['1-2', '3-4', '5-8', '9-16', '17+'];
const MEMORY = ['unknown', 'under 2 GB', '2-4 GB', '4-8 GB', '8-16 GB', '16-32 GB', 'over 32 GB'];
const AGE = ['unknown', 'under 1 week', '1-4 weeks', '1-3 months', '3-12 months', 'over 1 year'];
const LIBRARY = ['empty', '1-100', '101-1000', '1001-5000', '5001-20000', 'over 20000'];
const ACCOUNTS = ['1', '2-5', '6-20', 'over 20'];
const INDEXERS = ['0', '1-2', '3-5', '6-10', 'over 10'];

// The fields of one message, exactly. Anything else makes it invalid.
const FIELDS = ['schema', 'id', 'version', 'arch', 'docker', 'cores', 'memory', 'age', 'library', 'accounts', 'indexers', 'media', 'features', 'servers'];

// ---- the website survey (for people who prefer not to share from the app) ----
// One form on https://mediarium.app/survey/. Every answer is a choice from a fixed list,
// plus a short optional note that is kept but never shown. No ID, no address.
const SURVEY_MAX_PER_ADDRESS_PER_DAY = 5;
const SURVEY_DEVICE = ['synology', 'qnap', 'unraid', 'truenas', 'other-nas', 'raspberry-pi', 'mini-pc', 'pc-or-laptop', 'cloud-server', 'mac', 'windows-docker', 'other'];
const SURVEY_CPU = ['x86-64', 'arm64', 'not-sure'];
const SURVEY_RAM = ['under-2gb', '2-4gb', '4-8gb', '8-16gb', 'over-16gb', 'not-sure'];
const SURVEY_OS = ['linux', 'dsm', 'unraid', 'truenas', 'windows', 'macos', 'other'];
const SURVEY_INSTALL = ['compose', 'docker-run', 'portainer', 'app-store', 'other'];
const SURVEY_SERVERS = ['plex', 'jellyfin', 'emby', 'kodi', 'audiobookshelf', 'kavita', 'none', 'other'];
const SURVEY_MEDIA = ['movies', 'tv', 'music', 'ebooks', 'audiobooks'];
const SURVEY_DOWNLOADS = ['usenet', 'torrents', 'debrid'];
const SURVEY_SIZE = ['under-25', '25-100', '100-500', 'over-500', 'not-sure']; // titles in the library
const SURVEY_COUNTRY = ['us', 'gb', 'ca', 'au', 'nz', 'ie', 'de', 'fr', 'es', 'pt', 'br', 'it', 'nl', 'be', 'ch', 'at', 'se', 'no', 'dk', 'fi', 'pl', 'cz', 'hu', 'ro', 'gr', 'tr', 'ru', 'ua', 'il', 'in', 'jp', 'kr', 'cn', 'tw', 'sg', 'za', 'mx', 'ar', 'cl', 'co', 'mt', 'other', 'private']; // the country the person says they live in, if they choose to
const SURVEY_WISH = ['languages', 'phone-app', 'add-ons', 'config-file', 'queue-controls', 'better-search', 'more-sites', 'more-servers', 'other']; // what they would like next
const SURVEY_FIELDS = ['device', 'cpu', 'ram', 'os', 'install', 'size', 'country', 'servers', 'media', 'downloads', 'wish', 'note'];
// Not answers: the anti-spam fields of the form.
const SURVEY_CHECK_FIELDS = ['website', 'token', 'pow'];
const SURVEY_TOKEN_MIN_AGE = 6;      // seconds a person needs to fill the form
const SURVEY_TOKEN_MAX_AGE = 7200;
const SURVEY_POW_BITS = 16;
const SURVEY_MAX_PER_DAY = 500;
const SURVEY_KEEP_DAYS = 730; // survey answers are deleted after 24 months

function in_list($v, array $list): bool {
    return is_string($v) && in_array($v, $list, true);
}

function name_list($v, array $allowed): bool {
    if (!is_array($v) || array_keys($v) !== array_keys(array_values($v)) || count($v) > count($allowed)) {
        return false;
    }
    foreach ($v as $x) {
        if (!in_list($x, $allowed)) {
            return false;
        }
    }
    return count(array_unique($v)) === count($v);
}

// Returns the cleaned message, or null when anything is not exactly as allowed.
function check_message($m): ?array {
    if (!is_array($m) || array_keys($m) === [] || count(array_diff(array_keys($m), FIELDS)) > 0 || count(array_diff(FIELDS, array_keys($m))) > 0) {
        return null;
    }
    if ($m['schema'] !== SCHEMA || !is_string($m['id']) || !preg_match('/^[0-9a-f]{32}$/', $m['id'])) {
        return null;
    }
    if (!is_string($m['version']) || !preg_match('/^[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}(-[0-9A-Za-z.]{1,20})?$/', $m['version'])) {
        return null;
    }
    if (!is_bool($m['docker']) || !in_list($m['arch'], ARCHES) || !in_list($m['cores'], CORES) || !in_list($m['memory'], MEMORY) || !in_list($m['age'], AGE)
        || !in_list($m['library'], LIBRARY) || !in_list($m['accounts'], ACCOUNTS) || !in_list($m['indexers'], INDEXERS)) {
        return null;
    }
    if (!name_list($m['media'], MEDIA) || !name_list($m['features'], FEATURES) || !name_list($m['servers'], SERVERS)) {
        return null;
    }
    return $m;
}

function data_dir(): string {
    $dir = getenv('STATS_DATA_DIR');
    if (!$dir) {
        $dir = dirname($_SERVER['DOCUMENT_ROOT'] ?? __DIR__) . '/stats-data';
    }
    if (!is_dir($dir)) {
        mkdir($dir, 0700, true);
    }
    return $dir;
}

function db(?string $dir = null): PDO {
    $dir = $dir ?? data_dir();
    $pdo = new PDO('sqlite:' . $dir . '/stats.sqlite');
    $pdo->setAttribute(PDO::ATTR_ERRMODE, PDO::ERRMODE_EXCEPTION);
    $pdo->exec('PRAGMA journal_mode = WAL');
    $pdo->exec('CREATE TABLE IF NOT EXISTS pings (
        day TEXT NOT NULL, id TEXT NOT NULL, version TEXT NOT NULL, arch TEXT NOT NULL, docker INTEGER NOT NULL,
        cores TEXT NOT NULL, memory TEXT NOT NULL, age TEXT NOT NULL, library TEXT NOT NULL, accounts TEXT NOT NULL, indexers TEXT NOT NULL,
        media TEXT NOT NULL, features TEXT NOT NULL, servers TEXT NOT NULL, PRIMARY KEY (day, id))');
    $pdo->exec('CREATE INDEX IF NOT EXISTS pings_id ON pings (id)');
    $pdo->exec('CREATE TABLE IF NOT EXISTS limits (day TEXT NOT NULL, who TEXT NOT NULL, n INTEGER NOT NULL, PRIMARY KEY (day, who))');
    return $pdo;
}

// A one-way value for "this address, today". The salt is new every day and the
// old ones are deleted, so it cannot be used to follow anyone from one day to the next.
function who_today(string $address, string $day, string $dir): string {
    $file = $dir . '/salt-' . $day;
    if (!is_file($file)) {
        foreach (glob($dir . '/salt-*') ?: [] as $old) {
            unlink($old);
        }
        file_put_contents($file, bin2hex(random_bytes(16)));
        chmod($file, 0600);
    }
    return hash_hmac('sha256', $address, (string)file_get_contents($file));
}

// Stores one message. Returns 'ok', 'limited' or 'full'.
function store_message(PDO $pdo, array $m, string $day, string $who): string {
    $pdo->beginTransaction();
    try {
        $known = $pdo->prepare('SELECT 1 FROM pings WHERE day = ? AND id = ?');
        $known->execute([$day, $m['id']]);
        if (!$known->fetchColumn()) {
            $total = (int)$pdo->query("SELECT COUNT(*) FROM pings WHERE day = " . $pdo->quote($day))->fetchColumn();
            if ($total >= MAX_IDS_PER_DAY) {
                $pdo->rollBack();
                return 'full';
            }
            $n = $pdo->prepare('SELECT n FROM limits WHERE day = ? AND who = ?');
            $n->execute([$day, $who]);
            $seen = (int)$n->fetchColumn();
            if ($seen >= MAX_IDS_PER_ADDRESS_PER_DAY) {
                $pdo->rollBack();
                return 'limited';
            }
            $pdo->prepare('INSERT INTO limits (day, who, n) VALUES (?, ?, 1) ON CONFLICT(day, who) DO UPDATE SET n = n + 1')->execute([$day, $who]);
        }
        $pdo->prepare('INSERT INTO pings (day, id, version, arch, docker, cores, memory, age, library, accounts, indexers, media, features, servers)
            VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
            ON CONFLICT(day, id) DO UPDATE SET version = excluded.version, arch = excluded.arch, docker = excluded.docker, cores = excluded.cores,
                memory = excluded.memory, age = excluded.age, library = excluded.library, accounts = excluded.accounts, indexers = excluded.indexers,
                media = excluded.media, features = excluded.features, servers = excluded.servers')
            ->execute([$day, $m['id'], $m['version'], $m['arch'], $m['docker'] ? 1 : 0, $m['cores'], $m['memory'], $m['age'], $m['library'], $m['accounts'], $m['indexers'],
                implode(',', $m['media']), implode(',', $m['features']), implode(',', $m['servers'])]);
        $pdo->commit();
        return 'ok';
    } catch (Throwable $e) {
        $pdo->rollBack();
        throw $e;
    }
}

// Removes what is too old: the per-address counters after the day, the rows after KEEP_DAYS.
function tidy(PDO $pdo, string $day): void {
    $pdo->prepare('DELETE FROM limits WHERE day < ?')->execute([$day]);
    $pdo->exec('CREATE TABLE IF NOT EXISTS tokens (sig TEXT PRIMARY KEY, day TEXT NOT NULL)');
    $pdo->prepare('DELETE FROM tokens WHERE day < ?')->execute([gmdate('Y-m-d', strtotime($day . ' -2 days'))]);
    $pdo->prepare('DELETE FROM pings WHERE day < ?')->execute([gmdate('Y-m-d', strtotime($day . ' -' . KEEP_DAYS . ' days'))]);
    survey_table($pdo);
    $pdo->prepare('DELETE FROM survey WHERE day < ?')->execute([gmdate('Y-m-d', strtotime($day . ' -' . SURVEY_KEEP_DAYS . ' days'))]);
}

// ---- the public totals ----------------------------------------------------

// Counts how many of $rows (one per install, its latest message) have each value.
// A value seen on fewer than SHOW_AT_LEAST installs is not listed.
function tally(array $rows, string $field, bool $list = false): array {
    $c = [];
    foreach ($rows as $r) {
        $values = $list ? array_filter(explode(',', (string)$r[$field]), 'strlen') : [(string)$r[$field]];
        foreach ($values as $v) {
            $c[$v] = ($c[$v] ?? 0) + 1;
        }
    }
    arsort($c);
    $out = [];
    foreach ($c as $k => $n) {
        if ($n >= SHOW_AT_LEAST) {
            $out[] = ['name' => (string)$k, 'installs' => $n];
        }
    }
    return $out;
}

function make_summary(PDO $pdo, string $today): array {
    $since = fn(int $days): string => gmdate('Y-m-d', strtotime($today . ' -' . ($days - 1) . ' days'));
    $active = fn(int $days): int => (int)$pdo->query('SELECT COUNT(DISTINCT id) FROM pings WHERE day >= ' . $pdo->quote($since($days)))->fetchColumn();
    $ever = (int)$pdo->query('SELECT COUNT(DISTINCT id) FROM pings')->fetchColumn();

    // The latest message of every install that reported in the last 7 days.
    $rows = $pdo->query('SELECT p.* FROM pings p JOIN (SELECT id, MAX(day) d FROM pings WHERE day >= ' . $pdo->quote($since(7)) . ' GROUP BY id) l ON l.id = p.id AND l.d = p.day')->fetchAll(PDO::FETCH_ASSOC);
    $days = [];
    foreach ($pdo->query('SELECT day, COUNT(*) n FROM pings WHERE day >= ' . $pdo->quote($since(30)) . ' GROUP BY day ORDER BY day')->fetchAll(PDO::FETCH_ASSOC) as $d) {
        $days[] = ['day' => $d['day'], 'installs' => (int)$d['n']];
    }
    $a7 = $active(7);
    $summary = [
        'generated' => gmdate('c'),
        'survey' => survey_summary($pdo),
        'note' => 'Only installs whose administrator chose to share. Real use is higher. Groups under ' . SHOW_AT_LEAST . ' installs are not shown.',
        'sharing' => ['last7days' => $a7, 'last30days' => $active(30), 'ever' => $ever],
        'perDay' => $days,
    ];
    if ($a7 >= SHOW_AT_LEAST) {
        $docker = ['yes' => 0, 'no' => 0];
        foreach ($rows as $r) {
            $docker[$r['docker'] ? 'yes' : 'no']++;
        }
        $summary['versions'] = tally($rows, 'version');
        $summary['arch'] = tally($rows, 'arch');
        $summary['docker'] = array_values(array_filter([['name' => 'Docker', 'installs' => $docker['yes']], ['name' => 'Not Docker', 'installs' => $docker['no']]], fn($x) => $x['installs'] >= SHOW_AT_LEAST));
        $summary['cores'] = tally($rows, 'cores');
        $summary['memory'] = tally($rows, 'memory');
        $summary['age'] = tally($rows, 'age');
        $summary['library'] = tally($rows, 'library');
        $summary['accounts'] = tally($rows, 'accounts');
        $summary['indexers'] = tally($rows, 'indexers');
        $summary['media'] = tally($rows, 'media', true);
        $summary['features'] = tally($rows, 'features', true);
        $summary['servers'] = tally($rows, 'servers', true);
    }
    return $summary;
}


// ---- the survey -------------------------------------------------------------

function pick_all($v, array $allowed): ?array {
    if ($v === null || $v === '') {
        return [];
    }
    $v = is_array($v) ? array_values($v) : [$v];
    foreach ($v as $x) {
        if (!in_list($x, $allowed)) {
            return null;
        }
    }
    return array_values(array_unique($v));
}

// Returns the cleaned answers, or null when anything is not from its list.
function check_survey(array $f): ?array {
    foreach (array_keys($f) as $k) {
        if (!in_array($k, array_merge(SURVEY_FIELDS, SURVEY_CHECK_FIELDS), true)) {
            return null;
        }
    }
    $one = [['device', SURVEY_DEVICE], ['cpu', SURVEY_CPU], ['ram', SURVEY_RAM], ['os', SURVEY_OS], ['install', SURVEY_INSTALL], ['size', SURVEY_SIZE], ['country', SURVEY_COUNTRY]];
    $out = [];
    foreach ($one as [$name, $list]) {
        if (!in_list($f[$name] ?? null, $list)) {
            return null;
        }
        $out[$name] = $f[$name];
    }
    foreach ([['servers', SURVEY_SERVERS], ['media', SURVEY_MEDIA], ['downloads', SURVEY_DOWNLOADS], ['wish', SURVEY_WISH]] as [$name, $list]) {
        $v = pick_all($f[$name] ?? null, $list);
        if ($v === null) {
            return null;
        }
        $out[$name] = $v;
    }
    $note = isset($f['note']) && is_string($f['note']) ? $f['note'] : '';
    $note = preg_replace('/[ -]/u', ' ', $note) ?? '';
    $out['note'] = mb_substr(trim($note), 0, 200);
    return $out;
}

function survey_table(PDO $pdo): void {
    $pdo->exec('CREATE TABLE IF NOT EXISTS survey (
        day TEXT NOT NULL, device TEXT NOT NULL, cpu TEXT NOT NULL, ram TEXT NOT NULL, os TEXT NOT NULL, install TEXT NOT NULL, size TEXT NOT NULL,
        servers TEXT NOT NULL, media TEXT NOT NULL, downloads TEXT NOT NULL, note TEXT NOT NULL, country TEXT NOT NULL DEFAULT \'\', wish TEXT NOT NULL DEFAULT \'\')');
    $have = array_column($pdo->query('PRAGMA table_info(survey)')->fetchAll(PDO::FETCH_ASSOC), 'name');
    foreach (['country', 'wish'] as $column) {
        if (!in_array($column, $have, true)) {
            $pdo->exec('ALTER TABLE survey ADD COLUMN ' . $column . " TEXT NOT NULL DEFAULT ''");
        }
    }
    $pdo->exec('CREATE TABLE IF NOT EXISTS tokens (sig TEXT PRIMARY KEY, day TEXT NOT NULL)');
}

// Stores one answer. 'ok' or 'limited' (too many from one address in a day).
function store_survey(PDO $pdo, array $a, string $day, string $who): string {
    survey_table($pdo);
    $pdo->beginTransaction();
    try {
        $n = $pdo->prepare('SELECT n FROM limits WHERE day = ? AND who = ?');
        $n->execute([$day, 's' . $who]);
        if ((int)$n->fetchColumn() >= SURVEY_MAX_PER_ADDRESS_PER_DAY) {
            $pdo->rollBack();
            return 'limited';
        }
        $pdo->prepare('INSERT INTO limits (day, who, n) VALUES (?, ?, 1) ON CONFLICT(day, who) DO UPDATE SET n = n + 1')->execute([$day, 's' . $who]);
        if ((int)$pdo->query('SELECT COUNT(*) FROM survey WHERE day = ' . $pdo->quote($day))->fetchColumn() >= SURVEY_MAX_PER_DAY) {
            $pdo->rollBack();
            return 'limited';
        }
        $pdo->prepare('INSERT INTO survey (day, device, cpu, ram, os, install, size, country, servers, media, downloads, wish, note) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)')
            ->execute([$day, $a['device'], $a['cpu'], $a['ram'], $a['os'], $a['install'], $a['size'], $a['country'], implode(',', $a['servers']), implode(',', $a['media']), implode(',', $a['downloads']), implode(',', $a['wish']), $a['note']]);
        $pdo->commit();
        return 'ok';
    } catch (Throwable $e) {
        $pdo->rollBack();
        throw $e;
    }
}

function survey_summary(PDO $pdo): array {
    survey_table($pdo);
    $rows = $pdo->query('SELECT * FROM survey')->fetchAll(PDO::FETCH_ASSOC);
    $total = count($rows);
    $out = ['responses' => $total];
    if ($total >= SHOW_AT_LEAST) {
        foreach (['device', 'cpu', 'ram', 'os', 'install', 'size', 'country'] as $f) {
            $out[$f] = tally($rows, $f);
        }
        foreach (['servers', 'media', 'downloads', 'wish'] as $f) {
            $out[$f] = tally($rows, $f, true);
        }
    }
    return $out;
}


// ---- the survey's spam protection ---------------------------------------------
// The page asks for a token (made here, signed with a secret that stays on the server),
// waits a few seconds, does a small piece of work (a proof of work, so thousands of
// answers cost a bot real time), and sends both back with the answers. A token works
// once, only after SURVEY_TOKEN_MIN_AGE seconds, and only for two hours.

function survey_secret(string $dir): string {
    $file = $dir . '/survey-secret';
    if (!is_file($file)) {
        file_put_contents($file, bin2hex(random_bytes(32)));
        chmod($file, 0600);
    }
    return (string)file_get_contents($file);
}

function survey_token(string $dir, ?int $now = null): string {
    $t = (string)($now ?? time());
    $nonce = bin2hex(random_bytes(8));
    return $t . '.' . $nonce . '.' . hash_hmac('sha256', $t . '.' . $nonce, survey_secret($dir));
}

function leading_zero_bits(string $binary): int {
    $bits = 0;
    foreach (str_split($binary) as $ch) {
        $v = ord($ch);
        if ($v === 0) {
            $bits += 8;
            continue;
        }
        return $bits + (7 - (int)floor(log($v, 2)));
    }
    return $bits;
}

// Returns '' when the token is fine and has now been used, otherwise a short reason.
function check_survey_token(PDO $pdo, string $dir, $token, $pow, ?int $now = null): string {
    $now = $now ?? time();
    if (!is_string($token) || !is_string($pow) || strlen($token) > 120 || strlen($pow) > 40 || !preg_match('/^(\d{9,11})\.([0-9a-f]{16})\.([0-9a-f]{64})$/', $token, $m)) {
        return 'no token';
    }
    if (!hash_equals(hash_hmac('sha256', $m[1] . '.' . $m[2], survey_secret($dir)), $m[3])) {
        return 'bad token';
    }
    $age = $now - (int)$m[1];
    if ($age < SURVEY_TOKEN_MIN_AGE) {
        return 'too fast';
    }
    if ($age > SURVEY_TOKEN_MAX_AGE) {
        return 'too old';
    }
    if (leading_zero_bits(hash('sha256', $token . ':' . $pow, true)) < SURVEY_POW_BITS) {
        return 'no work';
    }
    survey_table($pdo);
    try {
        $pdo->prepare('INSERT INTO tokens (sig, day) VALUES (?, ?)')->execute([$m[3], gmdate('Y-m-d', $now)]);
    } catch (PDOException $e) {
        return 'used';
    }
    return '';
}

// ---- live numbers from GitHub and Docker Hub (public pages, nothing about people) ----

const PROJECT_CACHE_SECONDS = 600;

function fetch_public_json(string $url): ?array {
    $context = stream_context_create(['http' => ['method' => 'GET', 'timeout' => 5, 'header' => "User-Agent: mediarium-website\r\nAccept: application/json\r\n"]]);
    $body = @file_get_contents($url, false, $context);
    if ($body === false) {
        return null;
    }
    $data = json_decode($body, true);
    return is_array($data) ? $data : null;
}

// $fetch takes a URL and returns the decoded JSON or null (a test passes its own).
function project_numbers(callable $fetch): array {
    $out = ['github' => null, 'dockerhub' => null];
    $repo = $fetch('https://api.github.com/repos/rdborg/Mediarium');
    if ($repo && isset($repo['stargazers_count'])) {
        $out['github'] = ['stars' => (int)$repo['stargazers_count'], 'forks' => (int)($repo['forks_count'] ?? 0), 'openIssues' => (int)($repo['open_issues_count'] ?? 0)];
        $releases = $fetch('https://api.github.com/repos/rdborg/Mediarium/releases?per_page=100');
        if (is_array($releases) && array_is_list_compat($releases)) {
            $downloads = 0;
            foreach ($releases as $r) {
                foreach (($r['assets'] ?? []) as $a) {
                    $downloads += (int)($a['download_count'] ?? 0);
                }
            }
            $out['github']['releaseDownloads'] = $downloads;
            $out['github']['releases'] = count($releases);
            if (isset($releases[0]['tag_name'])) {
                $out['github']['latest'] = (string)$releases[0]['tag_name'];
                $out['github']['latestDate'] = substr((string)($releases[0]['published_at'] ?? ''), 0, 10);
            }
        }
    }
    $hub = $fetch('https://hub.docker.com/v2/repositories/rdborg/mediarium/');
    if ($hub && isset($hub['pull_count'])) {
        $out['dockerhub'] = ['pulls' => (int)$hub['pull_count'], 'stars' => (int)($hub['star_count'] ?? 0), 'updated' => substr((string)($hub['last_updated'] ?? ''), 0, 10)];
    }
    return $out;
}

function array_is_list_compat(array $a): bool {
    return array_keys($a) === array_keys(array_values($a));
}

// The numbers, kept for an hour in a file so the sites are not asked on every visit.
function cached_project_numbers(string $dir, ?callable $fetch = null): array {
    $file = $dir . '/projects.json';
    if (is_file($file) && time() - filemtime($file) < PROJECT_CACHE_SECONDS) {
        $cached = json_decode((string)file_get_contents($file), true);
        if (is_array($cached)) {
            return $cached;
        }
    }
    $fresh = project_numbers($fetch ?? 'fetch_public_json');
    if ($fresh['github'] === null && $fresh['dockerhub'] === null && is_file($file)) {
        $old = json_decode((string)file_get_contents($file), true);
        if (is_array($old)) {
            touch($file);
            return $old;
        }
    }
    $fresh['fetched'] = gmdate('c');
    file_put_contents($file, json_encode($fresh), LOCK_EX);
    return $fresh;
}
