# MeshMux 0.5.2

修复 DNS 解析卡死/无应答，以及 DNS 监听器对局域网暴露的问题。

## fallback 直连导致解析卡死

旧版默认 fallback 是裸 `https://dns.google/dns-query`（无 `#PROXY`），查询走直连；dns.google 在国内不可达，mihomo 每次都要等 fallback 超时：被墙域名（google.com、chatgpt.com、openai.com 等）DNS 直接无应答，其余非 CN 域名解析也被拖慢约 4 秒。

- 加载配置时自动把与旧默认值完全一致的 fallback 迁移为 `https://dns.google/dns-query#PROXY`，存量配置升级即自愈，无需手动改配置。
- 只迁移与旧默认值完全一致的条目；任何自定义 fallback 保持原样。

## DNS 监听器收窄到回环

Windows 此前生成 `listen: 0.0.0.0:1053`，且防火墙对核心有入站放行，局域网内任何设备都能使用本机的 DNS 解析器（并拿到代理视角的解析结果）。桌面 profile（Windows/Linux/macOS）现在统一生成 `listen: 127.0.0.1:1053`；TUN 劫持的 DNS 由核心在带内应答，不受此改动影响。

## 验证

- `go vet ./...`、`go test ./...` 通过；新增 Windows 回环绑定、旧 fallback 迁移、自定义 fallback 保留三个用例。
- Windows 实机验收：chatgpt.com / google.com 解析从 10 秒无应答降至约 300ms 且返回干净地址；OpenAI 系域名经代理解析、连接走代理出口；`strict-route` 开启时 Tailnet（tailscaled 在线、对端 ping）保持正常。
