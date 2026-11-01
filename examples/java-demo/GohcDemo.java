import io.gohc.Gohc;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.concurrent.CountDownLatch;

/** Demo application: no HTTP listener, Docker API, mounts or special environment. */
public final class GohcDemo {
    public static void main(String[] args) throws Exception {
        Gohc.Options options = new Gohc.Options();
        options.check = () -> Files.exists(Path.of("/tmp/gohc-demo-unhealthy"))
            ? Gohc.Health.unhealthy("Demo dependency is unavailable")
            : new Gohc.Health("healthy", "Java demo is doing useful work");
        Gohc heartbeat = Gohc.start("gohc-java-demo", options);
        System.out.println("Unpublished gohc client started; local report: " + heartbeat.reportPath());
        new CountDownLatch(1).await();
    }
}
