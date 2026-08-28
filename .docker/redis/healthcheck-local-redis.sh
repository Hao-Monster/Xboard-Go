#!/bin/sh
set -eu

password="$(tr -d '\r\n' </run/secrets/redis_password)"
export REDISCLI_AUTH="$password"
unset password
exec redis-cli --no-auth-warning ping
