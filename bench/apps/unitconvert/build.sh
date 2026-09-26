#!/bin/sh
# Builds build/UnitConvert.app with the Command Line Tools only: no Xcode, no SwiftPM cache (ADR 0019).
# One swiftc call over the package's sources, as the field runs built TipSplit. Package.swift
# is there for `swift build` on a developer's machine; the bench never uses it.
set -eu
cd "$(dirname "$0")"
name=UnitConvert
id=com.greenroom.bench.unitconvert
app="build/$name.app"
rm -rf build
mkdir -p "$app/Contents/MacOS"
swiftc -parse-as-library -O Sources/$name/*.swift -o "$app/Contents/MacOS/$name"
cat > "$app/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleExecutable</key><string>$name</string>
  <key>CFBundleIdentifier</key><string>$id</string>
  <key>CFBundleName</key><string>$name</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>CFBundleShortVersionString</key><string>1.0</string>
  <key>LSMinimumSystemVersion</key><string>14.0</string>
  <key>NSPrincipalClass</key><string>NSApplication</string>
</dict>
</plist>
PLIST
echo "built $app"
