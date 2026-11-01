package io.gohc;
import java.nio.file.*;
import java.time.Duration;

/** Runs without a test framework; output reports are also validated by Go. */
public final class GohcSmokeTest {
    public static void main(String[] args) throws Exception {
        if (args.length > 1 && args[1].equals("child")) {
            Gohc.Options childOptions = new Gohc.Options();
            childOptions.directory = Paths.get(args[0]);
            Gohc.start("shutdown-child", childOptions);
            System.out.println("ready"); System.out.flush();
            if (args.length > 2) Thread.sleep(60000);
            return; // Normal JVM exit must publish stopped without an explicit close.
        }
        Path directory=Paths.get(args[0]);
        Gohc.Options options=new Gohc.Options();options.directory=directory;
        options.interval=Duration.ofMillis(10);options.ttl=Duration.ofSeconds(1);
        options.instance="java-instance";
        options.check=() -> Gohc.Health.unhealthy("database \"offline\"\ncheck \\ path\t雪");
        Gohc r=Gohc.start("java-smoke",options);
        String first=Files.readString(r.reportPath());
        if (!first.contains("\"status\":\"unhealthy\"")) throw new AssertionError(first);
        Files.writeString(directory.resolve("java-initial.fixture"),first);
        long deadline=System.nanoTime()+Duration.ofSeconds(2).toNanos();
        while (first.equals(Files.readString(r.reportPath()))) {
            if (System.nanoTime()>deadline) throw new AssertionError("heartbeat did not advance");
            Thread.sleep(5);
        }
        r.close();r.close();
        String last=Files.readString(r.reportPath());
        if (!last.contains("\"status\":\"stopped\"")) throw new AssertionError(last);
        Files.writeString(directory.resolve("java-final.fixture"),last);
        verifyShutdown(directory.resolve("normal-exit"), false);
        verifyShutdown(directory.resolve("signal-exit"), true);
        System.out.println("Java immediate report, periodic update, escaping and shutdown passed.");
    }
    private static void verifyShutdown(Path directory, boolean signal) throws Exception {
        String javaExecutable = Paths.get(System.getProperty("java.home"), "bin", "java").toString();
        ProcessBuilder builder = new ProcessBuilder(javaExecutable, "-cp", System.getProperty("java.class.path"),
            GohcSmokeTest.class.getName(), directory.toString(), "child");
        if (signal) builder.command().add("wait");
        Process child = builder.redirectError(ProcessBuilder.Redirect.INHERIT).start();
        try {
            if (signal) {
                try (var reader = new java.io.BufferedReader(new java.io.InputStreamReader(child.getInputStream()))) {
                    if (!"ready".equals(reader.readLine())) throw new AssertionError("child did not start");
                }
                child.destroy(); // SIGTERM on Unix.
            }
            if (!child.waitFor(10, java.util.concurrent.TimeUnit.SECONDS)) throw new AssertionError("child did not exit");
            try (var files = Files.list(directory)) {
                Path report = files.filter(p -> p.toString().endsWith(".json")).findFirst().orElseThrow();
                String last = Files.readString(report);
                if (!last.contains("\"status\":\"stopped\"")) throw new AssertionError(last);
            }
        } finally { if (child.isAlive()) child.destroyForcibly(); }
    }
}
