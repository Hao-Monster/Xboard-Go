"""Content identity portable between Docker classic and containerd stores."""
import hashlib
import json
import re


def fingerprint(image):
    for field in ('Architecture', 'Os', 'RootFS', 'Config', 'Created'):
        if not image.get(field):
            raise ValueError('Incomplete collector image content identity')
    layers = image['RootFS'].get('Layers', [])
    if not layers or any(not re.fullmatch('sha256:[a-f0-9]{64}', value) for value in layers):
        raise ValueError('Invalid collector image layer identity')
    content = {key: image.get(key) for key in ('Architecture', 'Os', 'Variant', 'RootFS', 'Config', 'Created')}
    # Older inspect APIs materialize these defaults; newer stores omit them.
    # Preserve every non-default value and every unrecognized configuration key.
    defaults = dict(User='', WorkingDir='', Hostname='', Domainname='', Image='',
                    Cmd=None, Volumes=None, OnBuild=None, AttachStdin=False,
                    AttachStdout=False, AttachStderr=False, Tty=False,
                    OpenStdin=False, StdinOnce=False)
    content['Config'] = {key: value for key, value in image['Config'].items()
                         if key not in defaults or value != defaults[key]}
    return hashlib.sha256(json.dumps(content, sort_keys=True, separators=(',', ':')).encode()).hexdigest()
