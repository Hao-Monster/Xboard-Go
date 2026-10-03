import importlib.util
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location("stable_release", Path(__file__).with_name("stable-release.py"))
release = importlib.util.module_from_spec(spec)
spec.loader.exec_module(release)


class ReleaseInputsTest(unittest.TestCase):
    def test_accepts_stable_version_and_full_sha(self):
        release.validate("v1.2.0", "a" * 40)

    def test_rejects_tags_shell_input_and_prereleases(self):
        for version in ["stable", "v1.2", "v01.2.3", "v1.2.3-rc1", "v1.2.3\n", "$(id)"]:
            with self.subTest(version=version), self.assertRaises(ValueError):
                release.validate(version, "a" * 40)

    def test_rejects_non_exact_revision(self):
        for revision in ["main", "a" * 39, "A" * 40, "a" * 40 + "\n", "--help"]:
            with self.subTest(revision=revision), self.assertRaises(ValueError):
                release.validate("v1.2.3", revision)

    def test_rejects_existing_exact_tag(self):
        with self.assertRaises(ValueError):
            release.unused("v1.2.3", [{"ref": "refs/tags/v1.2.3"}])

    def test_allows_explicit_compatible_range(self):
        release.validate("v1.3.0", "a" * 40, "v1.2.0")

    def test_rejects_invalid_or_cross_major_range(self):
        for lower in ["v0.9.0", "v1.3.0", "v1.4.0", "stable", "v1.2.0\n"]:
            with self.subTest(lower=lower), self.assertRaises(ValueError):
                release.validate("v1.3.0", "a" * 40, lower)

    def test_allows_new_tag_without_prefix_collision(self):
        release.unused("v1.2.3", [{"ref": "refs/tags/v1.2.30"}])

    def test_invalid_api_response_fails_closed(self):
        with self.assertRaises(ValueError):
            release.unused("v1.2.3", {"message": "Forbidden"})


if __name__ == "__main__":
    unittest.main()
