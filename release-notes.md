# MeshMux 0.5.3

修复 tailnet 服务域名经系统代理访问时被错误送进日常代理、最终超时不可达的问题。

## 背景

0.4.0 把 tailnet 归属迁到官方 tailscaled 时，旧 tsnet 模型的 tailnet 路由（TS 出站、`tailscale.routes`/`tailscale.domains` 生成的规则）被一并移除，只留下 TUN 层的 `route-exclude-address`。这条豁免只对被 TUN 接管的流量有效；认系统代理（混合端口）的流量不经过 TUN 路由，规则链里没有任何条目认识 tailnet 网段——tailnet 服务域名（公网 DNS 直接解析到 100.64/10 地址）一路漏到 `MATCH,PROXY`，由远端代理节点去连够不着的 tailnet 地址，表现为超时。

## 修复

- tailscale 启用时，桌面目标（Windows/Linux/macOS）自动生成 `tailnet` 直连出站并绑定 tailscaled 网卡（Windows `Tailscale`、Linux `tailscale0`；macOS 的 utun 名是动态分配的，保持不绑定、走系统路由表）。只配 DIRECT 规则不够：mihomo 的 auto-detect-interface 会把 DIRECT 拨号绑到默认网卡，tailnet 地址照样超时。
- 规则链在 `GEOIP,CN` 之前生成 `IP-CIDR,100.64.0.0/10,tailnet` 与 `IP-CIDR6,fd7a:115c:a1e0::/48,tailnet`，不带 `no-resolve`：混合端口来的域名请求解析后即可命中，解析开销与现状相同（`GEOIP,CN` 本来就会解析这些漏网连接）。
- 任何解析到 tailnet 网段的域名自动覆盖，无需逐个配置域名；手机目标不生成（没有 tailscaled）。

## 验证

- `go vet ./...`、`go test ./...` 通过；新增用例覆盖 Windows/Linux 绑定、规则顺序、mobile 豁免与开关关闭场景。
- Windows 实机验收：经混合端口访问 tailnet 服务（dokploy.ops.lumokato.com → 100.90.26.22）从超时恢复为 200，连接表规则命中 `IPCIDR 100.64.0.0/10`、出站链 `tailnet`；TUN 路径行为不变。
