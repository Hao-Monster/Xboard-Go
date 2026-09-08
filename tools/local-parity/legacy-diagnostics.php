<?php

declare(strict_types=1);

// Keep 500 evidence safe to retain: identify an exception category and the
// first application/vendor frame only. Do not export raw requests or logs.
$files = glob('/www/storage/logs/laravel*.log') ?: [];
rsort($files, SORT_STRING);
$tail = '';
if ($files !== []) {
    $contents = file_get_contents($files[0]);
    if (is_string($contents)) {
        $tail = substr($contents, -262144);
    }
}

$category = 'unavailable';
if (preg_match('/(?:^|[\\s:])([A-Za-z_\\\\][A-Za-z0-9_\\\\]*(?:Exception|Error))(?:[\\s:]|$)/m', $tail, $match) === 1) {
    $category = $match[1];
}
$location = 'unavailable';
if (preg_match('#/www/(?:app|vendor)/[^:\\s]+:\\d+#', $tail, $match) === 1) {
    $location = $match[0];
}

echo json_encode([
    'source' => 'latest-laravel-log',
    'exception_category' => $category,
    'stack_location' => $location,
], JSON_THROW_ON_ERROR) . PHP_EOL;
