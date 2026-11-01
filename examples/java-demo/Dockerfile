FROM eclipse-temurin:17-jre-alpine
WORKDIR /app
COPY gohc-client-0.1.0.jar GohcDemo.class ./
USER 65534:65534
ENTRYPOINT ["java", "-Xms8m", "-Xmx32m", "-cp", "/app/gohc-client-0.1.0.jar:/app", "GohcDemo"]
