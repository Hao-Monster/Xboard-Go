#!/usr/bin/env python3
"""Converge Xboard-Go environments and GitHub releases to retain online-only resources."""
import argparse
import json
import os
from pathlib import Path
import re
import shutil
import socket
import subprocess
import sys
import urllib.error
import urllib.request

HEX40_RE = re.compile(r'^[0-9a-f]{40}$')
HEX64_RE = re.compile(r'^[0-9a-f]{64}$')
STALE_DEV_CONTAINER_RE = re.compile(
    r'^(?:xboard-go-(?:closeout|r006|d009|arch001|acceptance|func003|ver004|node006|golang-base|legacy-parity)(?:-|$)'
    r'|xboardme-preonline'
    r'|xboard-xdb-extract$)'
)
STALE_DEV_VOLUME_RE = re.compile(
    r'^(?:xboard-go-(?:closeout|r006|d009|arch001|acceptance|func003|ver004|ops002|node006|legacy-parity)[_-]'
    r'|xboardme-preonline[_-]'
    r'|[0-9a-f]{64}$)'
)
STALE_DEV_NETWORK_RE = re.compile(
    r'^(?:xboard-go-(?:closeout|r006|d009|arch001|acceptance|func003|ver004|golang-base|legacy-parity)[_-]'
    r'|xboardme-preonline)'
)
STALE_APPS_ENTRY_RE = re.compile(
    r'^(?:xboard-go-(?!freedom$)|Xboard-go-|xboardme-|xboard-node-|xboard-team-|rootless-smoke-|run-rootless-smoke\.edit\.sh$)'
)


def run_cmd(args, *, check=True, env=None, input_text=None):
    return subprocess.run(
        args,
        check=check,
        text=True,
        input=input_text,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        env=env,
    )


def disk_available_kib(path='/'):
    result = run_cmd(['df', '-Pk', path])
    lines = [line.strip() for line in result.stdout.splitlines() if line.strip()]
    if len(lines) < 2:
        raise RuntimeError(f'Unexpected df output for {path}')
    fields = lines[-1].split()
    return int(fields[3])


def check_http_health(url, timeout=15):
    req = urllib.request.Request(url, headers={'User-Agent': 'xboard-retain-online-only/1.0'})
    with urllib.request.urlopen(req, timeout=timeout) as resp:
        if resp.status != 200:
            raise RuntimeError(f'Health check HTTP {resp.status} for {url}')


def docker_cmd(args, *, docker_host=None, check=True):
    env = os.environ.copy()
    if docker_host:
        env['DOCKER_HOST'] = docker_host
    return run_cmd(['docker', *args], check=check, env=env)


def inspect_containers(docker_host=None):
    ids_raw = docker_cmd(['ps', '-aq', '--no-trunc'], docker_host=docker_host).stdout.splitlines()
    ids = [cid.strip() for cid in ids_raw if cid.strip()]
    if not ids:
        return []
    return json.loads(docker_cmd(['inspect', *ids], docker_host=docker_host).stdout)


def active_systemd_release_shas(unit_paths):
    shas = set()
    for path in unit_paths:
        p = Path(path)
        if p.is_file() and not p.is_symlink():
            text = p.read_text(encoding='utf-8', errors='replace')
            for match in re.finditer(r'/releases/([0-9a-f]{40})/', text):
                shas.add(match.group(1))
    return shas


def prune_sorted_files(files, keep_count, mode):
    ordered = sorted(files, key=lambda p: (p.stat().st_mtime, p.name), reverse=True)
    candidates = ordered[keep_count:]
    removed = 0
    if mode == 'cleanup':
        for item in candidates:
            if item.is_file() or item.is_symlink():
                item.unlink()
                removed += 1
    return len(candidates), removed


def remove_path_robust(target: Path, parent_mount: Path | None = None):
    if not target.exists() and not target.is_symlink():
        return
    if target.is_symlink() or target.is_file():
        target.unlink()
        return
    run_cmd(['chmod', '-R', 'u+rwX', str(target)], check=False)
    shutil.rmtree(target, ignore_errors=True)
    if target.exists() and parent_mount is not None:
        docker_cmd([
            'run', '--rm',
            '-v', f'{parent_mount}:/cleanup-target',
            'alpine:3.20',
            'rm', '-rf', '--', f'/cleanup-target/{target.name}',
        ], check=True)
    if target.exists():
        raise RuntimeError(f'Failed to remove directory: {target}')


def prune_unreferenced_images(protected_image_ids, protected_refs, mode, *, docker_host=None):
    lines = docker_cmd(
        ['image', 'ls', '--no-trunc', '--format', '{{.Repository}}:{{.Tag}}\t{{.ID}}'],
        docker_host=docker_host,
    ).stdout.splitlines()
    candidate_refs = []
    redundant_alias_refs = []
    candidate_ids = set()
    ids_with_protected_tag = set()
    for line in lines:
        if '\t' not in line:
            continue
        ref, img_id = line.split('\t', 1)
        ref = ref.strip()
        img_id = img_id.strip()
        if ref in protected_refs:
            protected_image_ids.add(img_id)
            ids_with_protected_tag.add(img_id)
    for line in lines:
        if '\t' not in line:
            continue
        ref, img_id = line.split('\t', 1)
        ref = ref.strip()
        img_id = img_id.strip()
        if ref in protected_refs:
            continue
        if img_id in protected_image_ids:
            if img_id in ids_with_protected_tag and ref and '<none>' not in ref:
                redundant_alias_refs.append(ref)
            continue
        if ref and '<none>' not in ref:
            candidate_refs.append(ref)
        else:
            candidate_ids.add(img_id)

    removed = 0
    if mode == 'cleanup':
        for ref in sorted(set(redundant_alias_refs)):
            if docker_cmd(['image', 'rm', ref], docker_host=docker_host, check=False).returncode == 0:
                removed += 1
        for ref in sorted(set(candidate_refs)):
            if docker_cmd(['image', 'rm', '-f', ref], docker_host=docker_host, check=False).returncode == 0:
                removed += 1
        remaining_lines = docker_cmd(
            ['image', 'ls', '--no-trunc', '-q'],
            docker_host=docker_host,
        ).stdout.splitlines()
        for img_id in sorted({x.strip() for x in remaining_lines if x.strip()} - protected_image_ids):
            if docker_cmd(['image', 'rm', '-f', img_id], docker_host=docker_host, check=False).returncode == 0:
                removed += 1
        docker_cmd(['builder', 'prune', '-af'], docker_host=docker_host, check=False)
    total_candidates = len(set(redundant_alias_refs)) + len(set(candidate_refs)) + len(candidate_ids)
    return total_candidates, removed


def run_production_host(mode: str):
    if socket.gethostname() != 'vmi3574179':
        raise RuntimeError('Unexpected production server hostname')
    before_kib = disk_available_kib('/')
    required_running = {
        'xboard-production-internal-app-1',
        'xboard-log-collector-production-collector-1',
        'remnawave',
        'remnawave-db',
        'remnawave-redis',
        'caddy',
    }
    containers = inspect_containers()
    by_name = {c['Name'].lstrip('/'): c for c in containers}
    for name in required_running:
        c = by_name.get(name)
        if not c or not c.get('State', {}).get('Running'):
            raise RuntimeError(f'Required production container is not running: {name}')
    app_health = by_name['xboard-production-internal-app-1'].get('State', {}).get('Health', {}).get('Status')
    if app_health != 'healthy':
        raise RuntimeError(f'Production app container is not healthy: {app_health}')

    check_http_health('http://127.0.0.1:7080/healthz')
    check_http_health('https://fast.hjy.ca:8443/healthz')

    optional_protected = {'remnanode'}
    protected_names = set(required_running)
    for opt in optional_protected:
        if opt in by_name and by_name[opt].get('State', {}).get('Running'):
            protected_names.add(opt)

    protected_cids = {by_name[name]['Id'] for name in protected_names}
    protected_images = {by_name[name]['Image'] for name in protected_names}
    protected_refs = {by_name[name]['Config']['Image'] for name in protected_names}

    candidate_containers = []
    for c in containers:
        cid = c['Id']
        if cid in protected_cids:
            continue
        if c.get('State', {}).get('Running'):
            raise RuntimeError(f'Unexpected running non-protected container on production: {c["Name"]}')
        candidate_containers.append(cid)

    removed_containers = 0
    if mode == 'cleanup':
        for cid in candidate_containers:
            docker_cmd(['container', 'rm', cid])
            removed_containers += 1

    cand_images, removed_images = prune_unreferenced_images(protected_images, protected_refs, mode)

    # Observability release directories
    collector_img = by_name['xboard-log-collector-production-collector-1']['Config']['Image']
    active_shas = active_systemd_release_shas(['/etc/systemd/system/xboard-logs-probe.service'])
    if ':' in collector_img and HEX40_RE.match(collector_img.split(':', 1)[1]):
        active_shas.add(collector_img.split(':', 1)[1])
    obs_releases = Path('/opt/xboard-observability/releases')
    cand_obs_dirs = []
    removed_obs_dirs = 0
    if obs_releases.is_dir() and not obs_releases.is_symlink():
        for entry in sorted(obs_releases.iterdir()):
            if HEX40_RE.match(entry.name) and entry.name not in active_shas:
                cand_obs_dirs.append(entry)
                if mode == 'cleanup':
                    shutil.rmtree(entry)
                    removed_obs_dirs += 1

    # /opt/xboard-go historical files
    app_dir = Path('/opt/xboard-go')
    cand_files = 0
    removed_files = 0
    if app_dir.is_dir() and not app_dir.is_symlink():
        c1, r1 = prune_sorted_files(list(app_dir.glob('.env.before-*')), 1, mode)
        c2, r2 = prune_sorted_files(list(app_dir.glob('compose.before-*.yaml')), 1, mode)
        c3, r3 = prune_sorted_files(list(app_dir.glob('*.failure.log')), 0, mode)
        cand_files += c1 + c2 + c3
        removed_files += r1 + r2 + r3

    # Volume backups (keep newest 2)
    vol_mount = docker_cmd(['volume', 'inspect', '-f', '{{.Mountpoint}}', 'xboard-production-internal_backups']).stdout.strip()
    bp = Path(vol_mount)
    if bp.is_dir() and not bp.is_symlink():
        cb, rb = prune_sorted_files(list(bp.glob('*.xbbackup')), 2, mode)
        cand_files += cb
        removed_files += rb

    check_http_health('http://127.0.0.1:7080/healthz')
    check_http_health('https://fast.hjy.ca:8443/healthz')
    after_kib = disk_available_kib('/')
    print(f'RETAIN_ONLINE_PRODUCTION=PASS mode={mode} candidate_containers={len(candidate_containers)} '
          f'removed_containers={removed_containers} candidate_images={cand_images} removed_images={removed_images} '
          f'candidate_obs_dirs={len(cand_obs_dirs)} removed_obs_dirs={removed_obs_dirs} '
          f'candidate_files={cand_files} removed_files={removed_files} '
          f'reclaimed_kib={after_kib - before_kib}')


def run_development_host(mode: str):
    if socket.gethostname() != 'bingo':
        raise RuntimeError('Unexpected development server hostname')
    before_kib = disk_available_kib('/')
    containers = inspect_containers()
    by_name = {c['Name'].lstrip('/'): c for c in containers}
    for req in ('xboard-go-freedom-app-1', 'xboard-log-collector-development-collector-1',
                'xboard-logs-auth-1', 'xboard-logs-victoria-logs-1'):
        c = by_name.get(req)
        if not c or not c.get('State', {}).get('Running'):
            raise RuntimeError(f'Required development container is not running: {req}')
    if by_name['xboard-go-freedom-app-1'].get('State', {}).get('Health', {}).get('Status') != 'healthy':
        raise RuntimeError('xboard-go-freedom-app-1 is not healthy')

    check_http_health('http://127.0.0.1:18082/healthz')
    check_http_health('https://freedom.openal.uk/healthz')

    protected_cids = set()
    protected_images = set()
    protected_refs = {
        'xboard-go:ci-base-3784c0a',
        'xboard-legacy-parity:8065164',
        'alpine:3.20',
    }
    protected_volumes = {
        'xboard-go-freedom_app_data',
        'xboard-go-freedom_app_backups',
        'celerity-panel_mongo_data',
        'freedompost-preflight_pgdata',
        'freedompost-preflight_redisdata',
        'freedompost-support-test_postgres_data',
        'freedompost-support-test_redis_data',
        'freedompost-support-test_storage_data',
        'wechatproduct-postgres-data',
        'wechatproduct-redis-data',
    }
    protected_networks = {
        'bridge', 'host', 'none',
        'xboard-go-freedom_default',
        'xboard-logs_default',
        'xboard-legacy-oracle_default',
        'celerity-panel_hysteria-net',
        'freedompost-support-test_default',
        'wechatproduct_backend',
        'wechatproduct_edge',
    }

    candidate_containers = []
    for c in containers:
        name = c['Name'].lstrip('/')
        cid = c['Id']
        running = bool(c.get('State', {}).get('Running'))
        restarting = bool(c.get('State', {}).get('Restarting'))
        if STALE_DEV_CONTAINER_RE.search(name) or (not running and not restarting):
            candidate_containers.append((cid, name, running or restarting))
        else:
            protected_cids.add(cid)
            protected_images.add(c['Image'])
            protected_refs.add(c['Config']['Image'])
            for m in c.get('Mounts', []):
                if m.get('Name'):
                    protected_volumes.add(m['Name'])
            for net_name in (c.get('NetworkSettings', {}).get('Networks') or {}):
                protected_networks.add(net_name)

    removed_containers = 0
    if mode == 'cleanup':
        for cid, _name, is_active in candidate_containers:
            if is_active:
                docker_cmd(['container', 'stop', '--time', '5', cid], check=False)
            if docker_cmd(['container', 'rm', '-f', cid], check=False).returncode == 0:
                removed_containers += 1

    # Networks
    net_lines = docker_cmd(['network', 'ls', '--no-trunc', '--format', '{{.ID}}\t{{.Name}}']).stdout.splitlines()
    candidate_networks = []
    removed_networks = 0
    for line in net_lines:
        if '\t' not in line:
            continue
        nid, nname = (x.strip() for x in line.split('\t', 1))
        if nname in protected_networks or not STALE_DEV_NETWORK_RE.search(nname):
            continue
        candidate_networks.append((nid, nname))
        if mode == 'cleanup':
            if docker_cmd(['network', 'rm', nid], check=False).returncode == 0:
                removed_networks += 1

    # Volumes
    vol_names = [v.strip() for v in docker_cmd(['volume', 'ls', '-q']).stdout.splitlines() if v.strip()]
    candidate_volumes = []
    removed_volumes = 0
    for vname in vol_names:
        if vname in protected_volumes or not STALE_DEV_VOLUME_RE.search(vname):
            continue
        candidate_volumes.append(vname)
        if mode == 'cleanup':
            if docker_cmd(['volume', 'rm', vname], check=False).returncode == 0:
                removed_volumes += 1

    # Images in system Docker
    cand_images, removed_images = prune_unreferenced_images(protected_images, protected_refs, mode)

    # Rootless Docker of bingo (/run/user/1000/docker.sock) if present
    rootless_sock = Path(f'/run/user/{os.getuid()}/docker.sock')
    if rootless_sock.is_socket():
        rl_host = f'unix://{rootless_sock}'
        rl_containers = inspect_containers(docker_host=rl_host)
        rl_prot_imgs = set()
        rl_prot_refs = {'xboard-legacy-parity:8065164', 'xboard-go:ci-base-3784c0a'}
        for c in rl_containers:
            if c.get('State', {}).get('Running'):
                rl_prot_imgs.add(c['Image'])
                rl_prot_refs.add(c['Config']['Image'])
            elif mode == 'cleanup':
                docker_cmd(['container', 'rm', '-f', c['Id']], docker_host=rl_host, check=False)
        ci_rl, ri_rl = prune_unreferenced_images(rl_prot_imgs, rl_prot_refs, mode, docker_host=rl_host)
        cand_images += ci_rl
        removed_images += ri_rl
        if mode == 'cleanup':
            docker_cmd(['volume', 'prune', '-f'], docker_host=rl_host, check=False)

    # Clean /home/bingo/apps offline directories
    apps_root = Path('/home/bingo/apps')
    protected_apps = {
        'xboard-go-freedom',
        'xboard-logs',
        'xboard-legacy-oracle',
        'freedompost-preflight-45dcc39',
        'freedompost-support',
    }
    cand_app_dirs = []
    removed_app_dirs = 0
    if apps_root.is_dir() and not apps_root.is_symlink():
        for entry in sorted(apps_root.iterdir()):
            if entry.name in protected_apps:
                continue
            if STALE_APPS_ENTRY_RE.search(entry.name):
                cand_app_dirs.append(entry)
                if mode == 'cleanup':
                    remove_path_robust(entry, parent_mount=apps_root)
                    removed_app_dirs += 1

    # Clean inside /home/bingo/apps/xboard-go-freedom
    freedom_dir = apps_root / 'xboard-go-freedom'
    cand_freedom_items = 0
    removed_freedom_items = 0
    if freedom_dir.is_dir() and not freedom_dir.is_symlink():
        c1, r1 = prune_sorted_files(list(freedom_dir.glob('.env.pre-*')), 1, mode)
        c2, r2 = prune_sorted_files(list(freedom_dir.glob('compose.yaml.pre-*')), 1, mode)
        cand_freedom_items += c1 + c2
        removed_freedom_items += r1 + r2
        keep_freedom = {
            '.env', 'compose.yaml', 'compose.observability.yaml',
            '.deployment.lock', '.secrets', 'node-releases',
            'nginx.conf', 'cloudflared.yml',
        }
        for p in sorted(freedom_dir.glob('.env.pre-*'), key=lambda x: (x.stat().st_mtime, x.name), reverse=True)[:1]:
            keep_freedom.add(p.name)
        for p in sorted(freedom_dir.glob('compose.yaml.pre-*'), key=lambda x: (x.stat().st_mtime, x.name), reverse=True)[:1]:
            keep_freedom.add(p.name)
        for item in sorted(freedom_dir.iterdir()):
            if item.name in keep_freedom or item.name.startswith('.deployment-failed'):
                continue
            cand_freedom_items += 1
            if mode == 'cleanup':
                remove_path_robust(item, parent_mount=freedom_dir)
                removed_freedom_items += 1

    # Clean old backups inside xboard-go-freedom_app_backups volume (keep newest 2)
    if mode == 'cleanup':
        docker_cmd([
            'run', '--rm',
            '-v', 'xboard-go-freedom_app_backups:/backups',
            'alpine:3.20',
            'sh', '-c',
            'ls -1t /backups/*.xbbackup 2>/dev/null | tail -n +3 | xargs -r rm -f --',
        ], check=False)

    # Clean /home/bingo/apps/xboard-logs/releases
    collector_img = by_name['xboard-log-collector-development-collector-1']['Config']['Image']
    active_log_shas = active_systemd_release_shas([
        Path.home() / '.config/systemd/user/xboard-logs-health.service',
        Path.home() / '.config/systemd/user/xboard-logs-probe.service',
    ])
    if ':' in collector_img and HEX40_RE.match(collector_img.split(':', 1)[1]):
        active_log_shas.add(collector_img.split(':', 1)[1])
    logs_releases = apps_root / 'xboard-logs/releases'
    if logs_releases.is_dir() and not logs_releases.is_symlink():
        for entry in sorted(logs_releases.iterdir()):
            if HEX40_RE.match(entry.name) and entry.name not in active_log_shas:
                cand_freedom_items += 1
                if mode == 'cleanup':
                    shutil.rmtree(entry)
                    removed_freedom_items += 1

    check_http_health('http://127.0.0.1:18082/healthz')
    check_http_health('https://freedom.openal.uk/healthz')
    after_kib = disk_available_kib('/')
    print(f'RETAIN_ONLINE_DEVELOPMENT=PASS mode={mode} candidate_containers={len(candidate_containers)} '
          f'removed_containers={removed_containers} candidate_networks={len(candidate_networks)} '
          f'removed_networks={removed_networks} candidate_volumes={len(candidate_volumes)} '
          f'removed_volumes={removed_volumes} candidate_images={cand_images} removed_images={removed_images} '
          f'candidate_app_dirs={len(cand_app_dirs)} removed_app_dirs={removed_app_dirs} '
          f'candidate_freedom_items={cand_freedom_items} removed_freedom_items={removed_freedom_items} '
          f'reclaimed_kib={after_kib - before_kib}')


def run_runner_host(mode: str, workspace: Path, keep_dirs: set[str]):
    cand_dirs = []
    removed_dirs = 0
    if workspace.is_dir() and not workspace.is_symlink():
        for entry in sorted(workspace.iterdir()):
            if entry.name in keep_dirs:
                continue
            if re.match(r'^(?:candidate|logging)-[0-9]+-[0-9]+$', entry.name):
                cand_dirs.append(entry)
                if mode == 'cleanup':
                    remove_path_robust(entry)
                    removed_dirs += 1

    containers = inspect_containers()
    prot_imgs = set()
    prot_refs = {'xboard-go:ci-base-3784c0a', 'xboard-legacy-parity:8065164'}
    for c in containers:
        if c.get('State', {}).get('Running'):
            prot_imgs.add(c['Image'])
            prot_refs.add(c['Config']['Image'])
        elif mode == 'cleanup':
            docker_cmd(['container', 'rm', '-f', c['Id']], check=False)

    cand_images, removed_images = prune_unreferenced_images(prot_imgs, prot_refs, mode)
    if mode == 'cleanup':
        docker_cmd(['volume', 'prune', '-f'], check=False)
        docker_cmd(['network', 'prune', '-f'], check=False)

    print(f'RETAIN_ONLINE_RUNNER=PASS mode={mode} candidate_dirs={len(cand_dirs)} '
          f'removed_dirs={removed_dirs} candidate_images={cand_images} removed_images={removed_images}')


def gh_api(path: str, *, method='GET', token: str):
    req = urllib.request.Request(
        'https://api.github.com/' + path.lstrip('/'),
        method=method,
        headers={
            'Authorization': f'Bearer {token}',
            'Accept': 'application/vnd.github+json',
            'X-GitHub-Api-Version': '2022-11-28',
        },
    )
    with urllib.request.urlopen(req, timeout=30) as resp:
        body = resp.read()
        return json.loads(body.decode('utf-8')) if body else None


def select_superseded_internal_releases(releases, keep_shas: set[str], keep_latest: int = 2):
    internal = []
    for rel in releases:
        tag = rel.get('tag_name', '')
        if re.fullmatch(r'internal-[0-9a-f]{40}', tag):
            internal.append(rel)
    internal.sort(key=lambda r: r.get('created_at') or '', reverse=True)
    protected_tags = {f'internal-{sha}' for sha in keep_shas if HEX40_RE.match(sha)}
    for rel in internal[:keep_latest]:
        protected_tags.add(rel['tag_name'])
    return [rel for rel in internal if rel['tag_name'] not in protected_tags]


def run_github_releases(mode: str, repo: str, keep_shas: set[str], keep_latest: int):
    token = os.environ.get('GH_TOKEN', '').strip()
    if not token:
        raise RuntimeError('GH_TOKEN is required for github-releases cleanup')
    releases = []
    page = 1
    while True:
        batch = gh_api(f'repos/{repo}/releases?per_page=100&page={page}', token=token)
        if not batch:
            break
        releases.extend(batch)
        if len(batch) < 100:
            break
        page += 1

    candidates = select_superseded_internal_releases(releases, keep_shas, keep_latest=keep_latest)
    removed = 0
    if mode == 'cleanup':
        for rel in candidates:
            rel_id = rel['id']
            tag = rel['tag_name']
            gh_api(f'repos/{repo}/releases/{rel_id}', method='DELETE', token=token)
            try:
                gh_api(f'repos/{repo}/git/refs/tags/{tag}', method='DELETE', token=token)
            except urllib.error.HTTPError as err:
                if err.code != 404:
                    raise
            removed += 1
    print(f'RETAIN_ONLINE_GITHUB_RELEASES=PASS mode={mode} total_releases={len(releases)} '
          f'candidate_internal_releases={len(candidates)} removed_internal_releases={removed}')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest='target', required=True)

    p_prod = sub.add_parser('production-host')
    p_prod.add_argument('--mode', choices=['audit', 'cleanup'], default='audit')

    p_dev = sub.add_parser('development-host')
    p_dev.add_argument('--mode', choices=['audit', 'cleanup'], default='audit')

    p_run = sub.add_parser('runner-host')
    p_run.add_argument('--mode', choices=['audit', 'cleanup'], default='audit')
    p_run.add_argument('--workspace', default=os.environ.get('GITHUB_WORKSPACE', '.'))
    p_run.add_argument('--keep-dir', action='append', default=[])

    p_gh = sub.add_parser('github-releases')
    p_gh.add_argument('--mode', choices=['audit', 'cleanup'], default='audit')
    p_gh.add_argument('--repo', default=os.environ.get('GITHUB_REPOSITORY', 'Hao-Monster/Xboard-Go'))
    p_gh.add_argument('--keep-sha', action='append', default=[])
    p_gh.add_argument('--keep-latest', type=int, default=2)

    args = parser.parse_args()
    if args.target == 'production-host':
        run_production_host(args.mode)
    elif args.target == 'development-host':
        run_development_host(args.mode)
    elif args.target == 'runner-host':
        run_runner_host(args.mode, Path(args.workspace), set(args.keep_dir))
    elif args.target == 'github-releases':
        run_github_releases(args.mode, args.repo, set(args.keep_sha), args.keep_latest)


if __name__ == '__main__':
    main()
