# kubebox

通用 Kubernetes Agent Sandbox 平台 —— 为 AI Agent 提供安全、快速、有状态的代码执行隔离环境。

## 核心能力

- **三档隔离**：runc（可信级）/ gVisor（不可信默认，systrap 无需 KVM）/ Kata（强隔离级，Dragonball microVM）
- **e2b 兼容七接口**：lifecycle / files / commands / process / pty / network / code-interpreter
- **声明式生命周期**：自研 CRD + Operator（资源模型对标 kubernetes-sigs/agent-sandbox），预热池亚秒分配
- **规模化**：支撑 1000 个并发沙箱实例（gVisor 默认档隔离层内存 ≤ 15GB）

## 架构文档

| 文档 | 说明 |
| --- | --- |
| [资料摘要](docs/material_digest.md) | 5 份原始资料的逐章精读摘要（G1） |
| [行业调研报告](docs/research_report.md) | 业内实现盘点与加权对比（G2） |
| [高层架构设计](docs/高层架构设计.md) | 业务边界、MVP 范围、功能清单（G3） |
| [系统设计](docs/系统设计.md) | 模块、接口、数据、可靠性、性能与可观测设计（G4） |
| [UserStory](docs/UserStory.md) | 角色、场景、验收标准与非功能需求（G4） |
| [部署设计](docs/部署设计.md) | 环境、拓扑、流水线、容量、成本与应急（G5） |
| [安全设计](docs/安全设计.md) | 威胁模型、IAM、数据安全、运行时防护与合规（G5） |

## 隔离技术栈

| 隔离档 | 运行时 | 启动 | 内存 | 适用 |
| --- | --- | --- | --- | --- |
| 可信级 | runc | 毫秒级 | ~0 | 内部可信代码 |
| 不可信级（默认） | gVisor (systrap) | 毫秒级 | ~15MB/沙箱 | 用户上传代码、无 KVM |
| 强隔离级 | Kata (Dragonball) | ~150ms | ~40MB + overhead | 多租户/敏感/合规 |

## MVP 实现

当前已提供可运行的 Go 控制面垂直切片：

- Sandbox 创建、查询、Drain、Deleted 生命周期
- 租户 / owner 配额与 allocation ledger 幂等释放
- `Idempotency-Key` 与请求指纹冲突保护
- HTTP API：`/healthz`、`/api/v1/sandboxes`
- Kubernetes 声明式基线：`deploy/kubernetes/mvp.yaml`，包含 Sandbox/SandboxClass/SandboxClaim CRD、gVisor RuntimeClass、控制面 Deployment、租户 deny-all NetworkPolicy

```bash
go test ./...
go run ./cmd/kubebox                       # 控制面（默认内存治理边界）
go run ./cmd/envd --sandbox-id sbx-1 --root /tmp/sbx-1   # 沙箱内 envd 主进程
go run ./cmd/envd-proxy                    # 公网数据面边界（需 KUBEBOX_TOKEN_SECRET/ADMIN_SECRET）
kubectl apply -f deploy/kubernetes/mvp.yaml        # CRD + gVisor RuntimeClass + 控制面
kubectl apply -f deploy/kubernetes/controller.yaml # SandboxClaim Operator + RBAC
kubectl apply -f deploy/kubernetes/runtime.yaml    # Kata 强隔离档 RuntimeClass
kubectl apply -f deploy/kubernetes/envd-proxy.yaml # 数据面边界（需先建 envd-proxy-secret）
```

已接入的实现骨架还包括：

- `internal/operator`：controller-runtime `SandboxClaim` Reconcile、Kubernetes Pod adapter（注入 sandbox id、envd 双端口与 emptyDir 沙箱卷）、finalizer/status 回填、`syncRoute` 路由联动（Ready 注册 / 删除注销）
- `internal/dataplane`：`TokenIssuer`（短期 scope JWT 签发/校验）、`Proxy`（凭证剥离 + sandbox id/scope 注入 + 反向代理）、`RouteRegistry`（沙箱路由表）、`Admin`（控制面路由管理 API）、`HealthMonitor`（后端健康探测注销）、`RouteClient`（operator 侧路由注册客户端）
- `internal/envd`：envd 最小 gRPC 执行服务 + e2b 风格 HTTP 门面（`/healthz`、`/commands`、`/files/read|write`）、scope 与 sandbox identity 校验、进程内 `ProcessExecutor`（限定沙箱 root 内执行与文件 IO、路径穿越/符号链接逃逸防护、超时与输出上限）、`MemoryExecutor`（测试用）
- `cmd/envd`：沙箱容器主进程（gRPC :50051 + HTTP :8080 双协议，共享 ProcessExecutor，信号优雅退出）
- `cmd/envd-proxy`：数据面边界主进程（凭证校验 + 路由管理 API + 健康探测，信号优雅退出）
- `internal/persistence`：PostgreSQL/MySQL 方言 SQL、配额原子条件更新、allocation CAS 事务骨架、内存版 `MemoryGovernanceStore`、`OpenSQLStore`（pgx/mysql 驱动 + DSN 解析 + 咨询锁迁移）

`internal/persistence` 已提供 `GovernanceStore` 接口贯穿到 HTTP 控制面：`sandbox.Store` 可注入该边界，配额/分配/幂等记账默认由 `MemoryGovernanceStore` 承接（进程默认即运行在治理边界上），`SetQuota`/`Create` 预留/`Drain` 释放均通过治理层原子条件更新与幂等 CAS 完成，内存 map 仅作读投影。接入真实数据库时设置 `KUBEBOX_DATABASE_URL`（`postgres://` 或 `mysql://`），启动时 `OpenSQLStore` 自动建连、`MigrateLocked`（pg_advisory_lock / GET_LOCK）迁移并替换内存边界。

数据面已形成完整可执行链路：`envd-proxy`（公网边界校验短期 scope JWT、剥离凭证、经 `/internal/v1/routes` 注册路由、健康探测注销死路由）→ 注入 sandbox id 与派生 scope → `envd` HTTP 门面（可信容器内复核身份/scope）→ `ProcessExecutor`（沙箱 root 内真实命令与文件 IO）。`cmd/envd` 以主进程嵌入沙箱容器（镜像 `deploy/docker/envd.Dockerfile`），`KubePodClient` 创建 Pod 时注入 sandbox id、挂载 emptyDir `/sandbox` 并暴露 gRPC/HTTP 双端口；`cmd/envd-proxy` 以独立 Deployment 运行（镜像 `deploy/docker/envd-proxy.Dockerfile`）。路由由 operator 在 SandboxClaim Ready/删除时通过 `RouteClient` 自动注册/注销（`KUBEBOX_ENVD_PROXY_URL` + `KUBEBOX_ADMIN_SECRET`）。

Kata 强隔离档已补齐：`deploy/kubernetes/runtime.yaml` 定义 Kata RuntimeClass（nodeSelector 绑定裸金属 KVM 节点池 + toleration），节点运行时部署见 `docs/kata-runtime-deployment.md`。

## 关键决策

- 隔离默认档：默认 gVisor + 敏感场景升级 Kata（三档分层）
- 控制面：自研对标 agent-sandbox，接口对齐 e2b 七接口事实标准
- 部署形态：自建 K8s 通用设计，不绑定云厂商（containerd CRI）
