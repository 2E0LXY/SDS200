# SDS200 Remote R8 rules.
# The app uses no reflection-based serialisation; keep rules are limited to the
# framework entry points and the XmlPullParser implementation supplied by Android.

# Manifest components are kept by AAPT-generated rules; keep their names stable in stack traces.
-keepattributes SourceFile,LineNumberTable,*Annotation*
-renamesourcefileattribute SourceFile

# XmlPullParser is provided by the platform (kxml2); never obfuscate the interface.
-keep class org.xmlpull.v1.** { *; }
-dontwarn org.xmlpull.v1.**
-dontwarn org.kxml2.**

# Parsed protocol models are plain data classes; keep their names for readable crash logs.
-keepnames class uk.co.twoe0lxy.sds200.protocol.** { *; }
