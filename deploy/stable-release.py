"""Validate publisher inputs before checking out or publishing a release."""
import json
import re
import sys


def validate(version, revision, compatible_from=""):
    if not re.fullmatch(r"v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)", version):
        raise ValueError("Version must be vMAJOR.MINOR.PATCH without leading zeros")
    if not re.fullmatch(r"[0-9a-f]{40}", revision):
        raise ValueError("Revision must be a full lowercase commit SHA")
    if compatible_from:
        validate(compatible_from, revision)
        lower = tuple(map(int, compatible_from[1:].split(".")))
        upper = tuple(map(int, version[1:].split(".")))
        if lower[0] != upper[0] or lower >= upper:
            raise ValueError("Automatic upgrade range must start below the target in the same major version")


def unused(version, refs):
    if not isinstance(refs, list):
        raise ValueError("Invalid GitHub refs response")
    if any(ref["ref"] == "refs/tags/" + version for ref in refs):
        raise ValueError("Version already exists; select a new version")


if __name__ == "__main__":
    try:
        if len(sys.argv) == 5 and sys.argv[1] == "validate":
            validate(sys.argv[2], sys.argv[3], sys.argv[4])
        elif len(sys.argv) == 4 and sys.argv[1] == "unused":
            with open(sys.argv[3], encoding="utf-8") as source:
                unused(sys.argv[2], json.load(source))
        else:
            raise ValueError("Expected validate VERSION SHA COMPATIBLE_FROM or unused VERSION REFS_JSON")
    except (ValueError, KeyError, TypeError) as error:
        sys.exit(str(error))
