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
| 系统设计 / UserStory / 部署设计 / 安全设计 | 进行中（G4/G5） |

## 隔离技术栈

| 隔离档 | 运行时 | 启动 | 内存 | 适用 |
| --- | --- | --- | --- | --- |
| 可信级 | runc | 毫秒级 | ~0 | 内部可信代码 |
| 不可信级（默认） | gVisor (systrap) | 毫秒级 | ~15MB/沙箱 | 用户上传代码、无 KVM |
| 强隔离级 | Kata (Dragonball) | ~150ms | ~40MB + overhead | 多租户/敏感/合规 |

## 关键决策

- 隔离默认档：默认 gVisor + 敏感场景升级 Kata（三档分层）
- 控制面：自研对标 agent-sandbox，接口对齐 e2b 七接口事实标准
- 部署形态：自建 K8s 通用设计，不绑定云厂商（containerd CRI）
