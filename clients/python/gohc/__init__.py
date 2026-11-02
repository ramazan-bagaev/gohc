"""Scheduled, atomic gohc reports. No network connection or runtime dependencies."""

import atexit
from contextlib import asynccontextmanager
from dataclasses import dataclass
from datetime import datetime, timezone
import hashlib
import json
import math
import os
from pathlib import Path
import socket
import tempfile
import threading
import uuid

__all__ = ["Health", "Reporter", "start", "instrument_fastapi"]
_STATUSES = {"healthy", "unhealthy", "unknown", "stopped"}


@dataclass(frozen=True)
class Health:
    status: str = "healthy"
    message: str = ""

    @staticmethod
    def healthy(message=""):
        return Health("healthy", message)

    @staticmethod
    def unhealthy(message=""):
        return Health("unhealthy", message)


def _default_directory():
    configured = os.environ.get("GOHC_DIR")
    if configured:
        return Path(configured)
    temporary = os.environ.get("TMPDIR") or ("/tmp" if os.name != "nt" else tempfile.gettempdir())
    return Path(temporary) / "gohc" / "reports"


class Reporter:
    """Write immediately, then every interval seconds; close writes 'stopped'.

    The optional check returns Health. Exceptions produce an unhealthy report.
    A daemon thread does not prevent process exit. Normal Python exit closes the
    reporter automatically; abrupt termination is detected through TTL expiry.
    """

    def __init__(self, service, *, directory=None, instance=None, interval=60,
                 ttl=None, check=None, on_error=None):
        interval = float(interval)
        ttl = float(ttl if ttl is not None else 3 * interval)
        if not math.isfinite(interval) or interval < .001:
            raise ValueError("interval must be a finite number of at least .001 seconds")
        if not math.isfinite(ttl) or not 1 <= ttl <= 86400 or ttl <= interval:
            raise ValueError("ttl must exceed interval and be between 1 and 86400 seconds")
        instance = instance if instance is not None else "%s-%s-%s" % (socket.gethostname(), os.getpid(), uuid.uuid4().hex[:12])
        for label, value in (("service", service), ("instance", instance)):
            if not isinstance(value, str) or not 1 <= len(value.encode("utf-8")) <= 256 or not value.strip():
                raise ValueError(label + " must contain 1 to 256 UTF-8 bytes")
        self.service, self.instance = service, instance
        self.interval, self.ttl = interval, math.ceil(ttl)
        self.check, self.on_error = check, on_error
        self.directory = Path(directory) if directory is not None else _default_directory()
        self.directory.mkdir(parents=True, exist_ok=True)
        identity = (service + "\0" + instance).encode("utf-8")
        self.path = self.directory / (hashlib.sha256(identity).hexdigest() + ".json")
        self._lock, self._stop = threading.RLock(), threading.Event()
        self._closed = False
        self._write()
        self._thread = threading.Thread(target=self._run, name="gohc-" + service, daemon=True)
        atexit.register(self.close)
        self._thread.start()

    def _write(self, health=None):
        if health is None:
            try:
                health = self.check() if self.check else Health()
            except Exception as error:
                health = Health.unhealthy(str(error))
        if not isinstance(health, Health) or health.status not in _STATUSES or not isinstance(health.message, str):
            raise ValueError("check must return Health with a supported status and string message")
        report = dict(version=1, service=self.service, instance=self.instance,
                      status=health.status, updated_at=datetime.now(timezone.utc).isoformat().replace("+00:00", "Z"),
                      ttl_seconds=self.ttl)
        if health.message:
            report["message"] = health.message
        payload = json.dumps(report, ensure_ascii=False, separators=(",", ":")).encode("utf-8")
        if len(payload) >= 65536:
            raise ValueError("report exceeds 64 KiB")
        descriptor, temporary = tempfile.mkstemp(prefix=".gohc-", dir=self.directory)
        try:
            with os.fdopen(descriptor, "wb") as output:
                output.write(payload)
            os.chmod(temporary, 0o644)
            os.replace(temporary, self.path)
        finally:
            Path(temporary).unlink(missing_ok=True)

    def _error(self, error):
        if self.on_error:
            try:
                self.on_error(error)
            except Exception:
                pass

    def _run(self):
        while not self._stop.wait(self.interval):
            with self._lock:
                if self._closed:
                    return
                try:
                    self._write()
                except Exception as error:
                    self._error(error)

    def close(self):
        with self._lock:
            if self._closed:
                return
            self._closed = True
            self._stop.set()
            try:
                self._write(Health("stopped"))
            except Exception as error:
                self._error(error)
            atexit.unregister(self.close)
        if threading.current_thread() is not self._thread:
            self._thread.join()

    def __enter__(self):
        return self

    def __exit__(self, *_):
        self.close()


def start(service, **options):
    return Reporter(service, **options)


def instrument_fastapi(app, service, **options):
    """Attach to a FastAPI/Starlette lifespan, preserving its state and hooks.

    Call once before the ASGI server starts. FastAPI is not a client dependency.
    """
    if getattr(app, "_gohc_instrumented", False):
        return app
    original = app.router.lifespan_context

    @asynccontextmanager
    async def lifespan(application):
        async with original(application) as state:
            with start(service, **options):
                yield state

    app.router.lifespan_context = lifespan
    app._gohc_instrumented = True
    return app
