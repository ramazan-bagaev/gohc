package io.gohc.spring;

import io.gohc.Gohc;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.concurrent.atomic.AtomicReference;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;
import org.springframework.boot.autoconfigure.EnableAutoConfiguration;
import org.springframework.boot.test.context.runner.ApplicationContextRunner;
import org.springframework.context.annotation.Bean;
import org.springframework.context.annotation.Configuration;
import static org.assertj.core.api.Assertions.assertThat;

class GohcAutoConfigurationTest {
    @TempDir Path directory;
    private ApplicationContextRunner runner() {
        // Discover the starter through its packaged imports, exactly as Boot does.
        return new ApplicationContextRunner().withUserConfiguration(App.class)
            .withPropertyValues("gohc.directory=" + directory);
    }
    @Configuration(proxyBeanMethods = false)
    @EnableAutoConfiguration
    static class App {}

    @Test void dependencyDiscoveryDefaultsAndContextShutdown() throws Exception {
        AtomicReference<Path> report = new AtomicReference<>();
        runner().withPropertyValues("spring.application.name=orders").run(context -> {
            assertThat(context).hasSingleBean(Gohc.class);
            Path path = context.getBean(Gohc.class).reportPath();
            report.set(path);
            assertThat(Files.readString(path)).contains("\"service\":\"orders\"", "\"status\":\"healthy\"", "\"ttl_seconds\":180");
        });
        assertThat(Files.readString(report.get())).contains("\"status\":\"stopped\"");
    }
    @Test void fallbackNameAndTimingOverrides() {
        runner().withPropertyValues("gohc.interval=2s").run(context -> {
            assertThat(context).hasNotFailed();
            assertThat(Files.readString(context.getBean(Gohc.class).reportPath()))
                .contains("\"service\":\"application\"", "\"ttl_seconds\":6");
        });
    }
    @Test void explicitPropertiesAndCustomCheck() {
        runner().withBean(GohcHealthCheck.class, () -> () -> Gohc.Health.unhealthy("database unavailable"))
            .withPropertyValues("spring.application.name=ignored", "gohc.service=custom", "gohc.instance=worker-1", "gohc.interval=2s", "gohc.ttl=10s")
            .run(context -> {
                assertThat(context).hasNotFailed();
                assertThat(Files.readString(context.getBean(Gohc.class).reportPath()))
                    .contains("\"service\":\"custom\"", "\"instance\":\"worker-1\"", "\"status\":\"unhealthy\"", "\"ttl_seconds\":10");
            });
    }
    @Test void disableDoesNotWriteReports() throws Exception {
        runner().withPropertyValues("gohc.enabled=false").run(context -> assertThat(context).doesNotHaveBean(Gohc.class));
        try (var files = Files.list(directory)) { assertThat(files.count()).isZero(); }
    }
    @Test void userClientPreventsDuplicateRegistration() throws Exception {
        Gohc.Options options = new Gohc.Options(); options.directory = directory;
        try (Gohc custom = Gohc.start("manual", options)) {
            runner().withBean(Gohc.class, () -> custom).run(context -> {
                assertThat(context).hasSingleBean(Gohc.class);
                assertThat(context.getBean(Gohc.class)).isSameAs(custom);
                try (var files = Files.list(directory)) { assertThat(files.count()).isEqualTo(1); }
            });
        }
    }
    @Test void invalidTimingFailsClearly() {
        runner().withPropertyValues("gohc.interval=10s", "gohc.ttl=5s")
            .run(context -> assertThat(context).hasFailed());
    }
    @Test void initializationFailureIsVisible() throws Exception {
        Path file = Files.createFile(directory.resolve("file"));
        runner().withPropertyValues("gohc.directory=" + file)
            .run(context -> assertThat(context).hasFailed());
    }
}
