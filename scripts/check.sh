#!/bin/sh
# The checks every change passes: gofmt, go vet, go test and govulncheck, over
# all packages. They run in the build container (scripts/build.sh makes it),
# because the GUI package needs cgo and the X11/Wayland/OpenGL headers.
# PSC_RENDER_DIR=dir also saves pictures of the windows there.
set -eu
cd "$(dirname "$0")/.."
bad=$(gofmt -l .)
if [ -n "$bad" ]; then
	echo "gofmt needed:" >&2
	echo "$bad" >&2
	exit 1
fi
engine=$(command -v podman || command -v docker)
image=localhost/psclassic-launcher-build
if ! "$engine" image inspect "$image" >/dev/null 2>&1; then
	"$engine" build -t "$image" -f scripts/Containerfile scripts
fi
render=""
if [ -n "${PSC_RENDER_DIR:-}" ]; then
	mkdir -p "$PSC_RENDER_DIR"
	render="-v $(cd "$PSC_RENDER_DIR" && pwd):/render:Z -e PSC_RENDER_DIR=/render"
fi
modcache=$(go env GOMODCACHE 2>/dev/null || echo "$HOME/go/pkg/mod")
mkdir -p "$HOME/.cache/psclassic-launcher-build"
# shellcheck disable=SC2086
"$engine" run --rm \
	-v "$PWD:/src:Z" -w /src \
	-v "$modcache:/go/pkg/mod:z" \
	-v "$HOME/.cache/psclassic-launcher-build:/root/.cache:Z" \
	-e GOTOOLCHAIN=local $render \
	"$image" sh -ec '
		go vet ./...
		go test -count=1 ./...
		go run golang.org/x/vuln/cmd/govulncheck@latest ./...
	'
