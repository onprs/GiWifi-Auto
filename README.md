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

进入后通过鼠标操作：点击底部 `新增` 添加账号，点击账号行选择账号，再点击 `编辑`、`检测`、`启用`、`停用`、`删除`、`刷新` 或 `帮助` 执行操作。删除账号需要再次点击确认按钮。填写账号时点击用户名或密码字段获得焦点，再输入内容并点击 `保存`。

`wan101`、`wan102`、`wan103`、`wan104` 分别对应 `eth1.101`、`eth1.102`、`eth1.103`、`eth1.104`。四条 VLAN 在交换机到路由器的 Trunk 口上全部使用 Tagged，不设置 untagged/native VLAN。

主界面会显示账号与接口的对应关系、接口是否存在、管理状态、物理载波、运行状态、IPv4 地址、认证状态、最近结果、重试次数和下次检查时间。底部会显示当前可用操作；按 `?` 可查看完整操作列表。按 `D` 或 `Delete` 删除当前账号，程序会要求确认。

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
