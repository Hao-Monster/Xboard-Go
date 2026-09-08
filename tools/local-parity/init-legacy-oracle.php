<?php

declare(strict_types=1);

// Transcript-reconstructed initialization template. The prior exact script was
// deleted; this must be revalidated against xboard-legacy-parity:8065164 before
// a run is recorded as evidence. It uses Laravel models after real migrations
// and never creates tables or writes a hand-crafted schema.

use Illuminate\Contracts\Console\Kernel;
use Illuminate\Support\Facades\Hash;

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

$user = $userClass::query()->firstOrNew(['email' => $email]);
$user->email = $email;
$user->password = Hash::make($password);
$user->is_admin = 1;
$user->save();

foreach (['secure_path', 'frontend_admin_path'] as $key) {
    $setting = $settingClass::query()->firstOrNew(['key' => $key]);
    $setting->key = $key;
    $setting->value = $adminPath;
    $setting->save();
}

$adminCount = $userClass::query()->where('is_admin', 1)->count();
$securePath = $settingClass::query()->where('key', 'secure_path')->value('value');
if ($securePath !== $adminPath) {
    throw new RuntimeException('secure_path readback did not match the generated administrator path');
}

echo json_encode([
    'admin_count' => $adminCount,
    'secure_path' => $securePath,
], JSON_THROW_ON_ERROR) . PHP_EOL;
