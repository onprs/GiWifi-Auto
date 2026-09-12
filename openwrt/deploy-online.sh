#!/bin/sh
set -eu

DEPLOY_HOST=${GIWIFI_DEPLOY_HOST:-openwrt}
REPOSITORY=onprs/GiWifi-Auto
RELEASE_BASE="https://github.com/$REPOSITORY/releases/latest/download"
RAW_BASE="https://raw.githubusercontent.com/$REPOSITORY/main/openwrt"
LOCAL_DIRECTORY=
REMOTE_DIRECTORY=

cleanup() {
	status=$?
	trap - 0 1 2 15
	if [ -n "$REMOTE_DIRECTORY" ]; then
		ssh -n -- "$DEPLOY_HOST" "rm -rf '$REMOTE_DIRECTORY'" >/dev/null 2>&1 || true
	fi
	if [ -n "$LOCAL_DIRECTORY" ]; then
		rm -rf "$LOCAL_DIRECTORY"
	fi
	exit "$status"
}
trap cleanup 0 1 2 15

for required_command in curl ssh scp mktemp sha256sum; do
	if ! command -v "$required_command" >/dev/null 2>&1; then
		echo "缺少部署命令: $required_command" >&2
		exit 1
	fi
done

printf '读取 %s 的设备架构...\n' "$DEPLOY_HOST"
MACHINE=$(ssh -n -- "$DEPLOY_HOST" uname -m)
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
		PACKAGE_ARCHITECTURES=$(ssh -n -- "$DEPLOY_HOST" "if command -v opkg >/dev/null 2>&1; then opkg print-architecture; fi")
		case "$PACKAGE_ARCHITECTURES" in
			*mipsel*) TARGET_ARCH=mipsle ;;
			*)
				echo "无法确认 MIPS 设备端序" >&2
				exit 1
				;;
		esac
		;;
	*)
		echo "不支持的目标架构: $MACHINE" >&2
		exit 1
		;;
esac

GITHUB_PROXY=${GIWIFI_GITHUB_PROXY:-${HTTPS_PROXY:-${https_proxy:-}}}
if [ -z "$GITHUB_PROXY" ] && command -v git >/dev/null 2>&1; then
	GITHUB_PROXY=$(git config --global --get https.proxy 2>/dev/null || true)
fi

download() {
	if [ -n "$GITHUB_PROXY" ]; then
		curl --proxy "$GITHUB_PROXY" --connect-timeout 20 --retry 2 -fsSL "$1" -o "$2"
	else
		curl --connect-timeout 20 --retry 2 -fsSL "$1" -o "$2"
	fi
}

LOCAL_DIRECTORY=$(mktemp -d "${TMPDIR:-/tmp}/giwifi-auto-deploy.XXXXXX")
PACKAGE_DIRECTORY="$LOCAL_DIRECTORY/openwrt"
BINARY_NAME="giwifi-auto-linux-$TARGET_ARCH"
BINARY_PATH="$LOCAL_DIRECTORY/$BINARY_NAME"
CHECKSUM_PATH="$LOCAL_DIRECTORY/SHA256SUMS"
mkdir -p "$PACKAGE_DIRECTORY/etc/init.d"

printf '下载 GiWifi-Auto 最新版本 (%s)...\n' "$TARGET_ARCH"
download "$RELEASE_BASE/$BINARY_NAME" "$BINARY_PATH"
download "$RELEASE_BASE/SHA256SUMS" "$CHECKSUM_PATH"
download "$RAW_BASE/install.sh" "$PACKAGE_DIRECTORY/install.sh"
download "$RAW_BASE/config.example" "$PACKAGE_DIRECTORY/config.example"
download "$RAW_BASE/etc/init.d/giwifi-auto" "$PACKAGE_DIRECTORY/etc/init.d/giwifi-auto"

CHECKSUM_LINE=$(grep -F "/$BINARY_NAME" "$CHECKSUM_PATH" | head -n 1)
EXPECTED_CHECKSUM=${CHECKSUM_LINE%% *}
ACTUAL_CHECKSUM=$(sha256sum "$BINARY_PATH")
ACTUAL_CHECKSUM=${ACTUAL_CHECKSUM%% *}
if [ -z "$EXPECTED_CHECKSUM" ] || [ "$EXPECTED_CHECKSUM" != "$ACTUAL_CHECKSUM" ]; then
	echo "发布文件校验失败" >&2
	exit 1
fi

REMOTE_DIRECTORY=$(ssh -n -- "$DEPLOY_HOST" 'mktemp -d /tmp/giwifi-auto-deploy.XXXXXX')
case "$REMOTE_DIRECTORY" in
	/tmp/giwifi-auto-deploy.*) ;;
	*)
		echo "目标设备返回了无效的临时目录" >&2
		exit 1
		;;
esac

printf '安装到 %s...\n' "$DEPLOY_HOST"
scp -O -r "$BINARY_PATH" "$PACKAGE_DIRECTORY" "$DEPLOY_HOST:$REMOTE_DIRECTORY/"
ssh -n -- "$DEPLOY_HOST" "set -eu
sh '$REMOTE_DIRECTORY/openwrt/install.sh' '$REMOTE_DIRECTORY/$BINARY_NAME'
if /etc/init.d/giwifi-auto running >/dev/null 2>&1; then
	/etc/init.d/giwifi-auto restart
else
	/etc/init.d/giwifi-auto start
fi
sleep 1
/etc/init.d/giwifi-auto running
/usr/bin/giwifi-auto version"

printf '部署完成：%s (%s)\n' "$DEPLOY_HOST" "$TARGET_ARCH"
