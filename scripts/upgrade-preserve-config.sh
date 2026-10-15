#!/usr/bin/env bash
# Grid panel update. Preserve the existing config, firewall and service units.
set -Eeuo pipefail
umask 077
PKG=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
CFG=/etc/5gpn-next/config.json
BIN=/usr/local/bin/5gpnd
SERVICE=5gpn-next.service
[ "$#" -eq 0 ] || { echo 'Usage: sudo bash update.sh (no options; configuration is preserved)' >&2; exit 2; }
[ "$EUID" -eq 0 ] || { echo 'Run as root.' >&2; exit 1; }
[ "$(uname -s)" = Linux ] && [ "$(uname -m)" = x86_64 ] || { echo 'This package requires Linux x86_64.' >&2; exit 1; }
for tool in python3 systemctl sha256sum cp mv cmp chmod chown mktemp; do command -v "$tool" >/dev/null || { echo "Missing: $tool" >&2; exit 1; }; done
[ -f "$CFG" ] && [ ! -L "$CFG" ] && [ -f "$BIN" ] && [ ! -L "$BIN" ] || { echo 'Expected standard installation paths with regular config and binary files.' >&2; exit 1; }
(cd "$PKG" && sha256sum -c SHA256SUMS)
TARGET=$(cat "$PKG/VERSION")
[ "$("$PKG/5gpnd-linux-amd64" version)" = "5gpn-next $TARGET" ] || { echo 'Package version mismatch.' >&2; exit 1; }
CURRENT=$("$BIN" version)
case "$CURRENT" in
  '5gpn-next v0.13.27'|'5gpn-next v0.13.27-custom.20261015.1'|"5gpn-next $TARGET") ;;
  *) echo "This package supports v0.13.27 and the reviewed custom build; found: $CURRENT. No changes made." >&2; exit 1 ;;
esac
EXEC=$(systemctl show "$SERVICE" --property=ExecStart --value)
[[ "$EXEC" == *"$BIN"* && "$EXEC" == *"$CFG"* ]] || { echo 'Nonstandard service command. No changes made.' >&2; exit 1; }
systemctl is-active --quiet "$SERVICE" || { echo 'Service is not running. Diagnose it before upgrading.' >&2; exit 1; }
# Refuse legacy configurations whose upstream startup migration could rewrite data.
python3 "$PKG/check-config.py" "$CFG"
"$PKG/5gpnd-linux-amd64" check -c "$CFG"
BACKUP=$(mktemp -d /root/5gpn-upgrade-backup-XXXXXXXX)
cp -a "$BIN" "$BACKUP/5gpnd"
cp -a /etc/5gpn-next "$BACKUP/etc-5gpn-next"
printf '%s\n' "$CURRENT" > "$BACKUP/previous-version.txt"
STOPPED=0
REPLACED=0
restore_binary() {
  cp -p "$BACKUP/5gpnd" "${BIN}.rollback-$$" && mv -f "${BIN}.rollback-$$" "$BIN"
}
on_failure() {
  local result=$1
  trap - ERR INT TERM
  set +e
  echo "Upgrade failed; backup: $BACKUP" >&2
  if [ "$REPLACED" -eq 1 ]; then restore_binary || echo 'BINARY RESTORE FAILED' >&2; fi
  if [ "$STOPPED" -eq 1 ]; then
    systemctl restart "$SERVICE"
    systemctl is-active --quiet "$SERVICE" || echo 'Old service did not restart; inspect journalctl.' >&2
  fi
  exit "$result"
}
trap 'on_failure $?' ERR
trap 'on_failure 130' INT
trap 'on_failure 143' TERM
# Stop only after validating the new binary and taking a config/binary backup.
STOPPED=1
systemctl stop "$SERVICE"
# Refresh config snapshot and capture data while the service is stopped.
cp -a /etc/5gpn-next/config.json "$BACKUP/etc-5gpn-next/config.json"
for state in /var/lib/5gpn-next /var/lib/mihomo-5gpn; do
  if [ -d "$state" ]; then cp -a "$state" "$BACKUP/$(basename "$state")"; fi
done
cp "$PKG/5gpnd-linux-amd64" "${BIN}.upgrade-$$"
chmod --reference="$BIN" "${BIN}.upgrade-$$"
chown --reference="$BIN" "${BIN}.upgrade-$$"
REPLACED=1
mv -f "${BIN}.upgrade-$$" "$BIN"
# This update never writes the configuration file.
cmp "$CFG" "$BACKUP/etc-5gpn-next/config.json"
systemctl start "$SERVICE"
EXPECTED=$(sha256sum "$PKG/5gpnd-linux-amd64"); EXPECTED=${EXPECTED%% *}
for attempt in 1 2 3 4; do
  sleep 3
  systemctl is-active --quiet "$SERVICE"
  MAINPID=$(systemctl show "$SERVICE" --property=MainPID --value)
  [ "$MAINPID" -gt 0 ]
  RUNNING=$(sha256sum "/proc/$MAINPID/exe"); RUNNING=${RUNNING%% *}
  [ "$RUNNING" = "$EXPECTED" ]
done
trap - ERR INT TERM
printf '\nUpgrade complete: %s\nBackup: %s\n' "$TARGET" "$BACKUP"
echo 'Configuration file was not modified. Browser and Mini App use the original internal-network access policy.'
echo 'Existing data directories, certificates, firewall and service units were not replaced.'
echo 'Use the custom update package for future upgrades; this build refuses upstream release downloads.'
