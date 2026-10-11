<?php
// POST /stats/survey.php: the answers from the form at https://mediarium.app/survey/.
// Every answer is a choice from a fixed list (lib.php); the one free field is a short
// optional note that is stored but never shown. Nothing identifies the person: no ID,
// no address. A hidden field that people never fill in catches most bots.
declare(strict_types=1);

require __DIR__ . '/lib.php';

header('Cache-Control: no-store');
header('X-Content-Type-Options: nosniff');

function back(string $to): void {
    header('Location: ' . $to, true, 303);
    exit;
}

if (($_SERVER['REQUEST_METHOD'] ?? '') === 'GET' && isset($_GET['token'])) {
    header('Content-Type: application/json; charset=utf-8');
    echo json_encode(['token' => survey_token(data_dir())]);
    exit;
}
if (($_SERVER['REQUEST_METHOD'] ?? '') !== 'POST') {
    header('Allow: POST');
    http_response_code(405);
    exit;
}
if ((int)($_SERVER['CONTENT_LENGTH'] ?? 0) > 8192) {
    http_response_code(413);
    exit;
}
$fields = $_POST;
if (!empty($fields['website'])) { // the hidden field
    back('/survey/thanks/');
}
$answers = check_survey($fields);
if ($answers === null) {
    back('/survey/?problem=1');
}
$dir = data_dir();
$day = gmdate('Y-m-d');
$pdo = db($dir);
if (check_survey_token($pdo, $dir, $fields['token'] ?? null, $fields['pow'] ?? null) !== '') {
    back('/survey/?problem=3');
}
if (store_survey($pdo, $answers, $day, who_today($_SERVER['REMOTE_ADDR'] ?? '', $day, $dir)) !== 'ok') {
    back('/survey/?problem=2');
}
back('/survey/thanks/');
