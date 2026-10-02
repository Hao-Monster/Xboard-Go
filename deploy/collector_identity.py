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
    return hashlib.sha256(json.dumps(content, sort_keys=True, separators=(',', ':')).encode()).hexdigest()
