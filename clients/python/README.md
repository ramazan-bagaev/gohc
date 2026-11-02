# gohc Python client

Python 3.9+, no runtime dependencies. Until a PyPI release is available, install
the versioned source package:

```sh
pip install 'gohc-client @ https://github.com/ramazan-bagaev/gohc/archive/refs/tags/python-v0.1.0.zip#subdirectory=clients/python'
```

```python
from gohc import start

with start("my-app"):
    run_application()
```

It writes immediately, then every 60 seconds, with a 180-second TTL. `GOHC_DIR`
overrides the default `$TMPDIR/gohc/reports` (or `/tmp/gohc/reports`). Inside
Docker, use the same defaults: the host monitor discovers reports through Docker
without port mappings, mounts or manual app registration.

`interval`, `ttl`, `directory`, `instance`, `check` and `on_error` are optional
keyword arguments to `start`. `check` returns `Health.healthy()` or
`Health.unhealthy("reason")`; exceptions become unhealthy reports. Without a
callback, the report is a heartbeat for the Python process. `on_error` receives
background file-writing failures.

For FastAPI, preserve startup/shutdown behavior by attaching before server start:

```python
from gohc import instrument_fastapi
instrument_fastapi(app, "my-api")
```

Closing the context or normal interpreter exit writes `stopped`. The library
does not replace application signal handlers. A crash, SIGKILL or forced exit is
detected by TTL expiry, and Docker lifecycle information when available.
