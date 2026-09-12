# GiWifi-Auto

面向山东科技大学 GiWiFi 的 OpenWrt 自动认证工具。程序由系统服务持续检查网络状态，在检测到 Portal 时自动完成认证，并支持多账号管理、状态查询、事件日志和终端界面。

## 功能

- 自动检测联网状态并完成 Portal 认证
- 认证失败自动退避重试，认证成功后再次检查网络
- 多账号独立启用、停用和手动触发
- 多账号可分别绑定 OpenWrt 网络接口进行认证
- 命令行、JSON 输出与终端界面
- 近期事件查询和实时日志跟随
- Portal 设备绑定确认
- 通过 OpenWrt procd 开机启动和进程守护

## 一键安装

登录自己的 OpenWrt 终端，直接执行：

```sh
wget -qO- https://raw.githubusercontent.com/onprs/GiWifi-Auto/refs/heads/main/openwrt/install-online.sh | sh
```

安装器会识别设备架构，下载并校验 GitHub 最新 Release，安装程序和 procd 服务，然后启动服务。当前支持 `amd64`、`arm64` 和小端 `mipsle`。

首次安装会创建 `/etc/config/giwifi-auto` 示例配置；再次执行同一命令即可更新程序，现有配置不会被覆盖。

## 配置

SSH 登录 OpenWrt 后直接运行：

```sh
giwifi-auto
```

TUI 中按 `a` 添加账号，只填写用户名和密码；按 `Enter` 或 `Ctrl+S` 保存。保存后账号会自动启用并开始认证；重复按 `a` 即可配置多个账号。按 `c` 编辑当前账号，密码留空会保留原密码。Portal 地址、账号 ID、显示名称和绑定网卡由程序自动处理。

密码保存在受限的 UCI 凭据配置中，不会显示在账号状态和事件日志里。

## 使用

查询服务和账号状态：

```sh
/etc/init.d/giwifi-auto status
/usr/bin/giwifi-auto status --config /etc/config/giwifi-auto
/usr/bin/giwifi-auto status --config /etc/config/giwifi-auto --json
```

管理账号：

```sh
/usr/bin/giwifi-auto account enable account_primary --config /etc/config/giwifi-auto
/usr/bin/giwifi-auto account disable account_primary --config /etc/config/giwifi-auto
/usr/bin/giwifi-auto account trigger account_primary --config /etc/config/giwifi-auto
```

查看事件：

```sh
/usr/bin/giwifi-auto logs --config /etc/config/giwifi-auto --limit 20
/usr/bin/giwifi-auto logs --config /etc/config/giwifi-auto --follow
```

启动终端界面：

```sh
giwifi-auto
```

也可以显式指定配置路径：

```sh
/usr/bin/giwifi-auto tui --config /etc/config/giwifi-auto
```

修改配置后可重载服务：

```sh
/etc/init.d/giwifi-auto reload
```

## 卸载

```sh
wget -qO- https://raw.githubusercontent.com/onprs/GiWifi-Auto/refs/heads/main/openwrt/uninstall.sh | sh
```

卸载不会删除 `/etc/config/giwifi-auto`。源码部署及更多说明见 [OpenWrt 部署文档](./openwrt/README.md)。
