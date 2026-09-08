<?php

declare(strict_types=1);

// This deliberately reports only generated route/settings state. It does not
// read credentials, tokens, request data, or arbitrary application settings.
use Illuminate\Contracts\Console\Kernel;

require '/www/vendor/autoload.php';

$app = require '/www/bootstrap/app.php';
$app->make(Kernel::class)->bootstrap();

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

echo json_encode([
    'database' => $database,
    'cache' => [
        'secure_path' => $cached['secure_path'] ?? null,
        'frontend_admin_path' => $cached['frontend_admin_path'] ?? null,
    ],
    'admin_user_fetch_routes' => $routes,
], JSON_THROW_ON_ERROR) . PHP_EOL;
