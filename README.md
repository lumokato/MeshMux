# MeshMux

MeshMux 是 Windows 托盘工具，用 mihomo 管理日常代理、WireGuard、Tailscale 和移动端配置发布。

English README: [README.en.md](README.en.md)

## 功能

- 常驻任务栏，启动、停止和重启 mihomo。
- 开启 Windows 系统代理，支持 TUN 模式。
- 浏览器配置页填写订阅、Sub-Store、WireGuard 和 Tailscale。
- 生成 Windows profile 与 mobile profile。
- 上传 mobile profile 到 Sub-Store Files。
- 安装包内置 `mihomo.exe`、`geoip.metadb` 和 MetaCubeXD。

## 使用

1. 安装并启动 MeshMux。
2. 右键托盘图标，打开配置页面。
3. 填入代理订阅、Sub-Store 地址、后端名和文件名。
4. 按需导入 WireGuard 配置，按需启用 Tailscale。
5. 保存配置，生成 Windows/mobile profile。
6. Android 端在 mihomo 客户端中导入 mobile profile 链接。

## 路径

程序目录：

```text
%LocalAppData%\Programs\MeshMux
```

用户数据目录：

```text
%LocalAppData%\MeshMux
```

用户数据目录保存本地配置、生成的 profile、日志和 mihomo 状态。

日志会自动按大小轮转。`mihomo.out.log` 和 `mihomo.err.log` 每个文件上限 8 MiB，最多保留 3 个备份；`meshmux.log` 上限 2 MiB，最多保留 2 个备份。启动核心前会清理超限的历史日志，写入日志时会隐藏 URL、密钥、令牌等敏感字段。

## 手机端

Android 端使用 mobile profile 接入同一套配置。MeshMux 负责生成可导入的 mobile profile，并通过 Sub-Store 提供同步入口。

## 许可证

MeshMux 使用 MIT 许可证。安装包内置组件见 [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)。
