# GiWifi-Auto

面向山东科技大学 GiWiFi 的 OpenWrt 自动认证工具。程序由系统服务持续检查网络状态，在检测到 Portal 时自动完成认证，并支持多账号管理、状态查询、事件日志和 SSH 终端界面。

## 功能

- 自动检测联网状态并完成 Portal 认证
- 认证失败自动退避重试，认证成功后再次检查网络
- 多账号独立启用、停用和手动触发
- 命令行、JSON 输出与 SSH TUI
- 近期事件查询和实时日志跟随
- Portal 设备绑定确认
- 通过 OpenWrt procd 开机启动和进程守护

## 一键部署

已配置 SSH 主机别名 `openwrt` 的开发机需要安装 `curl`、`ssh` 和 `scp`。在任意目录直接执行：

```sh
curl -fsSL https://raw.githubusercontent.com/onprs/GiWifi-Auto/refs/heads/main/openwrt/deploy-online.sh | sh
```

命令会识别 OpenWrt 设备架构，在开发机下载并校验最新 Release，然后自动上传、安装并启动服务。当前支持 `amd64`、`arm64` 和 `mipsle`，OpenWrt 无需访问 GitHub，也不需要在本地克隆仓库或安装 Go。

首次部署会创建 `/etc/config/giwifi-auto` 示例配置；再次执行同一命令即可更新程序，现有配置不会被覆盖。

## 配置

在 OpenWrt 上设置 Portal 登录地址、账号和凭据引用：

```sh
uci set 'giwifi-auto.main.portal_login_url=<Portal 登录接口地址>'
uci set 'giwifi-auto.account_primary.username=<GiWiFi 账号>'
uci set 'giwifi-auto.account_primary.credential_ref=uci:giwifi-credentials.account_primary.password'
uci set 'giwifi-auto.account_primary.enabled=1'

uci set 'giwifi-credentials.account_primary=credential'
uci set 'giwifi-credentials.account_primary.password=<GiWiFi 密码>'

uci commit giwifi-auto
uci commit giwifi-credentials
chmod 0600 /etc/config/giwifi-auto /etc/config/giwifi-credentials
/usr/bin/giwifi-auto check --config /etc/config/giwifi-auto
/etc/init.d/giwifi-auto restart
```

配置中只保存 `env:`、`file:` 或 `uci:` 凭据引用。完整字段示例见 [openwrt/config.example](./openwrt/config.example)。

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

通过 SSH 启动终端界面：

```sh
ssh -t openwrt '/usr/bin/giwifi-auto tui --config /etc/config/giwifi-auto'
```

修改配置后可重载服务：

```sh
/etc/init.d/giwifi-auto reload
```

## 卸载

```sh
curl -fsSL https://raw.githubusercontent.com/onprs/GiWifi-Auto/refs/heads/main/openwrt/uninstall.sh | ssh openwrt "sh -s"
```

卸载不会删除 `/etc/config/giwifi-auto`。源码部署及更多说明见 [OpenWrt 部署文档](./openwrt/README.md)。
