"""Regression checks for the production commit and v2 deployment guards."""

import os
from pathlib import Path
import subprocess
import textwrap
import unittest


ROOT = Path(__file__).resolve().parents[1]
DEPLOY = (ROOT / ".github/workflows/deploy.yml").read_text()
QUALITY = (ROOT / ".github/workflows/test.yml").read_text()


def literal_shell_blocks(source):
    lines = source.splitlines()
    for index, line in enumerate(lines):
        if line.strip() not in {"run: |", "script: |"}:
            continue
        indentation = len(line) - len(line.lstrip())
        block = []
        for content in lines[index + 1:]:
            if content.strip() and len(content) - len(content.lstrip()) <= indentation:
                break
            block.append(content)
        yield textwrap.dedent("\n".join(block))


class ReleaseWorkflowTest(unittest.TestCase):
    def test_deploy_uses_the_tested_commit_instead_of_a_moving_branch(self):
        self.assertIn("DEPLOY_SHA: ${{ github.sha }}", DEPLOY)
        self.assertIn("APP_DIR,DEPLOY_BRANCH,DEPLOY_SHA,", DEPLOY)
        self.assertIn('git reset --hard "$DEPLOY_SHA"', DEPLOY)
        self.assertIn('[ "$(git rev-parse HEAD)" = "$DEPLOY_SHA" ]', DEPLOY)
        self.assertNotIn('git reset --hard "origin/${DEPLOY_BRANCH}"', DEPLOY)

    def test_v2_defaults_are_enabled_and_explicit_disabling_fails(self):
        guard = textwrap.dedent(DEPLOY.split("# BEGIN REQUIRED V2 GUARD", 1)[1].split("# END REQUIRED V2 GUARD", 1)[0])
        for values, wanted in [({}, 0), ({"FLOWSPACE_ENABLE_MOBILE_SYNC_V2": "true", "FLOWSPACE_ENABLE_TASK_DOMAIN_V2_ROUTING": "true"}, 0), ({"FLOWSPACE_ENABLE_MOBILE_SYNC_V2": "false"}, 1), ({"FLOWSPACE_ENABLE_TASK_DOMAIN_V2_ROUTING": "false"}, 1)]:
            with self.subTest(values=values):
                environment = {"PATH": os.environ["PATH"], **values}
                result = subprocess.run(["sh", "-c", guard], env=environment, capture_output=True, text=True)
                self.assertEqual(result.returncode, wanted, result.stderr)
        self.assertIn('FLOWSPACE_ENABLE_MOBILE_SYNC_V2="${FLOWSPACE_ENABLE_MOBILE_SYNC_V2:-true}"', DEPLOY)
        self.assertIn('FLOWSPACE_ENABLE_TASK_DOMAIN_V2_ROUTING="${FLOWSPACE_ENABLE_TASK_DOMAIN_V2_ROUTING:-true}"', DEPLOY)

    def test_postgres_gate_includes_the_integration_package(self):
        self.assertIn("run: go test ./internal/japaneseintegration -count=1", QUALITY)

    def test_running_images_carry_the_tested_commit(self):
        self.assertIn('build --build-arg VCS_REF="$DEPLOY_SHA"', DEPLOY)
        for dockerfile in ["backend/Dockerfile", "frontend/Dockerfile"]:
            self.assertIn("LABEL org.opencontainers.image.revision=${VCS_REF}", (ROOT / dockerfile).read_text())

    def test_all_literal_shell_blocks_parse(self):
        for index, block in enumerate(literal_shell_blocks(DEPLOY)):
            with self.subTest(block=index):
                result = subprocess.run(["bash", "-n"], input=block, capture_output=True, text=True)
                self.assertEqual(result.returncode, 0, result.stderr)


if __name__ == "__main__":
    unittest.main()
