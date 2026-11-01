.PHONY: build test test-race java-client test-java
build:
	CGO_ENABLED=0 go build -o gohc .
test:
	CGO_ENABLED=0 go test ./...
	go vet ./...
test-race:
	go test -race ./...
java-client:
	mkdir -p .build/java
	javac --release 11 -d .build/java clients/java/src/main/java/io/gohc/Gohc.java
	mkdir -p .build/java/META-INF/maven/top.kanasuki.gohc/gohc-client
	cp LICENSE .build/java/META-INF/LICENSE
	cp clients/java/pom.xml .build/java/META-INF/maven/top.kanasuki.gohc/gohc-client/pom.xml
	printf 'groupId=top.kanasuki.gohc\nartifactId=gohc-client\nversion=0.1.0\n' > .build/java/META-INF/maven/top.kanasuki.gohc/gohc-client/pom.properties
	jar --create --file .build/gohc-client-0.1.0.jar -C .build/java io/gohc/Gohc.class -C .build/java 'io/gohc/Gohc$$Health.class' -C .build/java 'io/gohc/Gohc$$Options.class' -C .build/java META-INF
test-java: java-client
	javac --release 11 -cp .build/java -d .build/java clients/java/src/test/java/io/gohc/GohcSmokeTest.java
	mkdir -p .build/java-reports
	java -cp .build/java io.gohc.GohcSmokeTest .build/java-reports
	GOHC_JAVA_FIXTURES=.build/java-reports go test ./report -run TestJavaInteroperability -count=1
