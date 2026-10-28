# macOS 开机即 TUN：root 核心守护（LS-190）

日期：2026-10-28；执行机：taudeMac-Pro，macOS x86_64；对应 Issue LS-190。

## 问题

`com.meshmux.agent` 是登录后的用户 LaunchAgent，没有 TUN 权限；`com.meshmux.tailscaled`
只负责 Tailnet。此前 TUN 可用的唯一方式是成员手动以管理员启动核心（root 的
`meshmux _supervise`）。因此“开机自启已启用”不等于“开机即有 TUN”，每次重启仍要授权。

## 结构

| 组件 | 位置 | 身份 | 职责 |
| --- | --- | --- | --- |
| `com.meshmux.core` | `/Library/LaunchDaemons/com.meshmux.core.plist` | root | `RunAtLoad` + `KeepAlive`，启动 `meshmux _supervise`，负责核心启停与自愈 |
| 受保护配置 | `/Library/Application Support/MeshMux/core/` | root:wheel | `meshmux.local.json`、`profiles/darwin.yaml`、`providers/`、`bin/mihomo`、`state/`、`logs/` |
| `com.meshmux.agent` | `~/Library/LaunchAgents/com.meshmux.agent.plist` | 成员 | 只做菜单栏 UI、系统代理与状态展示；不再拉起核心 |

daemon 的工作目录和 `-config` / `-profile` 都指向受保护副本，启动参数与手动模式相同的
`_supervise`：

```
/Applications/MeshMux.app/Contents/MacOS/meshmux _supervise \
  -config '/Library/Application Support/MeshMux/core/meshmux.local.json' \
  -profile '/Library/Application Support/MeshMux/core/profiles/darwin.yaml'
```

`KeepAlive` 同时覆盖两种故障：核心进程退出时 supervisor 结束，launchd 重新拉起 supervisor；
supervisor 被子进程拖死时同理。托盘崩溃由 LaunchAgent 的 `KeepAlive` 重新拉起。

## 配置归属

- 只有安装/更新流程（显式管理员授权）读取成员目录：把 `meshmux.local.json` 复制为
  root 0600，复制 `providers/` 中的普通文件，再以受保护副本为输入运行
  `meshmux generate ... darwin`。成员目录里的 profile 不被信任、不复制。
- 运行中的 daemon 只读 `/Library/Application Support/MeshMux/core`。`bin/mihomo`、
  `geoip.metadb`、`dashboard/` 由 supervisor 从 `/Applications/MeshMux.app` 内的只读资源
  同步到受保护目录。
- 安装前校验配置中的路径字段：必须是相对路径且不能跳出运行目录。绝对路径（例如指向
  `~/Library/Application Support/MeshMux`）会拒绝安装，避免 root 执行成员可写的二进制或配置。
- 安装/更新脚本拒绝软链接输入、非 root 的 plist、以及其它程序占用的 Label。

## 命令

CLI（安装在 `/Applications/MeshMux.app/Contents/MacOS/meshmux`）：

```
meshmux core-service status                     # 只读，不提权
meshmux core-service plan install|sync|uninstall|start|stop|restart
meshmux core-service install                    # 弹管理员授权
meshmux core-service sync                       # 配置变更后刷新受保护副本并重启核心
meshmux core-service uninstall
```

托盘：核心开关与“重启核心”在已安装守护时改为请求 daemon（管理员授权），不再自己
`runner.Start`。退出托盘不再停止核心或改写系统代理。

脚本（管理员终端）：

```
sudo packaging/macos/install-core-daemon.sh
sudo packaging/macos/uninstall-core-daemon.sh
sh packaging/macos/verify-core-daemon.sh
```

## 首次安装与回滚

- 首次安装需要一次管理员授权。安装脚本在写入前备份现有 plist、配置和 providers 到
  `core/recovery-<时间>`，失败自动回滚并恢复旧 job。
- 从手动模式切换时，脚本结束成员目录中仍由旧 supervisor 托管的 mihomo（端口冲突），
  然后加载 daemon。手动 supervisor 会因核心退出自行结束。
- 卸载：`launchctl disable` + `bootout` + 删除 plist，恢复点为
  `core/recovery-uninstall-<时间>`；此后回到旧的手动管理员授权模式。
- 失败不会删除用户配置、Tailnet 身份或旧的 LaunchAgent。

## 验收证据

`verify-core-daemon.sh` 输出：plist 与 `launchctl print`（state/pid/runs）、进程启动时间
（`ps lstart`）、`utun` 地址、`0/1` 与 `128.0/1` 路由、`curl generate_204`、控制器版本。
冷启动判定：mihomo 的 `lstart` 早于托盘进程的 `lstart`，且 `who` 尚未出现登录会话时
`utun6` 已存在即证明 TUN 在登录前建立。

## 边界

- 未做 Developer ID 签名与公证，安装仍依赖显式管理员授权。
- 停止/启动/重启守护与刷新配置会要求管理员授权；这是刻意的，避免提供无授权的 root 控制通道。
- 未在本记录中声称完成真重启验收的项，见 Issue 的未完成清单与本机证据。
