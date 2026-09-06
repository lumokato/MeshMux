# MeshMux 0.3.1

本次正式版集中修复可靠性、配置处理和升级安全问题。

- 配置、缓存、生成文件和服务快照采用原子替换，增加跨进程互斥，避免并发写入或失败覆盖旧文件。
- 修复 Windows 服务快照回滚、核心重启的进程归属及日志脱敏；Linux 保留显式 MESHMUX_HOME 布局。
- 使用标准 YAML 解析订阅，修复 Sub-Store 文件选择、名称转义和查询参数保留。
- 组件下载强制校验 SHA-256，限制解压规模，拒绝路径穿越和符号链接；面板替换失败可恢复旧文件。
- 固定核心升级为 v1.19.29-meshmux.2，修复 Tailnet 后端尚未就绪就开始拨号和缺少对应 IP 地址时的错误处理。
- 整理架构和开发文档，隔离历史安装包，构建不再复用旧输出或隐式版本号。

提供 Windows x64 安装包、Linux amd64 应用包及 SHA-256 校验文件。核心二进制与对应源码见 [固定核心资产](https://github.com/lumokato/MeshMux/releases/tag/mihomo-v1.19.29-meshmux.2)。面板固定为 MetaCubeXD v1.273.0；核心、面板和 GeoIP 均通过发布流程中的内容哈希校验。

已有服务升级保留独立数据目录与 Tailnet 身份，不需要主动退出登录。操作前仍建议保留受保护的配置和身份备份。

验证边界：管理程序已通过 Windows 冷启动和 Linux LXC 重启、实际 SSH 与代理访问；Windows/Linux 单元测试与构建、Linux race 已通过。Windows race、休眠恢复、首次安装和 Linux 图形桌面交互未列为本次已完成的真实环境验收。
