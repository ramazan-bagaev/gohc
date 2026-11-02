# gohc

A small, local health monitor. Applications publish heartbeat files; gohc discovers
reports in a filesystem directory and inside Docker containers. No per-service
registration, shared container mounts, client network calls, or published app ports.
The monitor and Go client use only Go's standard library. The Java client has no
runtime dependencies.

## Run

Requires Go 1.18+ to build:

```sh
CGO_ENABLED=0 go build -o gohc .
./gohc start
```

Open http://127.0.0.1:8901. `start` runs in the foreground; Ctrl+C shuts it down.
No `app.json` is needed. Flags:

```sh
GOHC_DIR=/path/to/reports ./gohc start
./gohc start --dir /path/to/reports --interval 10s --listen 127.0.0.1:8901
./gohc start --docker=false
./gohc status                  # read the saved snapshot, without contacting sources
./gohc start --help
```

The local directory defaults to `$TMPDIR/gohc/reports`, or `/tmp/gohc/reports`
on Unix when TMPDIR is unset. Clients create it on their first report. Monitor
and local clients must use the same directory; different users, environments or
Java temporary-directory overrides should explicitly use `GOHC_DIR`. Reports may
be nested; symlinks and nonregular files are not followed. JSON files in this
directory must be gohc reports, not unrelated application configuration.

State defaults to the OS user configuration directory under
`gohc/state/observations.json`; change it with `--state`. It must be outside the
report directory. A missing or invalid report is visible without killing the
monitor. A corrupt state file causes a clear startup error and is left intact.
Use a different `--state` path to recover while preserving it for inspection.

## Update intervals

Defaults are one minute for source collection, machine sampling, dashboard refresh
and client heartbeats. The default client expiry is three minutes. First reports
and samples are immediate; CPU percentage needs a second sample. Less frequent
collection also means slower detection of new services and changed health.

Monitor timing can be set in the optional OS user configuration file
`gohc/config.json`, or a path supplied with `--config` or `GOHC_CONFIG`:

```json
{
  "poll_interval": "1m",
  "machine_interval": "1m",
  "dashboard_refresh": "1m"
}
```

No file is required. Command-line flags override the corresponding JSON values:

```sh
./gohc start --interval 2m --machine-interval 1m --refresh 1m
```

Intervals must be between one second and 24 hours; configuration changes take
effect on restart. These are monitor settings, not a service registration list.
Clients use their own `Options` values; a host config is not automatically shared
with applications or containers. The included demos now use the normal client defaults. A report's expiry should
allow for its heartbeat interval plus the monitor's polling interval and a margin;
an expiry shorter than collection can make cached reports appear stale even
while the application is still running.

## Go client

The Go client is a package in the root module; it shares the CLI's release tag.
Once the repository and `v0.1.0` tag are public:

```sh
go get github.com/ramazan-bagaev/gohc/client@v0.1.0
```

```go
import "github.com/ramazan-bagaev/gohc/client"

heartbeat, err := client.Start("my-service", client.Options{})
if err != nil {
    // Report the initialization failure through your application logger.
    log.Printf("health reporting unavailable: %v", err)
} else {
    defer heartbeat.Stop()
}
```

The first report is synchronous, then one report every 60 seconds, expiring after
180 seconds. A generated instance ID and opaque filename prevent replicas and
unusual service names from overwriting each other's reports. To report real
application health, provide `Options.Check`, returning `client.Health` with
`healthy`, `unhealthy` or `unknown` plus a message. Callbacks must return promptly.
By default a heartbeat reports only that the reporter is running. Use
`Options.OnError` to observe background write failures. `Stop` publishes `stopped`.

## Java client (Java 11+)

Source and Maven descriptor: `clients/java`. Build/install locally with
`mvn -f clients/pom.xml install`, then
use `top.kanasuki.gohc:gohc-client:0.1.0` from your local Maven repository. A dependency-free
build is also provided by `make java-client`.

```java
import io.gohc.Gohc;

Gohc.start("my-service");
```

`Gohc.Options` supports a directory, instance, interval, expiry, health callback,
and error callback. Startup writes immediately and can throw `IOException`;
choose your application's response to initialization failure. Its scheduler is
a daemon thread and does not keep the JVM alive. A shutdown hook automatically
publishes stopped on normal JVM exit or SIGTERM. Explicit `close()` also removes
the hook; it is safe to call repeatedly. Framework integrations can set
`options.shutdownHook = false` and own the lifecycle themselves. SIGKILL and
crashes cannot run shutdown hooks; report expiry still handles those.
Container detection is never required. Use `GOHC_DIR` for cross-language custom
paths. Java uses TMPDIR or `/tmp` on Unix, rather than a custom `java.io.tmpdir`
property, to keep Docker discovery deterministic.

## Spring Boot starter (Java 17+)

Build/install both Java artifacts locally:

```sh
mvn -f clients/pom.xml install
```

Then add this dependency to an existing Spring Boot application. No gohc startup
call, annotation, shutdown hook, Actuator dependency, or Docker changes are needed:

```xml
<dependency>
  <groupId>top.kanasuki.gohc</groupId>
  <artifactId>gohc-spring-boot-starter</artifactId>
  <version>0.1.0</version>
</dependency>
```

Auto-configuration creates one `Gohc` bean and Spring closes it when the application
context shuts down. The service name comes from `spring.application.name`, falling
back to `application`; `gohc.service` can override it. The default means heartbeat
liveness, not application readiness. Reporting starts during context initialization;
an initial report does not guarantee the application is ready to serve requests.
The monitor discovers the report on its next collection as usual.

All settings are optional:

```properties
# Defaults shown, except directory and instance which are normally automatic.
gohc.enabled=true
gohc.interval=1m
# When omitted, TTL is three times gohc.interval.
gohc.ttl=3m
# gohc.service=orders
# gohc.instance=worker-1
# gohc.directory=/tmp/gohc/reports
```

Without `gohc.directory`, the shared `GOHC_DIR`/TMPDIR defaults apply. Inside Docker,
keep the default path or use `GOHC_DIR` so the external monitor can find reports;
a Spring-only custom path is not advertised to the Docker monitor. Monitor polling
and dashboard refresh remain separate settings. Invalid timing or an unwritable
report directory fails client bean creation and application startup visibly.

To supply application health, optionally define one check bean:

```java
@Bean
GohcHealthCheck gohcHealthCheck() {
    return () -> databaseReady()
        ? Gohc.Health.healthy()
        : Gohc.Health.unhealthy("Database unavailable");
}
```

Import `io.gohc.Gohc` and `io.gohc.spring.GohcHealthCheck`. Checks run immediately
and on each heartbeat, must return promptly, and exceptions become unhealthy.
Without a check bean every heartbeat is healthy. An existing application-defined
`Gohc` bean prevents automatic creation of another reporter. The starter uses
Spring Boot's `AutoConfiguration.imports` mechanism and targets Boot 3.x/4.x;
its dependency baseline is Boot 3.5.7, while an application's Boot dependency
management selects its own framework versions. The core client remains Java 11+
and has no Spring dependencies.

## Docker discovery

Run the monitor as a user who can access the Docker Engine. Docker's CLI is not
required. gohc supports Unix sockets and TCP/TLS endpoints, `DOCKER_HOST`,
`DOCKER_CONTEXT`, `DOCKER_CONFIG`, and stored current-context metadata (including
Docker Desktop). SSH contexts and Windows named pipes are not implemented.
Remote contexts inspect that remote engine, not necessarily the local machine.
Only the monitor needs Docker access; this is a powerful permission, even though
gohc uses read requests only. No Docker socket is exposed to application clients.

Every poll lists containers and inspects their state and configured environment.
For each running container, it reads the report directory using Docker's archive
API, without executing a shell, copying to disk, or needing any mounts.
The directory is the container's `GOHC_DIR`, or its TMPDIR plus `/gohc/reports`,
otherwise `/tmp/gohc/reports`. **Use an absolute GOHC_DIR in containers.** If a
client uses an explicit code-level directory instead, set matching GOHC_DIR in
its deployment so the monitor can discover it. Default client initialization
requires no application deployment changes on ordinary writable containers.
Read-only containers still need an existing writable location.

Containers are inspected for discovery but become services only after their first
valid report. Previously registered services remain visible when reports stop or
containers disappear. Docker inventory without reports is not persisted or shown. Docker's
native health and lifecycle state are shown separately from application health.
The monitor polls rather than subscribing to events; discovery occurs on the
next poll. Each Docker scan has a 30-second overall timeout, and each request a
5-second timeout. With very many containers, those limits may need tuning in a
future version. Docker failure leaves filesystem monitoring running and is
retried. Live Docker behavior must be checked against your deployment before
relying on it; the automated suite uses a simulated Engine API.

## Machine status

The dashboard includes CPU, memory and disk usage, with raw readings under
`machine` in `GET /api/status`. Samples are collected every minute,
independently of Docker inspection. No external commands or dependencies are
used. CPU usage is the aggregate busy percentage between samples; memory usage
is MemTotal minus MemAvailable, so reclaimable cache is not treated as pressure.
Disk usage is for the filesystem containing the selected report directory and
uses space available to ordinary users, including reserved-block accounting.
Hostname, CPU count, uptime and collection errors are available only in the API.
Machine readings are live observations, not persisted health reports, and do not
register additional services or change application health statuses.

CPU, memory and uptime sampling currently support Linux. Disk sampling supports
Linux and macOS. Unsupported or failed readings are null in the API and shown as
an em dash; the first CPU reading waits for the next sample. Readings describe
the system/namespace visible to the monitor. If the monitor itself is containerized,
these are not guaranteed to represent the physical host or its cgroup limits.

## Monitor self-health

`gohc start` initializes the same Go client to publish its own `gohc` report into
the selected report directory. Its callback checks collection-loop progress,
source availability and persistence failures. A running timer alone is not enough
to report healthy. The monitor uses a stable instance identity and report filename
so restarts update its existing registration. Shutdown publishes `stopped`.
A crashed or stuck monitor's report can be checked by another monitor or a file
reader; a monitor that has crashed cannot serve its own dashboard.

## Persistence and freshness

Latest observations from every source are atomically saved, not a full event
history. Identity includes source and location (Docker engine/container included),
so matching service names from different sources remain separate. Last valid
reports survive malformed replacements, source outages and container deletion.
Absent entries remain visible; this version has no automatic history cleanup.

Re-reading a report never renews its timestamp. Stale means the client's timestamp
plus TTL has elapsed. `missing` means a previously found report disappeared;
`source_unavailable` means inspection failed, not that the application failed.
Restored snapshots start as `awaiting_verification`. Explicit stopped reports
remain stopped until replaced. Source clocks must be approximately synchronized;
reports more than five minutes in the future are rejected. A heartbeat is not a
substitute for application-specific readiness checks.

Dashboard: `/`. Non-healthy services show their last application report time beside the status,
localized to the browser timezone, with the full date and timezone on hover.
Healthy services show only the name and status. Current machine-readable view: `GET /api/status`. Both are read
only and bind to loopback by default. REST ingestion and multi-server aggregation
are intentionally deferred; they can feed the same observation store later.
Legacy arbitrary-text `.gohc` reports and the old directory registry in `app.json`
are not consumed by the new protocol. Existing files are left untouched; update
older integrations such as gowh to a version-1 client when migrating.

## Validation

```sh
make test
make test-java
mvn -f clients/pom.xml verify  # starter auto-discovery and lifecycle tests
make test-race  # optional; needs a compatible cgo toolchain
```

Go tests cover expiry, persistence/reverification, missing/malformed files,
source outages, container lifecycle, HTML escaping and a simulated Docker API.
`test-java` checks immediate and periodic writes, JSON escaping, stopped reports,
and parses the Java output with Go's protocol reader. It also starts separate JVMs
to check automatic stopped reports on normal exit and SIGTERM. Starter tests cover
dependency discovery, properties, checks, disabling, existing beans, shutdown, and
startup failures. Test another Boot version with `-Dspring-boot.version=4.0.0`.

## Status and timing semantics

Both clients write immediately, then every 60 seconds by default. The monitor
collects immediately and once a minute, and the dashboard refreshes once a minute.
A snapshot computes age = current time minus the application's updated_at.
A live report is stale at age >= its TTL (default 180 seconds). Reading the same
file does not renew it. With independent heartbeat and collection schedules, a
healthy cached report may be nearly two minutes old before the next collection;
clock synchronization and scheduling delays also matter. Changes in explicit
health may take a heartbeat interval plus a poll interval plus a dashboard refresh
to become visible. TTL and monitor cadence are independently configurable.

Unhealthy is an explicit failed application health check, not an expired report.
Call the Java client's close or the Go reporter's Stop to publish stopped. Docker
also supplies stopped/exited/dead lifecycle state even if the process could not
publish a final report. Paused is specific to Docker. A non-container process
that crashes without a final report becomes stale; an exited Docker container
can be classified stopped, while a hung process in a running container becomes
stale. Docker lifecycle and unavailable-source states take precedence over expiry.

The dashboard labels reports read through Docker with a small Docker badge.
Host filesystem services have no badge. Client libraries remain container-unaware;
the monitor determines provenance during discovery.

## License

The gohc monitor, Go client, Java client and Spring Boot starter are licensed
under the [Apache License 2.0](LICENSE). Java distributions include the license
in `META-INF/LICENSE`, and their Maven descriptors declare `Apache-2.0`.

## Java release metadata

`clients/pom.xml` is the shared `top.kanasuki.gohc:gohc-parent` parent and reactor
build. It owns the Java artifacts' group/version, Apache license, public developer
details, project/SCM URLs, encoding, compiler/test plugin versions and license
packaging. The two module POMs inherit those settings and define their own names,
descriptions and module-specific configuration. The core targets Java 11 and the
starter targets Java 17. Spring dependencies remain confined to the starter.

Install from `mvn -f clients/pom.xml install` so the parent is installed alongside
both artifacts. A public release must also publish the parent POM, since consumers
need it to resolve inherited metadata and build the effective artifact model.
SCM tag is `v0.1.0` for the initial release; update it with the artifact version
when cutting a later release. Signing keys and publishing credentials belong in
Maven settings or CI secrets, outside these POMs.

## Signed Java builds

The shared `release` Maven profile generates source and Javadoc JARs and signs the
parent POM, module POMs, binary JARs and both attachments at `verify`. Ordinary
builds do not need a signing key. With GnuPG configured, use `mvn -f clients/pom.xml
-Prelease clean verify`; supply the passphrase through `MAVEN_GPG_PASSPHRASE` or
gpg-agent, never a command-line argument or a committed POM/settings file.

For this machine's local signing setup, run:

```sh
python3 clients/release-local.py --install
```

The helper reads `signing.json`, a private `passphrase` file and the `gnupg/` keyring
from `~/Library/Application Support/gohc/signing` on macOS, or
`${XDG_DATA_HOME:-~/.local/share}/gohc/signing` on Linux. Override with
`GOHC_SIGNING_DIR` or `--signing-dir`. The directory must stay outside the repository.
The passphrase is passed only through the Maven process environment. The helper
builds/signs and optionally installs locally. It uploads only when explicitly
invoked with `--publish`.

Keep the signing directory and its revocation certificate backed up securely.
Only the exported public key belongs with shared release artifacts. Maven Central
also needs the public key available on a supported keyserver before publication.

## Shared monitor reporting

Any monitor can show other monitors alongside its own applications. Add optional
`name` and `remotes` entries to its usual config (`gohc/config.json` in the OS
config directory, or `GOHC_CONFIG` / `--config`):

```json
{
  "name": "digitalocean",
  "poll_interval": "1m",
  "remotes": [
    {
      "name": "static-ocean",
      "url": "https://gohc-static.example.com",
      "username": "gohc",
      "password_env": "GOHC_REMOTE_PASSWORD"
    }
  ]
}
```

Set the named environment variable in the monitor process (for a service, use a
private systemd `EnvironmentFile`). Omit both authentication fields for an
endpoint that does not require authentication. Use HTTPS outside a trusted local
network. Config changes take effect after restarting the monitor.

The dashboard groups apps and machine CPU/memory/disk by monitor. `/api/status`
returns the combined view. `/api/local` returns only that machine's own reports
and metrics, with API version 1. Remote collection always requests `/api/local`,
including when a base URL or `/api/status` URL was configured. A full mesh is safe:
no imported reports are re-exported, so reciprocal endpoints do not recurse or
multiply observations. Each configured monitor name gives its reports a distinct
identity, including services with identical names on different servers.

Remote endpoints need gohc **v0.2.0 or newer**. Collection uses the normal poll
interval, up to 32 peers in parallel, a five-second HTTP timeout per peer, and a
4 MiB snapshot limit. Redirects are not followed. Authentication values and remote
URLs are never exported in status responses or saved observations. Credentials
are sent only to the configured endpoint; keep password variables private.

Original heartbeat timestamps and TTLs are preserved. Fetching a remote snapshot
does not refresh an application's heartbeat. If a peer is unreachable, its last
valid reports remain cached and display **Unavailable**, including after a
restart until reverified. A peer's invalid, stale, missing, stopped and Docker
lifecycle statuses are preserved. An unreachable peer does not mark the local
monitor unhealthy. Removing a peer from config removes its cached observations
on restart. Cached reports remain available through `gohc status` without needing
the remote password environment variables.

Single-monitor setup is unchanged when `remotes` is absent or empty. Client
libraries and application containers require no changes for monitor sharing.

## CLI releases

Tagged releases provide Linux and macOS archives for amd64 and arm64. Each archive
contains `gohc`, the Apache license, README and example timing config. No Go or Java
runtime is required. Extract the matching archive, then run `./gohc version` and
`./gohc start`. The monitor needs Docker Engine access for Docker discovery.

Build archives with a current stable Go toolchain from a clean tagged checkout:

```sh
python3 scripts/release.py --version v0.1.0 --sign
```

The script runs Go tests/vet, builds four platforms, embeds the version/commit/date,
and creates SHA-256 checksums. `--sign` signs each archive and the checksum list
using the same private local key directory as the Java helper. Output defaults to
`dist/v0.1.0/`; generated files are ignored by Git. `--allow-dirty` is available
only for local candidates and marks their commit metadata as dirty.

The CLI and Go package share the root module/tag. After public release, the CLI
can alternatively be built with `go install github.com/ramazan-bagaev/gohc@v0.1.0`.

## Maven Central publication

Both Java modules and the shared parent publish together. Save the Central Portal
user-token username/password under server ID `central` in `~/.m2/settings.xml`;
keep that file private and outside the repository. The public signing key must
be available on a Central-supported keyserver. No Central credentials are included
in these sources or release archives.

```sh
# Build/sign and generate a Central bundle without uploading:
python3 clients/release-local.py --central-bundle
# Upload, validate and publish after a clean v0.1.0 tag is prepared:
python3 clients/release-local.py --publish
```

The publication helper checks for a clean checkout, matching local version tag and
Central server configuration before uploading. Publication is immutable; use a new
version for changes after release. See `RELEASING.md` for the initial release plan.

### Python applications

The stdlib-only [Python client](clients/python/README.md) uses the same report
protocol and Docker discovery as the Go and Java clients. Start a heartbeat with
`from gohc import start; health = start("my-app")`, or attach
`instrument_fastapi(app, "my-api")` before starting an ASGI server.

For static sites, `cmd/gohc-heartbeat` uses the Go client with a local HTTP check:
`gohc-heartbeat --service my-site --url http://127.0.0.1:80/`.
It belongs inside the app container; it requires no shared mounts or ports.
