"""Bind the tested collector artifact to its image identity and source revision."""
import hashlib
import json
from pathlib import Path
import re
import subprocess
import sys

directory, revision = Path(sys.argv[1]), sys.argv[2]
if not re.fullmatch('[a-f0-9]{40}', revision):
    raise SystemExit('Invalid source revision')
with (directory / 'collector.tar.gz').open('rb') as source:
    digest = hashlib.file_digest(source, 'sha256').hexdigest()
identity = subprocess.check_output(['docker', 'image', 'inspect', 'xboard-log-collector:' + revision,
                                    '--format', '{{.Id}}'], text=True).strip()
if not re.fullmatch('sha256:[a-f0-9]{64}', identity):
    raise SystemExit('Invalid image identity')
(directory / 'manifest.json').write_text(json.dumps(dict(revision=revision, sha256=digest, image_id=identity)))
