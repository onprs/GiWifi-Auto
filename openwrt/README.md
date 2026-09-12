# OpenWrt 部署

GiWifi-Auto 在 OpenWrt 上安装为 procd 服务，程序路径为 `/usr/bin/giwifi-auto`，配置路径为 `/etc/config/giwifi-auto`。

## 一键部署

开发机需要安装 `curl`、`ssh` 和 `scp`，并配置可直接登录目标设备的 SSH 主机别名 `openwrt`。在任意目录执行：

```sh
curl -fsSL https://raw.githubusercontent.com/onprs/GiWifi-Auto/main/openwrt/deploy-online.sh | sh
```

在线部署脚本会读取目标设备架构，在开发机下载 GitHub 最新 Release 与 SHA256 校验和，然后上传到 OpenWrt 完成安装。目标设备无需访问 GitHub，也不需要预先复制任何文件。

默认支持 `amd64`、`arm64` 和小端 `mipsle`。首次安装会从 [config.example](./config.example) 创建 `/etc/config/giwifi-auto` 并启用开机启动；已有配置不会被覆盖。更新版本时重新执行同一条命令即可。

使用其他 SSH 主机别名：

```sh
curl -fsSL https://raw.githubusercontent.com/onprs/GiWifi-Auto/main/openwrt/deploy-online.sh | GIWIFI_DEPLOY_HOST=<主机别名> sh
```

GitHub 需要代理时可通过 `GIWIFI_GITHUB_PROXY` 指定；脚本也会自动读取 `HTTPS_PROXY` 或 Git 的全局 `https.proxy`：

```sh
curl --proxy <代理地址> -fsSL https://raw.githubusercontent.com/onprs/GiWifi-Auto/main/openwrt/deploy-online.sh | GIWIFI_GITHUB_PROXY=<代理地址> sh
```

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

## 从源码部署

源码部署需要 Go 1.23 或更高版本、`ssh`、`scp`，以及可通过密钥登录的 SSH 主机别名。在仓库根目录执行：

```sh
sh ./openwrt/deploy.sh
```

脚本会读取目标设备架构、交叉编译当前源码、上传并重启服务。默认目标为 `openwrt`，可指定其他别名：

```sh
GIWIFI_DEPLOY_HOST=<主机别名> sh ./openwrt/deploy.sh
```

Makefile 提供等价入口：

```sh
make deploy
make deploy DEPLOY_HOST=<主机别名>
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

## 卸载

```sh
curl -fsSL https://raw.githubusercontent.com/onprs/GiWifi-Auto/main/openwrt/uninstall.sh | ssh openwrt "sh -s"
```

卸载会停止并禁用服务，删除程序和服务文件，保留 `/etc/config/giwifi-auto`。
