#!/bin/sh
set -eu

if [ "$(id -u)" -ne 0 ]; then
	echo "卸载需要管理员权限" >&2
	exit 1
fi

if [ -x /etc/init.d/giwifi-auto ]; then
	/etc/init.d/giwifi-auto stop || true
	/etc/init.d/giwifi-auto disable || true
fi
rm -f /etc/init.d/giwifi-auto /usr/bin/giwifi-auto
printf '%s\n' "程序已卸载，/etc/config/giwifi-auto 未删除"
