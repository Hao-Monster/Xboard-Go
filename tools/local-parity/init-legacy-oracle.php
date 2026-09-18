<?php

declare(strict_types=1);

// The prior exact script was deleted, so this implementation was reconstructed
// from the fixed image's migrations and installer. It was revalidated against
// xboard-legacy-parity:8065164 after real migrations on 2026-09-08. It uses
// Laravel models and never creates tables or writes a hand-crafted schema.

use Illuminate\Contracts\Console\Kernel;
use Illuminate\Support\Facades\Cache;
use App\Utils\Helper;

require '/www/vendor/autoload.php';

$app = require '/www/bootstrap/app.php';
$app->make(Kernel::class)->bootstrap();

function requiredEnvironment(string $name): string
{
    $value = getenv($name);
    if (!is_string($value) || trim($value) === '') {
        throw new RuntimeException($name . ' is required');
    }
    return trim($value);
}

$email = requiredEnvironment('LOCAL_PARITY_ADMIN_EMAIL');
$password = requiredEnvironment('LOCAL_PARITY_ADMIN_PASSWORD');
$adminPath = requiredEnvironment('LOCAL_PARITY_ADMIN_PATH');

if (!filter_var($email, FILTER_VALIDATE_EMAIL)) {
    throw new RuntimeException('LOCAL_PARITY_ADMIN_EMAIL must be a valid email');
}
if (!preg_match('/^[A-Za-z0-9_-]+$/', $adminPath)) {
    throw new RuntimeException('LOCAL_PARITY_ADMIN_PATH must contain only A-Z, a-z, 0-9, underscore, or hyphen');
}

$userClass = '\\App\\Models\\User';
$settingClass = '\\App\\Models\\Setting';
if (!class_exists($userClass) || !class_exists($settingClass)) {
    throw new RuntimeException('legacy User or Setting model was not found; update this reconstructed template before retrying');
}

$user = $userClass::byEmail($email)->first();
if ($user === null) {
    $user = new $userClass();
    $user->email = $email;
    $user->password = password_hash($password, PASSWORD_DEFAULT);
    // These are the two non-default identity columns in the image's real
    // v2_user migration. Match its installer instead of weakening SQLite.
    $user->uuid = Helper::guid(true);
    $user->token = Helper::guid();
    $user->is_admin = 1;
    $user->save();
} else {
    // Re-entry refreshes credentials/admin access but never rotates an
    // existing user's UUID or token identity.
    $user->password = password_hash($password, PASSWORD_DEFAULT);
    $user->is_admin = 1;
    $user->save();
}

foreach (['secure_path', 'frontend_admin_path'] as $key) {
    $settingClass::createOrUpdate($key, $adminPath);
}

// Kernel boot can load the same route files that call admin_setting(). The
// setting implementation remembers the entire set forever, so the CLI may
// have cached the pre-initialization empty database. Invalidate only that
// documented key; never flush the shared Redis database.
$cacheStore = config('cache.settings_store', 'redis');
Cache::store($cacheStore)->forget(\App\Support\Setting::CACHE_KEY);

$adminCount = $userClass::query()->where('is_admin', 1)->count();
$securePath = $settingClass::query()->where('name', 'secure_path')->value('value');
if ($securePath !== $adminPath) {
    throw new RuntimeException('secure_path readback did not match the generated administrator path');
}

echo json_encode([
    'admin_count' => $adminCount,
    'secure_path' => $securePath,
], JSON_THROW_ON_ERROR) . PHP_EOL;
