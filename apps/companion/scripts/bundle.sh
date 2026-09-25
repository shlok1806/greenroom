#!/usr/bin/env bash
# Builds the companion as a real macOS .app, so it can live in /Applications
# instead of being launched by a script. The bundle is ad-hoc signed: enough
# for the app to keep its identity across launches, not a distributable.
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

BUILD=".build"
APP="$BUILD/Companion.app"
CONTENTS="$APP/Contents"

# Only the app: the snapshot tool uses @testable and builds in debug alone.
swift build -c release --product Companion
BINARY="$(swift build -c release --show-bin-path)/Companion"

rm -rf "$APP"
mkdir -p "$CONTENTS/MacOS" "$CONTENTS/Resources"
cp "$BINARY" "$CONTENTS/MacOS/Companion"

# The fonts (with their OFL licences) and the design data live in SwiftPM's resource
# bundle. It goes in Contents/Resources, where codesign seals it and `AppResources`
# looks first; without it the app falls back to system fonts or stops at launch.
# Xcode's build system writes a macOS bundle (Contents/Resources); the Command Line
# Tools write a flat one. `Bundle.resourceURL` finds either.
RESOURCES="$(swift build -c release --show-bin-path)/Companion_Companion.bundle"
INNER="$RESOURCES"
[ -d "$RESOURCES/Contents/Resources" ] && INNER="$RESOURCES/Contents/Resources"
if [ ! -f "$INNER/Fonts/MonaSans-Regular.otf" ] || [ ! -f "$INNER/Design/tokens.json" ]; then
	echo "missing resource bundle at $RESOURCES" >&2
	exit 1
fi
cp -R "$RESOURCES" "$CONTENTS/Resources/"

cat > "$CONTENTS/Info.plist" <<'PLIST'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleName</key>
	<string>Greenroom Companion</string>
	<key>CFBundleDisplayName</key>
	<string>Greenroom Companion</string>
	<key>CFBundleIdentifier</key>
	<string>com.greenroom.companion</string>
	<key>CFBundleExecutable</key>
	<string>Companion</string>
	<key>CFBundlePackageType</key>
	<string>APPL</string>
	<key>CFBundleShortVersionString</key>
	<string>0.1.0</string>
	<key>CFBundleVersion</key>
	<string>1</string>
	<key>CFBundleIconFile</key>
	<string>AppIcon</string>
	<key>LSMinimumSystemVersion</key>
	<string>15.0</string>
	<key>NSHighResolutionCapable</key>
	<true/>
	<key>NSHumanReadableCopyright</key>
	<string></string>
</dict>
</plist>
PLIST

printf 'APPL????' > "$CONTENTS/PkgInfo"

# The icon is drawn here rather than checked in, so no binary artwork lives in
# the repo. sips resizes, iconutil packs.
ICONSET="$BUILD/AppIcon.iconset"
rm -rf "$ICONSET"
mkdir -p "$ICONSET"
swift scripts/make-icon.swift "$BUILD/icon-1024.png" > /dev/null
for size in 16 32 128 256 512; do
	sips -z "$size" "$size" "$BUILD/icon-1024.png" --out "$ICONSET/icon_${size}x${size}.png" > /dev/null
	double=$((size * 2))
	sips -z "$double" "$double" "$BUILD/icon-1024.png" --out "$ICONSET/icon_${size}x${size}@2x.png" > /dev/null
done
iconutil -c icns "$ICONSET" -o "$CONTENTS/Resources/AppIcon.icns"
rm -rf "$ICONSET" "$BUILD/icon-1024.png"

codesign --force --deep --sign - "$APP"

echo "bundled $(pwd)/$APP"
