import asyncio
from contextlib import asynccontextmanager
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import time
import unittest
from unittest.mock import patch

from gohc import Health, start, instrument_fastapi


class ClientTests(unittest.TestCase):
    def test_periodic_atomic_reports_and_stopped(self):
        with tempfile.TemporaryDirectory() as directory:
            with start('../../service/☀', instance='node', directory=directory, interval=.02, ttl=1) as reporter:
                first = json.loads(reporter.path.read_text())
                self.assertEqual('healthy', first['status'])
                deadline = time.monotonic() + 2
                while json.loads(reporter.path.read_text())['updated_at'] == first['updated_at']:
                    self.assertLess(time.monotonic(), deadline)
                    time.sleep(.01)
                self.assertEqual(Path(directory), reporter.path.parent)
            reporter.close()
            self.assertEqual('stopped', json.loads(reporter.path.read_text())['status'])
            self.assertEqual([reporter.path], list(Path(directory).iterdir()))

    def test_callback_failures_and_validation(self):
        def broken():
            raise RuntimeError('database unavailable')
        with tempfile.TemporaryDirectory() as directory:
            with start('api', directory=directory, check=broken) as reporter:
                report = json.loads(reporter.path.read_text())
                self.assertEqual(('unhealthy', 'database unavailable'), (report['status'], report['message']))
            for options in ({'interval': float('nan')}, {'ttl': float('inf')}, {'interval': 0},
                            {'ttl': 60}, {'check': lambda: Health('invalid')},
                            {'check': lambda: Health.unhealthy('x' * 65536)}):
                with self.subTest(options=options), self.assertRaises(ValueError):
                    start('api', directory=directory, **options)

    def test_environment_and_stable_explicit_identity(self):
        with tempfile.TemporaryDirectory() as directory, patch.dict(os.environ, {'GOHC_DIR': directory}):
            with start('api', instance='same') as first:
                first_path = first.path
            with start('api', instance='same') as second:
                self.assertEqual(first_path, second.path)
                self.assertEqual('healthy', json.loads(second.path.read_text())['status'])

    def test_normal_exit_closes_automatically(self):
        with tempfile.TemporaryDirectory() as directory:
            environment = dict(os.environ, GOHC_DIR=directory)
            subprocess.run([sys.executable, '-c', 'from gohc import start; start("subprocess")'],
                           env=environment, check=True, timeout=5)
            self.assertEqual('stopped', json.loads(next(Path(directory).glob('*.json')).read_text())['status'])

    def test_fastapi_lifespan_preserves_state_and_hooks(self):
        events = []
        with tempfile.TemporaryDirectory() as directory:
            @asynccontextmanager
            async def original(app):
                events.append('startup')
                self.assertEqual([], list(Path(directory).glob('*.json')))
                yield {'key': 'value'}
                events.append('shutdown')
                self.assertEqual('stopped', json.loads(next(Path(directory).glob('*.json')).read_text())['status'])
            class Router:
                lifespan_context = staticmethod(original)
            class App:
                router = Router()
            app = App()
            instrument_fastapi(app, 'api', directory=directory)
            wrapped = app.router.lifespan_context
            instrument_fastapi(app, 'api', directory=directory)
            self.assertIs(wrapped, app.router.lifespan_context)
            async def exercise():
                async with app.router.lifespan_context(app) as state:
                    self.assertEqual({'key': 'value'}, state)
                    self.assertEqual('healthy', json.loads(next(Path(directory).glob('*.json')).read_text())['status'])
            asyncio.run(exercise())
            self.assertEqual(['startup', 'shutdown'], events)


if __name__ == '__main__':
    unittest.main()
