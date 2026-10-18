# MeshMux 0.5.4

## macOS Intel

- 标准 MeshMux.app，支持 Finder / LaunchServices 启动、菜单栏图标、可选登录启动和同一数据目录单实例保护。配置和运行数据留在 Application Support，不写入应用包。
- 内置 tailscale/tailscaled，打包拒绝缺件、不可执行、非 Intel 架构和链接资源；签名后二进制 SHA-256 清单用于安装前与受保护副本复核。
- 设置页显式授权安装 MeshMux 专有 Tailnet LaunchDaemon。可执行文件、身份与 IPC 目录移至受保护的 Library 根目录；识别并保留旧 MeshMux 身份，不覆盖其它 Tailscale 安装。
- 显式启动/停止/重启、登录和断开入口。退出菜单栏停止用户 mihomo 核心并恢复其系统代理，但保持独立 Tailnet 网络服务；停止 Tailnet 服务同时禁用下次开机启动。
- 区分缺组件、服务未安装/未运行、需要登录、NeedsMachineAuth 和连接状态。已有 IP 或 Running 状态不等于实际 peer 连通。
- 安装更新保留受保护恢复点；安装失败停用新 job 并尝试恢复旧作业。普通启动不会静默提权、下载、重新登记或覆盖自启选择。

## 安全与兼容边界

本版 macOS 应用使用 ad-hoc 签名，未做 Developer ID 签名或公证。它能验证包内完整性，但不是发布者身份信任锚；安装前应校验正式 Release 资产 SHA-256，并仅对可信来源应用授予管理员权限。不得全局关闭 Gatekeeper。

Tailscale 组件是 MeshMux 从官方上游标签重构建的产物，不冒称官方签名的 macOS 安装器。mihomo TUN 的特权模式不由本版 Tailnet 服务代管。Windows/Linux 功能保持原有行为，由同版 CI 回归。

本机系统冷启动/注销未包含在不停机验收中；launchd 配置检查和可重复启停不替代真实重启测试。恢复目录和旧身份不会被自动删除。
