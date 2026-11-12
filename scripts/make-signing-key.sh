#!/bin/bash
# One-off: makes the release signing key.
#
# Run it yourself in a normal terminal, not inside a Claude Code session: it unlocks
# Bitwarden, and nothing secret may end up in a session log. The secret key goes
# straight from the generator into a new Bitwarden secure note, through a pipe,
# without touching the disk or the screen. Only the public key is written to a file,
# internal/selfupdate/release.pub, which gets committed and built into the launcher.
set -euo pipefail
cd "$(dirname "$0")/.."

item_name="psclassic-launcher release signing key"
for c in bw jq go; do
	command -v "$c" >/dev/null || { echo "$c is needed" >&2; exit 1; }
done
if [ "$(bw status | jq -r .status)" = unauthenticated ]; then
	echo "Log in to the Bitwarden CLI first: bw login" >&2
	exit 1
fi

tmp=$(mktemp -d)
locked_here=0
cleanup() {
	rm -rf "$tmp"
	if [ "$locked_here" = 1 ]; then bw lock >/dev/null || true; fi
}
trap cleanup EXIT

if [ -z "${BW_SESSION:-}" ]; then
	BW_SESSION="$(bw unlock --raw)"
	export BW_SESSION
	locked_here=1
fi
bw sync >/dev/null

count=$(bw list items --search "$item_name" | jq --arg n "$item_name" '[.[] | select(.name == $n)] | length')
if [ "$count" != 0 ]; then
	echo "Bitwarden already has an item named \"$item_name\". Nothing was changed." >&2
	exit 1
fi

go build -o "$tmp/release-sign" ./tools/release-sign
template=$(bw get template item)
# The secret key exists only in this pipe: generator -> jq -> bw encode -> bw create.
id=$("$tmp/release-sign" genkey -pub internal/selfupdate/release.pub |
	jq -R --arg name "$item_name" --argjson tmpl "$template" '
		. as $secret | $tmpl
		| .type = 2 | .secureNote = {type: 0} | .name = $name | .notes = $secret
		| .login = null | .card = null | .identity = null | .fields = []' |
	bw encode | bw create item | jq -r .id)
if [ -z "$id" ] || [ "$id" = null ]; then
	echo "Saving the key in Bitwarden failed." >&2
	exit 1
fi

# Build again so the new public key is built in, then check the stored secret matches it.
go build -o "$tmp/release-sign" ./tools/release-sign
bw get notes "$id" | "$tmp/release-sign" check

echo
echo "Saved in Bitwarden as \"$item_name\"."
echo "The public key, in internal/selfupdate/release.pub:"
cat internal/selfupdate/release.pub
