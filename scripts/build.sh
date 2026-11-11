#!/bin/sh
# Builds both programs into dist/.
#
#   psclassic-updater   static, no cgo: built with the Go on this machine.
#   psclassic-launcher  the GUI needs cgo and the X11/OpenGL headers (Fyne), so
#                       it is built in a Debian golang container with podman or
#                       docker. Set NATIVE=1 to build it here instead, when the
#                       headers are installed (libgl1-mesa-dev xorg-dev, or the
#                       distro's equivalent).
set -eu
cd "$(dirname "$0")/.."
GO_IMAGE=${GO_IMAGE:-docker.io/library/golang:1.27-trixie}  # when the build image is first made
mkdir -p dist

echo ">> psclassic-updater"
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o dist/psclassic-updater ./cmd/psclassic-updater

echo ">> psclassic-launcher"
if [ "${NATIVE:-0}" = 1 ]; then
	go build -trimpath -ldflags="-s -w" -o dist/psclassic-launcher ./cmd/psclassic-launcher
	exit 0
fi
engine=$(command -v podman || command -v docker || true)
if [ -z "$engine" ]; then
	echo "podman or docker is needed to build the GUI (or set NATIVE=1)" >&2
	exit 1
fi
modcache=$(go env GOMODCACHE 2>/dev/null || echo "$HOME/go/pkg/mod")
mkdir -p "$modcache" "$HOME/.cache/psclassic-launcher-build"
image=localhost/psclassic-launcher-build
if ! "$engine" image inspect "$image" >/dev/null 2>&1; then
	"$engine" build --build-arg GO_IMAGE="$GO_IMAGE" -t "$image" -f scripts/Containerfile scripts
fi
"$engine" run --rm \
	-v "$PWD:/src:Z" -w /src \
	-v "$modcache:/go/pkg/mod:z" \
	-v "$HOME/.cache/psclassic-launcher-build:/root/.cache:Z" \
	-e GOFLAGS=-mod=mod -e GOTOOLCHAIN=local \
	"$image" go build -buildvcs=false -trimpath -ldflags="-s -w" -o dist/psclassic-launcher ./cmd/psclassic-launcher
ls -l dist/
