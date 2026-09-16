# MeshMux 0.3.8

自 0.3.6 以来的合并发布（0.3.7 只在 dev 上构建，未单独发布）。这一版把 Tailnet 从自维护的转发实现换成官方 tailscale 客户端，补上 macOS 支持，最后修掉「组件放错目录、服务空转重启」那条链。

## 自 0.3.6 以来的主要变更

- **Tailnet 改用官方 tailscale**：内置并监督上游 tailscaled，界面可见 tailnet 状态；headless 运行也会拉起守护进程。
- **新增 macOS 支持**：runner、CLI 与系统代理设置接入 darwin，前台/后台与开机自启按平台分流。
- **发布流水线重建**：Windows 安装包随附上游 tailscale 组件与 wintun，核心与组件哈希在 CI 校验；推送 dev 只构建，打 `v*` 标签才发布。

## 0.3.8 修复

安装器把 mihomo、tailscaled、tailscale、wintun 放在自己身边，而系统服务是按数据目录解析 `bin/` 的，于是只有 mihomo.exe 曾经到过那里：tailscaled 每次启动都报「未找到」，服务每 5 秒重试一次，而每次重试还会把 mihomo 核心一起重启。残留的核心一直占着 mixed 与 controller 端口，自己那份核心反而绑不上、立刻退出，再喂回同一个循环。

- config：补上 tailscale 三件套与 wintun.dll 的内置路径。
- config：补上 `DefaultTailscaleCLIPath`，与其他默认路径对称。
- runner：把 tailscale 三件套和 wintun 同步进运行目录，逻辑对齐 `prepareMihomo`；已存在的组件不覆盖。
- runner：回收「由另一条路径启动、却占着本配置端口」的 mihomo 实例。原先只比绝对路径，永远看不见它们，端口就这么一直被占着。
- runner：核心被搬到别处时一并铺上 wintun.dll；少了它，复制出来的 mihomo 会静默丢掉 TUN。
- service：改为按组件各自退避（5 秒起、逐次翻倍，上限 5 分钟），不再固定间隔重试；tailscale 失败也不会再连带重启 mihomo 核心。
- service：准备快照时把内置 tailscale 组件装进数据目录，缺组件时跳过而不是整体失败。
- generator：配置里启用了 tailscale、但目标 profile 表达不出来时给出警告，不再无声丢弃。
- build：开发版版本号提到 0.3.8。

## 验证

- `go build ./...`、`go vet`（Windows 与 Linux 自有包）通过；`go test ./...` 10/10 通过，含服务退避相关用例。
- 原故障在实机复现：升级前 `service.log` 中每 5 秒一条 tailscaled「未找到」。

## 已知边界

- 未宣称完整重装、整机重启、睡眠恢复验收通过。
- 若机器上已存在被 wintun 卡死的残留核心进程，`taskkill` 无法结束它，需要重启才能清掉；新代码会在服务启动时自动回收仍可回收的残留实例。
