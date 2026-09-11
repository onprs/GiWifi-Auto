#!/bin/sh
set -eu

SOURCE=${1:-}
if [ -z "$SOURCE" ] || [ ! -f "$SOURCE" ]; then
	echo "用法: install.sh <linux 可执行文件路径>" >&2
	exit 2
fi
if [ "$(id -u)" -ne 0 ]; then
	echo "安装需要管理员权限" >&2
	exit 1
fi

mkdir -p /usr/bin /etc/config /etc/init.d
cp "$SOURCE" /usr/bin/giwifi-auto
chmod 0755 /usr/bin/giwifi-auto
cp "$(dirname "$0")/etc/init.d/giwifi-auto" /etc/init.d/giwifi-auto
chmod 0755 /etc/init.d/giwifi-auto

if [ ! -e /etc/config/giwifi-auto ]; then
	cp "$(dirname "$0")/config.example" /etc/config/giwifi-auto
	chmod 0600 /etc/config/giwifi-auto
fi

/etc/init.d/giwifi-auto enable
printf '%s\n' "安装完成，请先编辑 /etc/config/giwifi-auto 并运行 /etc/init.d/giwifi-auto start"
