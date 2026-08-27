# Kata 强隔离档节点运行时部署

本文档描述如何为 kubebox 沙箱节点池 B 部署 Kata 运行时，用于「强隔离档」（敏感/合规/多租户场景）。与 gVisor（默认档，systrap 无需 KVM）不同，**Kata 3.x（Dragonball VMM）依赖 KVM**，因此 Kata 节点必须是裸金属或支持直通 KVM 的虚机。

## 1. 运行时矩阵

| 隔离档 | 运行时 | VMM | 是否需 KVM | containerd handler | 节点池 |
| --- | --- | --- | --- | --- | --- |
| 可信级 | runc | — | 否 | runc | 池 A |
| 不可信级（默认） | gVisor | systrap | 否 | runsc | 池 A |
| 强隔离级 | Kata 3.6 | Dragonball | 是 | kata | 池 B（裸金属） |

## 2. 节点前置条件

- Kubernetes 1.29 +，containerd 1.7.x（CRI）。
- 节点已启用 KVM：`ls -l /dev/kvm` 存在且可读。
- 节点已打标签/污点，隔离降级防护依赖它：

```bash
kubectl label node <node> sandbox=vm kubebox.io/pool=kata
kubectl taint node <node> sandbox=vm:NoSchedule
```

## 3. 安装 Kata 运行时（Dragonball）

Kata 3.x 默认 VMM 建议配置为 Dragonball（轻量 VMM，内存开销 ~40MB/Pod）。

```bash
# 以 kata-containers 官方安装（各发行版适配，以下为 Ubuntu 示例）
ARCH=amd64
KATA_VERSION=3.6.0
# 安装 kata-runtime / kata-containerd-shim / dragonball VMM
curl -sSL https://github.com/kata-containers/kata-containers/releases/download/${KATA_VERSION}/kata-static-${KATA_VERSION}-${ARCH}.tar.xz \
  -o /tmp/kata.tar.xz
tar -xJf /tmp/kata.tar.xz -C /
```

> 生产建议使用发行版包或公司内网镜像仓库，锁定版本并过安全补丁评审（Kata = Apache-2.0，许可证已登记）。

## 4. containerd 注册 kata handler

编辑 `/etc/containerd/config.toml`，在 `[plugins."io.containerd.grpc.v1.cri".containerd.runtimes]` 下新增：

```toml
[plugins."io.containerd.grpc.v1.cri".containerd.runtimes.kata]
  runtime_type = "io.containerd.kata.v2"
  privileged_without_host_devices = false
  pod_annotations = ["io.katacontainers.*"]
  [plugins."io.containerd.grpc.v1.cri".containerd.runtimes.kata.options]
    ConfigPath = "/opt/kata/share/defaults/kata-containers/configuration-dragonball.toml"
```

重启并校验：

```bash
systemctl restart containerd
ctr runtime list   # 应包含 io.containerd.kata.v2
```

## 5. 创建 RuntimeClass

```bash
kubectl apply -f deploy/kubernetes/runtime.yaml
kubectl get runtimeclass kata gvisor runc
```

RuntimeClass `kata` 携带 `nodeSelector: sandbox=vm` 与对应 toleration，确保 Kata 沙箱只落到有 KVM 的节点池 B，避免调度到无 KVM 节点导致降级。

## 6. SandboxClass 声明 Kata 档

`SandboxClass` CRD 已允许 `runtimeClassName ∈ {runc, gvisor, kata}`。强隔离场景创建示例：

```yaml
apiVersion: sandbox.kubebox.io/v1alpha1
kind: SandboxClass
metadata:
  name: strong
spec:
  runtimeClassName: kata
  security:
    runAsNonRoot: true
    readOnlyRootFilesystem: true
    seccompProfile: RuntimeDefault
```

## 7. 验证

```bash
# 以 kata 运行时跑一个临时沙箱，确认 Pod 使用 Kata（guest 内核启动）
kubectl run -it --rm kata-check --image=busybox --overrides='{"spec":{"runtimeClassName":"kata","nodeSelector":{"sandbox":"vm"}}}' -- uname -r
# 期望输出 guest 内核版本，而非宿主机内核。

# 确认无 KVM 节点不会误调度（应 Pending 或回退策略触发）
kubectl get events -n sandbox-system | grep -i kata
```

## 8. 回退与降级策略

- Kata 节点池无可用资源时，强隔离档**不应静默降级到 gVisor/runc**；由 Admission Webhook 校验 `runtimeClassName ∈ 允许集合`，并由 SandboxClass 的 nodeSelector 兜底约束（继承《系统设计》§3.1.5 约束）。
- 若目标 IaaS 无嵌套虚拟化，Kata 节点池必须为裸金属；否则该档在容量规划中自动回退 gVisor，需在容量与 SLA（99.9%）上走跨界确认。

## 9. 与 gVisor 的关系

gVisor（systrap）是默认不可信档，无需 KVM，已在 `deploy/kubernetes/mvp.yaml` 定义 RuntimeClass。Kata 仅在「敏感/合规/多租户」强隔离档启用，两者可共存于不同节点池，由 SandboxClass + nodeSelector 区分。
