#!/usr/bin/env bash
# Builds build/bin/specwright.app and signs it with a stable code-signing
# identity, so macOS keeps the permissions you granted (Documents folder,
# keychain) across rebuilds. `wails build` alone signs ad-hoc: every build
# gets a new code hash, and macOS asks again as if it were a new app.
#
# The identity, first match wins:
#   1. $SPECWRIGHT_SIGN_IDENTITY (a name or SHA-1 from
#      `security find-identity -p codesigning -v`)
#   2. an "Apple Development" certificate (free: Xcode › Settings › Accounts)
#   3. a certificate named "SpecWright Local" (self-signed, see the README)
# With none, the app stays ad-hoc signed and the script says so.
#
#   scripts/build-mac.sh            build and sign
#   scripts/build-mac.sh --sign     sign the existing build only
set -euo pipefail
cd "$(dirname "$0")/.."
export PATH="$HOME/go/bin:$PATH"
APP=build/bin/specwright.app

if [[ "${1:-}" != "--sign" ]]; then
  wails build -clean
  touch frontend/dist/.gitkeep # the build empties frontend/dist; the placeholder is tracked
fi
[[ -d "$APP" ]] || { echo "no $APP: build first" >&2; exit 1; }

valid=$(security find-identity -p codesigning -v 2>/dev/null || true)
# all of them: a self-signed certificate signs fine while macOS does not
# trust it as a root (it is listed, but not as "valid")
listed=$(security find-identity -p codesigning 2>/dev/null || true)
pick() { grep -F "\"$2" <<<"$1" | head -1 | sed -E 's/.*"(.*)".*/\1/' || true; }
id="${SPECWRIGHT_SIGN_IDENTITY:-}"
[[ -n "$id" ]] || id=$(pick "$valid" "Apple Development")
[[ -n "$id" ]] || id=$(pick "$listed" "SpecWright Local")

if [[ -z "$id" ]]; then
  cat >&2 <<'EOF'

warning: no code-signing certificate found, so the app is ad-hoc signed.
macOS will ask again for the Documents folder after each rebuild.
To fix it once, create a certificate (README › "Signing the macOS app"),
then run: scripts/build-mac.sh --sign
EOF
  exit 0
fi

codesign --force --deep --sign "$id" "$APP"
codesign --verify --deep --strict "$APP"
echo "signed $APP with: $id"
codesign -dr - "$APP" 2>&1 | sed -n 's/^designated => /macOS remembers permissions for: /p'
