#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "$0")/.." && pwd)"
source_app="${1:-}"
output_dir="${FLOWSPACE_RELEASE_OUTPUT_DIR:-$repo_root/dist/macos}"
signing_identity="${FLOWSPACE_DEVELOPER_ID_APPLICATION:-}"
notary_profile="${FLOWSPACE_NOTARY_PROFILE:-}"
notary_keychain="${FLOWSPACE_NOTARY_KEYCHAIN:-}"

if [[ -z "$source_app" || ! -d "$source_app" ]]; then
  echo "usage: $0 /path/to/FlowSpaceMac.app" >&2
  exit 2
fi
if [[ -z "$signing_identity" ]]; then
  echo "FLOWSPACE_DEVELOPER_ID_APPLICATION is required" >&2
  exit 2
fi
if [[ -z "$notary_profile" ]]; then
  echo "FLOWSPACE_NOTARY_PROFILE is required" >&2
  exit 2
fi
if ! security find-identity -v -p codesigning | grep -F -- "$signing_identity" >/dev/null; then
  echo "Developer ID signing identity is not available: $signing_identity" >&2
  exit 1
fi

staging_root="$(mktemp -d "${TMPDIR:-/tmp}/flowspace-macos-package.XXXXXX")"
cleanup() {
  rm -rf -- "$staging_root"
}
trap cleanup EXIT

staged_app="$staging_root/$(basename "$source_app")"
ditto "$source_app" "$staged_app"

# Sparkle ships nested XPC services and helpers that must be signed from the
# inside out. Downloader.xpc has its own sandbox entitlements, so preserve them.
sparkle_framework="$staged_app/Contents/Frameworks/Sparkle.framework"
sparkle_version="$sparkle_framework/Versions/B"
if [[ ! -d "$sparkle_version" ]]; then
  echo "missing Sparkle framework version B: $sparkle_version" >&2
  exit 1
fi

while IFS= read -r nested_code; do
  codesign --force --options runtime --timestamp --sign "$signing_identity" "$nested_code"
done < <(
  find "$staged_app/Contents" -depth \
    \( -type d \( -name '*.app' -o -name '*.appex' -o -name '*.framework' -o -name '*.xpc' \) \
    -o -type f -name '*.dylib' \) \
    ! -path "$sparkle_framework" ! -path "$sparkle_framework/*" -print
)

codesign --force --options runtime --timestamp --sign "$signing_identity" \
  "$sparkle_version/XPCServices/Installer.xpc"
codesign --force --options runtime --timestamp --preserve-metadata=entitlements \
  --sign "$signing_identity" "$sparkle_version/XPCServices/Downloader.xpc"
codesign --force --options runtime --timestamp --sign "$signing_identity" \
  "$sparkle_version/Autoupdate"
codesign --force --options runtime --timestamp --sign "$signing_identity" \
  "$sparkle_version/Updater.app"
codesign --force --options runtime --timestamp --sign "$signing_identity" \
  "$sparkle_framework"

codesign \
  --force \
  --options runtime \
  --timestamp \
  --entitlements "$repo_root/macos/FlowSpaceMac/Resources/FlowSpaceMac.entitlements" \
  --sign "$signing_identity" \
  "$staged_app"

FLOWSPACE_REQUIRE_SIGNING=1 FLOWSPACE_REQUIRE_UPDATES=1 \
  "$repo_root/scripts/verify-macos-app.sh" "$staged_app"

info_plist="$staged_app/Contents/Info.plist"
version="$(/usr/libexec/PlistBuddy -c 'Print :CFBundleShortVersionString' "$info_plist")"
build_number="$(/usr/libexec/PlistBuddy -c 'Print :CFBundleVersion' "$info_plist")"
safe_version="$(printf '%s' "$version" | tr -cs '[:alnum:].-' '-')"
safe_build="$(printf '%s' "$build_number" | tr -cs '[:alnum:].-' '-')"
artifact_name="FlowSpaceMac-${safe_version}-${safe_build}.zip"

mkdir -p "$output_dir"
output_dir="$(cd "$output_dir" && pwd)"
artifact_path="$output_dir/$artifact_name"
checksum_path="$artifact_path.sha256"
if [[ -e "$artifact_path" || -e "$checksum_path" ]]; then
  echo "release artifact already exists: $artifact_path" >&2
  echo "remove it explicitly before rebuilding this version" >&2
  exit 1
fi

notary_upload="$staging_root/notary-upload.zip"
ditto -c -k --sequesterRsrc --keepParent "$staged_app" "$notary_upload"

notary_arguments=(--keychain-profile "$notary_profile" --wait --timeout 30m)
if [[ -n "$notary_keychain" ]]; then
  notary_arguments+=(--keychain "$notary_keychain")
fi
xcrun notarytool submit "$notary_upload" "${notary_arguments[@]}"

xcrun stapler staple "$staged_app"
FLOWSPACE_REQUIRE_SIGNING=1 FLOWSPACE_REQUIRE_NOTARIZATION=1 FLOWSPACE_REQUIRE_UPDATES=1 \
  "$repo_root/scripts/verify-macos-app.sh" "$staged_app"

ditto -c -k --sequesterRsrc --keepParent "$staged_app" "$artifact_path"
(
  cd "$output_dir"
  shasum -a 256 "$artifact_name" > "$(basename "$checksum_path")"
)

echo "Created notarized release: $artifact_path"
echo "Checksum: $checksum_path"
