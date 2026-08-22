plugins {
    kotlin("jvm") version "2.3.20"
    kotlin("plugin.spring") version "2.3.20"
    id("org.springframework.boot") version "4.0.6"
}

repositories { mavenCentral() }

dependencies {
    implementation("org.springframework.boot:spring-boot-starter-web:4.0.6")
    implementation("org.jetbrains.kotlin:kotlin-reflect:2.3.20")
    testImplementation("org.springframework.boot:spring-boot-starter-test:4.0.6")
}

kotlin { jvmToolchain(25) }
springBoot { mainClass = "dev.mds.ApplicationKt" }
tasks.test { useJUnitPlatform() }
