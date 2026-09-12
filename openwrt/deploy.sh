#!/bin/sh
set -eu

DEPLOY_HOST=${GIWIFI_DEPLOY_HOST:-openwrt}
SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
REPOSITORY_ROOT=$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd)
REMOTE_DIRECTORY=

cleanup() {
	status=$?
	trap - 0 1 2 15
	if [ -n "$REMOTE_DIRECTORY" ]; then
		ssh -- "$DEPLOY_HOST" "rm -rf '$REMOTE_DIRECTORY'" >/dev/null 2>&1 || true
	fi
	exit "$status"
}
trap cleanup 0 1 2 15

for required_command in go ssh scp; do
	if ! command -v "$required_command" >/dev/null 2>&1; then
		echo "缺少部署命令: $required_command" >&2
		exit 1
	fi
done

printf '读取 %s 的设备架构...\n' "$DEPLOY_HOST"
MACHINE=$(ssh -- "$DEPLOY_HOST" uname -m)
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
	*)
		echo "不支持的目标架构: $MACHINE" >&2
		exit 1
		;;
esac

cd "$REPOSITORY_ROOT"
mkdir -p bin
ARTIFACT="bin/giwifi-auto-linux-$TARGET_ARCH"
VERSION=${GIWIFI_DEPLOY_VERSION:-}
if [ -z "$VERSION" ]; then
	VERSION=$(git describe --tags --always --dirty 2>/dev/null || printf 'dev')
fi

printf '构建 GiWifi-Auto %s (%s)...\n' "$VERSION" "$TARGET_ARCH"
CGO_ENABLED=0 GOOS=linux GOARCH="$TARGET_ARCH" \
	go build -trimpath -ldflags "-X main.version=$VERSION" -o "$ARTIFACT" ./cmd/giwifi-auto

REMOTE_DIRECTORY=$(ssh -- "$DEPLOY_HOST" 'mktemp -d /tmp/giwifi-auto-deploy.XXXXXX')
case "$REMOTE_DIRECTORY" in
	/tmp/giwifi-auto-deploy.*) ;;
	*)
		echo "目标设备返回了无效的临时目录" >&2
		exit 1
		;;
esac

printf '上传并安装到 %s...\n' "$DEPLOY_HOST"
scp -O -r "$ARTIFACT" openwrt "$DEPLOY_HOST:$REMOTE_DIRECTORY/"
ARTIFACT_NAME=$(basename "$ARTIFACT")
ssh -- "$DEPLOY_HOST" "set -eu
sh '$REMOTE_DIRECTORY/openwrt/install.sh' '$REMOTE_DIRECTORY/$ARTIFACT_NAME'
if /etc/init.d/giwifi-auto running >/dev/null 2>&1; then
	/etc/init.d/giwifi-auto restart
else
	/etc/init.d/giwifi-auto start
fi
sleep 1
/etc/init.d/giwifi-auto running
/usr/bin/giwifi-auto version"

printf '部署完成：%s (%s)\n' "$DEPLOY_HOST" "$TARGET_ARCH"
