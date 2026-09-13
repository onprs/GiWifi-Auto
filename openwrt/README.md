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

SSH 登录 OpenWrt 后直接运行：

```sh
giwifi-auto
```

进入后通过鼠标操作：点击底部 `新增` 添加账号，点击账号行选择账号，再点击 `编辑`、`检测`、`启用`、`停用`、`删除`、`刷新` 或 `帮助` 执行操作。删除账号需要再次点击确认按钮。填写账号时点击用户名或密码字段获得焦点，再输入内容并点击 `保存`。

密码保存在受限的 UCI 凭据配置中，不会显示在账号状态和事件日志里。

主界面会显示账号与接口的对应关系、接口是否存在、管理状态、物理载波、运行状态、IPv4 地址、认证状态、最近结果、重试次数和下次检查时间。底部会显示当前可用操作；按 `?` 可查看完整操作列表。按 `D` 或 `Delete` 删除当前账号，程序会要求确认。

## 多账号与网络线路

程序会读取 OpenWrt 当前运行中的 IPv4 上联线路、地址和默认路由，自动识别实际出口接口，并按线路优先级为账号分配认证线路。添加账号时只填写用户名和密码，无需填写 VLAN、接口名称、网关或策略路由标记。线路暂时不可用时，程序会等待线路恢复。

多 WAN 的转发策略由 OpenWrt 当前网络配置负责，GiWifi-Auto 会自动为认证请求选择对应线路。在 OpenWrt 已建立网络线路的设备上，账号设置不依赖固定的 WAN 名称、VLAN 编号或物理网卡名称。

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
giwifi-auto
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
