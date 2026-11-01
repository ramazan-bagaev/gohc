package io.gohc;

import java.io.IOException;
import java.net.InetAddress;
import java.nio.charset.StandardCharsets;
import java.nio.file.*;
import java.time.Duration;
import java.time.Instant;
import java.util.UUID;
import java.util.concurrent.*;
import java.util.function.Consumer;
import java.util.function.Supplier;

/** Dependency-free Java 11+ heartbeat writer. It never needs to detect Docker. */
public final class Gohc implements AutoCloseable {
    /** Result of an application health check. */
    public static final class Health {
        /** Protocol status: healthy, unhealthy, unknown or stopped. */
        public final String status;
        /** Optional explanation shown in the detailed report. */
        public final String message;
        /**
         * Creates a check result.
         * @param status supported protocol status
         * @param message optional explanation; null is encoded as an empty string
         */
        public Health(String status, String message) { this.status = status; this.message = message; }
        /** @return a healthy result without a message */
        public static Health healthy() { return new Health("healthy", ""); }
        /**
         * @param message explanation of the failed check
         * @return an unhealthy result
         */
        public static Health unhealthy(String message) { return new Health("unhealthy", message); }
    }
    /** Settings copied when the reporter starts; later mutations have no effect. */
    public static final class Options {
        /** Report directory; defaults to the shared GOHC_DIR or temporary-directory path. */
        public Path directory = defaultDirectory();
        /** Optional instance identifier; generated automatically when absent. */
        public String instance;
        /** Delay between completed heartbeat writes; defaults to one minute. */
        public Duration interval = Duration.ofMinutes(1);
        /** Report expiry; defaults to three minutes and must exceed the interval. */
        public Duration ttl = Duration.ofMinutes(3);
        /** Must return promptly. Exceptions are reported as unhealthy. */
        public Supplier<Health> check = Health::healthy;
        /** Disable only when a framework owns the client lifecycle. */
        public boolean shutdownHook = true;
        /** Handles asynchronous write failures; defaults to logging to standard error. */
        public Consumer<Exception> onError = e -> System.err.println("gohc: " + e.getMessage());
    }
    private final String service, instance;
    private final Options options;
    private final Path path;
    private final ScheduledExecutorService executor;
    private boolean closed;
    private Thread shutdownHook;

    /** @return GOHC_DIR when set, otherwise the shared temporary report directory */
    public static Path defaultDirectory() {
        String explicit = System.getenv("GOHC_DIR");
        if (explicit != null && !explicit.isEmpty()) return Paths.get(explicit);
        String tmp = System.getenv("TMPDIR");
        if (tmp == null || tmp.isEmpty()) {
            tmp = System.getProperty("os.name").startsWith("Windows") ? System.getProperty("java.io.tmpdir") : "/tmp";
        }
        return Paths.get(tmp, "gohc", "reports");
    }
    /**
     * Starts reporting immediately with default settings and an automatic shutdown hook.
     * @param service application name, 1 to 256 UTF-8 bytes
     * @return a running reporter; close it to stop early
     * @throws IOException if the first report cannot be written
     * @throws IllegalArgumentException if the service name is invalid
     */
    public static Gohc start(String service) throws IOException { return start(service, new Options()); }
    /**
     * Starts reporting with application settings. A callback runs on startup and each update;
     * without one, every update reports healthy liveness. Callback exceptions report unhealthy.
     * @param service application name, 1 to 256 UTF-8 bytes
     * @param options settings to copy for this reporter
     * @return a running reporter
     * @throws IOException if the first report cannot be written
     * @throws IllegalArgumentException if a name, duration or health result is invalid
     */
    public static Gohc start(String service, Options options) throws IOException { return new Gohc(service, options); }
    private Gohc(String service, Options supplied) throws IOException {
        if (service == null || service.isEmpty() || service.getBytes(StandardCharsets.UTF_8).length > 256)
            throw new IllegalArgumentException("service must contain 1–256 UTF-8 bytes");
        // Capture options so mutations after initialization cannot race heartbeat writes.
        Options o = new Options();
        o.directory=supplied.directory; o.instance=supplied.instance; o.interval=supplied.interval;
        o.ttl=supplied.ttl; o.check=supplied.check; o.onError=supplied.onError;
        o.shutdownHook=supplied.shutdownHook;
        if (o.interval.toMillis() < 1 || o.ttl.compareTo(o.interval) <= 0 || o.ttl.compareTo(Duration.ofSeconds(1)) < 0 || o.ttl.compareTo(Duration.ofDays(1)) > 0)
            throw new IllegalArgumentException("TTL must exceed a positive interval and be between one second and 24 hours");
        String host;
        try { host=InetAddress.getLocalHost().getHostName(); } catch (Exception e) { host="local"; }
        this.service=service; this.options=o;
        this.instance=o.instance == null || o.instance.isEmpty() ? host+"-"+ProcessHandle.current().pid()+"-"+UUID.randomUUID() : o.instance;
        if (instance.getBytes(StandardCharsets.UTF_8).length>256) throw new IllegalArgumentException("instance exceeds 256 bytes");
        this.path=o.directory.resolve(UUID.randomUUID()+".json");
        write(null); // First report is immediate; initialization failures reach the caller.
        executor=Executors.newSingleThreadScheduledExecutor(r -> { Thread t=new Thread(r,"gohc-heartbeat");t.setDaemon(true);return t; });
        try {
            if (o.shutdownHook) {
                shutdownHook = new Thread(this::close, "gohc-shutdown");
                Runtime.getRuntime().addShutdownHook(shutdownHook);
            }
            executor.scheduleWithFixedDelay(() -> {
                synchronized (this) {
                    if (closed) return;
                    try { write(null); } catch (Exception e) { reportError(e); }
                }
            },o.interval.toMillis(),o.interval.toMillis(),TimeUnit.MILLISECONDS);
        } catch (RuntimeException e) {
            close();
            throw e;
        }
    }
    /** @return the file atomically replaced on each heartbeat */
    public Path reportPath() { return path; }
    private void reportError(Exception e) {
        try { if (options.onError!=null) options.onError.accept(e); } catch (RuntimeException ignored) { /* never stop the schedule */ }
    }
    private void write(String forcedStatus) throws IOException {
        Health h;
        if (forcedStatus!=null) h=new Health(forcedStatus, "");
        else {
            try { h=options.check == null ? Health.healthy() : options.check.get(); }
            catch (Exception e) { h=Health.unhealthy("health callback failed: "+e); }
        }
        if (h==null || !("healthy".equals(h.status)||"unhealthy".equals(h.status)||"unknown".equals(h.status)||"stopped".equals(h.status)))
            throw new IllegalArgumentException("invalid health status");
        long ttl=(options.ttl.toMillis()+999)/1000;
        String json="{\"version\":1,\"service\":"+quote(service)+",\"instance\":"+quote(instance)+",\"status\":"+quote(h.status)+",\"message\":"+quote(h.message)+",\"updated_at\":"+quote(Instant.now().toString())+",\"ttl_seconds\":"+ttl+"}\n";
        byte[] bytes=json.getBytes(StandardCharsets.UTF_8);
        if (bytes.length>64*1024) throw new IOException("report exceeds 64 KiB");
        Files.createDirectories(path.getParent());
        Path tmp=Files.createTempFile(path.getParent(),".gohc-",".tmp");
        try {
            Files.write(tmp,bytes);
            // Same-directory atomic replacement prevents readers from seeing partial JSON.
            Files.move(tmp,path,StandardCopyOption.ATOMIC_MOVE,StandardCopyOption.REPLACE_EXISTING);
        } finally { Files.deleteIfExists(tmp); }
    }
    private static String quote(String s) {
        if (s==null) return "\"\"";
        StringBuilder b=new StringBuilder("\"");
        for (int i=0;i<s.length();i++) {
            char c=s.charAt(i);
            switch(c) {
                case '"': b.append("\\\""); break;
                case '\\': b.append("\\\\"); break;
                case '\n': b.append("\\n"); break;
                case '\r': b.append("\\r"); break;
                case '\t': b.append("\\t"); break;
                default: if(c<32) b.append(String.format("\\u%04x",(int)c)); else b.append(c);
            }
        }
        return b.append('"').toString();
    }
    /** Publish stopped, stop scheduling and release the automatic shutdown hook. */
    @Override public synchronized void close() {
        if (closed) return;
        closed=true;executor.shutdownNow();
        if (shutdownHook != null) {
            try { Runtime.getRuntime().removeShutdownHook(shutdownHook); }
            catch (IllegalStateException ignored) { /* JVM shutdown is already in progress. */ }
            shutdownHook = null;
        }
        try { write("stopped"); } catch (Exception e) { reportError(e); }
    }
}
