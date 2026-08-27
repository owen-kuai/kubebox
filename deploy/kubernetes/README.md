# Kubernetes MVP 部署基线

`mvp.yaml` 提供当前 MVP 的声明式契约与运行安全基线：

- `SandboxClass`、`Sandbox`、`SandboxClaim` CRD
- gVisor `RuntimeClass`
- 控制面 API Deployment / Service
- 示例租户 namespace
- namespace 级 deny-all NetworkPolicy
- 仅允许 `envd-proxy` 访问沙箱端口

`controller.yaml` 提供 SandboxClaim Operator 的运行清单：

- `kubebox-controller` ServiceAccount
- `kubebox-controller-role` ClusterRole（SandboxClaim + Pod + leader election lease 最小权限）
- ClusterRoleBinding 绑定到 `sandbox-system` 命名空间
- controller Deployment（metrics :8080 / healthz :8081，非 root、只读根文件系统、drop ALL）
- 可选 metrics Service（Prometheus 抓取）

`runtime.yaml` 提供 Kata 强隔离档 RuntimeClass：

- `kata` RuntimeClass（`handler: kata`，nodeSelector 绑定裸金属 KVM 节点池 + toleration）
- 节点标签/污点参考命令（`sandbox=vm`、`kubebox.io/pool=kata`）
- 节点运行时部署与验证见 `docs/kata-runtime-deployment.md`

`envd-proxy.yaml` 提供公网数据面边界：

- `envd-proxy` ServiceAccount / Service / Deployment（replicas=2，非 root、只读根文件系统、drop ALL）
- 环境变量从 `envd-proxy-secret` 注入（`KUBEBOX_TOKEN_SECRET` / `KUBEBOX_ADMIN_SECRET`）
- 控制面专用路由管理 API：`/internal/v1/routes`（POST 注册 / GET 列出 / DELETE 注销）
- 后端健康探测：周期性探测 envd `/healthz`，连续失败自动注销路由

构建镜像：

```bash
docker build -f deploy/docker/controller.Dockerfile -t kubebox-controller:dev .
docker build -f deploy/docker/envd.Dockerfile      -t kubebox-envd:dev .
docker build -f deploy/docker/envd-proxy.Dockerfile -t kubebox-envd-proxy:dev .
```

部署顺序：

```bash
kubectl apply -f deploy/kubernetes/mvp.yaml          # CRD + gVisor RuntimeClass + 控制面
kubectl apply -f deploy/kubernetes/controller.yaml   # SandboxClaim Operator + RBAC
kubectl apply -f deploy/kubernetes/runtime.yaml      # Kata RuntimeClass（需先部署节点运行时）
kubectl apply -f deploy/kubernetes/envd-proxy.yaml   # 数据面边界（需先创建 envd-proxy-secret）
```

当前仓库的 `internal/operator` 已接入 controller-runtime：

- `kubeapi.SandboxClaim` 提供 CRD Go 类型和 Scheme 注册
- `operator.SetupManager(mgr, registrar)` 注册 `SandboxClaim` Controller，可注入 `RouteRegistrar` 做数据面路由联动
- `KubePodClient` 将 reconcile 的 Pod 副作用映射到 Kubernetes API（注入 sandbox id、envd 双端口 :50051/:8080、emptyDir `/sandbox` 卷、只读根文件系统）
- `SandboxClaimReconciler.syncRoute`：Ready 时向 envd-proxy 注册路由、删除时注销（`KUBEBOX_ENVD_PROXY_URL` + `KUBEBOX_ADMIN_SECRET` 注入，未配置则纯控制面运行）
- fake client 测试覆盖 CRD 读写、Pod 创建、status 回填、finalizer 与路由注册/注销
- `SandboxClaim` reconcile 核心覆盖确定性 Pod 名称、AlreadyExists 幂等、Ready 探活、删除回收和运行时白名单

`internal/dataplane` 提供数据面边界：

- `TokenIssuer` 签发/校验短期 scope JWT；`Proxy` 校验凭证、剥离、注入 sandbox id + 派生 scope、反向代理
- `RouteRegistry` 沙箱路由表（Set/Get/List/Unregister，并发安全）
- `Admin` 路由管理 API（共享密钥常量时间比较鉴权）
- `HealthMonitor` 后端健康探测（连续失败阈值注销）
- `RouteClient` 控制面/operator 侧的路由注册客户端（实现 `RouteRegistrar`）

生产接入前仍需补齐：

1. 为租户命名空间和 envd-proxy 配置生产 NetworkPolicy；
2. 接入 Webhook 校验 SandboxClass/runtimeClassName。

部署前应先为每个租户 namespace 创建 deny-all NetworkPolicy，再创建沙箱 Pod。示例清单中的 `sandbox-tenant-example` 仅用于开发验证。
