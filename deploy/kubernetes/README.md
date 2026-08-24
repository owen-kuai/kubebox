# Kubernetes MVP 部署基线

`mvp.yaml` 提供当前 MVP 的声明式契约与运行安全基线：

- `SandboxClass`、`Sandbox`、`SandboxClaim` CRD
- gVisor `RuntimeClass`
- 控制面 API Deployment / Service
- 示例租户 namespace
- namespace 级 deny-all NetworkPolicy
- 仅允许 `envd-proxy` 访问沙箱端口

当前仓库的 `internal/operator` 已接入 controller-runtime：

- `kubeapi.SandboxClaim` 提供 CRD Go 类型和 Scheme 注册
- `operator.SetupManager(mgr)` 注册 `SandboxClaim` Controller
- `KubePodClient` 将 reconcile 的 Pod 副作用映射到 Kubernetes API
- fake client 测试覆盖 CRD 读写、Pod 创建、status 回填和 finalizer
- `SandboxClaim` reconcile 核心覆盖确定性 Pod 名称、AlreadyExists 幂等、Ready 探活、删除回收和运行时白名单

生产接入前仍需补齐：

1. 使用真实 controller manager 启动 Operator Deployment；
2. 为租户命名空间和 envd-proxy 配置生产 NetworkPolicy；
3. 接入真实配额与 allocation ledger 存储；
4. 接入 envd-proxy 路由注册与健康探测；
5. 接入 Webhook 校验 SandboxClass/runtimeClassName。

部署前应先为每个租户 namespace 创建 deny-all NetworkPolicy，再创建沙箱 Pod。示例清单中的 `sandbox-tenant-example` 仅用于开发验证。
