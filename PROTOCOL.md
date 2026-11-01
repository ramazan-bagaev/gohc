# gohc file protocol v1

UTF-8 JSON, at most 64 KiB including whitespace. Files end in `.json` and live in
the configured report directory. Clients replace each file atomically using a
temporary file and same-directory rename. Write immediately, then periodically.
Each process instance owns a distinct file. Never rewrite a different instance's
file. Filename is opaque; service and instance identity are inside the report.

```json
{
  "version": 1,
  "service": "orders",
  "instance": "worker-1-unique-start-id",
  "status": "healthy",
  "message": "database reachable",
  "updated_at": "2026-10-31T12:00:00Z",
  "ttl_seconds": 60
}
```

Required: version, nonempty service/instance (maximum 256 UTF-8 bytes each),
status, UTC/RFC3339 updated_at, integer TTL from 1 to 86400 seconds. Status is
`healthy`, `unhealthy`, `unknown`, or `stopped`. Message is optional. Unknown
fields are tolerated so additional metadata can be introduced later.

`updated_at` changes on every successful report. Choose TTL greater than the
write interval (defaults: 60-second interval, 180-second TTL). The monitor's read
time never substitutes for updated_at. A scheduled reporter alone indicates
liveness of the reporter; application readiness requires an explicit check.

Default location: GOHC_DIR if nonempty; otherwise TMPDIR/gohc/reports, falling
back to /tmp/gohc/reports on Unix. GOHC_DIR must be absolute for Docker discovery.
A monitor checks each container's configured environment, not its own report
path. Applications using nonstandard paths must expose that choice in GOHC_DIR.

The monitor stores normalized observations separately and never writes synthetic
reports into a client's report directory. This prevents discovery loops and
preserves report time and source provenance. Future transports should deliver
this same report together with trustworthy source identity and receipt time.
