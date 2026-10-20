# Verified bot 入口契约

这是默认关闭的诊断配置；本批未启用线上规则。头名固定为 `X-OpenNavo-Verified-Bot`。

| 配置 | 默认/语义 |
|---|---|
| `OPENNAVO_CF_VERIFIED_BOT_ENABLED` | `false`；仅精确小写 `true` 启用 Caddy 保留路径 |
| `NUXT_CF_VERIFIED_BOT_ENABLED` | compose 从同一主开关传入；Nitro 必须显式检查，不能仅看头名是否非空 |
| `NUXT_VERIFIED_BOT_HEADER` | compose 固定 `X-OpenNavo-Verified-Bot`；仅名字，不代表开启 |
| `NUXT_TRUSTED_PROXIES` | 沿用 Caddy 的 socket peer 信任配置；不能信任整个任意来源网段 |

Caddy 三个站点共用清洗片段。关闭、开关非法或实际连接 peer 不属于列明的 Cloudflare IPv4/IPv6 CIDR 时，删除所有同名头；没有通过 XFF、CF-Connecting-IP 或 UA 反推可信来源。开启且来自 CF peer 时原样保留多值头，避免将重复值洗成单条 `true`。源站其余访问规则不变。

Nitro 在共享缓存过滤头之前捕获请求，使用原始 `rawHeaders`：主开关启用、socket peer 为可信 Caddy、头恰好一条且值精确为 `true`，才标记 `verified` / `verificationBasis=cf_client_bot`。一条 `false`、缺失、重复、逗号拼接、大小写或其他非法值不提升状态；依 UA 仍是 `ua_only` 或 `unknown`，basis 为 `none`。`false` 不代表恶意，CF known-good 标记不能证明 UA 声称的引擎身份。它不得改变身份认证、权限、WAF 或速率限制。Nitro 由 FE H3 实现。

启用前由负责部署者另行取得授权并确认：本站 Cloudflare Request Header Transform 对每个请求**覆盖**写入 `to_string(cf.client.bot)` 的 true/false；实际入口链和防绕过已核实，包括经其他代理/Cloudflare 路径的请求不能绕过本站覆盖规则。CIDR 只能识别来源网络，不能独自证明某个 zone 的规则已执行。未满足任何一项就保持关闭。若额外负载均衡/NAT 隐藏连接来源，或 PROXY protocol 能改写 peer，不以转发头补信任，须先重新核实入口设计。

CIDR 来自 [Cloudflare IPv4](https://www.cloudflare.com/ips-v4/) 和 [IPv6](https://www.cloudflare.com/ips-v6/)，核对于 2026-10-20，更新需要配置审查/重载。请求路径没有 DNS/HTTP 校验。Caddy 使用 [remote_ip matcher](https://caddyserver.com/docs/caddyfile/matchers#remote-ip) 与 [request_header 删除操作](https://caddyserver.com/docs/caddyfile/directives/request_header)。本地测试只证明配置行为，不证明线上 CF 覆盖规则已部署。

本地隔离验证：

```sh
python3 ops/caddy/verify-bot-ingress.py
python3 -I -m unittest discover -s ops -p test_verified_bot_config.py
```

第一条编译仅标准库的 Go 测试，使用已安装的 Caddy 2.11.6 镜像（缺失就失败，不自动拉取），在 `--network none`、只读容器中启动实际 Caddy 与本地 echo upstream。生产路由和过滤器保持，只有 upstream 改为 loopback；可信来源测试将 CF matcher 替换为单个测试 loopback IP，对照测试仍使用真实 CIDR 并拒绝本地来源。验证默认/false/非法/开启、两种实际 peer、伪造转发头、缺失/true/false/非法/重复值、web/API/admin/MCP 路由和最终 429。镜像 Caddy 带 NET_BIND_SERVICE 文件能力，测试容器仅保留该能力，不开放端口、不接生产网络。

第二条仅渲染既有虚构 compose fixture，核对两端开关一致、头名固定、web 未发布端口；不启动服务、不读取部署凭据。源站指标不包含边缘缓存命中或边缘阻断；没有边缘观测时填 unavailable，不与源站次数相加。
