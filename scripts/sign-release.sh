#!/bin/bash
# Signs dist/SHA256SUMS with the release key from Bitwarden, writing
# dist/SHA256SUMS.minisig. The launcher installs an update only when this
# signature checks out against the public key built into it.
#
# Run it yourself in a normal terminal, not inside a Claude Code session: it
# unlocks Bitwarden. The secret key goes from Bitwarden to the signer through a
# pipe and is never written to disk or shown.
#
# First build the release files (scripts/appimage.sh, scripts/steamrt.sh) and
# write their checksums:  (cd dist && sha256sum psclassic-launcher-*.AppImage psclassic-launcher-*.tar.gz > SHA256SUMS)
set -euo pipefail
cd "$(dirname "$0")/.."

item_name="psclassic-launcher release signing key"
version=$(sed -n 's/^var Version = "\(.*\)"$/\1/p' internal/cli/cli.go)
[ -n "$version" ] || { echo "could not read the version from internal/cli/cli.go" >&2; exit 1; }
[ -f dist/SHA256SUMS ] || { echo "dist/SHA256SUMS is missing" >&2; exit 1; }
for f in "psclassic-launcher-$version-x86_64.AppImage" "psclassic-launcher-$version-steamrt4-x86_64.tar.gz"; do
	grep -q "  $f\$" dist/SHA256SUMS || { echo "dist/SHA256SUMS does not list $f" >&2; exit 1; }
done
(cd dist && sha256sum -c --quiet SHA256SUMS)
for c in bw jq go; do
	command -v "$c" >/dev/null || { echo "$c is needed" >&2; exit 1; }
done

tmp=$(mktemp -d)
locked_here=0
cleanup() {
	rm -rf "$tmp"
	if [ "$locked_here" = 1 ]; then bw lock >/dev/null || true; fi
}
trap cleanup EXIT
go build -o "$tmp/release-sign" ./tools/release-sign

echo "Signing dist/SHA256SUMS for psclassic-launcher $version:"
sed 's/^/  /' dist/SHA256SUMS
if [ -z "${BW_SESSION:-}" ]; then
	BW_SESSION="$(bw unlock --raw)"
	export BW_SESSION
	locked_here=1
fi
bw sync >/dev/null
id=$(bw list items --search "$item_name" |
	jq -r --arg n "$item_name" '[.[] | select(.name == $n)] | if length == 1 then .[0].id else empty end')
[ -n "$id" ] || { echo "Expected exactly one Bitwarden item named \"$item_name\"." >&2; exit 1; }
bw get notes "$id" | "$tmp/release-sign" sign -comment "psclassic-launcher $version" dist/SHA256SUMS
