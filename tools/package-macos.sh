#!/usr/bin/env bash
set -euo pipefail
cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.."
if [[ $(uname -s) != Darwin ]]; then echo 'Build the macOS app on a macOS runner.' >&2; exit 1; fi
version=${1:-dev}
if [[ ! $version =~ ^[A-Za-z0-9.+_-]+$ ]]; then echo 'Invalid version' >&2; exit 2; fi
arch=$(go env GOARCH)
name="clipboard-translator-macos-$arch-$version"
mkdir -p dist
stage=$(mktemp -d "$PWD/dist/.macos-package-XXXXXX")
trap 'rm -rf -- "$stage"' EXIT
app="$stage/Clipboard Translator.app"
mkdir -p "$app/Contents/MacOS" "$app/Contents/Resources"
MACOSX_DEPLOYMENT_TARGET=13.0 CGO_ENABLED=1 go build -trimpath -buildvcs=false -ldflags "-s -w -X main.version=${version#v}" -o "$app/Contents/MacOS/clipboard-translator" .
cp assets/icon.png "$app/Contents/Resources/icon.png"
cp LICENSE README.md README.ja.md "$app/Contents/Resources/"
# Build an icns using the stock macOS tools.
mkdir "$stage/AppIcon.iconset"
for size in 16 32 128 256 512; do
    sips -z "$size" "$size" assets/icon.png --out "$stage/AppIcon.iconset/icon_${size}x${size}.png" >/dev/null
    double=$((size * 2))
    sips -z "$double" "$double" assets/icon.png --out "$stage/AppIcon.iconset/icon_${size}x${size}@2x.png" >/dev/null
done
iconutil -c icns "$stage/AppIcon.iconset" -o "$app/Contents/Resources/AppIcon.icns"
# CFBundleVersion must be numeric even for development/prerelease builds.
bundle_version=${version#v}
bundle_version=${bundle_version%%[-+]*}
if [[ ! $bundle_version =~ ^[0-9]+(\.[0-9]+){0,2}$ ]]; then bundle_version=0.0.0; fi
cat > "$app/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>CFBundleIdentifier</key><string>com.github.ypsilonmeister.clipboard-translator</string>
<key>CFBundleName</key><string>Clipboard Translator</string>
<key>CFBundleDisplayName</key><string>Clipboard Translator</string>
<key>CFBundleExecutable</key><string>clipboard-translator</string>
<key>CFBundlePackageType</key><string>APPL</string>
<key>CFBundleShortVersionString</key><string>$bundle_version</string>
<key>CFBundleVersion</key><string>$bundle_version</string>
<key>CFBundleIconFile</key><string>AppIcon</string>
<key>LSMinimumSystemVersion</key><string>13.0</string>
<key>LSUIElement</key><true/>
<key>NSHighResolutionCapable</key><true/>
</dict></plist>
PLIST
plutil -lint "$app/Contents/Info.plist"
# Ad-hoc signing only: this is not Developer ID signing or notarization.
codesign --force --sign - "$app"
codesign --verify --deep --strict "$app"
ditto -c -k --keepParent "$app" "dist/$name.zip"
(cd dist && shasum -a 256 "$name.zip" > "$name.zip.sha256")
echo "Built dist/$name.zip"
