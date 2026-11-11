#!/bin/sh
# Builds dist/psclassic-launcher-<version>-x86_64.AppImage.
#
# The program is built in an Ubuntu 22.04 container (scripts/Containerfile.appimage)
# so it needs only glibc 2.35. It links libGL and libwayland-client, which every
# desktop has, so nothing else is bundled. The AppImage uses the static type2
# runtime, which needs no libfuse2 on the host.
#
# appimagetool and the runtime are pinned by version and SHA-256, downloaded
# once into ~/.cache/psclassic-launcher-build/appimage-tools.
set -eu
cd "$(dirname "$0")/.."

TOOL_VERSION=1.9.1
TOOL_SHA256=ed4ce84f0d9caff66f50bcca6ff6f35aae54ce8135408b3fa33abfc3cb384eb0
RUNTIME_VERSION=20251108
RUNTIME_SHA256=2fca8b443c92510f1483a883f60061ad09b46b978b2631c807cd873a47ec260d

id=io.github.ltsjoe.psclassic-launcher
version=$(sed -n 's/^var Version = "\(.*\)"$/\1/p' internal/cli/cli.go)
[ -n "$version" ] || { echo "could not read the version from internal/cli/cli.go" >&2; exit 1; }
cache="$HOME/.cache/psclassic-launcher-build"
tools="$cache/appimage-tools"
mkdir -p "$tools" "$cache/jammy" dist

fetch() { # url file sha256
	if [ ! -f "$tools/$2" ]; then
		curl -fsSL -o "$tools/$2.part" "$1"
		mv "$tools/$2.part" "$tools/$2"
	fi
	echo "$3  $tools/$2" | sha256sum -c --quiet - || { echo "checksum mismatch: $tools/$2" >&2; exit 1; }
}
fetch "https://github.com/AppImage/appimagetool/releases/download/$TOOL_VERSION/appimagetool-x86_64.AppImage" \
	"appimagetool-$TOOL_VERSION-x86_64.AppImage" "$TOOL_SHA256"
fetch "https://github.com/AppImage/type2-runtime/releases/download/$RUNTIME_VERSION/runtime-x86_64" \
	"runtime-$RUNTIME_VERSION-x86_64" "$RUNTIME_SHA256"
chmod +x "$tools/appimagetool-$TOOL_VERSION-x86_64.AppImage"

engine=$(command -v podman || command -v docker || true)
[ -n "$engine" ] || { echo "podman or docker is needed" >&2; exit 1; }
image=localhost/psclassic-launcher-appimage
if ! "$engine" image inspect "$image" >/dev/null 2>&1; then
	"$engine" build -t "$image" -f scripts/Containerfile.appimage scripts
fi
modcache=$(go env GOMODCACHE 2>/dev/null || echo "$HOME/go/pkg/mod")
out="dist/psclassic-launcher-$version-x86_64.AppImage"

"$engine" run --rm \
	-v "$PWD:/src:Z" -w /src \
	-v "$modcache:/go/pkg/mod:z" \
	-v "$cache/jammy:/root/.cache:Z" \
	-v "$tools:/tools:ro,Z" \
	-e APPIMAGE_EXTRACT_AND_RUN=1 -e ARCH=x86_64 -e VERSION="$version" \
	"$image" sh -ec "
		go version
		app=\$(mktemp -d)/AppDir
		mkdir -p \$app/usr/bin \$app/usr/share/applications \$app/usr/share/icons/hicolor/scalable/apps
		go build -buildvcs=false -trimpath -ldflags='-s -w' -o \$app/usr/bin/psclassic-launcher ./cmd/psclassic-launcher
		cp packaging/$id.desktop \$app/usr/share/applications/
		cp cmd/psclassic-launcher/assets/icon.svg \$app/usr/share/icons/hicolor/scalable/apps/$id.svg
		cp packaging/$id.desktop \$app/
		cp cmd/psclassic-launcher/assets/icon.svg \$app/$id.svg
		ln -s $id.svg \$app/.DirIcon
		ln -s usr/bin/psclassic-launcher \$app/AppRun
		desktop-file-validate \$app/$id.desktop
		/tools/appimagetool-$TOOL_VERSION-x86_64.AppImage --no-appstream \
			--runtime-file /tools/runtime-$RUNTIME_VERSION-x86_64 \$app $out
	"
ls -l "$out"
