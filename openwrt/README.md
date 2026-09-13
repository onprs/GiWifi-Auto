# GiWifi-Auto 从零使用教程

本文适用于下面这套设备：

- TP-LINK TL-SG2008D
- RAX3000M eMMC 算力版 (使用OpenWrt作为系统)
- 4 个校园网有线网口
- 山东科技大学 GiWiFi 认证网络

完成后，四条校园网线路通过一根 Trunk 网线接入 OpenWrt。GiWifi-Auto 会自动识别活动线路并为账号分配认证线路，添加账号时只需填写用户名和密码。

## 一、先确认接线

本教程使用以下端口安排：

```text
校园网口 1 ── TL-SG2008D 端口 1
校园网口 2 ── TL-SG2008D 端口 2
校园网口 3 ── TL-SG2008D 端口 3
校园网口 4 ── TL-SG2008D 端口 4

TL-SG2008D 端口 8 ── RAX3000M WAN 口
RAX3000M LAN 口或 Wi-Fi ── 电脑、手机等终端
```

端口用途如下：


| 交换机端口 | 用途                | VLAN 角色                |
| ----- | ----------------- | ---------------------- |
| 1     | 校园网口 1            | VLAN 101，Untagged      |
| 2     | 校园网口 2            | VLAN 102，Untagged      |
| 3     | 校园网口 3            | VLAN 103，Untagged      |
| 4     | 校园网口 4            | VLAN 104，Untagged      |
| 5、6   | 暂不使用              | 不加入 101～104            |
| 7     | 临时管理交换机           | VLAN 1，Untagged        |
| 8     | 连接 RAX3000M WAN 口 | VLAN 101～104，全部 Tagged |


RAX3000M 使用项目当前固件时，WAN 物理口对应 `eth1`，LAN 侧由 `eth0` 和 `br-lan` 提供。账号认证线路使用 `eth1` 上的 VLAN 子接口。

## 二、准备 OpenWrt

本教程从 RAX3000M 已经成功运行 OpenWrt 开始。刷机过程取决于固件来源，本文不提供可能与固件版本不匹配的刷机命令。

登录 OpenWrt 的 LuCI 管理页面，确认以下内容：

- 可以使用 root 权限打开 SSH 终端。
- `eth1` 是连接交换机的 WAN 物理口。
- 路由器能够临时访问 GitHub。

安装阶段需要下载 GitHub 文件。如果校园网在认证前不允许访问 GitHub，可以暂时接入其他可用网络完成安装，安装并认证成功后再恢复本教程的四条校园网上联。

## 三、配置 TL-SG2008D

### 1. 登录交换机

首次配置时，让电脑直接连接交换机端口 7。根据 TL-SG2008D V3.0 说明书，出厂固定管理地址是 `10.18.18.251`：

1. 将电脑临时设置为 `10.18.18.10/24`。
2. 浏览器打开 `http://10.18.18.251`。
3. 首次登录时设置管理员账号和密码。

配置完成后，端口 7 继续保留为管理端口。它不连接校园网，也不连接 RAX3000M 的 WAN 口。

### 2. 建立 VLAN

进入交换机的 **VLAN -&gt; 802.1Q VLAN**，启用 802.1Q VLAN，建立下面四个 VLAN：


| VLAN ID | Untagged 端口 | Tagged 端口 | 其他端口            |
| ------- | ----------- | --------- | --------------- |
| 101     | 1           | 8         | Not Member(非成员) |
| 102     | 2           | 8         | Not Member(非成员) |
| 103     | 3           | 8         | Not Member(非成员) |
| 104     | 4           | 8         | Not Member(非成员) |


注意：端口 8 在 VLAN 101、102、103、104 中都必须是 **Tagged**。

为了保留交换机管理入口，可以让 VLAN 1 只保留端口 7：


| VLAN ID | Untagged 端口 | Tagged 端口 |
| ------- | ----------- | --------- |
| 1       | 7           | 无         |


### 3. 设置 PVID

进入 **VLAN -&gt; 802.1Q PVID**，设置：


| 端口  | PVID    |
| --- | ------- |
| 1   | 101     |
| 2   | 102     |
| 3   | 103     |
| 4   | 104     |
| 7   | 1       |
| 8   | 保持默认值 1 |


端口 8 的 PVID 只处理意外进入的未打标签报文。

点击保存配置，然后连接四个校园网口和 RAX3000M WAN 口。

## 四、配置 RAX3000M 的四条上联

进入 OpenWrt LuCI：

```text
网络 -> 接口
```

依次新增四个接口。每个接口都使用 DHCP 客户端，并勾选开机自动启动。metric 数字越小，线路优先级越高。


| 接口名称     | 设备         | 协议       | 路由 metric |
| -------- | ---------- | -------- | --------- |
| `wan101` | `eth1.101` | DHCP 客户端 | 10        |
| `wan102` | `eth1.102` | DHCP 客户端 | 20        |
| `wan103` | `eth1.103` | DHCP 客户端 | 30        |
| `wan104` | `eth1.104` | DHCP 客户端 | 40        |


如果 LuCI 没有直接显示 `eth1.101`，先在设备页面创建 802.1Q VLAN：

- 基础设备：`eth1`
- VLAN ID：`101`、`102`、`103`、`104`
- 设备名称：分别使用 `eth1.101`、`eth1.102`、`eth1.103`、`eth1.104`

然后把对应设备分配给四个 DHCP 接口。

注意：

1. 保存并应用后，四个接口都应获取 IPv4 地址。
2. 请在四个接口的高级设置中使用四个不同的 MAC 地址。

## 五、设置防火墙和多 WAN

### 1. 防火墙区域

在 LuCI 的防火墙设置中，将 `wan101`、`wan102`、`wan103`、`wan104` 加入 `wan` 区域。

四个接口都应位于同一个 `wan` 区域。保存并应用防火墙配置。

### 2. 安装 mwan3

通过 SSH 终端执行：

```sh
opkg update
opkg install mwan3 luci-app-mwan3
/etc/init.d/mwan3 enable
```

安装完成后进入 LuCI 的 **网络 -&gt; 负载均衡**。

在 mwan3 中建立四个接口：


| mwan3 接口 | 对应 OpenWrt 接口 | 地址族  | 状态  |
| -------- | ------------- | ---- | --- |
| `wan101` | `wan101`      | IPv4 | 启用  |
| `wan102` | `wan102`      | IPv4 | 启用  |
| `wan103` | `wan103`      | IPv4 | 启用  |
| `wan104` | `wan104`      | IPv4 | 启用  |


每个接口可以添加 `1.1.1.1` 和 `8.8.8.8` 作为线路检测地址。

为四个接口各建立一个 member，metric 和 weight 都设为 1：


| member         | 接口       | metric | weight |
| -------------- | -------- | ------ | ------ |
| `wan101_m1_w1` | `wan101` | 1      | 1      |
| `wan102_m1_w1` | `wan102` | 1      | 1      |
| `wan103_m1_w1` | `wan103` | 1      | 1      |
| `wan104_m1_w1` | `wan104` | 1      | 1      |


建立一个名为 `balanced` 的 policy，把四个 member 都加入其中。四个 member 的 metric 和 weight 都设为 1，表示四条可用线路参与平衡。再建立 IPv4 默认规则：

- 目标地址：`0.0.0.0/0`
- 使用 policy：`balanced`
- 地址族：`IPv4`

再增加一条 HTTPS 规则：

- 协议：TCP
- 目标端口：`443`
- 开启 sticky
- 使用 policy：`balanced`

如果安装后的 mwan3 已经带有默认 `wan` 接口或其他 policy，请将它们从默认规则中移除，只保留本教程的四条线路。

最后在 LuCI 的 **网络 -&gt; 防火墙 -&gt; 常规设置** 中关闭软件流量分载和硬件流量分载，然后重启 mwan3：

```sh
/etc/init.d/mwan3 restart
```

这一部分只需在路由器网络底座中配置一次。GiWifi-Auto 会自动读取活动线路和策略路由。

## 六、确认四条线路已经建立

在 LuCI 的接口页面确认：

- `wan101`、`wan102`、`wan103`、`wan104` 都是已连接状态。
- 四个接口都有 IPv4 地址。
- 四个接口都有默认路由。
- mwan3 状态页面显示四条线路可用。

如果其中一条线路没有 IPv4 地址，先检查对应校园网口、交换机端口 VLAN 和网线。GiWifi-Auto 只能使用已经建立的线路。

## 七、安装 GiWifi-Auto

确认路由器可以访问 GitHub 后，在 OpenWrt SSH 终端执行：

```sh
wget -qO- https://raw.githubusercontent.com/onprs/GiWifi-Auto/refs/heads/main/openwrt/install-online.sh | sh
```

安装器会自动完成：

- 下载并校验程序
- 安装 `/usr/bin/giwifi-auto`
- 安装 procd 服务
- 创建 GiWifi-Auto 配置
- 创建受限的 UCI 凭据存储
- 设置开机启动并启动服务

注意：安装器不会覆盖已有的 `/etc/config/giwifi-auto`。

## 八、添加多个账号

安装完成后启动终端界面：

```sh
giwifi-auto
```

操作步骤：

1. 选中初始的“示例账号”，点击 `编辑`，或者按 `c`。
2. 填写第一个 GiWiFi 用户名和密码。
3. 保存账号。
4. 点击 `新增`，或者按 `a`。
5. 填写第二个账号的用户名和密码并保存。
6. 按同样方式添加第三个和第四个账号。

每次保存后，账号会立即启用并开始认证。账号 ID、显示名称、接口名称和路由信息由程序自动处理。

程序会按照 OpenWrt 当前线路优先级分配账号：

- 第一个账号使用优先级最高的可用线路。
- 第二个账号使用下一条未分配线路。
- 后续账号依次分配剩余线路。

本教程中的 metric 为 10、20、30、40，因此添加账号的顺序应与 `wan101`、`wan102`、`wan103`、`wan104` 的优先级顺序一致。

## 九、检查认证结果

TUI 主界面会显示：

- 账号与线路的对应关系
- 线路是否存在
- IPv4 地址
- 线路状态
- 账号认证状态
- 最近一次结果
- 下次检查时间

四个账号正常时，账号状态应为“已认证”。也可以通过 SSH 查询：

```sh
/etc/init.d/giwifi-auto status
/usr/bin/giwifi-auto status --config /etc/config/giwifi-auto --json
```

如果修改了 OpenWrt 网络配置，再执行：

```sh
/etc/init.d/giwifi-auto reload
```

程序会重新读取线路并在已保存接口失效时自动修复账号映射。

## 常见问题

### 只发现一条或没有发现线路

检查交换机端口 1～4 是否分别为 VLAN 101～104 的 Untagged 成员，端口 8 是否在四个 VLAN 中都是 Tagged。再检查 RAX3000M 的四个 DHCP 接口是否都已经获取 IPv4 地址。

### 四个接口都没有 IPv4 地址

检查 RAX3000M WAN 口是否接在交换机 8 口，交换机 8 口是否接入了 RAX3000M 的 WAN 物理口。再检查校园网网线和 DHCP 状态。

### 多个账号没有分到独立线路

活动线路数量必须不少于账号数量。四个账号需要四条已经建立的 IPv4 上联。还要确认 mwan3 已启用，并且四条线路都加入了默认 IPv4 policy。

### 添加账号后一直处于离线状态

先在 TUI 中查看对应线路是否存在、是否有 IPv4 地址和物理载波。线路未建立时，程序会持续等待并重试，不需要重新填写接口名称。

### 无法下载安装程序

校园网认证前可能无法访问 GitHub。先使用临时可用网络完成安装，或者准备好离线安装文件后再安装。

### 交换机配置后无法登录管理页面

将电脑接回交换机端口 7，并把电脑 IPv4 临时设置到交换机管理地址所在网段。端口 7 负责管理，端口 1～4 是校园网接入口，端口 8 是路由器 Trunk 口。

## 更新与卸载

更新程序时再次执行安装命令，现有账号配置不会被覆盖：

```sh
wget -qO- https://raw.githubusercontent.com/onprs/GiWifi-Auto/refs/heads/main/openwrt/install-online.sh | sh
```

卸载程序但保留账号配置：

```sh
wget -qO- https://raw.githubusercontent.com/onprs/GiWifi-Auto/refs/heads/main/openwrt/uninstall.sh | sh
```

硬件参考：

- [TP-LINK TL-SG2008D 官方页面](https://www.tp-link.com.cn/m/product_1936.html?v=download)
- [OpenWrt RAX3000M 硬件页面](https://openwrt.org/toh/cmcc/rax3000m)
