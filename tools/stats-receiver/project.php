<?php
// GET /stats/project.php: the project's public GitHub and Docker Hub numbers only
// (stars, forks, downloads, pulls), for the star count in the website's header.
// Read from the public pages of those sites at most every ten minutes and kept in a file.
declare(strict_types=1);

require __DIR__ . '/lib.php';

header('Content-Type: application/json; charset=utf-8');
header('Cache-Control: public, max-age=300');
header('Access-Control-Allow-Origin: *');
header('X-Content-Type-Options: nosniff');

echo json_encode(cached_project_numbers(data_dir()), JSON_UNESCAPED_SLASHES);
