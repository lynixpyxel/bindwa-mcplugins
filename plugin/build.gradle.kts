plugins {
    `java`
    id("com.gradleup.shadow") version "8.3.6"
}

group = "com.dozzy"
version = "1.0.0"

java {
    toolchain {
        languageVersion.set(JavaLanguageVersion.of(21))
        vendor.set(JvmVendorSpec.ADOPTIUM)
    }
}

repositories {
    mavenCentral()
    maven("https://repo.papermc.io/repository/maven-public/")
    maven("https://repo.codemc.io/repository/maven-public/")
    // maven("https://repo.codemc.io/repository/maven-snapshots/")
    maven("https://mvn.wesjd.net/")
    maven("https://repo.opencollab.dev/main/")
}

dependencies {
    compileOnly("io.papermc.paper:paper-api:1.20.4-R0.1-SNAPSHOT")
    compileOnly("org.geysermc.floodgate:api:2.2.2-SNAPSHOT")

    implementation("net.wesjd:anvilgui:1.10.13-SNAPSHOT")
    implementation("org.xerial:sqlite-jdbc:3.45.1.0")

    testCompileOnly("io.papermc.paper:paper-api:1.20.4-R0.1-SNAPSHOT")
    testCompileOnly("org.geysermc.floodgate:api:2.2.2-SNAPSHOT")
    testImplementation("io.papermc.paper:paper-api:1.20.4-R0.1-SNAPSHOT")
    testImplementation("org.junit.jupiter:junit-jupiter:5.10.2")
    testRuntimeOnly("org.junit.platform:junit-platform-launcher")
}

tasks {
    shadowJar {
        archiveClassifier.set("")
        relocate("net.wesjd.anvilgui", "com.dozzy.libs.anvilgui")
        relocate("org.sqlite", "com.dozzy.libs.sqlite")
    }

    build {
        dependsOn(shadowJar)
    }

    test {
        useJUnitPlatform()
    }

    processResources {
        val props = mapOf("version" to version)
        inputs.properties(props)
        filteringCharset = "UTF-8"
        filesMatching("plugin.yml") {
            expand(props)
        }
    }
}
