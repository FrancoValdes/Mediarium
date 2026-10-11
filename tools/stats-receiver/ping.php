<?php
// POST /stats/ping.php: one message from an install that opted in. See lib.php and
// docs/usage-stats.md. The answer is only ever 204 (taken), 400, 405, 413 or 429.
declare(strict_types=1);

require __DIR__ . '/lib.php';

header('Cache-Control: no-store');
header('X-Content-Type-Options: nosniff');

function end_with(int $status): void {
    http_response_code($status);
    exit;
}

if (($_SERVER['REQUEST_METHOD'] ?? '') !== 'POST') {
    header('Allow: POST');
    end_with(405);
}
$length = (int)($_SERVER['CONTENT_LENGTH'] ?? 0);
if ($length <= 0 || $length > MAX_BODY_BYTES) {
    end_with($length > MAX_BODY_BYTES ? 413 : 400);
}
$body = file_get_contents('php://input', false, null, 0, MAX_BODY_BYTES + 1);
if ($body === false || strlen($body) > MAX_BODY_BYTES) {
    end_with(413);
}
$message = check_message(json_decode($body, true, 4));
if ($message === null) {
    end_with(400);
}

$dir = data_dir();
$day = gmdate('Y-m-d');
$pdo = db($dir);
$result = store_message($pdo, $message, $day, who_today($_SERVER['REMOTE_ADDR'] ?? '', $day, $dir));
if ($result !== 'ok') {
    end_with(429);
}
if (random_int(1, 100) === 1) {
    tidy($pdo, $day);
}
end_with(204);
