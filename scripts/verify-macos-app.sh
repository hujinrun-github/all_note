#!/usr/bin/env bash
set -euo pipefail

app_path="${1:-}"
expected_bundle_id="${FLOWSPACE_BUNDLE_ID:-com.flowspace.mac}"
require_signing="${FLOWSPACE_REQUIRE_SIGNING:-0}"
require_notarization="${FLOWSPACE_REQUIRE_NOTARIZATION:-0}"
require_updates="${FLOWSPACE_REQUIRE_UPDATES:-0}"

if [[ -z "$app_path" || ! -d "$app_path" ]]; then
  echo "usage: $0 /path/to/FlowSpaceMac.app" >&2
  exit 2
fi

info_plist="$app_path/Contents/Info.plist"
if [[ ! -f "$info_plist" ]]; then
  echo "missing Info.plist: $info_plist" >&2
  exit 1
fi

plist_value() {
  /usr/libexec/PlistBuddy -c "Print :$1" "$info_plist"
}

bundle_id="$(plist_value CFBundleIdentifier)"
executable_name="$(plist_value CFBundleExecutable)"
version="$(plist_value CFBundleShortVersionString)"
build_number="$(plist_value CFBundleVersion)"
icon_name="$(plist_value CFBundleIconFile)"
executable="$app_path/Contents/MacOS/$executable_name"

if [[ "$bundle_id" != "$expected_bundle_id" ]]; then
  echo "unexpected bundle identifier: $bundle_id" >&2
  exit 1
fi

if [[ ! -x "$executable" ]]; then
  echo "missing executable: $executable" >&2
  exit 1
fi

architectures="$(lipo -archs "$executable")"
for architecture in arm64 x86_64; do
  if [[ " $architectures " != *" $architecture "* ]]; then
    echo "missing $architecture slice: $architectures" >&2
    exit 1
  fi
done

if [[ "$icon_name" != *.icns ]]; then
  icon_name="$icon_name.icns"
fi
if [[ ! -f "$app_path/Contents/Resources/$icon_name" ]]; then
  echo "missing application icon: $icon_name" >&2
  exit 1
fi
if [[ ! -f "$app_path/Contents/Resources/Assets.car" ]]; then
  echo "missing compiled asset catalog" >&2
  exit 1
fi
if [[ ! -f "$app_path/Contents/Resources/RichEditor/index.html" ]]; then
  echo "missing bundled rich text editor" >&2
  exit 1
fi
if [[ ! -d "$app_path/Contents/Frameworks/Sparkle.framework" ]]; then
  echo "missing Sparkle update framework" >&2
  exit 1
fi

if [[ "$require_updates" == "1" ]]; then
  feed_url="$(plist_value SUFeedURL)"
  public_key="$(plist_value SUPublicEDKey)"
  installer_service="$(plist_value SUEnableInstallerLauncherService)"

  if [[ "$feed_url" != https://* ]]; then
    echo "SUFeedURL must use HTTPS for distribution: $feed_url" >&2
    exit 1
  fi
  if [[ -z "$public_key" ]]; then
    echo "SUPublicEDKey is required for distribution" >&2
    exit 1
  fi
  public_key_bytes="$(
    (printf '%s' "$public_key" | base64 --decode 2>/dev/null || true) |
      wc -c | tr -d '[:space:]'
  )"
  if [[ "$public_key_bytes" != "32" ]]; then
    echo "SUPublicEDKey must be a Base64-encoded 32-byte Ed25519 public key" >&2
    exit 1
  fi
  if [[ "$installer_service" != "true" ]]; then
    echo "SUEnableInstallerLauncherService must be enabled for the sandboxed app" >&2
    exit 1
  fi
fi

if [[ "$require_signing" == "1" ]]; then
  codesign --verify --deep --strict --verbose=2 "$app_path"
fi

if [[ "$require_notarization" == "1" ]]; then
  xcrun stapler validate "$app_path"
  spctl --assess --type execute --verbose=2 "$app_path"
fi

echo "Verified $app_path"
echo "Bundle: $bundle_id $version ($build_number)"
echo "Architectures: $architectures"
