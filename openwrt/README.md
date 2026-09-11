# OpenWrt 部署

当前服务脚本使用 procd 托管前台运行的 `giwifi-auto daemon`，配置路径为 `/etc/config/giwifi-auto`。程序收到 `SIGTERM` 时会停止账号运行器，收到 `SIGHUP` 时会重新读取并校验配置。

## 安装

先在开发机根据实际设备信息交叉编译。不要仅依据路由器商品名选择架构；需要确认设备的 `ubus call system board`、CPU 架构和端序：

```sh
CGO_ENABLED=0 GOOS=linux GOARCH=<target> go build -trimpath -o ./bin/giwifi-auto ./cmd/giwifi-auto
```

将产物复制到设备后执行：

```sh
./openwrt/install.sh ./bin/giwifi-auto
```

安装脚本不会覆盖已有 `/etc/config/giwifi-auto`。卸载脚本也会保留该配置文件。

## 配置

[config.example](./config.example) 是 UCI 配置示例。账号启用前需要配置有效的 `portal_login_url` 和凭据引用。凭据引用可以指向 `env:`、权限受限的 `file:` 或 OpenWrt `uci:` 值；不要把密码写入配置、命令行或日志。

## 服务操作

```sh
/etc/init.d/giwifi-auto enable
/etc/init.d/giwifi-auto start
/etc/init.d/giwifi-auto reload
/etc/init.d/giwifi-auto stop
```

`reload` 会向守护进程发送 `SIGHUP`，procd 会继续托管原进程。网络请求和认证流程的真实设备验证尚未在本仓库完成。
