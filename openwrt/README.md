# OpenWrt 部署

GiWifi-Auto 在 OpenWrt 上安装为 procd 服务，程序路径为 `/usr/bin/giwifi-auto`，配置路径为 `/etc/config/giwifi-auto`。

## 部署准备

开发机需要：

- Go 1.23 或更高版本
- `ssh` 和 `scp`
- 可通过密钥直接登录 OpenWrt 的 SSH 主机别名

默认部署目标为 `openwrt`。目标设备需要使用 `amd64`、`arm64` 或小端 `mipsle` 架构。

## 一键部署

在仓库根目录执行：

```sh
sh ./openwrt/deploy.sh
```

部署命令会自动读取目标设备的 `uname -m`，选择对应的 Go 架构，完成交叉编译、上传、安装、服务重启和运行状态检查。

指定其他 SSH 主机别名：

```sh
GIWIFI_DEPLOY_HOST=<主机别名> sh ./openwrt/deploy.sh
```

也可以使用 Makefile 中的等价入口：

```sh
make deploy
make deploy DEPLOY_HOST=<主机别名>
```

首次安装会从 [config.example](./config.example) 创建 `/etc/config/giwifi-auto` 并启用开机启动；已有配置不会被覆盖。更新版本时重新执行部署命令即可。

## 配置账号

下面的命令配置默认账号，并将密码保存在独立的 UCI package 中：

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
```

配置支持三种凭据引用。UCI 账号 section 名称只能使用字母、数字和下划线，并与程序中的账号 ID 保持一致。

- `uci:package.section.option`：读取 UCI 值，适合 OpenWrt
- `file:/absolute/path`：读取权限受限的单行文件
- `env:NAME`：读取服务进程环境变量

配置完成后检查并重启：

```sh
/usr/bin/giwifi-auto check --config /etc/config/giwifi-auto
/etc/init.d/giwifi-auto restart
```

## 服务管理

```sh
/etc/init.d/giwifi-auto enable
/etc/init.d/giwifi-auto start
/etc/init.d/giwifi-auto restart
/etc/init.d/giwifi-auto reload
/etc/init.d/giwifi-auto stop
/etc/init.d/giwifi-auto status
```

`reload` 会重新读取配置。修改控制 Socket 地址后需要使用 `restart`。

## 状态与日志

```sh
/usr/bin/giwifi-auto status --config /etc/config/giwifi-auto
/usr/bin/giwifi-auto status --config /etc/config/giwifi-auto --json
/usr/bin/giwifi-auto logs --config /etc/config/giwifi-auto --limit 50
/usr/bin/giwifi-auto logs --config /etc/config/giwifi-auto --follow
/usr/bin/giwifi-auto tui --config /etc/config/giwifi-auto
```

## 手动安装

根据设备实际架构构建，不能仅依据路由器型号判断：

```sh
ubus call system board
CGO_ENABLED=0 GOOS=linux GOARCH=<目标架构> \
  go build -trimpath -o ./bin/giwifi-auto ./cmd/giwifi-auto
```

将二进制和 `openwrt` 目录复制到设备后执行：

```sh
sh ./openwrt/install.sh ./giwifi-auto
```

安装脚本只启用服务。配置完成后再启动或重启服务。

## 卸载

在仓库根目录将卸载脚本发送到设备执行：

```sh
ssh openwrt 'sh -s' < ./openwrt/uninstall.sh
```

卸载会停止并禁用服务，删除程序和服务文件，保留 `/etc/config/giwifi-auto`。
