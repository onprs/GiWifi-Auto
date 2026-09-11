# GiWifi-Auto

OpenWrt 上的 GiWiFi 自动认证工具。支持后台自动认证、多账号管理、命令行操作和 SSH 终端界面。

## 功能

- 自动检测网络状态并完成 Portal 认证
- 多账号独立启停和手动认证
- 断网重试和认证后联网检查
- CLI、JSON 输出和 SSH TUI
- 实时日志和近期事件查询
- 支持 Portal 的设备绑定确认流程
- 通过 procd 作为 OpenWrt 服务运行

## 构建

需要 Go 1.23 或更高版本。根据目标设备的实际架构构建：

```sh
CGO_ENABLED=0 GOOS=linux GOARCH=<target> \
  go build -trimpath -o ./bin/giwifi-auto ./cmd/giwifi-auto
```

不要仅根据路由器型号选择 `GOARCH`，请在设备上查看：

```sh
ubus call system board
```

## 配置

开发环境可以使用 JSON 配置：

```sh
cp config.example.json config.local.json
go run ./cmd/giwifi-auto check --config ./config.local.json
```

编辑 `config.local.json`，至少填写：

- `runtime.portal_login_url`：Portal 登录接口地址
- `accounts[].username`：认证用户名
- `accounts[].credential_ref`：密码引用
- `accounts[].enabled`：是否启用账号

密码通过外部引用读取，不要写入 JSON、命令行参数或 Git。支持以下引用格式：

- `env:NAME`：环境变量
- `file:/absolute/path`：权限受限的单行文件
- `uci:package.section.option`：OpenWrt UCI 配置项

OpenWrt 的 UCI 示例见 [openwrt/config.example](./openwrt/config.example)。

## 使用

启动后台服务：

```sh
go run ./cmd/giwifi-auto daemon --config ./config.local.json
```

查询状态和日志：

```sh
go run ./cmd/giwifi-auto status --config ./config.local.json
go run ./cmd/giwifi-auto status --config ./config.local.json --json
go run ./cmd/giwifi-auto logs --config ./config.local.json --limit 20
go run ./cmd/giwifi-auto logs --config ./config.local.json --follow
```

管理账号：

```sh
go run ./cmd/giwifi-auto account enable <account-id> --config ./config.local.json
go run ./cmd/giwifi-auto account disable <account-id> --config ./config.local.json
go run ./cmd/giwifi-auto account trigger <account-id> --config ./config.local.json
```

启动终端界面：

```sh
go run ./cmd/giwifi-auto tui --config ./config.local.json
```

控制接口默认使用本机 Unix Socket，不开放远程管理端口。

## OpenWrt

交叉编译后，将二进制复制到设备并执行：

```sh
./openwrt/install.sh ./bin/giwifi-auto
```

服务操作：

```sh
/etc/init.d/giwifi-auto enable
/etc/init.d/giwifi-auto start
/etc/init.d/giwifi-auto reload
/etc/init.d/giwifi-auto stop
```

详细部署说明见 [openwrt/README.md](./openwrt/README.md)。
