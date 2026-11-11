#!/bin/sh
# Installs the programs built by scripts/build.sh for this user:
#   ~/.local/bin/psclassic-launcher and psclassic-updater
#   a menu entry and icon under ~/.local/share
# PREFIX=/some/dir installs there instead of ~/.local.
set -eu
cd "$(dirname "$0")/.."
prefix=${PREFIX:-$HOME/.local}
id=io.github.ltsjoe.psclassic-launcher
for f in dist/psclassic-launcher dist/psclassic-updater; do
	[ -x "$f" ] || { echo "$f is missing: run scripts/build.sh first" >&2; exit 1; }
done
install -Dm755 dist/psclassic-launcher "$prefix/bin/psclassic-launcher"
install -Dm755 dist/psclassic-updater "$prefix/bin/psclassic-updater"
install -Dm644 cmd/psclassic-launcher/assets/icon.svg "$prefix/share/icons/hicolor/scalable/apps/$id.svg"
sed "s|^Exec=psclassic-launcher|Exec=$prefix/bin/psclassic-launcher|" packaging/$id.desktop >"$prefix/share/applications/$id.desktop.tmp"
chmod 644 "$prefix/share/applications/$id.desktop.tmp"
mv "$prefix/share/applications/$id.desktop.tmp" "$prefix/share/applications/$id.desktop"
command -v update-desktop-database >/dev/null && update-desktop-database -q "$prefix/share/applications" || true
echo "Installed to $prefix (menu: Planetside Classic Launcher)."
