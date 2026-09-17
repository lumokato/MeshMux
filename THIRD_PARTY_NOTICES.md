# 第三方组件

MeshMux 安装包包含以下上游组件。各组件按其上游许可证发布，源码和许可文本以对应仓库为准。

| 组件 | 用途 | 来源 | 许可证 |
| --- | --- | --- | --- |
| mihomo | 代理核心 | https://github.com/MetaCubeX/mihomo | GPL-3.0 |
| tailscaled / tailscale | Tailnet 守护进程与 CLI；以上游官方 tag 的源码构建，无任何代码改动 | https://github.com/tailscale/tailscale | BSD-3-Clause |
| MetaCubeXD | mihomo 面板 | https://github.com/MetaCubeX/metacubexd | MIT |
| geoip.metadb | GEOIP 数据库 | https://github.com/MetaCubeX/meta-rules-dat | GPL-3.0 |

核心下载按明确的版本与 SHA-256 校验，默认来源为上游 MetaCubeX/mihomo 的发布资产。

发布安装包时保留本文件，Release 页面同步标注上述组件来源。
