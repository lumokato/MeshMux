# 安全

MeshMux 会处理代理订阅、内网访问和私有网络凭据。

敏感信息包括：

- 订阅链接。
- 生成后的 profile。
- WireGuard 私钥。
- Tailscale auth key。
- Sub-Store token。
- 运行日志。
- mihomo 生成配置。

凭据进入 Git 历史时，立即吊销并重新生成。

本地配置 API 绑定到 `127.0.0.1`，并使用随机的一次性 token。
