plugins {
    java
    id("org.springframework.boot") version "4.0.6"
}

repositories { mavenCentral() }

dependencies {
    implementation("org.springframework.boot:spring-boot-starter-web:4.0.6")
    testImplementation("org.springframework.boot:spring-boot-starter-test:4.0.6")
}

java { toolchain { languageVersion = JavaLanguageVersion.of(25) } }
tasks.test { useJUnitPlatform() }
