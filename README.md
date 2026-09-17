# MeshMux
> v0.3.1 已撤回，v0.3.2 是历史修复版本。当前源码发布必须使用 GitHub Actions 显式生成新的版本号并通过确切安装包验收。

MeshMux 是面向 Windows、macOS 与 Linux 桌面/headless 环境的 mihomo 管理工具，用于日常代理、WireGuard 和移动端配置发布。Tailnet 由官方 Tailscale 客户端负责，MeshMux 不再内嵌 tsnet 节点。

Windows 安装版将核心注册为自动启动的系统服务，未登录桌面时也会运行；托盘在用户登录后启动，负责当前用户的系统代理、配置页面和核心控制。服务只读取 `ProgramData\MeshMux` 中受系统保护的运行快照，用户配置固定保存在 `%LocalAppData%\MeshMux`；升级时若该文件仍是安装器空模板，MeshMux 会依次尝试恢复旧 `%AppData%\MeshMux` 配置和最后一次成功的服务快照。启动或重启服务时会先提权生成新快照，并拒绝用空模板覆盖已有真实配置。服务使用独立的 Tailnet 身份目录，首次启动时通过配置中的 Auth Key 重新登录，不迁移旧用户核心的登录缓存。安装、卸载或人工启停服务时才需要 UAC，正常开机不需要确认。

English README: [README.en.md](README.en.md)

## 开发与版本入口

源码结构、修复记录与剩余风险见 [架构说明](docs/ARCHITECTURE.md)；当前源码、核心配对、历史归档和构建规则见 [开发指南](docs/DEVELOPMENT.md)。本地未发布改动不等于已安装版本，历史安装包不得作为默认启动或打包来源。

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
3. 填入日常代理订阅；只有确实不使用订阅时才勾选“仅直连模式”。缺少订阅链接且没有有效缓存时，MeshMux 仍会启动并显示降级状态，代理组不会伪装成可用节点。
4. 按需导入 WireGuard 配置。
5. 保存配置，生成 Windows/mobile profile。
6. Android 端在 mihomo 客户端中导入 mobile profile 链接。

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

升级时，MeshMux 会在停止旧核心后同步安装包内置的默认 `bin\mihomo.exe`。显式配置的自定义核心路径不会被覆盖；通过 MeshMux 下载功能更新的默认核心也会被保留。

日志会自动按大小轮转。`mihomo.out.log` 和 `mihomo.err.log` 每个文件上限 8 MiB，最多保留 3 个备份；`meshmux.log` 上限 2 MiB，最多保留 2 个备份。写入日志时会隐藏 URL、密钥、令牌等敏感字段；正常启动不再重新扫描和脱敏整套历史日志。

## Linux

Linux 可使用 `meshmux run linux` 运行常驻核心，使用 `meshmux serve` 提供仅监听 loopback 的配置页面。仓库中的 `packaging/linux` 包含 systemd 单元、XFCE 登录自启动项和受限 sudoers 部署示例；在其他账户或目录安装前需要按实际环境调整。核心服务与托盘相互独立：退出托盘不会停止代理，无图形会话时也不会额外启动托盘。

使用 `meshmux config-check -config <配置路径>` 可以只读检查运行所需输入。命令只输出订阅、缓存和 WireGuard 是否已配置，不启动核心、不开临时端口，也不会输出订阅地址或私钥。缺少运行输入时输出 `degraded`，但不阻止管理器或核心启动。

## 手机端

Android 端使用 mobile profile 接入同一套配置。MeshMux 负责生成可导入的 mobile profile，并通过 Sub-Store 提供同步入口。

## 许可证

MeshMux 使用 MIT 许可证。安装包内置组件见 [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)。

Mihomo 核心默认来自上游 MetaCubeX/mihomo，下载按明确的版本和 SHA-256 校验；运行中的 Windows 服务核心单独保存在受保护的 `ProgramData\MeshMux\bin\mihomo.exe`，核心更新通过显式更新动作替换，不会因为安装器升级或托盘启动被静默降级。指向旧补丁仓库（lumokato/MeshMux）且未锁定 SHA-256 的核心选择，会在应用默认值时迁移到上游。
