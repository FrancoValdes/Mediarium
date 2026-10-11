<?php
// GET /stats/summary.php: the public totals, as JSON, for https://mediarium.app/stats/.
// Made at most every ten minutes and kept in a file. See lib.php (make_summary).
declare(strict_types=1);

require __DIR__ . '/lib.php';

header('Content-Type: application/json; charset=utf-8');
header('Cache-Control: public, max-age=300');
header('Access-Control-Allow-Origin: *');
header('X-Content-Type-Options: nosniff');

$dir = data_dir();
$cache = $dir . '/summary.json';
if (is_file($cache) && time() - filemtime($cache) < 600) {
    readfile($cache);
    exit;
}
$summary = make_summary(db($dir), gmdate('Y-m-d'));
$summary['projects'] = cached_project_numbers($dir);
$json = json_encode($summary, JSON_UNESCAPED_SLASHES | JSON_PRETTY_PRINT);
file_put_contents($cache, $json, LOCK_EX);
echo $json;
