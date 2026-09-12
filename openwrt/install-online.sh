#!/bin/sh
set -eu

REPOSITORY=onprs/GiWifi-Auto
RELEASE_BASE="https://github.com/$REPOSITORY/releases/latest/download"
TEMPORARY_DIRECTORY=

cleanup() {
	status=$?
	trap - 0 1 2 15
	if [ -n "$TEMPORARY_DIRECTORY" ]; then
		rm -rf "$TEMPORARY_DIRECTORY"
	fi
	exit "$status"
}
trap cleanup 0 1 2 15

if [ "$(id -u)" -ne 0 ]; then
	echo "安装需要 root 权限" >&2
	exit 1
fi

if ! command -v wget >/dev/null 2>&1 && ! command -v curl >/dev/null 2>&1; then
	echo "安装需要 wget 或 curl" >&2
	exit 1
fi
for required_command in mktemp sha256sum; do
	if ! command -v "$required_command" >/dev/null 2>&1; then
		echo "缺少安装命令: $required_command" >&2
		exit 1
	fi
done

download() {
	if command -v wget >/dev/null 2>&1; then
		if wget -T 30 -t 3 -qO "$2" "$1" </dev/null; then
			return 0
		fi
	fi
	if command -v curl >/dev/null 2>&1; then
		curl --http1.1 --connect-timeout 30 --max-time 600 --retry 2 -fsSL "$1" -o "$2" </dev/null
		return 0
	fi
	return 1
}

MACHINE=$(uname -m)
case "$MACHINE" in
	x86_64 | amd64)
		TARGET_ARCH=amd64
		;;
	aarch64 | arm64)
		TARGET_ARCH=arm64
		;;
	mipsel | mipsle)
		TARGET_ARCH=mipsle
		;;
	mips)
		if command -v opkg >/dev/null 2>&1 && opkg print-architecture | grep -q 'mipsel'; then
			TARGET_ARCH=mipsle
		else
			echo "无法确认 MIPS 设备端序" >&2
			exit 1
		fi
		;;
	*)
		echo "不支持的设备架构: $MACHINE" >&2
		exit 1
		;;
esac

TEMPORARY_DIRECTORY=$(mktemp -d /tmp/giwifi-auto-install.XXXXXX)
BINARY_NAME="giwifi-auto-linux-$TARGET_ARCH"
BINARY_PATH="$TEMPORARY_DIRECTORY/$BINARY_NAME"
CHECKSUM_PATH="$TEMPORARY_DIRECTORY/SHA256SUMS"
INIT_PATH="$TEMPORARY_DIRECTORY/giwifi-auto.init"
CONFIG_PATH="$TEMPORARY_DIRECTORY/giwifi-auto.config"

printf '下载 GiWifi-Auto 最新版本 (%s)...\n' "$TARGET_ARCH"
download "$RELEASE_BASE/$BINARY_NAME" "$BINARY_PATH"
download "$RELEASE_BASE/SHA256SUMS" "$CHECKSUM_PATH"

CHECKSUM_LINE=$(grep -F "/$BINARY_NAME" "$CHECKSUM_PATH" | head -n 1)
EXPECTED_CHECKSUM=${CHECKSUM_LINE%% *}
ACTUAL_CHECKSUM=$(sha256sum "$BINARY_PATH")
ACTUAL_CHECKSUM=${ACTUAL_CHECKSUM%% *}
if [ -z "$EXPECTED_CHECKSUM" ] || [ "$EXPECTED_CHECKSUM" != "$ACTUAL_CHECKSUM" ]; then
	echo "发布文件校验失败" >&2
	exit 1
fi

cat >"$INIT_PATH" <<'INIT_EOF'
#!/bin/sh /etc/rc.common

START=95
STOP=01
USE_PROCD=1

PROG=/usr/bin/giwifi-auto
CONFIG=/etc/config/giwifi-auto
SERVICE=giwifi-auto

start_service() {
	[ -x "$PROG" ] || return 1
	[ -r "$CONFIG" ] || return 1

	procd_open_instance
	procd_set_param command "$PROG" daemon --config "$CONFIG"
	procd_set_param file "$CONFIG"
	procd_set_param respawn 3600 5 5
	procd_set_param stdout 1
	procd_set_param stderr 1
	procd_set_param term_timeout 10
	procd_close_instance
}

reload_service() {
	procd_send_signal "$SERVICE" "*" HUP
}

service_triggers() {
	procd_add_reload_trigger giwifi-auto
}
INIT_EOF

cat >"$CONFIG_PATH" <<'CONFIG_EOF'
config runtime 'main'
	option version '1'
	option connectivity_url 'http://captive.apple.com/'
	option portal_login_url ''
	option check_interval_seconds '30'
	option request_timeout_seconds '10'
	option retry_initial_seconds '10'
	option retry_max_seconds '300'
	option max_concurrent_requests '2'
	option log_level 'info'
	option event_buffer_size '256'
	option control_socket '/var/run/giwifi-auto.sock'

config account 'account_primary'
	option display_name '示例账号'
	option username 'example@example.com'
	option credential_ref 'uci:giwifi-credentials.account_primary.password'
	option enabled '0'
	option priority '100'
	option network_interface ''
CONFIG_EOF

mkdir -p /usr/bin /etc/config /etc/init.d
cp "$BINARY_PATH" /usr/bin/giwifi-auto
chmod 0755 /usr/bin/giwifi-auto
cp "$INIT_PATH" /etc/init.d/giwifi-auto
chmod 0755 /etc/init.d/giwifi-auto
if [ ! -e /etc/config/giwifi-auto ]; then
	cp "$CONFIG_PATH" /etc/config/giwifi-auto
	chmod 0600 /etc/config/giwifi-auto
fi
if [ ! -e /etc/config/giwifi-credentials ]; then
	: > /etc/config/giwifi-credentials
fi
chmod 0600 /etc/config/giwifi-credentials

/etc/init.d/giwifi-auto enable
if /etc/init.d/giwifi-auto running >/dev/null 2>&1; then
	/etc/init.d/giwifi-auto restart
else
	/etc/init.d/giwifi-auto start
fi
sleep 1
/etc/init.d/giwifi-auto running
/usr/bin/giwifi-auto version
printf 'GiWifi-Auto 安装完成 (%s)\n' "$TARGET_ARCH"
