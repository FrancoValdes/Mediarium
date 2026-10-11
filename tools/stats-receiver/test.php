<?php
// Tests for the receiver. Run: php tools/stats-receiver/test.php (needs the PDO SQLite extension).
declare(strict_types=1);

require __DIR__ . '/lib.php';

$failed = 0;
function check(bool $ok, string $what): void {
    global $failed;
    if (!$ok) {
        $failed++;
        fwrite(STDERR, "FAIL: $what\n");
    }
}

function good(string $id): array {
    return ['schema' => 1, 'id' => $id, 'version' => '3.0.0', 'arch' => 'amd64', 'docker' => true, 'cores' => '3-4', 'memory' => '4-8 GB', 'age' => '1-4 weeks',
        'library' => '101-1000', 'accounts' => '2-5', 'indexers' => '1-2', 'media' => ['movies', 'tv'], 'features' => ['usenet', 'torrents'], 'servers' => ['plex']];
}

$id = str_repeat('ab', 16);
check(check_message(good($id)) !== null, 'a good message is accepted');

foreach ([
    'extra field' => fn($m) => $m + ['hostname' => 'nas'],
    'missing field' => function ($m) { unset($m['servers']); return $m; },
    'free text in a band' => function ($m) { $m['cores'] = 'my server'; return $m; },
    'unknown feature' => function ($m) { $m['features'][] = 'nzbgeek.info'; return $m; },
    'duplicate' => function ($m) { $m['media'] = ['tv', 'tv']; return $m; },
    'object instead of list' => function ($m) { $m['media'] = ['a' => 'tv']; return $m; },
    'upper-case id' => function ($m) { $m['id'] = strtoupper($m['id']); return $m; },
    'short id' => function ($m) { $m['id'] = 'abc'; return $m; },
    'bad version' => function ($m) { $m['version'] = '3.0.0; DROP TABLE pings'; return $m; },
    'wrong schema' => function ($m) { $m['schema'] = 2; return $m; },
    'docker as text' => function ($m) { $m['docker'] = 'yes'; return $m; },
] as $name => $mutate) {
    check(check_message($mutate(good($id))) === null, "refused: $name");
}
check(check_message('text') === null && check_message(null) === null && check_message([]) === null, 'refused: not an object');

$dir = sys_get_temp_dir() . '/stats-test-' . bin2hex(random_bytes(4));
mkdir($dir, 0700, true);
$pdo = db($dir);
$day = '2026-10-11';
$who = who_today('203.0.113.9', $day, $dir);
check($who === who_today('203.0.113.9', $day, $dir), 'the same address gives the same value on one day');
check(strpos($who, '203.0.113.9') === false, 'the address is not in the value');
check(store_message($pdo, good($id), $day, $who) === 'ok', 'store');
check(store_message($pdo, good($id), $day, $who) === 'ok', 'the same install again on one day is an update');
check((int)$pdo->query('SELECT COUNT(*) FROM pings')->fetchColumn() === 1, 'one row per install per day');
$columns = array_column($pdo->query('PRAGMA table_info(pings)')->fetchAll(PDO::FETCH_ASSOC), 'name');
check(!array_intersect($columns, ['ip', 'address', 'host', 'user_agent']), 'no column could hold an address');

// One address may only report so many different installs a day.
$other = who_today('198.51.100.1', $day, $dir);
$results = [];
for ($i = 0; $i < MAX_IDS_PER_ADDRESS_PER_DAY + 3; $i++) {
    $m = good(sprintf('%032x', $i + 1000));
    $m['servers'] = [];
    $results[] = store_message($pdo, $m, $day, $other);
}
check(count(array_keys($results, 'ok', true)) === MAX_IDS_PER_ADDRESS_PER_DAY, 'one address is limited per day');
check(end($results) === 'limited', 'the limit answers limited');

// Totals: groups under the minimum are never shown.
for ($i = 0; $i < 12; $i++) {
    $m = good(sprintf('%032x', 5000 + $i));
    $m['servers'] = $i < 3 ? ['plex'] : [];
    $m['features'] = $i < 11 ? ['usenet'] : ['usenet', 'kobo'];
    store_message($pdo, $m, $day, who_today('192.0.2.' . $i, $day, $dir));
}
$s = make_summary($pdo, $day);
check($s['sharing']['last7days'] >= 12, 'active installs counted');
$names = array_column($s['features'] ?? [], 'name');
check(in_array('usenet', $names, true), 'a common feature is shown');
check(!in_array('kobo', $names, true), 'a feature on fewer than 10 installs is hidden');
check(!in_array('plex', array_column($s['servers'] ?? [], 'name'), true), 'a server on fewer than 10 installs is hidden');
check(strpos(json_encode($s), '203.0.113') === false, 'no address in the totals');

// A salt does not live past its day.
who_today('203.0.113.9', '2026-10-12', $dir);
check(count(glob($dir . '/salt-*')) === 1, 'old salts are deleted');
tidy($pdo, '2026-10-12');
check((int)$pdo->query('SELECT COUNT(*) FROM limits')->fetchColumn() === 0, 'old counters are deleted');

// The website survey.
$form = ['device' => 'synology', 'cpu' => 'x86-64', 'ram' => '4-8gb', 'os' => 'dsm', 'install' => 'compose', 'size' => '100-500', 'country' => 'mt', 'wish' => ['languages'],
    'servers' => ['plex', 'jellyfin'], 'media' => ['movies'], 'downloads' => ['usenet'], 'note' => "  my nas
 has a fan  ", 'website' => ''];
$a = check_survey($form);
check($a !== null && $a['note'] === 'my nas  has a fan', 'a good form is accepted and the note is cleaned');
foreach ([
    'unknown device' => ['device' => 'my-house'],
    'unknown field' => ['email' => 'a@b.c'],
    'odd server' => ['servers' => ['plex', 'http://x']],
    'no cpu' => ['cpu' => ''],
] as $name => $change) {
    check(check_survey(array_merge($form, $change)) === null, "survey refused: $name");
}
check(mb_strlen(check_survey(array_merge($form, ['note' => str_repeat('x', 500)]))['note']) === 200, 'the note is cut to 200');
$sday = '2026-10-11';
$sw = who_today('203.0.113.77', $sday, $dir);
$got = [];
for ($i = 0; $i < SURVEY_MAX_PER_ADDRESS_PER_DAY + 2; $i++) {
    $got[] = store_survey($pdo, $a, $sday, $sw);
}
check(count(array_keys($got, 'ok', true)) === SURVEY_MAX_PER_ADDRESS_PER_DAY, 'a survey is limited per address per day');
for ($i = 0; $i < 12; $i++) {
    store_survey($pdo, $a, $sday, who_today('192.0.2.' . (100 + $i), $sday, $dir));
}
$ss = survey_summary($pdo);
check($ss['responses'] >= 17 && in_array('synology', array_column($ss['device'] ?? [], 'name'), true), 'survey totals appear once there are enough answers');
check(!isset($ss['note']) && strpos(json_encode($ss), 'fan') === false, 'the notes are never in the totals');

// The survey's spam protection.
$secretDir = $dir;
$now = 1800000000;
$token = survey_token($secretDir, $now - 10);
$pow = 0;
while (leading_zero_bits(hash('sha256', $token . ':' . $pow, true)) < SURVEY_POW_BITS) {
    $pow++;
}
$pdo2 = db($dir);
check(check_survey_token($pdo2, $dir, $token, (string)$pow, $now) === '', 'a good token with its work is accepted');
check(check_survey_token($pdo2, $dir, $token, (string)$pow, $now) === 'used', 'a token works only once');
$fresh = survey_token($dir, $now - 2);
check(check_survey_token($pdo2, $dir, $fresh, '1', $now) === 'too fast', 'a form sent within seconds is refused');
$old = survey_token($dir, $now - SURVEY_TOKEN_MAX_AGE - 5);
check(check_survey_token($pdo2, $dir, $old, '1', $now) === 'too old', 'an old token is refused');
$forged = '1800000000.0123456789abcdef.' . str_repeat('0', 64);
check(check_survey_token($pdo2, $dir, $forged, '1', $now) === 'bad token', 'a forged token is refused');
$t2 = survey_token($dir, $now - 10);
check(check_survey_token($pdo2, $dir, $t2, 'x', $now) === 'no work', 'a token without the work is refused');
check(check_survey_token($pdo2, $dir, null, null, $now) === 'no token', 'no token is refused');

// Live numbers from GitHub and Docker Hub, with a pretend fetch.
$fake = function (string $url) {
    if (strpos($url, 'releases') !== false) {
        return [['tag_name' => 'v3.0.0', 'published_at' => '2026-10-11T08:00:00Z', 'assets' => [['download_count' => 5], ['download_count' => 7]]], ['tag_name' => 'v2.1.10', 'assets' => [['download_count' => 3]]]];
    }
    if (strpos($url, 'api.github.com') !== false) {
        return ['stargazers_count' => 160, 'forks_count' => 12, 'open_issues_count' => 20];
    }
    return ['pull_count' => 321, 'star_count' => 4, 'last_updated' => '2026-10-11T07:00:00Z'];
};
$p = project_numbers($fake);
check($p['github']['stars'] === 160 && $p['github']['releaseDownloads'] === 15 && $p['github']['latest'] === 'v3.0.0' && $p['dockerhub']['pulls'] === 321, 'project numbers are read');
$down = project_numbers(fn($u) => null);
check($down['github'] === null && $down['dockerhub'] === null, 'a site that does not answer gives nothing');
$cacheDir = sys_get_temp_dir() . '/stats-cache-' . bin2hex(random_bytes(4));
mkdir($cacheDir, 0700, true);
$calls = 0;
$counting = function ($u) use ($fake, &$calls) { $calls++; return $fake($u); };
cached_project_numbers($cacheDir, $counting);
$first = $calls;
cached_project_numbers($cacheDir, $counting);
check($first > 0 && $calls === $first, 'the numbers are fetched once an hour, not on every visit');

echo $failed === 0 ? "all good\n" : "$failed failed\n";
exit($failed === 0 ? 0 : 1);
