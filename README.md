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

准备一台安装了 Go 1.23 或更高版本及 OpenSSH 客户端的开发机，并在 `~/.ssh/config` 中配置可直接登录 OpenWrt 的主机别名 `openwrt`。在仓库根目录执行：

```sh
sh ./openwrt/deploy.sh
```

该命令会识别目标设备架构、交叉编译、上传程序、安装服务并重启。当前支持 `amd64`、`arm64` 和 `mipsle`。

使用其他 SSH 主机别名时执行：

```sh
GIWIFI_DEPLOY_HOST=<主机别名> sh ./openwrt/deploy.sh
```

首次部署会创建 `/etc/config/giwifi-auto` 示例配置；再次部署不会覆盖现有配置。

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

在 OpenWrt 上查询服务和账号状态：

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

## 更新与卸载

更新时拉取最新代码并重新执行一键部署命令，现有 UCI 配置会保留。

卸载程序和服务：

```sh
ssh openwrt 'sh -s' < ./openwrt/uninstall.sh
```

卸载不会删除 `/etc/config/giwifi-auto`。更多部署说明见 [OpenWrt 部署文档](./openwrt/README.md)。
