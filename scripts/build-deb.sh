#!/usr/bin/env bash
# Build a .deb for one architecture using only Go and dpkg-deb.
# Usage: scripts/build-deb.sh <version> <deb-arch: amd64|arm64|armhf> [outdir]
set -euo pipefail
VERSION="${1:?version}"; DEBARCH="${2:?arch}"; OUT="${3:-dist}"
VERSION="${VERSION#v}"
case "$DEBARCH" in
  amd64) GOARCH=amd64 ;;
  arm64) GOARCH=arm64 ;;
  armhf) GOARCH=arm; export GOARM=7 ;;
  *) echo "unsupported arch $DEBARCH" >&2; exit 2 ;;
esac
ROOT="$(mktemp -d)"; trap 'rm -rf "$ROOT"' EXIT
PKG="$ROOT/sds200-webapp_${VERSION}_${DEBARCH}"
install -d "$PKG/DEBIAN" "$PKG/usr/bin" "$PKG/lib/systemd/system" "$PKG/etc/default" "$PKG/usr/share/doc/sds200-webapp"
CGO_ENABLED=0 GOOS=linux GOARCH=$GOARCH go build -trimpath -ldflags="-s -w -X main.appVersion=${VERSION}" -o "$PKG/usr/bin/sds200-webapp" .
install -m 0644 packaging/linux/sds200-webapp.service "$PKG/lib/systemd/system/"
install -m 0644 packaging/linux/sds200-webapp.default "$PKG/etc/default/sds200-webapp"
install -m 0644 README.md FUNCTION_MAP.txt packaging/linux/Caddyfile.example "$PKG/usr/share/doc/sds200-webapp/"
SIZE=$(du -sk "$PKG" | cut -f1)
cat > "$PKG/DEBIAN/control" <<CTRL
Package: sds200-webapp
Version: ${VERSION}
Section: hamradio
Priority: optional
Architecture: ${DEBARCH}
Installed-Size: ${SIZE}
Maintainer: Daren Loxley 2E0LXY <2e0lxy.daren@gmail.com>
Homepage: https://github.com/2E0LXY/SDS200
Description: Web remote control for Uniden SDS200/SDS200E scanners
 Single static binary serving a browser front panel, live RTSP audio,
 waterfall, favourites quick keys and diagnostics over the scanner LAN port.
CTRL
echo "/etc/default/sds200-webapp" > "$PKG/DEBIAN/conffiles"
cat > "$PKG/DEBIAN/postinst" <<'POST'
#!/bin/sh
set -e
if [ "$1" = "configure" ] && [ -d /run/systemd/system ]; then
  systemctl daemon-reload
  systemctl enable sds200-webapp.service >/dev/null 2>&1 || true
  systemctl restart sds200-webapp.service || true
fi
POST
cat > "$PKG/DEBIAN/prerm" <<'PRE'
#!/bin/sh
set -e
if [ -d /run/systemd/system ]; then
  systemctl stop sds200-webapp.service >/dev/null 2>&1 || true
  [ "$1" = "remove" ] && systemctl disable sds200-webapp.service >/dev/null 2>&1 || true
fi
PRE
cat > "$PKG/DEBIAN/postrm" <<'POSTRM'
#!/bin/sh
set -e
[ -d /run/systemd/system ] && systemctl daemon-reload || true
POSTRM
chmod 0755 "$PKG/DEBIAN/postinst" "$PKG/DEBIAN/prerm" "$PKG/DEBIAN/postrm"
mkdir -p "$OUT"
dpkg-deb --root-owner-group --build "$PKG" "$OUT/sds200-webapp_${VERSION}_${DEBARCH}.deb"
