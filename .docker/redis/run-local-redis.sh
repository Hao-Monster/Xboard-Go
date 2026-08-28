#!/bin/sh
set -eu

password="$(tr -d '\r\n' </run/secrets/redis_password)"
case "$password" in
  ''|*[!0-9A-Za-z_-]*)
    echo "redis password must contain only 0-9, A-Z, a-z, underscore, or hyphen" >&2
    exit 1
    ;;
esac
if [ "${#password}" -lt 32 ] || [ "${#password}" -gt 128 ]; then
  echo "redis password must contain between 32 and 128 characters" >&2
  exit 1
fi

umask 077
{
  printf 'bind 0.0.0.0\n'
  printf 'protected-mode yes\n'
  printf 'save ""\n'
  printf 'appendonly no\n'
  printf 'dir /data\n'
  printf 'requirepass %s\n' "$password"
} >/tmp/redis.conf
unset password
exec redis-server /tmp/redis.conf
