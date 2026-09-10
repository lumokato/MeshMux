# MeshMux 0.3.5

内置核心升级到 v1.19.30-meshmux.1，基于 mihomo v1.19.30，保留 Tailnet 入站转发与就绪修复。管理程序沿用 0.3.4 的启动和监督实现。

- Windows/Linux 核心与对应源码一起发布并校验 SHA-256。
- 新安装使用新版内置核心；已有 Windows 服务的独立核心不由安装器覆盖，请使用应用的核心更新功能切换。
- 不要求退出 Tailnet，不迁移或删除身份数据。

验证：Windows/Linux 核心构建、重点测试与 vet 通过。LXC IPv4 TUN HTTPS、显式代理 HTTPS、Tailnet 出站 SSH banner 与服务重启通过，机器身份密钥保持一致。

已知边界：测试环境 AAAA 查询超时在旧 .29 和新 .30 均可复现；全量核心测试的入站协议测试超时也在未修改的上游 .30 复现。未宣称完整 SSH 登录、Tailnet 入站实机、整机重启、Windows 升级或睡眠恢复验收通过。
