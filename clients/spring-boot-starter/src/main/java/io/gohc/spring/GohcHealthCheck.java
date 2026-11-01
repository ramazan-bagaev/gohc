package io.gohc.spring;

import io.gohc.Gohc;

/** Optional application check. Must return promptly; exceptions report unhealthy. */
@FunctionalInterface
public interface GohcHealthCheck {
    /** @return the current application health; called immediately and on each heartbeat */
    Gohc.Health check();
}
