#!/usr/bin/env python3
"""Exercise update safety with real temporary Git repos and a fake Docker CLI."""
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

SCRIPT = Path(__file__).with_name('update-server.sh').resolve()
FAKE_DOCKER = r'''#!/usr/bin/env python3
import io, json, os, pathlib, sqlite3, sys, tarfile
p = pathlib.Path(os.environ['UPDATE_TEST_STATE'])
a = sys.argv[1:]
with (p/'commands').open('a') as f: f.write(json.dumps(a)+'\n')
mode = os.environ.get('UPDATE_TEST_FAILURE', '')
if a == ['info']: pass
elif a[:2] == ['compose', 'ps']: print('fixture-container')
elif a[:2] == ['compose', 'config']:
    volume = 'different-volume' if mode == 'volume' and (p/'configs').exists() else 'home-hq_household-data'
    (p/'configs').touch()
    print(json.dumps({'name':'home-hq', 'services':{'dashboard':{'image':'home-hq:local', 'build':{'context':'.'}, 'volumes':[{'type':'volume','source':'household-data','target':'/data'}]}},'volumes':{'household-data':{'name':volume}}}))
elif a[0] == 'inspect':
    if 'org.opencontainers.image.revision' in a[2]: print((p/'running').read_text())
    elif '.Image' in a[2]: print('sha256:previous')
    elif 'com.docker.compose.project' in a[2]: print('home-hq')
    elif '.Mounts' in a[2]: print('home-hq_household-data')
    else: sys.exit('Unexpected inspect')
elif a[0] == 'tag': pass
elif a[:2] == ['compose', 'build']:
    if mode == 'build': sys.exit('Synthetic build failure')
    (p/'built').write_text(next(x.split('=',1)[1] for x in a if x.startswith('HOME_HQ_REVISION=')))
elif a[:2] == ['image', 'inspect']: print((p/'built').read_text())
elif a[0] == 'exec' and a[-1] == '-healthcheck': pass
elif a[0] == 'exec' and '-backup' in a:
    with sqlite3.connect(p/'snapshot') as db:
        db.execute('CREATE TABLE household(value TEXT)')
        db.execute("INSERT INTO household VALUES ('keep these edits')")
elif a[0] == 'cp' and a[-1] == '-':
    b = b'bad sqlite' if mode == 'backup' else (p/'snapshot').read_bytes()
    with tarfile.open(fileobj=sys.stdout.buffer, mode='w|') as t:
        i = tarfile.TarInfo('household.sqlite'); i.size = len(b); t.addfile(i, io.BytesIO(b))
elif a[:2] == ['compose', 'up']:
    if mode == 'startup': sys.exit('Synthetic unhealthy startup')
    (p/'running').write_text((p/'built').read_text())
elif a[:2] == ['compose', 'logs']: print('Fixture diagnostics')
else: sys.exit('Unexpected Docker call: '+repr(a))
'''

class UpdateTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix='home-hq-update-test-')
        self.addCleanup(self.temp.cleanup)
        self.base = Path(self.temp.name)
        self.env = os.environ.copy()
        self.env.update(GIT_CONFIG_NOSYSTEM='1', GIT_CONFIG_GLOBAL='/dev/null',
                        GIT_AUTHOR_NAME='Update Test', GIT_AUTHOR_EMAIL='test@example.invalid',
                        GIT_COMMITTER_NAME='Update Test', GIT_COMMITTER_EMAIL='test@example.invalid')
        self.remote = self.base/'remote.git'
        self.repo = self.base/'server'
        self.producer = self.base/'producer'
        self.call('git', 'init', '--bare', '--initial-branch=main', str(self.remote))
        self.call('git', 'clone', str(self.remote), str(self.producer))
        (self.producer/'scripts').mkdir()
        shutil.copy(SCRIPT, self.producer/'scripts/update-server.sh')
        (self.producer/'.gitignore').write_text('.env\n.deployment/\nbackups/\n')
        (self.producer/'version').write_text('initial')
        self.git(self.producer, 'add', '.')
        self.git(self.producer, 'commit', '-m', 'initial')
        self.git(self.producer, 'push', 'origin', 'main')
        self.call('git', 'clone', str(self.remote), str(self.repo))
        self.previous = self.git(self.repo, 'rev-parse', 'HEAD').stdout.strip()
        (self.repo/'.env').write_text('DASHBOARD_BIND_IP=192.168.50.10\n')
        (self.producer/'version').write_text('next')
        self.git(self.producer, 'commit', '-am', 'next')
        self.git(self.producer, 'push', 'origin', 'main')
        self.target = self.git(self.producer, 'rev-parse', 'HEAD').stdout.strip()
        self.state = self.base/'state'; self.state.mkdir()
        (self.state/'running').write_text('unknown')
        (self.state/'commands').touch()
        self.bin = self.base/'bin'; self.bin.mkdir()
        docker = self.bin/'docker'; docker.write_text(FAKE_DOCKER); docker.chmod(0o755)
        self.env.update(PATH=str(self.bin)+os.pathsep+self.env['PATH'], UPDATE_TEST_STATE=str(self.state))

    def call(self, *args, **kw):
        return subprocess.run(args, env=self.env, text=True, capture_output=True, check=True, **kw)

    def git(self, repo, *args):
        return self.call('git', '-C', str(repo), *args)

    def update(self, *args, failure=''):
        env = dict(self.env, UPDATE_TEST_FAILURE=failure)
        return subprocess.run(['bash', str(self.repo/'scripts/update-server.sh'), *args],
                              env=env, text=True, capture_output=True, timeout=20)

    def commands(self):
        return [json.loads(x) for x in (self.state/'commands').read_text().splitlines()]

    def restarted(self):
        return any(c[:2] == ['compose','up'] for c in self.commands())

    def test_success_preserves_backup_and_previous_configuration(self):
        result = self.update(); self.assertEqual(result.returncode, 0, result.stdout+result.stderr)
        self.assertTrue(self.restarted())
        commands = self.commands()
        build = next(i for i,c in enumerate(commands) if c[:2] == ['compose','build'])
        backup = next(i for i,c in enumerate(commands) if '-backup' in c)
        restart = next(i for i,c in enumerate(commands) if c[:2] == ['compose','up'])
        self.assertLess(build, backup); self.assertLess(backup, restart)
        recovery = next((self.repo/'backups').iterdir())
        self.assertEqual((recovery/'household.sqlite').stat().st_mode & 0o777, 0o600)
        import sqlite3
        with sqlite3.connect(recovery/'household.sqlite') as db:
            self.assertEqual(db.execute('SELECT value FROM household').fetchone()[0], 'keep these edits')
        config = json.loads((recovery/'rollback.compose.json').read_text())
        self.assertTrue(config['services']['dashboard']['image'].startswith('home-hq:before-'))
        self.assertNotIn('build', config['services']['dashboard'])
        self.assertEqual((self.state/'running').read_text(), self.target)
        (self.state/'commands').write_text('')
        result = self.update(); self.assertEqual(result.returncode, 0, result.stderr)
        self.assertFalse(self.restarted()); self.assertFalse(any(c[:2] == ['compose','build'] for c in self.commands()))

    def test_already_running_target_fast_forwards_checkout(self):
        (self.state/'running').write_text(self.target)
        result = self.update(); self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.git(self.repo, 'rev-parse', 'HEAD').stdout.strip(), self.target)
        self.assertFalse(self.restarted())
        result = self.update('--force'); self.assertEqual(result.returncode, 0, result.stderr)
        self.assertTrue(self.restarted())

    def test_build_backup_and_volume_failures_never_replace_container(self):
        for failure in ['build', 'backup', 'volume']:
            with self.subTest(failure=failure):
                (self.state/'commands').write_text('')
                (self.state/'configs').unlink(missing_ok=True)
                (self.state/'snapshot').unlink(missing_ok=True)
                result = self.update(failure=failure)
                self.assertNotEqual(result.returncode, 0, result.stdout)
                self.assertFalse(self.restarted())
                self.assertEqual((self.state/'running').read_text(), 'unknown')

    def test_failed_startup_keeps_recovery_and_does_not_restore_data(self):
        result = self.update(failure='startup')
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('rollback.compose.json', result.stderr)
        self.assertIn('No automatic database restore', result.stderr)
        self.assertEqual(sum(c[:2] == ['compose','up'] for c in self.commands()), 1)
        self.assertTrue(any((self.repo/'backups').glob('*/household.sqlite')))

    def test_check_only_and_dirty_checkout_do_not_deploy(self):
        result = self.update('--check'); self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.git(self.repo, 'rev-parse', 'HEAD').stdout.strip(), self.previous)
        self.assertEqual(self.commands(), [])
        (self.repo/'version').write_text('user edit')
        result = self.update(); self.assertNotEqual(result.returncode, 0)
        self.assertIn('local changes', result.stderr); self.assertEqual(self.commands(), [])

    def test_diverged_checkout_is_not_reset(self):
        (self.repo/'version').write_text('local commit')
        self.git(self.repo, 'commit', '-am', 'local')
        local = self.git(self.repo, 'rev-parse', 'HEAD').stdout.strip()
        result = self.update(); self.assertNotEqual(result.returncode, 0)
        self.assertIn('ahead or diverged', result.stderr)
        self.assertEqual(self.git(self.repo, 'rev-parse', 'HEAD').stdout.strip(), local)
        self.assertEqual(self.commands(), [])

if __name__ == '__main__': unittest.main()
