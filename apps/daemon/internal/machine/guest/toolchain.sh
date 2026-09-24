#!/bin/sh
# : greenroom-toolchain-manifest
# toolchain.sh: measure the image's Swift toolchain and write /usr/local/greenroom/toolchain.json
# (ADR 0019, issue #44). Runs in the guest as the admin user through `greenroom prepare-image`.
# Every field is measured: a tiny XCTest package and a tiny swift-testing package are built
# and run with a plain `swift test`, with no extra search paths, exactly as an agent would.
# The daemon passes the file through to machine_wait as it is.
set -u

out=/usr/local/greenroom/toolchain.json
work="$(mktemp -d /tmp/greenroom-toolchain.XXXXXX)"
plist="$work/toolchain.plist"
trap 'rm -rf "$work"' EXIT

put() { defaults write "$plist" "$@"; } # put <key> -string|-bool <value>

put known -bool true
put measuredAt -string "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
put macos -string "$(sw_vers -productVersion) ($(sw_vers -buildVersion))"

dev="$(xcode-select -p 2>/dev/null || true)"
put developerDir -string "$dev"
xcode=""
for app in /Applications/Xcode*.app; do
  [ -d "$app/Contents/Developer" ] && { xcode="$app"; break; }
done
if [ -n "$xcode" ]; then
  put xcode -bool true
  put xcodePath -string "$xcode"
  put xcodeVersion -string "$(DEVELOPER_DIR="$xcode/Contents/Developer" xcodebuild -version 2>/dev/null | tr '\n' ' ' | sed 's/ *$//')"
else
  put xcode -bool false
fi

clt="$(pkgutil --pkg-info=com.apple.pkg.CLTools_Executables 2>/dev/null | sed -n 's/^version: //p')"
put commandLineTools -string "${clt:-none}"
put swiftVersion -string "$(swift --version 2>&1 | head -n 1)"

# probe <name> <import> <test source>: writes <name> (bool) and <name>Error (first error line).
probe() {
  name="$1"; dir="$work/$1"
  mkdir -p "$dir/Sources/Lib" "$dir/Tests/LibTests"
  cat > "$dir/Package.swift" <<'EOF'
// swift-tools-version:5.9
import PackageDescription
let package = Package(name: "Probe", targets: [
  .target(name: "Lib"),
  .testTarget(name: "LibTests", dependencies: ["Lib"]),
])
EOF
  echo 'public func two() -> Int { 2 }' > "$dir/Sources/Lib/Lib.swift"
  printf '%s\n' "$2" > "$dir/Tests/LibTests/LibTests.swift"
  if (cd "$dir" && swift test > "$dir/log" 2>&1) && grep -q -i -E "passed|0 failures" "$dir/log"; then
    put "$name" -bool true
  else
    put "$name" -bool false
    put "${name}Error" -string "$({ grep -m 1 'no such module' "$dir/log" || grep -m 1 'error:' "$dir/log"; } | sed 's/^.*error: //' | cut -c1-200)"
  fi
}
probe xctest 'import XCTest
@testable import Lib
final class LibTests: XCTestCase { func testTwo() { XCTAssertEqual(two(), 2) } }'
probe swiftTesting 'import Testing
@testable import Lib
@Test func two2() { #expect(two() == 2) }'

put note -string "swift test runs only the frameworks marked true here. Never delete or exclude a project's own tests to get a green run; report that the toolchain cannot run them."

sudo -n mkdir -p "$(dirname "$out")"
plutil -convert json -o "$work/toolchain.json" "$plist"
sudo -n install -m 644 "$work/toolchain.json" "$out"
[ "$(plutil -extract known raw -o - "$out")" = true ] || { echo "toolchain: $out does not read back" >&2; exit 1; }
cat "$out"
