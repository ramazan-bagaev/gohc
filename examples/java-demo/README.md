# Unpublished Java client demo

Run these commands from the gohc repository root. No artifact registry is used.

```sh
make java-client
mkdir -p .build/demo
cp .build/gohc-client-0.1.0.jar .build/demo/
cp examples/java-demo/Dockerfile .build/demo/
javac --release 11 -cp .build/gohc-client-0.1.0.jar -d .build/demo examples/java-demo/GohcDemo.java
docker build -t gohc-java-demo:local .build/demo
docker run -d --name gohc-java-demo --network none --memory 96m --cpus 0.15 --pids-limit 128 gohc-java-demo:local
./gohc start
```

The Java application runs as an unprivileged user. There are no mounts, published
ports, Docker socket access, GOHC_DIR settings or outbound network access in this
container. Its only integration is the client JAR and initialization in Java.
The monitor needs access to Docker; the demo application does not.

GohcDemo.java uses the normal one-minute interval and three-minute expiry.
Ordinary applications can use Gohc.start("service-name") with the defaults.
The client registers its shutdown hook automatically. A callback should check real
application readiness; without one the reporter only establishes liveness.

Exercise the demo only:

```sh
# Make the application's health callback fail; remove the marker to recover.
docker exec gohc-java-demo touch /tmp/gohc-demo-unhealthy
docker exec gohc-java-demo rm /tmp/gohc-demo-unhealthy

# Freeze the Java process without stopping its container; after three minutes the
# heartbeat should become stale. Always resume it after checking.
docker kill --signal=STOP gohc-java-demo
docker kill --signal=CONT gohc-java-demo
```

These exec commands are only manual controls for the test fixture. The monitor
itself does not execute commands inside containers; it reads files via Docker.
