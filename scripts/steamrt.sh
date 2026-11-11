#!/bin/sh
# Builds dist/psclassic-launcher-<version>-steamrt4-x86_64.tar.gz: both programs,
# built in Valve's Steam Runtime 4 SDK (scripts/Containerfile.steamrt). They run
# inside the Steam Linux Runtime 4.0 container, and directly on a host whose glibc
# has every symbol the launcher uses (2.38 or newer for 0.2.0; check with
# `objdump -T psclassic-launcher | grep -o 'GLIBC_[0-9.]*' | sort -uV | tail -1`).
#
# The archive holds psclassic-launcher, psclassic-updater, the menu entry, the
# icon, README.md and LICENSE in one folder. Its files get the last commit's time,
# so building the same commit again gives the same archive.
set -eu
cd "$(dirname "$0")/.."

id=io.github.ltsjoe.psclassic-launcher
version=$(sed -n 's/^var Version = "\(.*\)"$/\1/p' internal/cli/cli.go)
[ -n "$version" ] || { echo "could not read the version from internal/cli/cli.go" >&2; exit 1; }
engine=$(command -v podman || command -v docker || true)
[ -n "$engine" ] || { echo "podman or docker is needed" >&2; exit 1; }
image=localhost/psclassic-launcher-steamrt
if ! "$engine" image inspect "$image" >/dev/null 2>&1; then
	"$engine" build -t "$image" -f scripts/Containerfile.steamrt scripts
fi
cache="$HOME/.cache/psclassic-launcher-build"
modcache=$(go env GOMODCACHE 2>/dev/null || echo "$HOME/go/pkg/mod")
mkdir -p "$modcache" "$cache/steamrt4" dist
name=psclassic-launcher-$version-steamrt4-x86_64
mtime=$(git log -1 --format=%ct 2>/dev/null || date +%s)

"$engine" run --rm \
	-v "$PWD:/src:Z" -w /src \
	-v "$modcache:/go/pkg/mod:z" \
	-v "$cache/steamrt4:/root/.cache:Z" \
	-e GOTOOLCHAIN=local \
	"$image" sh -ec "
		. /etc/os-release
		echo \"\$PRETTY_NAME \${BUILD_ID:-}, \$(go version)\"
		tmp=\$(mktemp -d)
		out=\$tmp/$name
		mkdir -p \$out
		go build -buildvcs=false -trimpath -ldflags='-s -w' -o \$out/psclassic-launcher ./cmd/psclassic-launcher
		CGO_ENABLED=0 go build -buildvcs=false -trimpath -ldflags='-s -w' -o \$out/psclassic-updater ./cmd/psclassic-updater
		cp packaging/$id.desktop \$out/
		cp cmd/psclassic-launcher/assets/icon.svg \$out/$id.svg
		cp README.md LICENSE \$out/
		chmod 644 \$out/LICENSE
		tar -C \$tmp --sort=name --owner=0 --group=0 --numeric-owner --mtime=@$mtime -cf - $name \
			| gzip -n9 >dist/$name.tar.gz
	"
ls -l "dist/$name.tar.gz"
