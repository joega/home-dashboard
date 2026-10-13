#!/usr/bin/env python3
"""Check unit rendering and invalid-input guards without installing services."""
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

INSTALLER = Path(__file__).with_name('install-auto-update.sh').resolve()

class TimerPreparationTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix='home-hq-unit-test-')
        self.addCleanup(self.temp.cleanup)
        self.base = Path(self.temp.name)
        self.output = self.base/'units'
        self.marker = self.base/'unexpected-system-action'
        commands = self.base/'bin'; commands.mkdir()
        # Any Docker/Git/service operation in prepare mode is a failure.
        for command in ['docker', 'git', 'systemctl']:
            p = commands/command
            p.write_text('#!/bin/sh\necho touched >> "$UNIT_TEST_MARKER"\nexit 99\n')
            p.chmod(0o755)
        self.env = dict(os.environ, PATH=str(commands)+os.pathsep+os.environ['PATH'], UNIT_TEST_MARKER=str(self.marker))

    def run_installer(self, *args):
        return subprocess.run(['bash', str(INSTALLER), *args], env=self.env,
                              text=True, capture_output=True, timeout=10)

    def test_prepare_has_no_host_or_git_actions_and_preserves_service_policy(self):
        for interval in ['15', '30', '60']:
            result = self.run_installer('--prepare', str(self.output), '--interval', interval)
            self.assertEqual(result.returncode, 0, result.stdout+result.stderr)
            self.assertFalse(self.marker.exists())
            self.assertIn('OnUnitInactiveSec='+interval+'min', (self.output/'home-hq-update.timer').read_text())
        service = (self.output/'home-hq-update.service').read_text()
        self.assertIn('User=root', service)
        self.assertIn('ProtectHome=true', service)
        self.assertIn('ProtectSystem=strict', service)
        self.assertIn('GIT_TERMINAL_PROMPT=0', service)
        self.assertIn('COMPOSE_PROJECT_NAME=home-hq', service)
        self.assertIn('/usr/local/libexec/home-hq-auto-update', service)
        self.assertEqual(self.output.stat().st_mode & 0o777, 0o700)
        verification = subprocess.run(['systemd-analyze', 'verify',
             str(self.output/'home-hq-update.service'), str(self.output/'home-hq-update.timer')],
             text=True, capture_output=True, timeout=20)
        self.assertEqual(verification.returncode, 0, verification.stdout+verification.stderr)

    def test_invalid_intervals_fail_before_writing_or_privileged_actions(self):
        for interval in ['0', '-1', '1441', '30m', '30\nExecStart=/bin/false', '']: 
            with self.subTest(interval=interval):
                result = self.run_installer('--prepare', str(self.output), '--interval', interval)
                self.assertNotEqual(result.returncode, 0)
                self.assertFalse(self.output.exists())
                self.assertFalse(self.marker.exists())

    def test_default_and_missing_values(self):
        result = self.run_installer('--prepare', str(self.output))
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn('OnUnitInactiveSec=30min', (self.output/'home-hq-update.timer').read_text())
        for argument in ['--interval', '--prepare', '--unknown']:
            result = self.run_installer(argument)
            self.assertNotEqual(result.returncode, 0)
        self.assertFalse(self.marker.exists())

if __name__ == '__main__': unittest.main()
