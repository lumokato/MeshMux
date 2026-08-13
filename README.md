# MeshMux

MeshMux 是面向 Windows 桌面与 Linux 桌面/headless 环境的 mihomo 管理工具，用于日常代理、WireGuard、Tailscale 和移动端配置发布。

Windows 安装版将核心注册为自动启动的系统服务，未登录桌面时也会运行；托盘在用户登录后启动，负责当前用户的系统代理、配置页面和核心控制。服务只读取 `ProgramData\MeshMux` 中受系统保护的运行快照，用户配置固定保存在 `%LocalAppData%\MeshMux`；升级时若该文件仍是安装器空模板，MeshMux 会依次尝试恢复旧 `%AppData%\MeshMux` 配置和最后一次成功的服务快照。启动或重启服务时会先提权生成新快照，并拒绝用空模板覆盖已有真实配置。服务使用独立的 Tailnet 身份目录，首次启动时通过配置中的 Auth Key 重新登录，不迁移旧用户核心的登录缓存。安装、卸载或人工启停服务时才需要 UAC，正常开机不需要确认。

English README: [README.en.md](README.en.md)

## 功能

- 常驻任务栏，启动、停止和重启 mihomo。
- 开启 Windows 系统代理，支持 TUN 模式。
- 浏览器配置页填写订阅、Sub-Store、WireGuard 和 Tailscale。
- 将 Tailnet TCP/UDP 端口转发到 Windows 本机服务。
- 生成 Windows profile 与 mobile profile。
- 上传 mobile profile 到 Sub-Store Files。
- 安装包内置 `mihomo.exe`、`geoip.metadb` 和 MetaCubeXD。
- Linux 支持 systemd 常驻核心、loopback 配置服务和 XFCE 托盘控制。

## 使用

1. 安装并启动 MeshMux。
2. 右键托盘图标，打开配置页面。
3. 填入日常代理订阅；只有确实不使用订阅时才勾选“仅直连模式”。缺少订阅链接且没有有效缓存时，MeshMux 会拒绝保存或启动，不再静默生成全 `DIRECT`。
4. 按需导入 WireGuard 配置，按需启用 Tailscale。
5. 如需 Tailnet 入站，在高级页面按“名称,协议,监听端口,目标地址”填写映射。
6. 保存配置，生成 Windows/mobile profile。
7. Android 端在 mihomo 客户端中导入 mobile profile 链接。

## Tailnet 入站转发

MeshMux 使用补丁版 Mihomo 的 `type: tailscale` 代理，在对应 tsnet 节点的 Tailnet IPv4/IPv6 地址上监听。监听不会绑定 Windows 局域网或公网网卡，来源访问控制继续由 Tailscale ACL/Grants 负责。

`meshmux.local.json` 示例：

```json
{
  "tailscale": {
    "enabled": true,
    "inboundForwards": [
      {
        "name": "windows-ssh",
        "network": "tcp",
        "listenPort": 22,
        "target": "127.0.0.1:22"
      },
      {
        "name": "example-udp",
        "network": "udp",
        "listenPort": 12345,
        "target": "127.0.0.1:12345"
      }
    ]
  }
}
```

- `network` 支持 `tcp` 和 `udp`。
- 同一协议下监听端口不能重复。
- 仅 Windows profile 生成入站转发；mobile profile 不携带这些映射。
- 未配置 `inboundForwards` 时行为与旧版本完全一致，Tailscale 仍按需作为出站使用。
- 配置重载会先关闭旧转发和旧 tsnet 实例，再启动新映射；停止 MeshMux 核心会移除全部 Tailnet 监听。

## 路径

程序目录：

```text
C:\Program Files\MeshMux
```

用户数据目录：

```text
%LocalAppData%\MeshMux
```

用户数据目录保存本地配置、生成的 profile、日志和 mihomo 状态。

升级到带补丁核心的版本时，MeshMux 会在停止旧核心后同步安装包内置的默认 `bin\mihomo.exe`。显式配置的自定义核心路径不会被覆盖；通过 MeshMux 下载功能更新的默认核心也会被保留。

日志会自动按大小轮转。`mihomo.out.log` 和 `mihomo.err.log` 每个文件上限 8 MiB，最多保留 3 个备份；`meshmux.log` 上限 2 MiB，最多保留 2 个备份。启动核心前会清理超限的历史日志，写入日志时会隐藏 URL、密钥、令牌等敏感字段。

## Linux

Linux 可使用 `meshmux run linux` 运行常驻核心，使用 `meshmux serve` 提供仅监听 loopback 的配置页面。仓库中的 `packaging/linux` 包含 systemd 单元、XFCE 登录自启动项和受限 sudoers 部署示例；在其他账户或目录安装前需要按实际环境调整。核心服务与托盘相互独立：退出托盘不会停止代理，无图形会话时也不会额外启动托盘。

使用 `meshmux config-check -config <配置路径>` 可以只读检查配置完整性。命令只输出订阅、缓存、Tailnet 鉴权、WireGuard 和入站映射是否已配置，不启动核心、不开临时端口，也不会输出订阅地址、Auth Key 或私钥。

## 手机端

Android 端使用 mobile profile 接入同一套配置。MeshMux 负责生成可导入的 mobile profile，并通过 Sub-Store 提供同步入口。

## 许可证

MeshMux 使用 MIT 许可证。安装包内置组件见 [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)。

补丁版 Mihomo 基于上游 `v1.19.29`，作为 [MeshMux 固定核心资产](https://github.com/lumokato/MeshMux/releases/tag/mihomo-v1.19.29-meshmux.1) 发布，包含二进制和对应源码包。MeshMux CI 只下载固定资产并校验 SHA-256，不在每次应用发版时重新编译完整核心。
