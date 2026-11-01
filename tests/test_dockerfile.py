"""The image builds the web app on the Node that web/.node-version names
(those-who-run/paddock#131 R1), the one `make web-check` and CI's `web` job
insist on. Renovate bumps the two in one PR (renovate.json's `node` group);
this keeps them from drifting apart. Standard library only."""

from __future__ import annotations

import re
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent

NODE = re.compile(r"^FROM\s+docker\.io/library/node:(\d+\.\d+\.\d+)-\S*@sha256:[0-9a-f]{64}\s+AS\s+web\s*$", re.M)


def node_versions(dockerfile: str) -> list[str]:
    return NODE.findall(dockerfile)


class DockerfileNode(unittest.TestCase):
    def test_the_image_builds_on_web_node_version(self):
        found = node_versions((ROOT / "Dockerfile").read_text(encoding="utf-8"))
        want = (ROOT / "web/.node-version").read_text(encoding="utf-8").strip()
        self.assertEqual(len(found), 1, "the Dockerfile should build its web stage on one pinned Node image")
        self.assertEqual(found[0], want)

    def test_a_difference_is_found(self):
        found = node_versions(
            "FROM docker.io/library/node:26.11.0-trixie-slim@sha256:" + "0" * 64 + " AS web\n"
        )
        self.assertEqual(found, ["26.11.0"])
        self.assertNotEqual(found[0], "26.12.0")


if __name__ == "__main__":
    unittest.main()
