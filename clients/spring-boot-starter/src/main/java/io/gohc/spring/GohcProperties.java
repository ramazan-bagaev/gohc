package io.gohc.spring;

import java.nio.file.Path;
import java.time.Duration;
import org.springframework.boot.context.properties.ConfigurationProperties;

/** Optional gohc settings bound from Spring properties with the gohc prefix. */
@ConfigurationProperties("gohc")
public class GohcProperties {
    private boolean enabled = true;
    private String service;
    private String instance;
    private Path directory;
    private Duration interval = Duration.ofMinutes(1);
    private Duration ttl;
    /** @return whether automatic reporting is enabled */
    public boolean isEnabled() { return enabled; }
    /** @param enabled whether automatic reporting is enabled */
    public void setEnabled(boolean enabled) { this.enabled = enabled; }
    /** @return the service name override; otherwise spring.application.name is used */
    public String getService() { return service; }
    /** @param service the service name override; otherwise spring.application.name is used */
    public void setService(String service) { this.service = service; }
    /** @return the instance identifier override; null generates an identifier */
    public String getInstance() { return instance; }
    /** @param instance the instance identifier override; null generates an identifier */
    public void setInstance(String instance) { this.instance = instance; }
    /** @return the report directory override; null uses the shared client default */
    public Path getDirectory() { return directory; }
    /** @param directory the report directory override; null uses the shared client default */
    public void setDirectory(Path directory) { this.directory = directory; }
    /** @return the heartbeat interval; defaults to one minute */
    public Duration getInterval() { return interval; }
    /** @param interval the heartbeat interval; defaults to one minute */
    public void setInterval(Duration interval) { this.interval = interval; }
    /** @return the report expiry; null uses three times the interval */
    public Duration getTtl() { return ttl; }
    /** @param ttl the report expiry; null uses three times the interval */
    public void setTtl(Duration ttl) { this.ttl = ttl; }
}
