# Kubernetes MVP 部署基线

`mvp.yaml` 提供当前 MVP 的声明式契约与运行安全基线：

- `SandboxClass`、`Sandbox`、`SandboxClaim` CRD
- gVisor `RuntimeClass`
- 控制面 API Deployment / Service
- 示例租户 namespace
- namespace 级 deny-all NetworkPolicy
- 仅允许 `envd-proxy` 访问沙箱端口

当前仓库的 `internal/operator` 已实现可独立测试的 `SandboxClaim` reconcile 核心，包括：

- 由 `tenantId + idempotencyKey` 生成确定性 Pod 名称
- Pod 已存在和 `AlreadyExists` 的 leader 切换幂等
- envd Ready/Healthy 后回填 Claim Ready
- Deleted/Draining 删除幂等
- gVisor 默认 RuntimeClass 与 runc/gVisor/Kata 白名单

生产接入时，需要用 controller-runtime client 实现 `operator.PodClient`，并在 reconciler 外层完成：

1. watch `SandboxClaim` 变更；
2. 更新 `status` 子资源；
3. 配置 OwnerReference/finalizer；
4. 接入真实配额与 allocation ledger 存储；
5. 接入 envd-proxy 路由注册与健康探测。

部署前应先为每个租户 namespace 创建 deny-all NetworkPolicy，再创建沙箱 Pod。示例清单中的 `sandbox-tenant-example` 仅用于开发验证。
