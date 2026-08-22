# AICoding 架构设计 · UserStory

> 本文档为《AICoding 架构设计》核心产物之一，对应**UserStory 模板**，定位为**产品需求与用户故事**。
> 上游输入：《高层架构设计》（G3 冻结）中的需求概要、行业调研、业务架构、产品原型 + 《资料摘要》（G1 冻结，含 34 条用户故事 US-1~34 + 5 角色 + P0/P1/P2 优先级）；
> 下游输出：驱动《系统设计》《部署设计》《安全设计》的具体功能实现。

> **工具说明**：复用《高层架构设计》的部分结果产物（需求边界、产品模块全景图、功能清单、产品原型）。

> **事实 / 假设 / 建议 标记约定**：全文以 `【事实】`（来自 G1/G2 冻结资料）、`【假设】`（基于事实的合理推断）、`【建议】`（待下游压测/校验校准的目标值）三种标签区分；`【经中间确认】` 表示经阶段内中间确认由用户裁决的决策。

---

## 0. 元信息：修订记录

```yaml
标题: 通用 Agent Sandbox（Kubernetes）- UserStory v1.0
版本: v1.0
状态: Draft   # Draft | Reviewing | Approved | Deprecated
创建日期: 2026-08-18
最后更新: 2026-08-18
作者: product-story-designer（顾全景）
评审人:
  - team-lead（主理人）

关联文档:
  上游输入:
    - 高层架构设计: .workbuddy/output/高层架构设计.md（G3 已冻结）
    - 资料摘要: .workbuddy/output/material_digest.md（G1 已冻结，含 34 条 US）
    - 业务原始诉求: "基于 agent-runtime-sandbox-design.md 调研业内实现，设计通用 K8s Agent Sandbox，支撑 1000 并发请求"
  下游产出:
    - 系统设计: AICoding架构设计-2-系统设计.md
    - 部署设计: AICoding架构设计-3-部署设计.md
    - 安全设计: AICoding架构设计-4-安全设计.md
```

| 版本 | 日期 | 作者 | 变更内容 | 评审状态 |
| --- | --- | --- | --- | --- |
| v1.0 | 2026-08-18 | product-story-designer（顾全景） | 初稿：以高层架构冻结的角色/场景/模块/MVP 范围为边界，展开 34 条用户故事七段式 + 非功能需求 | Draft |

> **版本管理纪律**：破坏性变更（章节结构调整 / 关键决策反转）升 MAJOR；新增章节、扩充内容升 MINOR。

---

## 1. 业务背景与价值

### 1.1 业务背景

- **当前业务现状**：AI Agent（LLM 应用）在完成多步任务时，需要运行 LLM 生成的代码、执行第三方工具、读写文件、开终端、装依赖。此类「不可信代码执行」若直接落在宿主或弱隔离容器（runc 共享宿主内核）上，存在容器逃逸与数据泄漏风险【事实，高层架构 §2.2 P1】。当前行业内缺乏「通用、隔离、有状态、可弹性扩缩且不绑定云厂商」的统一执行底座，各团队重复自建、依赖每次现装（10-60s）导致冷启动慢、执行环境无状态【事实，高层架构 §1.1】。
- **触发本次需求的事件**：由「基于 agent-runtime-sandbox-design.md 预研并完成业内实现调研（e2b / Modal / Daytona / Google Agent Sandbox / 国内云安全容器）」驱动立项，目标支撑 **1000 个并发沙箱实例**。
- **本系统在产品矩阵中的位置**：本系统（通用 Agent Sandbox）在「AI 平台」中承担「**隔离执行层**」核心职责——向上为 Agent Runtime 提供执行原语（七接口 + SDK），向下以 K8s + 开源运行时（runc/gVisor/Kata）为底座，与「镜像仓库、可观测、审计」等上下游模块形成完整业务闭环【事实，高层架构 §4.1】。

### 1.2 行业方案

> 同类功能、痛点的行业标杆系统及解决方案（整合自《高层架构设计》§3.1，G2 冻结）。

| 标杆系统 | 场景覆盖 | 技术亮点 | 对本产品的借鉴点 |
| --- | --- | --- | --- |
| e2b | AI agent 代码执行、code-interpreter、Computer Use | Firecracker microVM ~150ms、七接口事实标准、pause/resume + snapshot | **接口蓝本**：七接口命名空间 + Template + Snapshot/Volume |
| Modal Sandboxes | 不可信代码执行、GPU agent、大规模并发 | gVisor 隔离、内存快照、1000 sandbox/s、50k+ 并发 session | **并发调度思想** + gVisor 默认 + egress 默认拒绝 |
| Google Agent Sandbox on K8s | AI agent 运行时、代码执行、浏览器自动化 | Sandbox CRD + WarmPool、gVisor+Kata 双后端、K8s 原生 | **最直接参照**：CRD + 预热池 + 双后端 + K8s 原生 |
| Daytona | AI agent 沙箱、GPU 开发环境、Computer Use | 容器 低于 90ms、GPU VFIO、VM 沙箱 | 快启动优化、预热池思路（不借鉴其容器级默认隔离） |
| 国内云安全容器（阿里云 ACK v2 等） | 多租户强隔离、金融政务合规 | Kata(Dragonball) ~150ms、virtio-fs、runC 90% 性能 | Kata 强隔离档 + Pod overhead 配额 + 节点池/污点调度 |

**不借鉴（否决）**：Cloudflare Sandbox / Vercel Sandbox —— 均为托管 Serverless 形态、无自托管 / BYOC 能力，与「自建 K8s 通用设计」诉求冲突，仅作生态对照【事实，高层架构 §3.1】。

**隔离默认档裁定【经中间确认·方案 A】**：底层隔离三档分层（runc 可信级 / gVisor 不可信级 / Kata 强隔离级），**默认档 = gVisor(systrap)**，敏感/多租户/合规场景经 `SandboxClass` 显式升级 Kata(Dragonball)。

### 1.3 方案收益与价值

| 项 | 说明 |
| --- | --- |
| 功能模块 | 通用 Agent Sandbox（七接口执行原语 + 三档隔离 + 声明式生命周期 + 治理 + 预热池） |
| 预期价值收益 | 为 AI Agent 提供通用、隔离、有状态、可弹性扩缩的执行环境；平台团队以声明式 CRD 统一管理沙箱生命周期；三档隔离按敏感度选档，审计留痕满足合规，容量/成本可压测可规划 |
| 量化标准 | 就绪时延 P95 ≤ 500ms（预热命中）/ ≤ 3s（冷启动）【事实，高层架构 V2】；不可信代码 100% 落入 gVisor/Kata 隔离档【事实，V1】；1000 并发隔离层内存 ≤ 15GB【建议，V4】；provider seam 可替换覆盖率 100%【事实，V2 相关】 |

### 1.4 术语清单

| 术语 | 英文 | 含义 |
| --- | --- | --- |
| 沙箱 | Sandbox | 每 agent 任务一个隔离执行实例，独立文件系统/进程空间/环境变量/网络规则/日志/状态 |
| 模板 | Template | 预构建执行环境（Docker Image : Container 关系），固化依赖，避免每次启动现装 |
| 沙箱类 | SandboxClass | 隔离等级模板 CRD：runtimeClassName + 安全策略 + 资源默认值 + 预热池配置 |
| 沙箱申请 | SandboxClaim | consumer 级声明式申请 Pod 沙箱的 CRD（owner/template/ttl/resources/network） |
| 七接口 | Seven Namespaces | lifecycle / files / commands / process / pty / network / code-interpreter |
| 沙箱内代理 | envd | 沙箱内 gRPC 数据面代理，暴露七接口，负责 health check 与状态指标 |
| 预热池 | Warm Pool | 维护一批未绑定的 Ready Pod，claim 到来直接领用，省去拉镜像+起 agent |
| 运行时类 | RuntimeClass | K8s 原生机制，切换 runc / gVisor / Kata 三档隔离 |
| gVisor | gVisor | 用户态内核（Sentry）拦截 syscall 的隔离运行时，systrap 无需 KVM，内存 ~15MB |
| Kata | Kata Containers | 每 Pod 一个轻量 VM + 独立 guest 内核的强隔离运行时，Dragonball ~150ms/~40MB，需 KVM |
| runc | runc | namespace + cgroup 共享宿主内核的可信级运行时，兼容性 100% |
| 快照 | Snapshot | 运行现场保存（文件快照 / microVM 内存快照），用于环境复用/任务恢复/状态复制 |
| 持久卷 | Volume | 跨沙箱 / 跨任务数据共享的持久化存储 |
| 可替换缝 | Provider Seam | 统一 seam 三角色（Def/Provider/Consumer），一行替换隔离后端/执行世界 |
| 声明式控制器 | Operator / CRD | K8s 原生自定义资源 + 控制器，reconcile 沙箱生命周期 |
| TTL | Time To Live | 沙箱存活时长，到期 drain（先停进程再删） |
| GC | Garbage Collection | TTL 回收 + 孤儿 Pod 兜底回收 |
| mTLS | Mutual TLS | 控制面与数据面双向证书认证的通信安全 |
| 人在环审批 | Human-in-the-loop | pre-execute 返回 ask/allow/deny，无审批降级 fail-closed |

---

## 2. 范围与边界

### 2.1 系统内模块及功能

> 一级功能清单（对齐《高层架构设计》§6.3 功能清单 F1~F12 与 §6.1 In-Scope）。

| 一级模块 | 一级功能（对应高层架构编号） |
| --- | --- |
| 生命周期 | F1 声明式生命周期（Sandbox CRD 创建/探活/TTL/GC/状态机） |
| 隔离 | F2 三档隔离 + 硬化（runc/gVisor 默认/Kata + 文件效应 + 容器硬化 + 危险 syscall 阻断） |
| 执行 | F3 七接口 + 流式 + 取消（lifecycle/files/commands/process/pty/network/code-interpreter） |
| 网络 | F4 出站白名单 + 入站隔离（默认拒绝 + 白名单 + NetworkPolicy） |
| 治理 | F5 资源与配额（CPU/内存硬上限 + TTL + 单次超时 + 多租户上限）；F6 凭据安全；F7 人在环审批（P1）；F8 审计日志（P1） |
| 架构 | F9 控制面/数据面分离；F10 provider 可替换 + 热重载 |
| 运营 | F11 预热池（P1）；F12 mTLS + 可观测（P1） |

**非功能（In-Scope）**：N1 支撑 1000 个并发沙箱实例；N2 多租户隔离（租户级 namespace + 配额 + RBAC）。

### 2.2 系统外模块及功能

> 当前系统**不覆盖**的功能，及其原因（对齐《高层架构设计》§6.1 Out-of-Scope O1~O5）。

| 编号 | 不做的事 | 原因 | 后续计划 |
| --- | --- | --- | --- |
| O1 | GPU 沙箱（VFIO / GPU 节点池） | 依赖 GPU 硬件与 VFIO 直通，MVP/完整版不具备条件 | 增强版 |
| O2 | Firecracker 极简 serverless 档 | 与三档分层路线可后续扩展，非本期必选 | 增强版 |
| O3 | 机密计算（Kata CoCo SEV-SNP/TDX） | 依赖特殊硬件与合规场景，本期不做 | 增强版/待业务确认 |
| O4 | 托管 SaaS / 计费 / 多租户账单 | 本平台为自建内部执行底座，计费由上层 Agent 平台承担 | 不做（其他系统承担） |
| O5 | 会话 fork/恢复 + 暂停/恢复（US-31/32，P2） | 依赖 Snapshot 与 microVM 内存快照，属增强项 | 增强版 |

### 2.3 外部依赖

| 依赖系统 | 提供方 | 依赖能力 | 接入方式 | 接口人 |
| --- | --- | --- | --- | --- |
| Agent Runtime（LLM 应用） | Agent 平台团队 | 沙箱创建/查询/销毁 + 七接口执行 | REST/OpenAPI（控制面）+ gRPC 双向流（数据面） | Agent 平台团队接口人 |
| 容器镜像仓库 | 平台基础设施团队 | 模板镜像 / agent 镜像拉取 | containerd 拉取 | 平台基础设施团队接口人 |
| Kubernetes 集群 | 自建 | CRD / RuntimeClass / NetworkPolicy / CSI / RBAC / Pod Overhead | K8s 原生 API | 平台基础设施团队接口人 |
| 隔离运行时（runc/gVisor/Kata） | 开源社区 | 三档隔离 | containerd runtime + RuntimeClass | 开源社区（无单一接口人） |
| CSI / 对象存储 | 平台基础设施团队 | 工作区卷 / 持久卷 / 快照 | CSI PVC + VolumeSnapshot + 对象存储直传 | 平台基础设施团队接口人 |
| 可观测栈（Prometheus/OTel/Loki） | OPS 团队 | 指标 / 日志 / 追踪 | OTLP / 指标抓取 / 日志采集 | OPS 团队接口人 |
| 审计系统 | 安全合规团队 | 生命周期 + 执行审计事件 | 审计日志推送（append-only） | 安全合规团队接口人 |
| kubernetes-sigs/agent-sandbox | kubernetes-sigs | CRD 资源模型参照 + 已知 issue 避坑清单（设计参照，非代码底座）【经中间确认】 | 自研对标（不直接依赖仓库） | 开源社区（无单一接口人） |

---

## 3. 功能清单

> **定位**：全景骨架表，进入「角色 / 场景 / US」之前先看到完整功能版图。
> **互查约束**：本节与《高层架构设计》§6.3 功能清单（F1~F12）逐项一致，不新增、不裁剪；P0/P1/P2 优先级与 MVP/完整版范围继承高层架构 §4.3 已冻结划分。

### 3.1 功能清单结构

| 一级模块 | 二级模块 | 功能项 | 对应 US | 优先级 | MVP 范围 | 完整版范围 | 备注 |
| --- | --- | --- | --- | --- | --- | --- | --- |
| 生命周期 | 声明式生命周期 | Sandbox CRD 创建/探活/TTL/GC/状态机 | US-24/25/26/18 | P0 | ✅ | ✅ | 对齐 V2；状态机 Pending→Provisioning→Ready→Draining→Deleted |
| 隔离 | 三档隔离 | runc 可信/gVisor 默认/Kata 强隔离，SandboxClass 选档 | US-1 | P0 | ✅ | ✅ | 对齐 V1【经中间确认·方案 A】 |
| 隔离 | 文件效应策略 | 只读模式写 workspace 外拒绝，错误可识别为「策略拦截」 | US-2 | P0 | ✅ | ✅ | 对齐 V1；无法 enforce 时 fail-closed |
| 隔离 | 容器边界硬化 | runAsNonRoot/readOnlyRootFilesystem/seccomp/drop ALL caps | US-3 | P0 | ✅ | ✅ | 对齐 V1 |
| 隔离 | 危险 syscall 阻断 | seccomp 显式 deny ptrace/mount/keyring | US-4 | P1 | ❌ | ✅ | 对齐 V1；SIGSYS 终止 + 日志 |
| 执行 | 文件能力 | 读/写原子+防覆盖/编辑字面替换/列目录 | US-5/6/7/8 | P0 | ✅ | ✅ | 对齐 V3；version guard + FS_STALE_VERSION |
| 执行 | 命令/进程能力 | 跑命令流式输出/多 reader/终止进程树/取消传播 | US-9/10/11/12 | P0（US-10 P1） | ✅ | ✅ | 对齐 V2/V3；spawn 立即返回 handle |
| 执行 | 终端/共享世界 | PTY 字节流双向/fs 与 subprocess 共享执行世界 | US-13/14 | P0 | ✅ | ✅ | 对齐 V3；processPath 接合点 |
| 网络 | 出站/入站隔离 | 出站默认拒绝+白名单；入站 NetworkPolicy | US-15/16 | P0 | ✅ | ✅ | 对齐 V1 |
| 治理 | 资源与配额 | CPU/内存硬上限 + TTL + 单次超时 + 多租户上限 | US-17/18/19/22 | P0（US-22 P1） | ✅ | ✅ | 对齐 V4；Pod Overhead 计入配额 |
| 治理 | 凭据安全 | 敏感环境变量剥离 + 显式转发可审计 | US-20 | P0 | ✅ | ✅ | 对齐 V1 |
| 治理 | 人在环审批 | pre-execute ask/allow/deny（fail-closed） | US-21 | P1 | ❌ | ✅ | 对齐 V1 |
| 治理 | 审计日志 | append-only + 按 owner/时间检索 | US-23 | P1 | ❌ | ✅ | 对齐 V1 |
| 架构 | 控制面/数据面分离 | runtime 直连 envd，Operator 不代理数据 | US-28 | P0 | ✅ | ✅ | 对齐 V2 |
| 架构 | provider 可替换 | 统一 seam 三角色，一行替换后端 + 热重载可逆注册 | US-33/34 | P0（US-34 P1） | ✅ | ✅ | 对齐 V2；隔离后端切换零业务改动 |
| 运营 | 预热池 | min/max Ready Pod 补给与裁剪 | US-27 | P1 | ❌ | ✅ | 对齐 V2；亚秒分配 |
| 运营 | mTLS + 可观测 | 双向证书 + 指标/日志/追踪/告警 | US-29/30 | P1 | ❌ | ✅ | 对齐 V4 |
| 增强 | 会话增强 | 会话 fork/恢复 + 暂停/恢复 | US-31/32 | P2 | ❌ | ❌（增强版） | 对齐 V3；Out-of-Scope O5 |

**MVP 范围汇总**：P0 = 23 条（US-1/2/3/5/6/7/8/9/11/12/13/14/15/16/17/18/19/20/24/25/26/28/33）；P1 = 9 条（US-4/10/21/22/23/27/29/30/34）；P2 = 2 条（US-31/32）。与《高层架构设计》§4.3 完全一致。

---

## 4. 角色与场景

### 4.1 角色清单

> 继承《高层架构设计》§2.1 已冻结的 5 类角色，不新增、不细分（材料摘要 D4 的「SU Session User」操作已并入「Agent Runtime」角色覆盖，遵循「不绕开高层架构重定义角色范围」纪律）。

| 角色 | 业务身份 | 主要操作 | 核心关注点 |
| --- | --- | --- | --- |
| 甲方决策者 | 平台负责人 / 技术总监 | 决策自建 vs 采购、审批容量与成本预算 | 1000 并发能力 + 不绑定云厂商 + 隔离层成本可控 |
| Agent Runtime | agent 开发者（最终用户 A） | 经 SDK/CLI 调用七接口创建沙箱、执行代码、读写文件、发起任务/看结果/中断 | 隔离可靠 + 快速就绪 + 有状态连续 + 流式输出 + 历史不丢 |
| Platform Engineer | 平台工程师（最终用户 B） | 部署/扩容/换隔离后端/配安全策略/发布模板 | 声明式生命周期 + 控制面数据面分离 + 后端可替换不宕机 |
| Security / Compliance | 安全合规工程师（受影响方） | 审计、审批、隔离强度声明、凭据管控 | 隔离强度可声明 + 密钥不泄漏 + 全程可审计 |
| OPS / SRE | 运维工程师（受影响方） | 监控、告警、容量规划、GC、预热池自愈 | 可观测 + 预热池/GC 自愈 + 容量可压测 |

### 4.2 关键场景清单

| 编号 | 角色 | 触发条件 | 期望结果 | 频率（日均 / QPS） |
| --- | --- | --- | --- | --- |
| S1 | Agent Runtime | Agent 需要执行不可信代码，调用 `Sandbox.create` | 沙箱就绪返回 sandboxId + 连接信息（P95 ≤ 500ms 预热 / ≤ 3s 冷） | 峰值对齐 1000 并发实例【事实】；稳态创建速率【建议】待压测 |
| S2 | Agent Runtime | 多步任务需「第 3 步看到第 1 步装的依赖」 | 同沙箱内文件/依赖/环境变量 100% 保留（有状态连续） | 高频【假设】 |
| S3 | Agent Runtime | 跑命令需流式看到 stdout/stderr，中途取消 | stdout/stderr 独立流并发读，取消传播到子进程 | 高频【假设】 |
| S4 | Platform Engineer | 声明式 `apply Sandbox` 换隔离后端/配策略 | 换后端零业务代码改动，运行中沙箱不宕机 | 低频【假设】 |
| S5 | Security / Compliance | 敏感/多租户/合规任务需强隔离 | 经 SandboxClass 升级 Kata(Dragonball)，隔离覆盖率 100% | 低频【假设】 |
| S6 | OPS / SRE | 池耗尽 / Pending 堆积 / 逃逸检测事件 | 实时告警 + 一键扩容/调池 | 按事件触发【假设】 |
| S7 | 甲方决策者 | 评估 1000 并发容量达成度与成本 | 容量/成本报告可压测可规划 | 月度【假设】 |

---

## 5. 用户旅程（UserStory）

> 34 条 US 编号与优先级**完全继承**《资料摘要》D4 与《高层架构设计》§4.3 已冻结划分，不拆分、不合并、不调整优先级。每条 US 按 5.x.1 ~ 5.x.7 七段式展开。
>
> **核心业务主链路**（端到端，复用高层架构 §5.3）：
>
> ```mermaid
> flowchart LR
>     A[Agent 选模板] --> B[SDK create]
>     B --> C[控制面认证+配额检查]
>     C --> D[调度：预热池领用 / 新建]
>     D --> E[隔离层启动沙箱 RuntimeClass 选档]
>     E --> F[envd 就绪 + health check]
>     F --> G[返回 sandboxId + 连接信息]
>     G --> H[files 写输入]
>     H --> I[commands/process 执行 流式 stdout/stderr]
>     I --> J[files 读产物]
>     J --> K[snapshot/volume 保存]
>     K --> L[kill 销毁 / pause 挂起 / 归还预热池]
> ```

### 5.1 US-1：选择隔离强度（P0）

#### 5.1.1 业务场景

- **视角**：Agent Runtime / Platform Engineer
- **描述逻辑**：Platform Engineer 在配置层（SandboxClass）声明某类沙箱的隔离后端（runc/gVisor/Kata），Agent 开发者无需改任何 agent 代码，其创建沙箱时即按模板落到对应隔离档；当某任务因 gVisor syscall 兼容缺口失败时，可经 SandboxClass 升级 Kata，切换时 fs/subprocess/terminal 一起迁移【经中间确认·方案 A】。

#### 5.1.2 业务流程

- **视角**：用户
- **描述方式**：
  - Given 平台已注册三档隔离 SandboxClass（runc/gVisor 默认/Kata）
  - When Agent 经 SDK 以默认模板创建沙箱
  - Then 沙箱落到 gVisor(systrap) 档，agent 代码零改动
  - When 任务升级为敏感/合规场景、显式指定 Kata 档
  - Then 沙箱以 Kata(Dragonball) 档启动，且 fs/subprocess/terminal 一并迁移

#### 5.1.3 UE 原型

- 配置层：`SandboxClass` CRD 的 `runtimeClassName` 字段（`runc | gvisor | kata`）；SDK 侧无感知，模板引用 class 名即可。
- 交互约束：切换隔离档时错误语义不改变，agent 看到的仍是同一七接口。

#### 5.1.4 业务逻辑

- **视角**：业务系统
- **描述方式**：SandboxController reconcile 时读取 spec 引用的 SandboxClass → 解析 `runtimeClassName` → 建 Pod 时注入 RuntimeClass → kubelet→containerd 选择对应 runtime（runc/gVisor/Kata-shim-v2）→ 同一套 envd agent 暴露七接口，上层无感。

#### 5.1.5 数据描述

- 核心数据：`Sandbox.spec.class` → `SandboxClass.runtimeClassName` → Pod `spec.runtimeClassName` → `status.podName/grpcPort`。切换隔离档仅改变 runtimeClassName 字段，不改变七接口契约与 workspace 语义。

#### 5.1.6 验收标准 AC

- **Given** 已定义 runc/gVisor/Kata 三档 SandboxClass，**When** 用默认模板创建沙箱，**Then** 沙箱 runtimeClassName = gvisor(systrap)，agent 代码零改动
- **Given** 指定强隔离 class 创建沙箱，**When** 节点具备 KVM，**Then** 沙箱以 Kata(Dragonball) 启动且 fs/subprocess/terminal 一起迁移
- **Given** 指定强隔离 class 但节点无 KVM（异常路径），**When** 创建沙箱，**Then** 调度失败并返回可识别的「隔离档不可用」错误，不静默降级到 runc
- **Given** 隔离后端切换，**When** 查询 provider seam 覆盖率，**Then** 业务代码零改动（provider seam 覆盖率 100%）

#### 5.1.7 外部集成接口

- 依赖开源运行时：gVisor(systrap) / Kata 3.x(Dragonball) / runc，经 containerd `config.toml` + RuntimeClass 接入，无需换 CRI【事实，高层架构 §2.4】。Kata 档要求节点具备 KVM + 嵌套虚拟化。

### 5.2 US-2：文件效应策略（P0）

#### 5.2.1 业务场景

- **视角**：Agent Runtime
- **描述逻辑**：沙箱在只读模式下执行 agent 指令，若 agent 尝试写 workspace 之外的路径（如 rootfs 系统路径），平台须拦截并让错误**可识别为「策略拦截」而非「命令失败」**，以便 agent 正确归因；当策略无法 enforce 时必须 fail-closed，绝不静默放行。

#### 5.2.2 业务流程

- **视角**：用户
- **描述方式**：
  - Given 沙箱处于只读文件效应模式
  - When agent 调用 files.write 写 workspace 外的路径
  - Then 返回「策略拦截」错误（区别于普通命令失败）
  - When 底层无法 enforce 该策略
  - Then 操作被拒绝（fail-closed），不静默放行

#### 5.2.3 UE 原型

- 错误语义：返回错误码/类型标记为 `policy-violation`（策略拦截），而非 `command-failed`（命令失败）；SDK 侧可据此区分并提示。

#### 5.2.4 业务逻辑

- **视角**：业务系统
- **描述方式**：envd agent 在 files.write/edit 前校验目标路径是否落在可写 workspace 卷内 → 命中则执行原子写 → 未命中返回策略拦截错误；策略 enforce 能力缺失（如文件系统不支持）→ 直接拒绝（fail-closed）。

#### 5.2.5 数据描述

- 核心数据：目标路径 → 是否在 `workspace` 可写卷边界内 → 策略拦截结果。策略按调用携带，随每次 files 调用评估。

#### 5.2.6 验收标准 AC

- **Given** 只读模式下写 workspace 内文件，**When** 调用 files.write，**Then** 写入成功
- **Given** 只读模式下写 workspace 外路径，**When** 调用 files.write，**Then** 返回「策略拦截」错误且错误可识别为策略拦截而非命令失败
- **Given** 底层无法 enforce 文件效应策略（异常路径），**When** 执行写操作，**Then** 操作被拒绝（fail-closed），绝不静默放行

#### 5.2.7 外部集成接口

- 依赖 CSI 存储提供的可写 workspace 卷边界（PVC/emptyDir）；rootfs 只读由容器运行时分区保证【事实，D1 §6 / D3 §10】。

### 5.3 US-3：容器边界硬化（P0）

#### 5.3.1 业务场景

- **视角**：Platform Engineer / Security
- **描述逻辑**：每个沙箱 Pod 默认以非 root、只读 rootfs、seccomp、drop ALL capabilities 启动，workspace 为唯一可写卷，杜绝特权逃逸路径。

#### 5.3.2 业务流程

- **视角**：用户
- **描述方式**：
  - Given 创建任意沙箱
  - When Pod 被调度
  - Then 以 runAsNonRoot + readOnlyRootFilesystem + seccomp(RuntimeDefault) + drop ALL caps 启动，workspace 为唯一可写卷
  - When 尝试挂载 hostPath 或 privileged
  - Then AdmissionWebhook 拒绝

#### 5.3.3 UE 原型

- 对 agent 无 UI 变化；对 Platform Engineer 表现为 SandboxTemplate/Class 中 security 段默认值（runAsNonRoot/runAsUser 10001/readOnlyRootFilesystem/seccomp/dropCapabilities ALL）。

#### 5.3.4 业务逻辑

- **视角**：业务系统
- **描述方式**：sandbox-webhook（Admission）强制校验并注入安全上下文 → 拒绝 hostPath/privileged/hostNetwork/hostPID/hostIPC → 强制 runAsNonRoot/readOnlyRootFilesystem/drop ALL → 注入 seccomp。

#### 5.3.5 数据描述

- 核心数据：Pod securityContext（runAsNonRoot=true、runAsUser=10001、readOnlyRootFilesystem=true、seccompProfile=RuntimeDefault、capabilities.drop=ALL）→ workspace 卷挂载点 `/workspace`。

#### 5.3.6 验收标准 AC

- **Given** 创建沙箱，**When** 校验 Pod 安全上下文，**Then** 满足非 root + 只读 rootfs + seccomp + drop ALL，workspace 唯一可写
- **Given** 尝试声明 hostPath/privileged（异常路径），**When** 提交 Sandbox/模板，**Then** AdmissionWebhook 拒绝并返回明确错误
- **Given** 业务确需某 capability，**When** 在模板显式按需 add，**Then** 仅该 capability 被放行且可审计

#### 5.3.7 外部集成接口

- 依赖 K8s AdmissionWebhook 机制 + 容器运行时 seccomp 能力；安全底座（seccomp 基线）【事实，高层架构 §6.2】。

### 5.4 US-4：危险 syscall 阻断（P1）

#### 5.4.1 业务场景

- **视角**：Security
- **描述逻辑**：通过 seccomp 显式 deny `ptrace/mount/keyring` 等危险 syscall，沙箱内进程触发即被 SIGSYS 终止并记日志，防止特权提升与宿主逃逸。

#### 5.4.2 业务流程

- **视角**：用户
- **描述方式**：
  - Given 沙箱启用危险 syscall 阻断策略
  - When 沙箱内进程调用 ptrace/mount/keyring
  - Then 进程被 SIGSYS 终止，事件记入日志
  - When 合法代码调用白名单 syscall
  - Then 正常放行

#### 5.4.3 UE 原型

- 配置层：seccomp profile 中 deny 列表；告警端可检索 SIGSYS 阻断事件。

#### 5.4.4 业务逻辑

- **视角**：业务系统
- **描述方式**：seccomp profile 显式 deny 危险 syscall → 触发时内核投递 SIGSYS → 进程终止 → 事件上报审计/日志。

#### 5.4.5 数据描述

- 核心数据：syscall 名 → seccomp action（SCMP_ACT_ERRNO/SCMP_ACT_KILL）→ SIGSYS 事件 → 日志。

#### 5.4.6 验收标准 AC

- **Given** 沙箱启用阻断策略，**When** 进程调用 ptrace/mount/keyring，**Then** 进程被 SIGSYS 终止且日志可检索
- **Given** 进程调用非危险白名单 syscall（正常路径），**When** 执行，**Then** 正常放行
- **Given** seccomp profile 缺失（异常路径），**When** 启动沙箱，**Then** 沙箱拒绝启动（不降级为无 seccomp）

#### 5.4.7 外部集成接口

- 依赖容器运行时 seccomp 能力；安全底座 + Falco 逃逸检测【事实，高层架构 §6.2】。

### 5.5 US-5：读文件（P0）

#### 5.5.1 业务场景

- **视角**：Agent Runtime
- **描述逻辑**：agent 读取沙箱内文件（读产物、读源码、读日志），路径须 resolve 成稳定身份，超出大小上限或二进制内容时返回明确错误。

#### 5.5.2 业务流程

- **视角**：用户
- **描述方式**：
  - Given 沙箱内存在文本文件
  - When agent 调用 files.read 并给定路径
  - Then 返回 UTF-8 文本内容
  - When 文件超过 maxBytes 或为二进制（异常）
  - Then 返回 FS_TOO_LARGE / 二进制拒绝错误

#### 5.5.3 UE 原型

- SDK 接口：`files.read(path)` 返回文本；别名与 targetKey 指向同一稳定身份。

#### 5.5.4 业务逻辑

- **视角**：业务系统
- **描述方式**：envd 将路径 resolve 成稳定身份（inode/绝对路径归一化）→ 校验 UTF-8 边界与二进制 → 校验大小上限 → 返回内容或错误码。

#### 5.5.5 数据描述

- 核心数据：路径 → 稳定身份（targetKey）→ 内容字节流 → 大小/编码校验 → 文本或错误码（FS_TOO_LARGE 等）。

#### 5.5.6 验收标准 AC

- **Given** 文件存在且为 UTF-8 文本，**When** 调用 files.read，**Then** 返回文本内容
- **Given** 文件超过 maxBytes（异常路径），**When** 调用 files.read，**Then** 返回 FS_TOO_LARGE
- **Given** 文件为二进制（异常路径），**When** 调用 files.read，**Then** 拒绝并返回明确错误
- **Given** 路径为别名，**When** 调用 files.read，**Then** 与原始 targetKey 返回一致

#### 5.5.7 外部集成接口

- 依赖 envd 内 gRPC fs 服务 + workspace 卷文件系统。

### 5.6 US-6：写文件原子 + 防覆盖（P0）

#### 5.6.1 业务场景

- **视角**：Agent Runtime
- **描述逻辑**：agent 写文件须原子提交（temp + fsync + rename），并发写同一文件时用 version guard 防止覆盖，冲突返回 FS_STALE_VERSION 并返回新版本号。

#### 5.6.2 业务流程

- **视角**：用户
- **描述方式**：
  - Given 沙箱 workspace 可写
  - When agent 调用 files.write
  - Then 原子写入并返回新版本号
  - When 版本不匹配（异常）
  - Then 返回 FS_STALE_VERSION 且不覆盖

#### 5.6.3 UE 原型

- SDK 接口：`files.write(path, content, version?)` 返回新版本号；冲突时返回 FS_STALE_VERSION。

#### 5.6.4 业务逻辑

- **视角**：业务系统
- **描述方式**：envd 写 tempfile → fsync → rename 原子替换 → version guard（mtime/ns 计数）校验 → 版本冲突返回 FS_STALE_VERSION。

#### 5.6.5 数据描述

- 核心数据：内容字节流 → tempfile → rename → version 计数 → 新版本号 / FS_STALE_VERSION。

#### 5.6.6 验收标准 AC

- **Given** 目标文件不存在或版本匹配，**When** 调用 files.write，**Then** 原子写入成功并返回新版本号
- **Given** 版本不匹配（异常路径），**When** 调用 files.write，**Then** 返回 FS_STALE_VERSION 且不覆盖原文件
- **Given** 写入中途崩溃（异常路径），**When** 恢复后读取，**Then** 文件保持旧版本或完整新版本（无半写状态）

#### 5.6.7 外部集成接口

- 依赖 envd 内 gRPC fs 服务 + workspace 卷文件系统（fsync/rename 语义）。

### 5.7 US-7：编辑文件字面替换（P0）

#### 5.7.1 业务场景

- **视角**：Agent Runtime
- **描述逻辑**：agent 编辑文件做**字面（非正则）**替换，多处匹配时报错，版本不匹配拒绝，原子提交。

#### 5.7.2 业务流程

- **视角**：用户
- **描述方式**：
  - Given 文件存在且版本匹配
  - When agent 调用 files.edit 指定 old/new 字面串
  - Then 唯一匹配处被替换并原子提交
  - When 多处匹配或版本不匹配（异常）
  - Then 报错且不修改

#### 5.7.3 UE 原型

- SDK 接口：`files.edit(path, oldString, newString, version?)`；多处匹配返回「多处匹配」错误。

#### 5.7.4 业务逻辑

- **视角**：业务系统
- **描述方式**：envd 做字面查找（非正则）→ 统计匹配数 → 恰好一处才替换 → version guard → 原子提交。

#### 5.7.5 数据描述

- 核心数据：oldString/newString → 匹配位置 → 匹配计数 → 替换结果 + 新版本号。

#### 5.7.6 验收标准 AC

- **Given** oldString 唯一匹配且版本一致，**When** 调用 files.edit，**Then** 字面替换成功且原子提交
- **Given** oldString 多处匹配（异常路径），**When** 调用 files.edit，**Then** 报错且文件不变
- **Given** 版本不匹配（异常路径），**When** 调用 files.edit，**Then** 返回 FS_STALE_VERSION 且不修改

#### 5.7.7 外部集成接口

- 依赖 envd 内 gRPC fs 服务。

### 5.8 US-8：列目录（P0）

#### 5.8.1 业务场景

- **视角**：Agent Runtime
- **描述逻辑**：agent 列目录获取文件名与元数据，返回稳定 name 序，lstat 不跟随符号链接。

#### 5.8.2 业务流程

- **视角**：用户
- **描述方式**：
  - Given 目录存在
  - When agent 调用 files.listDir
  - Then 返回稳定 name 序的条目（仅元数据）
  - When 目录不存在（异常）
  - Then 返回明确错误

#### 5.8.3 UE 原型

- SDK 接口：`files.listDir(path)` 返回条目列表（name + stat 元数据）。

#### 5.8.4 业务逻辑

- **视角**：业务系统
- **描述方式**：envd 用 lstat 读取（不跟随符号链接）→ 按 name 稳定排序 → 返回元数据（不读文件内容）。

#### 5.8.5 数据描述

- 核心数据：目录路径 → 条目 name + stat（类型/大小/mtime）→ 排序列表。

#### 5.8.6 验收标准 AC

- **Given** 目录存在，**When** 调用 files.listDir，**Then** 返回稳定 name 序条目且仅含元数据
- **Given** 目录不存在（异常路径），**When** 调用 files.listDir，**Then** 返回明确错误
- **Given** 目录含符号链接，**When** 调用 files.listDir，**Then** lstat 不跟随链接

#### 5.8.7 外部集成接口

- 依赖 envd 内 gRPC fs 服务。

### 5.9 US-9：跑命令流式输出（P0）

#### 5.9.1 业务场景

- **视角**：Agent Runtime
- **描述逻辑**：agent 跑命令需 spawn 立即返回 handle，stdout/stderr 独立流可并发读，done 返回 exit code + signal + duration，stdin 可写。

#### 5.9.2 业务流程

- **视角**：用户
- **描述方式**：
  - Given 沙箱就绪
  - When agent 调用 commands.run（前台/后台）
  - Then 立即返回 handle，stdout/stderr 独立流并发输出
  - When 命令结束
  - Then done 带 exit code + signal + duration

#### 5.9.3 UE 原型

- SDK 接口：`commands.run(cmd, {on_stdout, on_stderr, cwd, user, envs})`；spawn 立即返回 handle，on_stdout/on_stderr 流式回调。

#### 5.9.4 业务逻辑

- **视角**：业务系统
- **描述方式**：envd 经 gRPC 双向流执行命令 → stdout/stderr 分独立流 → 可写 stdin → 进程退出结算 exit code/signal/duration → 回传 done。

#### 5.9.5 数据描述

- 核心数据：cmd + cwd/env → gRPC 流（stdout 帧/stderr 帧/stdin 帧）→ exit code + signal + duration。

#### 5.9.6 验收标准 AC

- **Given** 命令可执行，**When** 调用 commands.run，**Then** spawn 立即返回 handle 且 stdout/stderr 独立流并发读
- **Given** 命令退出，**When** 流结束，**Then** done 带 exit code + signal + duration
- **Given** 命令不存在（异常路径），**When** 调用 commands.run，**Then** 返回明确错误且不阻塞
- **Given** 命令需交互输入，**When** 写 stdin，**Then** 命令读取到输入

#### 5.9.7 外部集成接口

- 依赖 envd 内 gRPC 双向流数据面（stdio 流式）【事实，D3 §6.1】。

### 5.10 US-10：多 reader 不互相消费（P1）

#### 5.10.1 业务场景

- **视角**：Agent Runtime
- **描述逻辑**：多个消费者（agent 主循环 + 日志采集）读取同一进程 stdout/stderr，采用 offset 非消费读 + spill 文件 + ring buffer，互相不消费对方数据。

#### 5.10.2 业务流程

- **视角**：用户
- **描述方式**：
  - Given 进程持续输出
  - When 多个 reader 按各自 offset 读 stdout/stderr
  - Then 各自读到完整内容，互不影响
  - When reader 从指定 offset 续读
  - Then 仅返回 offset 之后的新增内容

#### 5.10.3 UE 原型

- SDK 接口：reader 支持按 offset 读取；日志采集侧可按 offset 轮询。

#### 5.10.4 业务逻辑

- **视角**：业务系统
- **描述方式**：envd 将 stdout/stderr 落 spill 文件 + ring buffer，reader 按 offset 非消费读取，截断时按给定 offset 截断。

#### 5.10.5 数据描述

- 核心数据：stdout/stderr 字节流 → spill 文件 + ring buffer → 各 reader 的 offset 游标 → 新增内容。

#### 5.10.6 验收标准 AC

- **Given** 多个 reader 并发读同一流，**When** 各自按 offset 读取，**Then** 均读到完整内容互不消费
- **Given** reader 从 offset=N 续读（正常路径），**When** 调用读，**Then** 仅返回 N 之后新增内容
- **Given** 输出量超过 ring buffer 上限（异常路径），**When** 老 offset 读取，**Then** 明确返回「数据已截断」而非静默丢数据

#### 5.10.7 外部集成接口

- 依赖 envd 内 gRPC 流 + spill 文件存储。

### 5.11 US-11：终止进程树（P0）

#### 5.11.1 业务场景

- **视角**：Agent Runtime
- **描述逻辑**：agent 终止命令时须终止整棵进程树（非仅父进程），采用 SIGTERM→grace→SIGKILL 阶梯，waitForExit 等整树静止。

#### 5.11.2 业务流程

- **视角**：用户
- **描述方式**：
  - Given 进程派生了子进程
  - When agent 调用 terminate
  - Then 整棵进程树被终止（SIGTERM→grace→SIGKILL）
  - When 等待退出
  - Then waitForExit 返回时整树静止

#### 5.11.3 UE 原型

- SDK 接口：`process.kill` / `commands.kill`；terminate 阶梯终止。

#### 5.11.4 业务逻辑

- **视角**：业务系统
- **描述方式**：envd 按 cgroup/process group 识别进程树 → SIGTERM → graceMs → SIGKILL → 确认整树静止后结算。

#### 5.11.5 数据描述

- 核心数据：进程组/cgroup 标识 → 信号序列（SIGTERM→grace→SIGKILL）→ 退出状态。

#### 5.11.6 验收标准 AC

- **Given** 进程有子进程，**When** 调用 terminate，**Then** 整棵进程树被终止（树作用域）
- **Given** 进程在 grace 内自行退出（正常路径），**When** terminate，**Then** 正常结算不触发 SIGKILL
- **Given** 进程忽略 SIGTERM（异常路径），**When** grace 超时，**Then** 发送 SIGKILL 强制终止
- **Given** 调用 waitForExit，**When** 返回，**Then** 整树已静止

#### 5.11.7 外部集成接口

- 依赖容器 cgroup/process group 能力【事实，D3 §6.2】。

### 5.12 US-12：取消信号传播（P0）

#### 5.12.1 业务场景

- **视角**：Agent Runtime
- **描述逻辑**：agent 取消任务时，AbortController 信号沿 step→工具→子进程传播；已启动调用正常结算，未启动调用合成错误，历史可重放。

#### 5.12.2 业务流程

- **视角**：用户
- **描述方式**：
  - Given agent 触发取消（AbortController）
  - When 信号传播到子进程
  - Then 已启动子进程被终止并正常结算，未启动调用合成错误
  - When 取消后重放历史
  - Then 历史记录自洽可重放

#### 5.12.3 UE 原型

- SDK 接口：取消经 AbortController → gRPC Signal 帧 → agent 杀进程树。

#### 5.12.4 业务逻辑

- **视角**：业务系统
- **描述方式**：取消信号沿调用链传播 → gRPC Signal 帧 → 终止已启动进程 → 未启动工具调用合成 synthetic error result（保证 log 自洽）。

#### 5.12.5 数据描述

- 核心数据：AbortController 信号 → Signal 帧 → 进程终止状态 → 合成错误结果。

#### 5.12.6 验收标准 AC

- **Given** 任务取消，**When** 信号传播，**Then** 已启动子进程正常结算、未启动调用合成错误
- **Given** 取消后（正常路径），**When** 重放历史，**Then** 历史自洽可重放
- **Given** 取消时进程无法立即终止（异常路径），**When** 强制取消，**Then** 仍能收敛且不产生悬挂引用

#### 5.12.7 外部集成接口

- 依赖 envd 内 gRPC Signal 帧 + 上层 AbortController 机制【事实，D5 §4/§5】。

### 5.13 US-13：分配 PTY（P0）

#### 5.13.1 业务场景

- **视角**：Agent Runtime
- **描述逻辑**：agent 开交互式终端，PTY 字节流双向、resize（TIOCSWINSZ）、前台进程组信号，终止时等整会话静止。

#### 5.13.2 业务流程

- **视角**：用户
- **描述方式**：
  - Given agent 调用 pty.create
  - When 建立交互会话
  - Then 字节流双向（write/read）可用
  - When 终端尺寸变化
  - Then resize 生效（TIOCSWINSZ）

#### 5.13.3 UE 原型

- SDK 接口：`pty.create()` + `send_input()` + `resize()` + `kill()`。

#### 5.13.4 业务逻辑

- **视角**：业务系统
- **描述方式**：envd 经 creack/pty 分配伪终端 → 双向字节流（TerminalIO）→ resize 帧转 TIOCSWINSZ → 前台进程组信号 → 终止等整会话静止。

#### 5.13.5 数据描述

- 核心数据：pty fd → 双向字节流（read/write）→ resize 尺寸 → 前台进程组信号。

#### 5.13.6 验收标准 AC

- **Given** 调用 pty.create，**When** 建立会话，**Then** 字节流双向可用
- **Given** 终端 resize，**When** 发送 resize 帧，**Then** TIOCSWINSZ 生效
- **Given** 发送前台信号（如 Ctrl-C），**When** 调用信号，**Then** 信号送达前台进程组
- **Given** 终止会话（异常路径），**When** 调用 kill，**Then** 等整会话静止后返回

#### 5.13.7 外部集成接口

- 依赖 envd 内 gRPC 双向流 + 内核 PTY 能力。

### 5.14 US-14：fs 与 subprocess 共享执行世界（P0）

#### 5.14.1 业务场景

- **视角**：Agent Runtime
- **描述逻辑**：fs 与 subprocess 指向同一执行世界——processPath 返回的路径必须能被 subprocess 打开执行，同一后端路径一致，换后端时一起迁移。

#### 5.14.2 业务流程

- **视角**：用户
- **描述方式**：
  - Given 沙箱内存在文件
  - When agent 经 fs.resolve 取得 processPath
  - Then 该路径可直接交给 subprocess 执行
  - When 换隔离后端
  - Then fs 与 subprocess 一起迁移、路径一致

#### 5.14.3 UE 原型

- SDK 接口：`fs.resolve(path)` 返回 Pod 内绝对路径，`subprocess.resolveExecutable` 可消费该路径。

#### 5.14.4 业务逻辑

- **视角**：业务系统
- **描述方式**：fs 与 subprocess 共享同一 envd 执行世界 → processPath 返回沙箱内绝对路径 → subprocess 在同一后端打开 → 换 provider 时两者统一替换。

#### 5.14.5 数据描述

- 核心数据：路径 → processPath（Pod 内绝对路径）→ subprocess 可执行对象 → 后端标识。

#### 5.14.6 验收标准 AC

- **Given** fs.resolve 返回 processPath，**When** 交给 subprocess，**Then** 可正常打开执行
- **Given** 同一后端（正常路径），**When** fs 与 subprocess 分别解析，**Then** 路径一致
- **Given** 换隔离后端（异常/切换路径），**When** 迁移完成，**Then** fs 与 subprocess 一起迁移且路径一致

#### 5.14.7 外部集成接口

- 依赖 envd 统一执行世界（fs + subprocess 共享）【事实，D3 §7.2/§7.3】。

### 5.15 US-15：出站网络白名单（P0）

#### 5.15.1 业务场景

- **视角**：Agent Runtime / Security
- **描述逻辑**：沙箱默认拒绝出站，仅允许白名单目标（如 pip 源、镜像源），DNS 受控，违规流量 drop 并记录。

#### 5.15.2 业务流程

- **视角**：用户
- **描述方式**：
  - Given 沙箱网络策略为 Restricted
  - When agent 访问白名单目标（如 pypi.org:443）
  - Then 放行
  - When 访问白名单外目标（异常）
  - Then drop 并记录

#### 5.15.3 UE 原型

- 配置层：`network.egress(Restricted|Full) + allowedEgress(pypi.org:443, registry-1.docker.io:443)`。

#### 5.15.4 业务逻辑

- **视角**：业务系统
- **描述方式**：NetworkPolicy 默认拒绝出站 → 按 allowedEgress 放行 → DNS 受控 → 违规 drop + 记录。

#### 5.15.5 数据描述

- 核心数据：egress 策略（Restricted/Full）→ allowedEgress 目标列表 → 放行/drop 记录。

#### 5.15.6 验收标准 AC

- **Given** Restricted 模式访问白名单目标，**When** 发起请求，**Then** 放行
- **Given** 访问白名单外目标（异常路径），**When** 发起请求，**Then** drop 且记录可检索
- **Given** Full 模式（正常路径），**When** 访问任意出站，**Then** 放行（仅受信任场景启用）

#### 5.15.7 外部集成接口

- 依赖 K8s NetworkPolicy + CNI【事实，高层架构 §2.4】。

### 5.16 US-16：入站隔离（P0）

#### 5.16.1 业务场景

- **视角**：Security / Platform Engineer
- **描述逻辑**：沙箱入站仅允许来自控制面/runtime 的 gRPC 端口，用 NetworkPolicy 限定入站源 namespace/label，禁止横向访问。

#### 5.16.2 业务流程

- **视角**：用户
- **描述方式**：
  - Given 沙箱 Pod 运行中
  - When runtime（授权 namespace）连接沙箱 gRPC 端口
  - Then 放行
  - When 其他 Pod 横向访问（异常）
  - Then 拒绝

#### 5.16.3 UE 原型

- 配置层：NetworkPolicy 限定入站源（namespace/label）+ 端口（grpcPort 50051）。

#### 5.16.4 业务逻辑

- **视角**：业务系统
- **描述方式**：NetworkPolicy 默认拒绝入站 → 仅放行控制面/runtime namespace 到 gRPC 端口 → 禁止沙箱间横向。

#### 5.16.5 数据描述

- 核心数据：入站源 namespace/label → 端口 50051 → 放行/拒绝。

#### 5.16.6 验收标准 AC

- **Given** 授权 runtime 连接沙箱 gRPC 端口，**When** 连接，**Then** 放行
- **Given** 非授权 Pod 横向访问（异常路径），**When** 连接，**Then** 拒绝
- **Given** 沙箱尝试访问其他沙箱（异常路径），**When** 发起，**Then** 被网络策略阻断

#### 5.16.7 外部集成接口

- 依赖 K8s NetworkPolicy + CNI。

### 5.17 US-17：资源配额（P0）

#### 5.17.1 业务场景

- **视角**：Platform Engineer
- **描述逻辑**：每沙箱 CPU/内存硬上限（OOM）、临时存储上限、fd/进程数上限，模板可配，防止单沙箱拖垮节点。

#### 5.17.2 业务流程

- **视角**：用户
- **描述方式**：
  - Given 模板定义 resources（如 cpu 2 + memory 4Gi）
  - When 沙箱超用内存
  - Then OOM 终止，不影响同节点其他沙箱
  - When 沙箱超用 fd/进程数
  - Then 被 cgroup 上限拦截

#### 5.17.3 UE 原型

- 配置层：`resources(cpu, memory)` + 临时存储上限 + fd/进程数上限（SandboxTemplate/Class 可配）。

#### 5.17.4 业务逻辑

- **视角**：业务系统
- **描述方式**：Pod requests/limits → cgroup 强制 → 超内存 OOM → fd/进程数经 pids 控制器限制 → Pod Overhead（Kata 50-100MiB + 0.1 core）计入配额。

#### 5.17.5 数据描述

- 核心数据：resources 上限 → cgroup 用量 → OOM/超限事件。

#### 5.17.6 验收标准 AC

- **Given** 沙箱内存超限（异常路径），**When** 运行，**Then** OOM 终止且不影响同节点其他沙箱
- **Given** 沙箱 CPU/内存/存储/fd 在限内（正常路径），**When** 运行，**Then** 正常执行
- **Given** 强隔离档沙箱，**When** 调度，**Then** Pod Overhead 计入配额（不超卖）

#### 5.17.7 外部集成接口

- 依赖 K8s 资源配额（requests/limits）+ cgroup + Pod Overhead。

### 5.18 US-18：TTL（P0）

#### 5.18.1 业务场景

- **视角**：Agent Runtime / Platform Engineer
- **描述逻辑**：沙箱 TTL 到期先 drain（停进程）再删，提供续期接口，剩余时长可查。

#### 5.18.2 业务流程

- **视角**：用户
- **描述方式**：
  - Given 沙箱 ttlSeconds=1800
  - When 到期
  - Then 先 drain 停进程再删除
  - When agent 提前续期
  - Then TTL 重置、剩余可查

#### 5.18.3 UE 原型

- SDK 接口：`setTimeout/refresh`；status 返回 `expiresAt`。

#### 5.18.4 业务逻辑

- **视角**：业务系统
- **描述方式**：Reconcile 按 RequeueAfter ttl 重入队 → 到期转 Draining → drain（停进程）→ 删 Pod → 续期重置 expiresAt。

#### 5.18.5 数据描述

- 核心数据：ttlSeconds → expiresAt → drain 事件 → 续期后新 expiresAt。

#### 5.18.6 验收标准 AC

- **Given** TTL 到期，**When** 触发回收，**Then** 先停进程再删 Pod（drain 语义）
- **Given** agent 续期（正常路径），**When** 调用 refresh，**Then** TTL 重置且剩余可查
- **Given** 进程在 drain 中拒绝退出（异常路径），**When** grace 超时，**Then** SIGKILL 后删除

#### 5.18.7 外部集成接口

- 依赖 Operator reconcile 定时器 + 进程终止能力。

### 5.19 US-19：单次调用超时（P0）

#### 5.19.1 业务场景

- **视角**：Agent Runtime
- **描述逻辑**：单次调用（命令/文件操作）超时则阶梯终止（SIGTERM→SIGKILL），结果写历史不丢。

#### 5.19.2 业务流程

- **视角**：用户
- **描述方式**：
  - Given 单次调用设定超时
  - When 执行超时
  - Then 阶梯终止
  - When 终止后
  - Then 结果写历史不丢

#### 5.19.3 UE 原型

- SDK 接口：调用携带 timeout 参数；超时返回明确「超时」语义。

#### 5.19.4 业务逻辑

- **视角**：业务系统
- **描述方式**：超时定时器 → 阶梯 terminate → 结果（含超时态）写历史。

#### 5.19.5 数据描述

- 核心数据：timeout 阈值 → 终止事件 → 历史记录（结果/超时态）。

#### 5.19.6 验收标准 AC

- **Given** 调用在超时内完成（正常路径），**When** 执行，**Then** 正常结算
- **Given** 调用超时（异常路径），**When** 触发，**Then** 阶梯终止（SIGTERM→SIGKILL）
- **Given** 超时终止后，**When** 查询历史，**Then** 结果不丢且标记超时

#### 5.19.7 外部集成接口

- 依赖 envd 进程终止 + 会话历史存储。

### 5.20 US-20：凭据不泄漏（P0）

#### 5.20.1 业务场景

- **视角**：Security
- **描述逻辑**：平台密钥不进沙箱，剥离 KEY/PASSWORD/SECRET/TOKEN 及平台前缀（DSH_*），显式转发经 spec.env 合并且可审计。

#### 5.20.2 业务流程

- **视角**：用户
- **描述方式**：
  - Given 环境含敏感变量（DEEPSEEK_API_KEY 等）
  - When 创建沙箱
  - Then 敏感变量被剥离，不进入沙箱
  - When 显式转发某变量
  - Then 经 spec.env 合并注入且可审计

#### 5.20.3 UE 原型

- 配置层：`scrubbedParentEnv` 剥离规则 + `spec.env` 显式转发列表。

#### 5.20.4 业务逻辑

- **视角**：业务系统
- **描述方式**：envd/operator 剥 KEY/PASSWORD/SECRET/TOKEN 及平台前缀 → 仅 spec.env 显式列出的变量注入 → 注入记录可审计。

#### 5.20.5 数据描述

- 核心数据：父环境变量 → 剥离规则匹配 → 注入白名单（spec.env）→ 审计记录。

#### 5.20.6 验收标准 AC

- **Given** 父环境含 KEY/PASSWORD/SECRET/TOKEN 或 DSH_*，**When** 创建沙箱，**Then** 均被剥离不进沙箱
- **Given** 显式转发变量（正常路径），**When** 经 spec.env 注入，**Then** 注入成功且可审计
- **Given** 敏感变量误入沙箱（异常路径），**When** 审计，**Then** 可识别并告警

#### 5.20.7 外部集成接口

- 依赖 envd/operator 环境处理 + 审计系统。

### 5.21 US-21：人在环审批（P1）

#### 5.21.1 业务场景

- **视角**：Security / Agent Runtime
- **描述逻辑**：高危执行（pre-execute）返回 ask 暂停，经 allow/deny 裁决；无审批支持时降级 deny（fail-closed）。

#### 5.21.2 业务流程

- **视角**：用户
- **描述方式**：
  - Given 高危工具调用触发 pre-execute 审批
  - When 返回 ask
  - Then 执行暂停等待裁决
  - When 无审批人响应（异常）
  - Then 降级 deny（fail-closed）

#### 5.21.3 UE 原型

- SDK/审批交互：pre-execute 返回 ask → 审批人 allow/deny → 继续或中止。

#### 5.21.4 业务逻辑

- **视角**：业务系统
- **描述方式**：pre-execute waterfall → 命中审批策略返回 ask 暂停 → allow 继续 / deny 中止 → 超时/无审批降级 deny。

#### 5.21.5 数据描述

- 核心数据：审批策略命中 → ask 状态 → allow/deny 裁决 → 降级 deny 记录。

#### 5.21.6 验收标准 AC

- **Given** 高危调用触发审批，**When** 返回 ask，**Then** 执行暂停等待裁决
- **Given** 审批人 allow（正常路径），**When** 裁决，**Then** 继续执行
- **Given** 审批人 deny（正常路径），**When** 裁决，**Then** 中止执行
- **Given** 无审批支持（异常路径），**When** 触发，**Then** 降级 deny（fail-closed）

#### 5.21.7 外部集成接口

- 依赖上层 Agent 平台的人机交互/审批交互通道【事实，D5 §1/§5】。

### 5.22 US-22：多租户配额（P1）

#### 5.22.1 业务场景

- **视角**：Platform Engineer
- **描述逻辑**：每 owner 并发上限，超限排队；总资源配额；耗尽返回明确错误可重试。

#### 5.22.2 业务流程

- **视角**：用户
- **描述方式**：
  - Given owner 并发达上限
  - When 新沙箱申请
  - Then 排队等待
  - When 资源耗尽（异常）
  - Then 返回明确错误可重试

#### 5.22.3 UE 原型

- 配置层：每 owner 并发上限 + ResourceQuota（namespace 级）+ LimitRange。

#### 5.22.4 业务逻辑

- **视角**：业务系统
- **描述方式**：配额检查（Pool.CanAllocate）→ 超限 RequeueAfter 排队 → 资源耗尽返回明确错误 → 释放后重试。

#### 5.22.5 数据描述

- 核心数据：owner 并发计数 → ResourceQuota 用量 → 排队队列 → 错误码。

#### 5.22.6 验收标准 AC

- **Given** owner 并发未达上限，**When** 申请沙箱，**Then** 立即分配
- **Given** owner 并发达上限（正常路径），**When** 申请，**Then** 排队等待
- **Given** 总资源耗尽（异常路径），**When** 申请，**Then** 返回明确错误且可重试

#### 5.22.7 外部集成接口

- 依赖 K8s ResourceQuota + LimitRange + Operator 配额检查。

### 5.23 US-23：审计日志（P1）

#### 5.23.1 业务场景

- **视角**：Security / OPS
- **描述逻辑**：记录 owner/session/操作/参数/结果/时间，append-only 不可篡改，按 owner/时间检索。

#### 5.23.2 业务流程

- **视角**：用户
- **描述方式**：
  - Given 沙箱发生生命周期/执行事件
  - When 事件发生
  - Then 追加审计记录（append-only）
  - When Security 检索
  - Then 按 owner/时间检索返回结果

#### 5.23.3 UE 原型

- 审计日志页：按 owner/时间筛选、导出（完整版）。

#### 5.23.4 业务逻辑

- **视角**：业务系统
- **描述方式**：生命周期 + 执行事件 → append-only 日志推送 → 按 owner/时间索引 → 不可篡改。

#### 5.23.5 数据描述

- 核心数据：owner/session/操作/参数/结果/时间戳 → append-only 存储 → 检索索引。

#### 5.23.6 验收标准 AC

- **Given** 事件发生，**When** 记录，**Then** 含 owner/session/操作/参数/结果/时间且 append-only
- **Given** 检索（正常路径），**When** 按 owner/时间查询，**Then** 返回对应记录
- **Given** 尝试篡改（异常路径），**When** 修改历史记录，**Then** 被拒绝（不可篡改）

#### 5.23.7 外部集成接口

- 依赖审计系统（append-only 推送）【事实，高层架构 §5.2】。

### 5.24 US-24：声明式申请（P0）

#### 5.24.1 业务场景

- **视角**：Platform Engineer / Agent Runtime
- **描述逻辑**：以声明式 CRD 申请沙箱（含 owner/template/resources/ttl），状态机 Pending→Provisioning→Ready→Draining→Deleted，Ready 返回连接信息（IP/port/凭证）。

#### 5.24.2 业务流程

- **视角**：用户
- **描述方式**：
  - Given 提交 Sandbox 声明
  - When Operator reconcile
  - Then 状态机推进，Ready 返回 podName/podIP/grpcPort
  - When TTL 到期/删除
  - Then Draining→Deleted

#### 5.24.3 UE 原型

- 声明式：`apply Sandbox(spec.template/resources/ttl)`；status 返回 `phase/podName/podIP/grpcPort/ready/expiresAt`。

#### 5.24.4 业务逻辑

- **视角**：业务系统
- **描述方式**：Operator reconcile → Pending 排队/领池 → Provisioning 建 Pod + 起 envd → Ready 绑状态返回连接信息 → TTL/删除转 Draining → Deleted。

#### 5.24.5 数据描述

- 核心数据：Sandbox spec（owner/template/resources/ttl）→ status（phase/podName/podIP/grpcPort/ready/expiresAt）。

#### 5.24.6 验收标准 AC

- **Given** 提交声明，**When** reconcile，**Then** 状态机推进且 Ready 返回 IP/port/凭证
- **Given** 资源不足（异常路径），**When** 申请，**Then** 排队（Pending）不失败
- **Given** 删除声明（正常路径），**When** 触发，**Then** Draining→Deleted 幂等

#### 5.24.7 外部集成接口

- 依赖 K8s CRD + Operator + 预热池/新建。

### 5.25 US-25：就绪探活（P0）

#### 5.25.1 业务场景

- **视角**：OPS / Agent Runtime
- **描述逻辑**：仅当 Pod Ready + envd /healthz 通过才标 Ready；连续失败 N 次标 Failed 并通知重试。

#### 5.25.2 业务流程

- **视角**：用户
- **描述方式**：
  - Given 沙箱 Provisioning
  - When Pod Ready 且 envd /healthz 通过
  - Then status Ready
  - When 连续 health 失败 N 次（异常）
  - Then 标 Failed 并通知

#### 5.25.3 UE 原型

- status.conditions（PodReady, AgentHealthy）；Failed 通知。

#### 5.25.4 业务逻辑

- **视角**：业务系统
- **描述方式**：checkReady = podReady + agentHealthCheck(gRPC) → Ready + ExpiresAt → 连续失败 N 次 → Failed + 通知。

#### 5.25.5 数据描述

- 核心数据：PodReady 状态 → agent /healthz 结果 → conditions → Failed 事件。

#### 5.25.6 验收标准 AC

- **Given** Pod Ready 且 agent health 通过，**When** 检查，**Then** status Ready
- **Given** Pod Ready 但 agent 未健康（正常路径），**When** 检查，**Then** 保持 Provisioning 不标 Ready
- **Given** 连续 health 失败 N 次（异常路径），**When** 触发，**Then** 标 Failed 并通知重试

#### 5.25.7 外部集成接口

- 依赖 envd /healthz gRPC + Pod readiness。

### 5.26 US-26：回收 GC（P0）

#### 5.26.1 业务场景

- **视角**：OPS
- **描述逻辑**：TTL 到期 drain；owner 消失（孤儿 Pod）兜底回收；回收幂等。

#### 5.26.2 业务流程

- **视角**：用户
- **描述方式**：
  - Given TTL 到期
  - When reconcile
  - Then drain 回收
  - When owner 消失但 Pod 残留（异常）
  - Then 按 label 兜底删除

#### 5.26.3 UE 原型

- OPS 观察：GC 回收事件 + 孤儿 Pod 清理。

#### 5.26.4 业务逻辑

- **视角**：业务系统
- **描述方式**：TTL 重入队 → Draining → 删 Pod；孤儿 Pod 按 label 兜底删除；幂等（重复回收无副作用）。

#### 5.26.5 数据描述

- 核心数据：TTL 到期事件 → owner 状态 → 孤儿 Pod label → 删除事件。

#### 5.26.6 验收标准 AC

- **Given** TTL 到期，**When** 回收，**Then** drain 后删除
- **Given** owner 消失（异常路径），**When** 兜底，**Then** 孤儿 Pod 按 label 删除
- **Given** 重复触发回收（正常路径），**When** 执行，**Then** 幂等无副作用

#### 5.26.7 外部集成接口

- 依赖 Operator reconcile + K8s 删除。

### 5.27 US-27：预热池（P1）

#### 5.27.1 业务场景

- **视角**：OPS / Platform Engineer
- **描述逻辑**：维持 minSize 空闲 Ready Pod，优先领池，池空再新建，超 maxSize 裁剪，亚秒分配。

#### 5.27.2 业务流程

- **视角**：用户
- **描述方式**：
  - Given 池中空闲 Ready Pod 充足
  - When 新 claim 到来
  - Then 直接领用（亚秒分配）
  - When 空闲低于 min / 高于 max
  - Then 补足至 min / 裁剪至 max

#### 5.27.3 UE 原型

- OPS 面板：池命中率 / min/max 配置（完整版）。

#### 5.27.4 业务逻辑

- **视角**：业务系统
- **描述方式**：Pool.Acquire → 命中则领用，ErrEmpty 则 buildPod 新建 → idle 低于 min 补足 / idle 高于 max 裁剪。

#### 5.27.5 数据描述

- 核心数据：idle Ready Pod 数 → min/max → 领用/新建/裁剪事件 → 命中率。

#### 5.27.6 验收标准 AC

- **Given** 池有空闲 Ready Pod，**When** claim 到来，**Then** 领用（亚秒分配，P95 ≤ 500ms）
- **Given** 池空（正常路径），**When** claim 到来，**Then** 新建沙箱
- **Given** idle 低于 min（异常路径），**When** reconcile，**Then** 补足至 min
- **Given** idle 高于 max（正常路径），**When** reconcile，**Then** 裁剪至 max

#### 5.27.7 外部集成接口

- 依赖 K8s 调度 + envd 镜像预热。

### 5.28 US-28：控制面数据面分离（P0）

#### 5.28.1 业务场景

- **视角**：Platform Engineer
- **描述逻辑**：runtime 直连 envd agent（gRPC/WS），不经 Operator；Operator 挂掉不影响在跑会话；数据面双向流（stdio/pty）。

#### 5.28.2 业务流程

- **视角**：用户
- **描述方式**：
  - Given 沙箱 Ready
  - When runtime 执行七接口调用
  - Then 直连 envd（不经 Operator）
  - When Operator 故障（异常）
  - Then 在跑会话不受影响

#### 5.28.3 UE 原型

- 连接方式：集群外 port-forward / 集群内 Service+mTLS；数据面 gRPC 双向流。

#### 5.28.4 业务逻辑

- **视角**：业务系统
- **描述方式**：控制面（Operator）只管生命周期 → 数据面 runtime 直连 envd → Operator 不代理数据流量 → 故障隔离。

#### 5.28.5 数据描述

- 核心数据：sandboxId + 连接信息（podName/grpcPort）→ gRPC 双向流 → 数据不经过 Operator。

#### 5.28.6 验收标准 AC

- **Given** 沙箱 Ready，**When** runtime 执行调用，**Then** 直连 envd（不经过 Operator）
- **Given** Operator 故障（异常路径），**When** 在跑会话继续，**Then** 不受影响
- **Given** 数据面流式传输（正常路径），**When** 执行 stdio/pty，**Then** 双向流正常

#### 5.28.7 外部集成接口

- 依赖 envd gRPC 数据面 + port-forward/Service+mTLS【事实，D3 §8】。

### 5.29 US-29：mTLS（P1）

#### 5.29.1 业务场景

- **视角**：Security / Platform Engineer
- **描述逻辑**：控制面与数据面双向证书认证，cert-manager 自动轮转，证书不进 session log。

#### 5.29.2 业务流程

- **视角**：用户
- **描述方式**：
  - Given 生产环境
  - When runtime 连接 envd / 控制面
  - Then 双向证书认证通过
  - When 证书到期
  - Then cert-manager 自动轮转

#### 5.29.3 UE 原型

- 配置层：cert-manager Issuer（runtime 与 agent 各一份）+ caCert/clientCert/clientKey。

#### 5.29.4 业务逻辑

- **视角**：业务系统
- **描述方式**：mTLS 握手双向认证 → cert-manager 自动轮转 → 证书信息脱敏不进日志。

#### 5.29.5 数据描述

- 核心数据：caCert/clientCert/clientKey → 轮转周期 → 握手结果。

#### 5.29.6 验收标准 AC

- **Given** 证书有效，**When** 双向连接，**Then** mTLS 认证通过
- **Given** 证书到期（正常路径），**When** 轮转，**Then** cert-manager 自动更新且连接不断
- **Given** 证书错误（异常路径），**When** 连接，**Then** 拒绝且证书不进 session log

#### 5.29.7 外部集成接口

- 依赖 cert-manager + K8s Service。

### 5.30 US-30：可观测指标（P1）

#### 5.30.1 业务场景

- **视角**：OPS
- **描述逻辑**：Prometheus 指标（沙箱数按 phase/class、创建时延、池命中率、失败率）+ 告警（池耗尽/错误率飙升/Failed 堆积/逃逸检测）。

#### 5.30.2 业务流程

- **视角**：用户
- **描述方式**：
  - Given 平台运行
  - When 指标采集
  - Then 沙箱数/时延/命中率/失败率可查
  - When 异常告警触发
  - Then 实时告警（P99 ≤ 1s 呈现）

#### 5.30.3 UE 原型

- 监控/预警面板：实时刷新、一键扩容、告警（完整版）。

#### 5.30.4 业务逻辑

- **视角**：业务系统
- **描述方式**：OpenTelemetry 埋点贯穿 create→执行→destroy → Prometheus 采集 → 告警规则触发 → 通知 OPS。

#### 5.30.5 数据描述

- 核心数据：指标（沙箱数/创建时延/池命中率/失败率）→ traceId → 告警事件。

#### 5.30.6 验收标准 AC

- **Given** 平台运行，**When** 查询指标，**Then** 沙箱数按 phase/class + 创建时延 + 命中率 + 失败率可查
- **Given** 异常（池耗尽/Failed 堆积/逃逸检测），**When** 触发，**Then** 告警 P99 ≤ 1s 呈现
- **Given** traceId 贯穿（正常路径），**When** 追踪，**Then** 控制面与数据面关联可查

#### 5.30.7 外部集成接口

- 依赖 Prometheus/OpenTelemetry/Loki 可观测栈【事实，高层架构 §5.2】。

### 5.31 US-31：会话 fork/恢复（P2）

#### 5.31.1 业务场景

- **视角**：Agent Runtime
- **描述逻辑**：fork 传源 session + 边界点，新沙箱取边界点前快照，原 session 不受影响（增强版）。

#### 5.31.2 业务流程

- **视角**：用户
- **描述方式**：
  - Given 源 session 有边界点快照
  - When fork 新 session
  - Then 新沙箱取边界前快照，原 session 不受影响

#### 5.31.3 UE 原型

- SDK 接口：`fork(sourceSession, boundary)`。

#### 5.31.4 业务逻辑

- **视角**：业务系统
- **描述方式**：从 session log 定位边界点 → 取对应快照 → 新沙箱恢复 → 原 session 继续。

#### 5.31.5 数据描述

- 核心数据：源 session id + 边界点 → 快照 → 新 sandboxId。

#### 5.31.6 验收标准 AC

- **Given** 源 session 有快照，**When** fork，**Then** 新沙箱取边界前快照
- **Given** fork 后（正常路径），**When** 原 session 继续，**Then** 不受影响
- **Given** 边界点无快照（异常路径），**When** fork，**Then** 返回明确错误

#### 5.31.7 外部集成接口

- 依赖 Snapshot/microVM 内存快照（Out-of-Scope O5，增强版）。

### 5.32 US-32：暂停/恢复（P2）

#### 5.32.1 业务场景

- **视角**：Agent Runtime
- **描述逻辑**：暂停不占 CPU/内存（超卖），恢复现场（cwd/环境/打开文件）一致，恢复有时限（增强版）。

#### 5.32.2 业务流程

- **视角**：用户
- **描述方式**：
  - Given 沙箱运行中
  - When 暂停
  - Then 不占 CPU/内存
  - When 恢复
  - Then cwd/环境/打开文件一致

#### 5.32.3 UE 原型

- SDK 接口：`pause()` / `resume()`。

#### 5.32.4 业务逻辑

- **视角**：业务系统
- **描述方式**：microVM 内存快照挂起 → 释放 CPU/内存 → 恢复时从快照还原现场。

#### 5.32.5 数据描述

- 核心数据：暂停快照 → 现场（cwd/env/打开文件）→ 恢复时限。

#### 5.32.6 验收标准 AC

- **Given** 沙箱暂停，**When** 观察资源，**Then** 不占 CPU/内存
- **Given** 恢复（正常路径），**When** 触发，**Then** cwd/环境/打开文件一致
- **Given** 超恢复时限（异常路径），**When** 恢复，**Then** 返回明确错误

#### 5.32.7 外部集成接口

- 依赖 microVM 内存快照（Out-of-Scope O5，增强版）。

### 5.33 US-33：provider 可替换（P0）

#### 5.33.1 业务场景

- **视角**：Platform Engineer
- **描述逻辑**：统一 seam 三角色（fs/subprocess/terminal），配置层一行替换隔离后端/执行世界，agent loop 无感知。

#### 5.33.2 业务流程

- **视角**：用户
- **描述方式**：
  - Given 配置声明 provider
  - When 替换后端（如 gVisor→Kata）
  - Then 一行替换，agent loop 无感知
  - When 查询 seam 覆盖率
  - Then 100%

#### 5.33.3 UE 原型

- 配置层：`provider` 字段一行替换；agent 代码零改动。

#### 5.33.4 业务逻辑

- **视角**：业务系统
- **描述方式**：seam 三角色（Def/Provider/Consumer）→ Provider 注册 → 配置切换 → fs/subprocess/terminal 一起替换。

#### 5.33.5 数据描述

- 核心数据：provider 标识 → seam 三角色注册 → 替换生效范围。

#### 5.33.6 验收标准 AC

- **Given** 配置切换 provider，**When** 生效，**Then** fs/subprocess/terminal 一起替换且 agent 无感知
- **Given** 后端替换（正常路径），**When** 验证，**Then** 业务代码零改动（seam 覆盖率 100%）
- **Given** provider 未注册（异常路径），**When** 引用，**Then** 返回明确错误

#### 5.33.7 外部集成接口

- 依赖 capability seam 三角色架构 + 隔离运行时后端。

### 5.34 US-34：热重载可逆注册（P1）

#### 5.34.1 业务场景

- **视角**：Platform Engineer
- **描述逻辑**：注册返回 disposer，按序回滚，无悬挂引用/监听器残留。

#### 5.34.2 业务流程

- **视角**：用户
- **描述方式**：
  - Given 注册某 provider/effect
  - When 热重载
  - Then 返回 disposer，按序回滚
  - When 回滚后
  - Then 无悬挂引用/监听器残留

#### 5.34.3 UE 原型

- 配置层：热重载接口返回 disposer，按注册逆序回滚。

#### 5.34.4 业务逻辑

- **视角**：业务系统
- **描述方式**：Registrations are reversible effects → disposer 按序回滚 → 校验无残留引用。

#### 5.34.5 数据描述

- 核心数据：注册项 → disposer → 回滚顺序 → 残留检测。

#### 5.34.6 验收标准 AC

- **Given** 注册 provider，**When** 热重载，**Then** 返回 disposer 且按序回滚
- **Given** 回滚后（正常路径），**When** 检查，**Then** 无悬挂引用/监听器残留
- **Given** 回滚失败（异常路径），**When** 触发，**Then** 返回明确错误且状态可查

#### 5.34.7 外部集成接口

- 依赖 Cordis 可逆 effects 框架【事实，D5 §3】。

## 6. 非功能性需求

### 6.1 易用性需求

- **操作便利性**：Agent Runtime 经 SDK/CLI 一行创建沙箱（`Sandbox.create(template, timeout)` 上下文管理），七接口命名空间语义对齐 e2b 事实标准，降低接入成本。
- **UI 一致性**：SDK 错误语义统一区分「策略拦截 / 命令失败 / 超时 / 资源不足 / 隔离档不可用」，避免 agent 误判。
- **引导提示**：SDK/CLI 提供 `sandbox create/list/kill/logs` 帮助；SandboxClass/模板配置提供默认值（runAsNonRoot/readOnlyRootFilesystem/seccomp/drop ALL）。
- **错误反馈**：策略拦截、版本冲突（FS_STALE_VERSION）、文件超限（FS_TOO_LARGE）等均有可识别的错误码/类型，支持 agent 正确归因与重试。
- **无障碍支持**：CLI 与声明式 API 输出可被脚本/agent 稳定解析（结构化状态、稳定字段名）。

### 6.2 性能响应需求

> 本节区分「继承冻结目标」与「待压测校准建议值」：前者为上游已冻结的对外承诺，后者以 `【建议】` 标注、需人工确认或压测校准，不构成硬承诺。

| 指标 | 目标值 | 口径 | 来源/状态 |
| --- | --- | --- | --- |
| 并发沙箱实例数 | **1000 个并发沙箱实例**（同一时刻最多 1000 个活跃隔离执行环境） | 峰值并发 | 用户原始诉求【事实，N1】 |
| 沙箱就绪时延 P95 | ≤ 500ms（预热命中）/ ≤ 3s（冷启动） | P95 | 【事实，V2】 |
| 就绪时延 P50/P90（细分） | 【建议】P50 ≤ 200ms（预热）/ ≤ 1s（冷），P90 ≤ 400ms（预热）/ ≤ 2.5s（冷） | 待压测校准 | 【建议，待确认】 |
| 隔离层内存总量 | ≤ 15GB（gVisor 默认档，1000 并发） | 完整版压测 | 【建议，V4】 |
| 有状态连续性 | 同沙箱跨调用上下文保留率 100% | MVP 上线 | 【事实，V3】 |
| 隔离覆盖率 | 不可信代码 100% 落入 gVisor/Kata 隔离档 | MVP 上线 | 【事实，V1】 |
| 平台吞吐 QPS/TPS | 【建议】创建/销毁 QPS 由压测确定（对齐 1000 并发实例稳态负载），数据面 gRPC 流式吞吐不受 Operator 瓶颈限制 | 待压测校准 | 【建议，待确认】 |
| 告警呈现时延 | 告警触发到呈现 P99 ≤ 1s | 完整版 | 【事实，高层架构 §6.5】 |

### 6.3 操作与环境需求

- **运行环境**：自建 Kubernetes 集群（不绑定任何云厂商）【经上游锁定，need_cloud_baseline_check=false】；runc/gVisor 两档任意节点可跑，Kata 档要求节点具备 KVM + 嵌套虚拟化（裸金属最佳）【事实，高层架构 §5.2】。
- **网络环境**：集群外经 port-forward 连接数据面；集群内经 Service + mTLS 连接；出站默认拒绝 + 白名单。
- **设备/客户端规格**：SDK 支持主流语言（Python 等，对齐 e2b 形态）；CLI 与声明式 API 面向脚本/agent 可解析。
- **浏览器兼容性**：管理控制台（沙箱列表/详情/审计/监控面板）兼容主流现代浏览器（Chrome/Edge/Safari/Firefox 近两个大版本）。

### 6.4 安全性需求

> 满足《高层架构设计》§6.1 F2/F4/F6/F8/F9 与安全底座相关安全标准；本节覆盖 §6.4.1~§6.4.6 全部子节。

#### 6.4.1 安全密码设置

- 平台账号密码（若涉及）需满足 **8 位以上大小写字母 + 数字 + 特殊字符** 强度；沙箱内凭据采用 per-sandbox secret 注入，非环境级明文传递【事实，D1 §6】。

#### 6.4.2 安全软件架构

- **通信安全**：控制面与数据面采用 mTLS 双向证书（完整版）；集群内经 Service + NetworkPolicy 限定入站源。
- **认证与访问控制**：RBAC namespace 级授权（租户只操作自己的 Sandbox）；AdmissionWebhook 强制约束；per-owner 配额 + ResourceQuota。
- **外系统接口安全**：与 Agent Runtime、审计系统等外系统交互时限制未经许可的接口访问，使用加密与认证手段（REST/OpenAPI + gRPC mTLS），限制外部系统可获取内容（最小权限 + 凭据剥离）。

#### 6.4.3 安全设计

- **认证授权**：提供 RBAC 认证授权功能；多租户隔离粒度 = 租户级 namespace + per-owner 配额 + RBAC。

#### 6.4.4 安全开发

- 对函数入口参数（七接口 RPC 参数、CRD spec 字段）进行合法性与准确性检查。
- 输入边界检查：路径长度/格式、文件大小（maxBytes）、命令长度、TTL 取值范围等。
- 保证不因代码编写导致可被直接利用的高危漏洞（容器逃逸、注入、越权）。
- 输入输出模块过滤，防范恶意指令与内部信息泄露（敏感环境变量剥离）。
- 禁止使用未经授权和验证的代码（镜像签名 + 漏洞扫描）。
- 保证应用不存在可绕行安全机制的行为或遗留后门（seccomp/AppArmor 注入 + AdmissionWebhook 强制）。

#### 6.4.5 安全测试和部署

- 进行安全扫描测试（镜像漏洞扫描、依赖扫描）。
- 进行安全配置基线检查（非 root/只读 rootfs/drop ALL/seccomp）。
- 进行安全功能测试（隔离覆盖率、逃逸检测、审计完整性）。
- 系统上线前不存在高危风险（高危漏洞清零）。

#### 6.4.6 数据安全

- **数据存储和传输加密**：用户密码、身份鉴别信息、平台密钥等重要数据存储与传输过程适当加密；凭据不进沙箱、不进 session log；审计日志 append-only 不可篡改。

---

## 7. 待确认项（需人工确认）

> 本节汇总本阶段未由上游冻结、需人工确认或压测校准的项，不阻塞 G4 冻结，但需在进入下游前明确。

| 编号 | 待确认项 | 现状 | 影响范围 | 建议 |
| --- | --- | --- | --- | --- |
| C1 | 七接口各操作时延 P50/P90 细分目标 | 上游仅冻结就绪时延 P95（≤500ms/≤3s） | §6.2 性能基线 / 下游压测口径 | 压测后校准，本文档暂按【建议】值 |
| C2 | 平台吞吐 QPS/TPS 目标 | 上游仅冻结「1000 并发实例」 | 容量规划 / 压测 | 压测后校准 |
| C3 | 单沙箱默认资源配额（cpu 2/memory 4Gi） | 为 material 示例值 | 模板默认值 / 成本 | 需平台团队确认默认档 |
| C4 | 文件读取 maxBytes 默认上限 | material 仅给错误码 FS_TOO_LARGE，未给数值 | US-5/6 验收阈值 | 需确认默认值 |
| C5 | 日志 / 审计保留期 | 上游未明确 | 存储成本 / 合规 | 需安全合规团队确认 |
| C6 | 出站白名单默认列表 | 上游给示例（pypi/registry），非最终清单 | US-15 验收 | 需确认默认白名单 |
| C7 | 场景频率（QPS）估值 | §4.2 S1~S7 频率为【假设】 | 容量规划 | 需业务方提供真实负载 |

---

## 附录 A：中间确认自检报告

> 按中间确认协议 §2.4，在 §3 / §4 / §5 / §6 关键章节后插入自检：先按 §2.1 判定，再按 §2.3 反向验证 3 问。本阶段 4 个自检节点均未命中触发条件，反向验证 3 问答案与证据如实记录如下。

### A.1 §3 完成功能清单后自检

**§2.1 判定**：未命中方案分歧型。
- 功能清单 F1~F12 + US 优先级 P0/P1/P2 + MVP/完整版范围，全部继承《高层架构设计》§6.3 与 §4.3 已冻结划分，未做任何 P0↔P1/P2 调整，未新增/裁剪功能。

**反向验证 3 问（对「功能优先级与 MVP 范围」决策）**：
- Q1：若 3 个月后推翻（某功能 P0↔P1 调整），返工成本可控吗？——返工范围 = §3 功能清单 + §5 对应 US 的 MVP 标记，切换成本 低于 1 人周。**可控**。
- Q2：用户/客户/监管能感知到吗？——P0/P1 分界已由上游 34 条用户故事（D4）与高层架构 §4.3 显式定义，本阶段仅继承，未引入新的用户可见取舍。**感知点属继承，非新增分歧**。
- Q3：与用户原始诉求一致吗？——用户诉求「通用 K8s agent sandbox + 1000 并发」，MVP=全部 P0 覆盖隔离/七接口/生命周期/核心治理/可替换，未偏离。**一致**。

**结论**：未命中，不发起中间确认。

### A.2 §4 完成角色与场景清单后自检

**§2.1 判定**：未命中方案分歧型。
- 角色清单直接继承《高层架构设计》§2.1 已冻结的 5 类角色，未细分、未新增（材料摘要 D4 的「SU」操作已并入「Agent Runtime」覆盖，遵循「不绕开高层架构重定义角色范围」纪律）。

**反向验证 3 问（对「角色清单继承（不新增 SU 子角色）」决策）**：
- Q1：若 3 个月后推翻（新增 SU 独立角色），返工成本可控吗？——返工范围 = §4.1 角色表 + 涉及 US 的角色归属标注，切换成本 低于 1 人周。**可控**。
- Q2：用户/客户/监管能感知到吗？——SU 的「发起任务/看结果/中断」操作已由「Agent Runtime」角色完整覆盖，不新增角色不改变任何用户可见能力。**感知不到**。
- Q3：与用户原始诉求一致吗？——用户诉求未显式要求角色细分，5 角色已覆盖甲方决策/开发/平台/安全/运维全部利益方。**一致**。

**结论**：未命中，不发起中间确认。

### A.3 §5 完成全部 US 七段式展开后自检

**§2.1 判定**：未命中方案分歧型。
- US 拆分粒度：34 条 US 编号与粒度完全继承《资料摘要》D4 已冻结划分（P0=23/P1=9/P2=2），未拆分、未合并、未调整；US 编号与《高层架构设计》§4.3 完全一致。
- 验收标准严格度：错误率上限、超时阈值、就绪时延等均继承上游冻结值（V1~V4、N1/N2）；缺失阈值（maxBytes、默认资源、白名单）标注【建议】并列入 §7 待确认项，未硬编码为新对外承诺。

**反向验证 3 问（对「US 粒度继承 + 验收阈值标注方式」决策）**：
- Q1：若 3 个月后推翻（US 重新拆分/合并），返工成本可控吗？——返工范围 = §5 用户旅程 + §3 功能清单总数，切换成本 约 1~2 周。**可控**（且粒度已由上游 34 条 US 基线锁定，推翻概率极低）。
- Q2：用户/客户/监管能感知到吗？——US 粒度不改变对外七接口与隔离档形态；验收阈值中未冻结部分已标注【建议】+ 待确认，未擅自承诺 SLA。**感知不到（未新增 SLA 承诺）**。
- Q3：与用户原始诉求一致吗？——用户诉求「1000 并发」已作为 N1 纳入 §6.2，七接口/三档隔离/生命周期全部覆盖。**一致**。

**结论**：未命中，不发起中间确认。

### A.4 §6 完成非功能性需求后自检（最后一次完整复核）

**§2.1 判定**：未命中新的方案分歧。
- §6.2 性能目标值：继承上游冻结基线（就绪 P95 ≤500ms/≤3s、1000 并发、内存 ≤15GB、隔离覆盖率 100%、有状态连续性 100%）；缺失细分值（P50/P90、QPS）标注【建议】并列入 §7 待确认项，不构成对外承诺。
- §6.4 安全：覆盖 §6.4.1~§6.4.6 全部子节，与《高层架构设计》F2/F4/F6/F8/F9 安全基线对齐。

**反向验证 3 问（对「§6.2 性能目标值」决策）**：
- Q1：若 3 个月后推翻（性能目标值调整），返工成本可控吗？——返工范围 = §6.2 指标表 + 下游压测口径，切换成本 低于 1 人周。**可控**。
- Q2：用户/客户/监管能感知到吗？——继承的对外承诺（就绪时延/1000 并发/内存）已由上游冻结；【建议】值明确标注为待压测校准、非承诺，不构成可感知的 SLA。**感知不到（未新增承诺）**。
- Q3：与用户原始诉求一致吗？——「1000 并发」显式纳入并发沙箱实例数目标，未偏离原始诉求。**一致**。

**结论**：未命中，不发起中间确认。

---

> **附录 A 结论**：四个自检节点（§3/§4/§5/§6）均未命中协议 §2.1 与 §2.2 触发条件；反向验证 3 问 Q1 均「可控」、Q2 均「感知不到或属继承」、Q3 均「一致」，证据已如实记录。本阶段**无需发起 `[中间确认]`**，全部决策继承 G1/G3 已冻结范围，缺失阈值均以【建议】标注并列入 §7 待确认项，不擅自新增对外承诺。



