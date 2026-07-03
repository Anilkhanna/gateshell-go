#!/usr/bin/env sh
# install.sh — one-line installer for gateshell-agent.
#
#   curl -fsSL https://gateshell.com/dl/install-agent.sh | sh -s -- --token <PAIRING_TOKEN>
#
# What this does:
#   1. Detects OS/arch and downloads the matching release binary.
#   2. Installs it to /usr/local/bin/gateshell-agent.
#   3. Writes a config file + env file containing the pairing token.
#   4. Installs + enables + (re)starts a systemd unit.
#
# Idempotent: safe to re-run (e.g. to upgrade, or to rotate the token with
# --token). Re-running replaces the binary and config, then restarts the
# service.
#
# NOTE: this script targets systemd-based Linux distros, matching the
# agent's primary deployment target (see deploy/gateshell-agent.service).
# macOS / non-systemd Linux users should build from source and run the
# binary directly (`gateshell-agent serve`) or manage it with their own
# supervisor (launchd, runit, etc.) -- this installer intentionally does
# not attempt to cover those cases.

set -eu

# ---- defaults (overridable via flags) --------------------------------
INSTALL_DIR="/usr/local/bin"
CONFIG_DIR="/etc/gateshell-agent"
DATA_DIR="/var/lib/gateshell-agent"
SERVICE_USER="gateshell-agent"
REPO="Anilkhanna/gateshell-go" # binaries published via GitHub Releases
TOKEN=""
NTFY_TOPIC=""
LISTEN_ADDR=":8443"
VERSION="latest" # TODO: pin/resolve a real version once releases are published

# ---- arg parsing -------------------------------------------------------
while [ $# -gt 0 ]; do
	case "$1" in
	--token)
		TOKEN="$2"
		shift 2
		;;
	--ntfy-topic)
		NTFY_TOPIC="$2"
		shift 2
		;;
	--listen-addr)
		LISTEN_ADDR="$2"
		shift 2
		;;
	--version)
		VERSION="$2"
		shift 2
		;;
	*)
		echo "unknown argument: $1" >&2
		exit 1
		;;
	esac
done

if [ -z "$TOKEN" ]; then
	echo "error: --token <PAIRING_TOKEN> is required (generate one with 'gateshell-agent pair' on an existing install, or let the app generate one during setup)" >&2
	exit 1
fi

# ---- OS/arch detection ---------------------------------------------------
OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
case "$OS" in
linux) ;;
darwin) ;;
*)
	echo "error: unsupported OS: $OS (gateshell-agent targets Linux servers; macOS is supported for local dev only)" >&2
	exit 1
	;;
esac

ARCH_RAW="$(uname -m)"
case "$ARCH_RAW" in
x86_64 | amd64) ARCH="amd64" ;;
aarch64 | arm64) ARCH="arm64" ;;
*)
	echo "error: unsupported architecture: $ARCH_RAW" >&2
	exit 1
	;;
esac

if [ "$VERSION" = "latest" ]; then
	DOWNLOAD_BASE_URL="https://github.com/${REPO}/releases/latest/download"
else
	DOWNLOAD_BASE_URL="https://github.com/${REPO}/releases/download/${VERSION}"
fi
BINARY_URL="${DOWNLOAD_BASE_URL}/gateshell-agent-${OS}-${ARCH}"
echo "==> Installing gateshell-agent ${VERSION} for ${OS}/${ARCH}"
echo "    (source: ${BINARY_URL})"

# ---- privilege check ------------------------------------------------------
if [ "$(id -u)" -ne 0 ]; then
	echo "error: this installer must be run as root (it installs a systemd unit and a service user)" >&2
	exit 1
fi

# ---- service user (idempotent: only create if missing) --------------------
if ! id "$SERVICE_USER" >/dev/null 2>&1; then
	echo "==> Creating system user '$SERVICE_USER'"
	useradd --system --no-create-home --shell /usr/sbin/nologin "$SERVICE_USER"
fi

# ---- download binary --------------------------------------------------
echo "==> Downloading binary"
TMP_BINARY="$(mktemp)"
# TODO: verify a checksum/signature once release infra publishes one
# (e.g. a companion .sha256 file next to the binary at BINARY_URL).
curl -fsSL "$BINARY_URL" -o "$TMP_BINARY"
chmod +x "$TMP_BINARY"
mv "$TMP_BINARY" "${INSTALL_DIR}/gateshell-agent"
echo "==> Installed ${INSTALL_DIR}/gateshell-agent"

# ---- directories --------------------------------------------------------
mkdir -p "$CONFIG_DIR" "$DATA_DIR"
chown "$SERVICE_USER":"$SERVICE_USER" "$DATA_DIR"

# ---- config file (idempotent: overwritten on every run) --------------
CONFIG_FILE="${CONFIG_DIR}/config.json"
cat >"$CONFIG_FILE" <<EOF
{
  "listen_addr": "${LISTEN_ADDR}",
  "db_path": "${DATA_DIR}/gateshell-agent.db",
  "poll_interval": "5m",
  "ntfy_topic": "${NTFY_TOPIC}",
  "server_name": "$(hostname)"
}
EOF
echo "==> Wrote ${CONFIG_FILE}"

# ---- env file (secrets live here, not in the JSON config or unit file) ----
ENV_FILE="${CONFIG_DIR}/gateshell-agent.env"
cat >"$ENV_FILE" <<EOF
GATESHELL_AGENT_PAIRING_TOKEN=${TOKEN}
EOF
chmod 600 "$ENV_FILE"
chown "$SERVICE_USER":"$SERVICE_USER" "$ENV_FILE"
echo "==> Wrote ${ENV_FILE} (mode 600)"

# ---- systemd unit (idempotent: overwritten on every run) ----------------
SERVICE_FILE="/etc/systemd/system/gateshell-agent.service"
if [ -f "$(dirname "$0")/deploy/gateshell-agent.service" ]; then
	cp "$(dirname "$0")/deploy/gateshell-agent.service" "$SERVICE_FILE"
else
	# Fallback for curl|sh one-liner installs where deploy/ isn't present
	# locally: fetch the unit template from the same release channel.
	curl -fsSL "${DOWNLOAD_BASE_URL}/gateshell-agent.service" -o "$SERVICE_FILE"
fi
echo "==> Installed ${SERVICE_FILE}"

systemctl daemon-reload
systemctl enable gateshell-agent
systemctl restart gateshell-agent

echo "==> gateshell-agent installed and running."
echo "    Check status with: systemctl status gateshell-agent"
echo "    Tail logs with:    journalctl -u gateshell-agent -f"
