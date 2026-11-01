package io.gohc.spring;

import java.io.IOException;
import io.gohc.Gohc;
import org.springframework.beans.factory.ObjectProvider;
import org.springframework.boot.autoconfigure.AutoConfiguration;
import org.springframework.boot.autoconfigure.condition.ConditionalOnMissingBean;
import org.springframework.boot.autoconfigure.condition.ConditionalOnProperty;
import org.springframework.boot.context.properties.EnableConfigurationProperties;
import org.springframework.context.annotation.Bean;
import org.springframework.core.env.Environment;

/** Automatically creates one reporter unless disabled or an application supplies its own. */
@AutoConfiguration
@EnableConfigurationProperties(GohcProperties.class)
@ConditionalOnProperty(prefix = "gohc", name = "enabled", havingValue = "true", matchIfMissing = true)
public class GohcAutoConfiguration {
    @Bean(destroyMethod = "close")
    @ConditionalOnMissingBean(Gohc.class)
    Gohc gohc(GohcProperties properties, Environment environment,
              ObjectProvider<GohcHealthCheck> checks) throws IOException {
        String service = properties.getService();
        if (service == null || service.isBlank()) {
            service = environment.getProperty("spring.application.name", "application");
            if (service.isBlank()) service = "application";
        }
        Gohc.Options options = new Gohc.Options();
        if (properties.getDirectory() != null) options.directory = properties.getDirectory();
        options.instance = properties.getInstance();
        options.interval = properties.getInterval();
        options.ttl = properties.getTtl() == null ? options.interval.multipliedBy(3) : properties.getTtl();
        options.shutdownHook = false; // Spring closes the bean with the application context.
        GohcHealthCheck check = checks.getIfAvailable();
        if (check != null) options.check = check::check;
        return Gohc.start(service, options);
    }
}
