# 第三方组件

MeshMux 安装包包含以下上游组件。各组件按其上游许可证发布，源码和许可文本以对应仓库为准。

| 组件 | 用途 | 来源 | 许可证 |
| --- | --- | --- | --- |
| mihomo | 代理核心；MeshMux 基于固定上游提交应用 Tailnet 入站转发补丁后构建 | https://github.com/MetaCubeX/mihomo | GPL-3.0 |
| MetaCubeXD | mihomo 面板 | https://github.com/MetaCubeX/metacubexd | MIT |
| geoip.metadb | GEOIP 数据库 | https://github.com/MetaCubeX/meta-rules-dat | GPL-3.0 |

Mihomo 的固定提交、补丁、构建命令和升级流程见 `third_party/mihomo/README.md`；每个 Release 同时发布补丁版二进制和对应源码包。

发布安装包时保留本文件，Release 页面同步标注上述组件来源。
