<?php

declare(strict_types=1);

// This deliberately reports only generated route/settings state. It does not
// read credentials, tokens, request data, or arbitrary application settings.
use Illuminate\Contracts\Console\Kernel;

require '/www/vendor/autoload.php';

$app = require '/www/bootstrap/app.php';
$app->make(Kernel::class)->bootstrap();

$expected = getenv('LOCAL_PARITY_ADMIN_PATH');
if (!is_string($expected) || preg_match('/^[A-Za-z0-9_-]+$/', $expected) !== 1) {
    throw new RuntimeException('LOCAL_PARITY_ADMIN_PATH must contain the generated administrator path');
}

$model = '\\App\\Models\\Setting';
$database = [];
foreach (['secure_path', 'frontend_admin_path'] as $key) {
    $database[$key] = $model::query()->where('name', $key)->value('value');
}

$cached = app(\App\Support\Setting::class)->toArray();
$routes = [];
foreach (app('router')->getRoutes()->getRoutes() as $route) {
    $uri = $route->uri();
    if (preg_match('#^api/v2/[^/]+/user/fetch$#', $uri) === 1) {
        $routes[] = $uri;
    }
}

$expectedRoute = 'api/v2/' . $expected . '/user/fetch';
if ($database['secure_path'] !== $expected || $database['frontend_admin_path'] !== $expected) {
    throw new RuntimeException('legacy database administrator paths did not match the generated path');
}
if (($cached['secure_path'] ?? null) !== $expected || ($cached['frontend_admin_path'] ?? null) !== $expected) {
    throw new RuntimeException('legacy Redis-backed administrator-path cache did not match the generated path');
}
if ($routes !== [$expectedRoute]) {
    throw new RuntimeException('legacy registered administrator route did not match the generated path');
}

echo json_encode([
    'database' => $database,
    'cache' => [
        'secure_path' => $cached['secure_path'] ?? null,
        'frontend_admin_path' => $cached['frontend_admin_path'] ?? null,
    ],
    'admin_user_fetch_routes' => $routes,
    'verified_path' => $expected,
], JSON_THROW_ON_ERROR) . PHP_EOL;
