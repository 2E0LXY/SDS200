import java.util.Base64

plugins {
    alias(libs.plugins.android.application)
    alias(libs.plugins.kotlin.android)
    alias(libs.plugins.kotlin.compose)
}

// Version: APP_VERSION (e.g. "v0.8.1" from a tag) else 0.8.0-dev; code from the CI run number.
val appVersionName: String = (System.getenv("APP_VERSION") ?: "").trim().removePrefix("v").ifEmpty { "0.8.0-dev" }
val appVersionCode: Int = System.getenv("GITHUB_RUN_NUMBER")?.toIntOrNull()?.coerceIn(1, 2_100_000_000) ?: 1

// Release signing from environment (CI secrets). Falls back to the debug key so
// every build produces an installable APK.
val keystoreB64: String? = System.getenv("SDS200_KEYSTORE_B64")?.takeIf { it.isNotBlank() }
val releaseKeystoreFile: File? = keystoreB64?.let { b64 ->
    val f = layout.buildDirectory.file("signing/release.jks").get().asFile
    f.parentFile.mkdirs()
    f.writeBytes(Base64.getMimeDecoder().decode(b64.trim()))
    f
}

android {
    namespace = "uk.co.twoe0lxy.sds200"
    compileSdk = 35

    defaultConfig {
        applicationId = "uk.co.twoe0lxy.sds200"
        minSdk = 26
        targetSdk = 35
        versionCode = appVersionCode
        versionName = appVersionName
    }

    signingConfigs {
        if (releaseKeystoreFile != null) {
            create("release") {
                storeFile = releaseKeystoreFile
                storePassword = System.getenv("SDS200_KEYSTORE_PASSWORD")
                keyAlias = System.getenv("SDS200_KEY_ALIAS")
                keyPassword = System.getenv("SDS200_KEY_PASSWORD") ?: System.getenv("SDS200_KEYSTORE_PASSWORD")
            }
        }
    }

    buildTypes {
        release {
            isMinifyEnabled = true
            isShrinkResources = true
            proguardFiles(getDefaultProguardFile("proguard-android-optimize.txt"), "proguard-rules.pro")
            signingConfig = if (releaseKeystoreFile != null) {
                signingConfigs.getByName("release")
            } else {
                signingConfigs.getByName("debug")
            }
        }
        debug {
            applicationIdSuffix = ".debug"
            versionNameSuffix = "-debug"
        }
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
    buildFeatures {
        compose = true
        buildConfig = true
    }
    packaging {
        resources.excludes += setOf("/META-INF/{AL2.0,LGPL2.1}", "/META-INF/*.version", "DebugProbesKt.bin")
    }
    lint {
        abortOnError = true
        warningsAsErrors = false
        checkReleaseBuilds = true
        disable += setOf("GradleDependency", "NewerVersionAvailable", "AndroidGradlePluginVersion", "OldTargetApi")
    }
    testOptions {
        unitTests.isReturnDefaultValues = true
    }
}

kotlin {
    jvmToolchain(21)
    compilerOptions {
        jvmTarget.set(org.jetbrains.kotlin.gradle.dsl.JvmTarget.JVM_17)
    }
}

// SDS200-Remote-<version>.apk
@Suppress("DEPRECATION")
android.applicationVariants.all {
    val variant = this
    outputs.all {
        val suffix = if (variant.buildType.name == "release") "" else "-${variant.buildType.name}"
        (this as com.android.build.gradle.internal.api.BaseVariantOutputImpl).outputFileName =
            "SDS200-Remote-$appVersionName$suffix.apk"
    }
}

dependencies {
    implementation(libs.androidx.core.ktx)
    implementation(libs.androidx.lifecycle.runtime.ktx)
    implementation(libs.androidx.lifecycle.runtime.compose)
    implementation(libs.androidx.lifecycle.viewmodel.compose)
    implementation(libs.androidx.activity.compose)
    implementation(platform(libs.androidx.compose.bom))
    implementation(libs.androidx.compose.ui)
    implementation(libs.androidx.compose.ui.graphics)
    implementation(libs.androidx.compose.material3)
    implementation(libs.androidx.navigation.compose)
    implementation(libs.androidx.datastore.preferences)
    implementation(libs.kotlinx.coroutines.android)

    testImplementation(libs.junit)
    testImplementation(libs.kxml2)
    testImplementation(libs.kotlinx.coroutines.test)
}
