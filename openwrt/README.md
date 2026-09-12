# OpenWrt 部署

GiWifi-Auto 在 OpenWrt 上安装为 procd 服务，程序路径为 `/usr/bin/giwifi-auto`，配置路径为 `/etc/config/giwifi-auto`。

## 一键安装

登录 OpenWrt 终端后执行：

```sh
wget -qO- https://raw.githubusercontent.com/onprs/GiWifi-Auto/refs/heads/main/openwrt/install-online.sh | sh
```

安装器会自动识别 `amd64`、`arm64` 和小端 `mipsle`，下载 GitHub 最新 Release 与 SHA256 校验和，安装后启动或重启服务。设备需要能够访问 GitHub，并安装 `wget` 或 `curl`。

首次安装会从 [config.example](./config.example) 创建 `/etc/config/giwifi-auto` 并启用开机启动；已有配置不会被覆盖。更新版本时重新执行同一条命令即可。

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

配置支持三种凭据引用。UCI 账号 section 名称只能使用字母、数字和下划线，并与程序中的账号 ID 保持一致。每个账号可以使用独立的 `network_interface`，让探测和 Portal 登录请求从对应线路发出。

- `uci:package.section.option`：读取 UCI 值，适合 OpenWrt
- `file:/absolute/path`：读取权限受限的单行文件
- `env:NAME`：读取服务进程环境变量

## 多账号与网络接口

每条需要独立认证的上联都应先在 OpenWrt 中配置为独立网络接口，再为账号填写对应的 Linux 设备名。下面示例使用两条线路，设备名请按 `ip -br link` 的实际输出替换：

```sh
uci set 'giwifi-auto.account_primary.network_interface=eth1'
uci set 'giwifi-auto.account_secondary=account'
uci set 'giwifi-auto.account_secondary.display_name=第二条线路账号'
uci set 'giwifi-auto.account_secondary.username=<第二个 GiWiFi 账号>'
uci set 'giwifi-auto.account_secondary.credential_ref=uci:giwifi-credentials.account_secondary.password'
uci set 'giwifi-auto.account_secondary.enabled=1'
uci set 'giwifi-auto.account_secondary.priority=100'
uci set 'giwifi-auto.account_secondary.network_interface=lan2'

uci set 'giwifi-credentials.account_secondary=credential'
uci set 'giwifi-credentials.account_secondary.password=<第二个 GiWiFi 密码>'
uci commit giwifi-auto
uci commit giwifi-credentials
chmod 0600 /etc/config/giwifi-auto /etc/config/giwifi-credentials
/usr/bin/giwifi-auto check --config /etc/config/giwifi-auto
/etc/init.d/giwifi-auto reload
```

`GiWifi-Auto` 负责每条线路的 Portal 认证，线路之间的转发负载均衡由 OpenWrt 的 `mwan3` 负责。多 WAN 使用前安装并启用 `mwan3`：

```sh
opkg update
opkg install mwan3
/etc/init.d/mwan3 enable
```

将各上联网络加入防火墙的 `wan` zone，并在 `mwan3` 中为每条上联创建 interface/member，加入同一个 `balanced` policy，再将 IPv4 默认规则指向该 policy。启用策略路由时应关闭防火墙的 `flow_offloading` 和 `flow_offloading_hw`。

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

源码部署需要 Go 1.23 或更高版本、`ssh` 和 `scp`。在仓库根目录执行：

```sh
GIWIFI_DEPLOY_HOST=<SSH 主机> sh ./openwrt/deploy.sh
```

脚本会读取目标设备架构、交叉编译当前源码、上传并重启服务。Makefile 提供等价入口：

```sh
make deploy DEPLOY_HOST=<SSH 主机>
```

## 手动安装

根据设备实际架构构建：

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
wget -qO- https://raw.githubusercontent.com/onprs/GiWifi-Auto/refs/heads/main/openwrt/uninstall.sh | sh
```

卸载会停止并禁用服务，删除程序和服务文件，保留 `/etc/config/giwifi-auto`。
