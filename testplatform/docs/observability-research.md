# LLM/Agent 可观测性工具深度调研

<!-- AGENT-GUIDE-START -->
> **FOR AI AGENTS（机器可读导读）**
>
> - **本文档用途**：为 `testplatform/`（基于 trpc-agent-go 的 Agent 测试监控平台）的迭代开发提供参考。当被要求"实现某个观测/评测功能"时，先查 §4 对应设计模式与 §5 路线图条目，再看 §2 中代表工具的实现细节。
> - **结构约定**：§2 每个工具档案都是固定小节（定位理念/数据模型/功能清单/接入实现/自托管架构/HITL/借鉴要点），可按小节名定位提取。§3 是能力×工具矩阵。§5 每条建议含「参考对象」与「落地位置」（映射到本仓库代码路径）。
> - **可信度约定**：全部事实经过独立核验代理反驳式核查；仍不确定的在文中标注「未核实」。§7 附核验记录。日期基准 2026-07-08，工具迭代快，实现前建议对关键 API 再查官方文档。
> - **落地位置速查**：trace 数据模型 → `testplatform/internal/platform/trace.go`；采集/埋点 → `collector.go`；断点 HITL → `breakpoint.go`；用例与断言 → `testcase.go` / `testrun.go`；上下文归因 → `provenance.go`；HTTP API → `api.go`；前端 → `web/`。
<!-- AGENT-GUIDE-END -->

本报告调研了 20+ 个主流 LLM/Agent 可观测性与评测工具，覆盖商业平台、开源自托管方案、协议标准、开发期调试器与测试框架五类。目标不是排名，而是**拆出每家的设计理念与实现方式，提炼可复用的设计模式，并映射为本平台的开发路线图**。

## 1. 领域全景与分类

按"埋点位置"和"服务阶段"两个轴，这个领域可以切成五类：

| 类别 | 解决什么 | 代表 | 与本平台的关系 |
| --- | --- | --- | --- |
| **全生命周期观测平台** | 生产级 tracing + 评测 + prompt 管理 + 数据集闭环 | LangSmith、Langfuse、Braintrust、W&B Weave、Opik、Arize Phoenix | 功能范围的主要参照系 |
| **网关/代理式观测** | 换个 baseURL 就能观测所有 LLM 流量 | Helicone、Portkey、Braintrust proxy | 一种接入形态，与回调式互补 |
| **协议与标准层** | 统一 span 语义与传输，避免厂商锁定 | OpenTelemetry GenAI semconv、OpenInference、OpenLLMetry | P1 对齐目标 |
| **开发期调试 UI** | 单机断点、状态编辑、time-travel | LangGraph Studio、Google ADK Web、OpenAI Traces | 本平台断点介入的直接对标 |
| **测试/评测框架** | 用例矩阵、断言体系、CI 门禁、红队 | promptfoo、DeepEval、autoevals | 本平台测试用例模块的直接对标 |

**本平台的独特站位**：市面上"生产观测平台"（Langfuse 等）普遍没有断点介入，"开发调试器"（Studio 等）普遍没有测试用例与批量回归——本平台同时占住这两格（开发期深度介入 + 测试闭环），这是差异化，也是后文路线图刻意保持的边界。

### 1.1 全景查漏：其余值得知道的工具

### LLM/Agent 可观测性：补充工具盘点（2026-07）

以下为此前未覆盖、但在 2025–2026 各类对比文章与清单中反复出现的工具。

### 网关式观测（Gateway + Observability）

**Portkey** — 定位为企业级 AI Gateway，以路由、fallback、负载均衡、缓存为核心，顺带提供请求级日志与成本观测。网关本身开源（Portkey-AI/gateway，支持 1,600+ 模型、50+ 内置护栏），跑在边缘节点上，宣称 20–40ms 开销、99.99% 可用性；但评测/质量监控层较薄，通常需搭配专门评测平台。托管控制台为商业产品。

**Keywords AI** — YC 系创业公司，走"LLM 应用的 Datadog"路线：统一网关代理 200+ 模型，叠加日志、成本/延迟监控与 Prompt 管理。闭源 SaaS，主打小团队快速接入；在 2026 年的主流对比文章中曝光度明显低于 Portkey/Helicone。

**TrueFoundry** — 企业级 AI Gateway + 观测组合，强调治理与私有化部署，闭源。

### 全栈工程平台（Trace + Eval + Prompt 管理）

**Galileo** — 定位"评测智能"（evaluation intelligence）：用自研 Luna-2 小模型（基于 Llama 3B/8B 微调）做低成本评估器，宣称 sub-200ms、比 LLM-as-judge 便宜 97%，可对 100% 生产流量实时评分并作为护栏拦截幻觉/毒性输出。2025 年推出免费的 Agent Reliability Platform。平台闭源，累计融资 $68M。

**HoneyHive** — 全生命周期平台：OTel 基础上的 tracing、评测、Prompt 管理与人工评审工作流，人评能力是其相对亮点。闭源 SaaS。

**Vellum** — 低代码 LLM 编排平台，把 Prompt 版本管理、工作流编排、评测与生产观测捆绑在一套 UI 里，面向产品/非工程角色友好。闭源。

**Maxim AI** — 端到端评测+观测，特色是"模拟优先"：合成多轮对话做发布前 Agent 仿真测试，以及无代码拖拽式 eval builder。平台闭源，但开源了高性能 LLM 网关 Bifrost。

**LangWatch** — 开源 LLMOps 平台（GitHub 2,500+ stars），OTel 原生，覆盖多轮 Agent 追踪、自动评测生成、仿真测试与治理，可自托管。

**Latitude** — 开源 Prompt/Agent 平台，差异点是通过 MCP 与 Claude Code、Cursor 等编码 Agent 打通，把"发现生产故障 → 自动开 PR 修复"做成闭环。

### 插桩层 / 标准生态

**Langtrace**（Scale3 Labs）— 开源、OTel 标准的 tracing SDK + 平台，强调无厂商锁定，云版 SOC 2 Type II 认证，可自托管。

**Traceloop 平台** — OpenLLMetry（开源，7,200+ stars）背后公司的商业托管层，补充仪表盘、告警与可靠性监控；因数据是纯 OTel 格式，可无缝转发 Datadog/New Relic/Honeycomb。

**Honeycomb** — 通用 OTel 观测厂商，无专门 LLM 产品线，但凭高基数事件分析 + OpenLLMetry/GenAI 语义约定摄入，成为"复用现有观测栈"路线的代表。闭源 SaaS。

### 已退场（警示案例）

**Literal AI**（Chainlit 团队）— 曾是协作式观测+评测平台，2025-10-31 正式停服、数据删除，仅留开源 Data Layer；Chainlit 本体也转入社区维护。是该赛道整合出清的典型样本。

### 全景分类总结

结合已覆盖工具，整个领域可分七类：**(1) 全栈 LLM 工程平台**（trace+eval+prompt 一体）：LangSmith、Langfuse、Braintrust、W&B Weave、Opik、HoneyHive、Vellum、Maxim、LangWatch；**(2) 网关/代理式观测**：Helicone、Portkey、Keywords AI、TrueFoundry；**(3) Agent 原生观测**：AgentOps、Laminar、Lunary、Latitude；**(4) 开放标准与插桩层**：OTel GenAI 语义约定、OpenLLMetry/Traceloop、OpenInference/Phoenix、Langtrace——三套语义约定仍在竞争；**(5) 评测优先/护栏**：Galileo、promptfoo、DeepEval/Confident AI；**(6) APM 厂商延伸**：Datadog、New Relic、Honeycomb；**(7) 开发期调试 UI**：LangGraph Studio、ADK Web。2026 年的两条主线：一是行业加速整合（Langfuse 于 2026-01 被 ClickHouse 收购，Helicone 于 2026-03 被 Mintlify 收购后转维护模式，Literal AI 停服），二是选型标准趋同——OTel 原生、评测与观测耦合、能把失败 trace 聚类成可修复问题的平台胜出；市场规模约 $2.69B（2026），预计 2030 年达 $9.26B。

### Sources

- [Confident AI: 10 LLM Observability Tools 2026](https://www.confident-ai.com/knowledge-base/compare/10-llm-observability-tools-to-evaluate-and-monitor-ai-2026)
- [MLflow: Top LLM Observability Tools in 2026](https://mlflow.org/articles/top-llm-observability-tools-in-2026-a-pro-guide/)
- [Latitude: Best AI Agent Observability Tools 2026](https://latitude.so/blog/best-ai-agent-observability-tools-2026-comparison)
- [Latitude: 15 AI Agent Observability Platforms 2026](https://latitude.so/blog/15-ai-agent-observability-platforms-2026-agentic-complexity)
- [Portkey Gateway (GitHub)](https://github.com/portkey-ai/gateway)
- [Galileo: Free Agent Reliability Platform (PR Newswire)](https://www.prnewswire.com/news-releases/galileo-announces-free-agent-reliability-platform-302508172.html)
- [Galileo Luna-2 Docs](https://docs.galileo.ai/concepts/luna/luna)
- [Traceloop / OpenLLMetry (GitHub)](https://github.com/traceloop/openllmetry)
- [Traceloop: Honeycomb 集成](https://www.traceloop.com/docs/openllmetry/integrations/honeycomb)
- [LangWatch (GitHub)](https://github.com/langwatch/langwatch)
- [Maxim AI 官网](https://www.getmaxim.ai/)
- [Vellum Observe](https://www.vellum.ai/products/monitoring)
- [Literal AI Migration Guide](https://docs.literalai.com/more/migration-guide)
- [Firecrawl: Best LLM Observability Tools 2026](https://www.firecrawl.dev/blog/best-llm-observability-tools)
- [Braintrust: Best LLM Gateways for Observability 2026](https://www.braintrust.dev/articles/best-llm-gateways-observability-2026)

## 2. 工具深度档案

> 每份档案结构固定：定位理念 / 数据模型 / 功能清单 / 接入实现 / 自托管架构 / HITL / 借鉴要点。全部事实经独立核验代理反驳式核查；被反驳的条目已在档案末尾以「核验修正」标注订正。

### 2.1 LangSmith (LangChain)

**类别**：tracing平台 / LLM可观测+评测一体化平台（闭源商业SaaS+自托管企业版）

#### 定位与设计理念
LangSmith 是 LangChain 官方的 LLM 应用「可观测 + 评测 + 提示词工程」一体化平台，覆盖从开发调试、离线回归测试到生产监控的全生命周期。核心取舍：**闭源商业产品**（SaaS 为主，自托管是 Enterprise 计划附加项、需 license key），换取深度打磨的 UI 与开箱即用体验；框架无关（不必用 LangChain，可用 SDK 装饰器或纯 OTel 接入），但与 LangChain/LangGraph 集成最深。一句话哲学：**"trace 是一切的原料"**——先把每一步执行记录成结构化 Run 树，再在其上叠加反馈、评测、数据集、监控，形成 online→offline 的闭环（生产 trace 一键入 dataset，dataset 跑 experiment，评分回流监控）。

#### 数据模型
层级为 **Project（tracing project，内部字段名 session_id）→ Trace → Run**，多轮对话用 **Thread** 把多条 trace 串起来（通过 metadata 中的 `session_id`/`thread_id`/`conversation_id` 键关联）。
- **Run（= OTel span 概念）** 是核心实体，字段非常具体：`id`(UUID)、`name`、`inputs`/`outputs`(对 llm run 通常是 messages 数组)、`run_type`、`start_time`/`end_time`、`error`、`status`(success/error/pending)、`events`(流式事件)、`tags`、`extra`(含 metadata)、`trace_id`(即根 run 的 id)、`parent_run_id`、`child_run_ids`、`feedback_stats`、`reference_example_id`(评测运行关联的数据集样例)、token/成本字段（`prompt_tokens`/`completion_tokens`/`total_tokens`、`prompt_cost`/`completion_cost`/`total_cost`、`first_token_time`）、`in_dataset`、`session_id`(所属项目)。
- **run_type** 枚举：`chain`(默认)、`llm`、`tool`、`retriever`、`embedding`、`prompt`、`parser`——语义标签，决定 UI 渲染（llm 显示 token/延迟，retriever 显示文档列表）与统计归类。
- **dotted_order**：独特设计，一个可排序字符串 `<start_time>Z<uuid>.<child_start>Z<child_uuid>...`，单字段完整编码了 run 在树中的位置与时间序，前缀即祖先链，直接按字典序排序就得到树的先序遍历。
- **Feedback（score）**：绑定到 run id 的评分记录，`key`(指标名) + `score`(数值) 或 `value`(类别) + `comment`，人工标注、用户反馈、评测器输出统一走这一实体。
- **Dataset → Example**（inputs + 可选 reference outputs + metadata）；**Experiment** = 某应用版本在一个 dataset 上的一次评测结果集，可多实验并排对比。SaaS trace 保留 400 天，dataset 永久。

#### 核心功能清单
- **Trace 视图**：有。树状 trace tree，按 run_type 差异化渲染，逐节点显示 token/成本/延迟聚合。
- **会话/线程**：有。Threads 视图按 thread 聚合多轮对话，支持 thread 级成本聚合与 thread 级在线评测（多轮评估）。
- **Playground**：有。UI 中直接改 prompt/模型/参数重跑，可从任意 trace 打开、可对 dataset 跑评测；自托管中是独立 playground 服务（代理转发到各 LLM API）。
- **Prompt 管理**：有。prompt 模板存储、commit/tag 版本化、f-string/mustache 变量、SDK 拉取，面向非工程角色协作。
- **数据集**：有。dataset/example 管理、从生产 trace 或标注队列导出入库、多版本、按 metadata 过滤切分。
- **实验对比**：有。experiment 与 dataset 绑定，多实验并排 diff、pairwise 对比。
- **评测**：有，双轨制。离线（对 dataset，可比对参考输出）+ 在线（对生产 runs/threads，无参考输出）；评测器类型：代码规则、LLM-as-judge（模板/自定义、few-shot 校正、多模态附件输入）、pairwise、composite；在线评测器支持**过滤器 + 采样率（如 0.1）+ 历史回填**，是 workspace 级资源可复用。
- **标注/人工反馈**：有。annotation queues（详见 HITL 节）+ inline 标注 + SDK 提交用户反馈（含 presigned feedback token）。
- **监控告警**：有。每项目自动预建 dashboard（Traces/LLM Calls/Cost&Tokens/Tools/Run Types/Feedback Scores 六节，支持按 tag/metadata 分组）+ 自定义 dashboard；Alerts 为项目级阈值告警，指标含 Run Count、Cost、Errors(计数/比率)、Feedback Score、Latency，可按 run_type/tag/error 过滤，路由 Slack/PagerDuty/Dynatrace/任意 webhook。
- **成本核算**：有。主流 provider 自动按 token 计价（区分 cache read/reasoning 等子类），支持自定义价格表（price_model_id）与任意 run 的自定义成本上报，trace/项目/dashboard 三级聚合。

#### 接入与实现方式
四种形态并存：1) **`@traceable` 装饰器**（Python/TS/Java/Kotlin），包装任意函数为 run，自动跨嵌套调用传播上下文形成树，可指定 run_type/name/自定义 run id；2) **`wrap_openai`/`wrapOpenAI` 客户端包装器**，包一层 OpenAI client 即自动记录所有调用；3) **`trace` 上下文管理器**（Python）与低层 **RunTree API**（显式建树，不受 LANGSMITH_TRACING 开关控制）；4) **框架集成**即自动埋点（LangChain/LangGraph/OpenAI/Anthropic/CrewAI 等）。埋点原理是 SDK 内在进程内维护 run tree 上下文并**批量异步 POST 到 LangSmith ingest API**，环境变量 `LANGSMITH_TRACING`/`LANGSMITH_API_KEY`/`LANGSMITH_PROJECT` 控制。
**与 OTel 的关系**：双向兼容而非 OTel 原生。平台暴露 **OTLP ingest 端点** `https://api.smith.langchain.com/otel`（自托管为 `<host>/api/v1/otel`，可能需追加 `/v1/traces`），header 用 `x-api-key` 认证、`Langsmith-Project` 指定项目；识别 GenAI 语义约定 `gen_ai.*` 属性，并用自有属性 `langsmith.span.kind`、`langsmith.metadata.*` 映射到 run 模型。反向可设 `LANGSMITH_OTEL_ENABLED=true` 让 LangChain 应用经 OTel 协议导出，并可经 OTel Collector 扇出到多后端。

#### 自托管与后端架构
仅企业版可自托管（license key），**闭源**，Helm 装到 Kubernetes（GKE/EKS/AKS/OpenShift，官方提供 AWS/Azure Terraform 模块）。组件（与官方文档逐一对应）：
- **frontend**：Nginx，服务 UI 并路由 API，唯一必须暴露的入口；
- **backend**：CRUD API 主入口，业务逻辑、trace 预处理、hub API；
- **platform backend**：认证、**run ingestion** 等高吞吐路径；
- **queue（worker）**：异步消费 trace/feedback，做完整性校验、入库与失败重试；
- **playground** 服务：转发 LLM 请求；**ACE backend**：沙箱内执行任意代码（支撑自定义代码评测器）。
存储：**ClickHouse** 存 trace 与 feedback（高容量 OLAP，UI 性能关键）；**PostgreSQL** 存其余事务性/运营数据；**Redis（官方支持 Valkey 替换）** 做队列与缓存；**Blob storage（S3/GCS/Azure Blob，生产强烈推荐）** 存大对象（inputs/outputs 落 S3 后 run 上只留 `inputs_s3_urls` 引用）。默认全部内置打包，生产建议全部外接托管服务。可选叠加 LangSmith Deployment（control plane + data plane）部署 agent。

#### 人工介入与 HITL
- **标注队列（annotation queues）**：功能最完整的部分。两种队列：单 run 队列（一次审一条，配 rubric 指标 + 说明 + assertions 验收条件）与 **pairwise 队列**（两个 run 并排选优）。协作机制很细：run 预留锁（reservation + 超时释放）、每 run 需 N 个审阅人、指定审阅人（Needs Review → Needs Others' Review → Completed 三态）、审阅人互相看不到对方评分（防偏置）但共享评论、审完一键导出到 dataset。支持 SDK 编程管理。
- **审计**：evaluator 评分可人工审计修正（audit evaluator scores），few-shot 修正样例可回喂 LLM 评审。
- **断点/状态编辑/审批流**：**无**。LangSmith 作为观测平台不提供 agent 运行时断点或 state 编辑——这属于 LangGraph（interrupt/checkpoint）的能力范畴；LangSmith 侧的"人工介入"是事后标注与 Playground 修改重跑，未核实其 Deployment 附加组件 UI 中的 agent 交互能力细节。

#### 值得借鉴的点
1. **dotted_order 排序键**：用 `时间戳Z+UUID` 点分级联串编码整棵 run 树，ClickHouse 里按此单列排序即可还原树形与时序，无需递归查询。Go 平台若用 ClickHouse 存 span，直接抄这个设计做主排序键，树展开查询变成前缀扫描。
2. **run_type 语义枚举（llm/chain/tool/retriever/…）**：仅 7 个值却驱动了 UI 差异渲染、dashboard 自动分节（LLM Calls/Tools/Run Types）、告警过滤。比开放式 span kind 更可控，建议在 trpc-agent-go 的 span 属性上定义同等枚举并贯穿到查询层。
3. **OTLP 端点 + 自有属性命名空间**：不重造协议，直接收 OTLP，用 `x-api-key` header 认证、`langsmith.span.kind`/`langsmith.metadata.*` 属性 + `gen_ai.*` 语义约定映射到内部模型。Go 生态 OTel 是事实标准，这是接入成本最低的路径，且天然兼容 Collector 扇出。
4. **feedback 统一实体**：人工标注、终端用户反馈、在线 LLM 评审、离线评测器输出全部收敛为 `{key, score|value, comment}` 绑定 run id 一张表，dashboard/告警/实验对比全部消费同一数据，避免评测与监控两套评分体系。
5. **在线评测器 = 过滤器 + 采样率 + 回填**：把"对生产流量跑 LLM 评审"产品化成三个旋钮（run 过滤条件、采样比例控成本、历史回填补数据），并作为 workspace 级资源跨项目复用，实现成本可控且立即可用。
6. **写路径架构**：ingest 由高吞吐 platform-backend 收下后进 Redis 队列，worker 异步校验、重试、批量写 ClickHouse，大 payload 卸载到 blob storage 只存引用。这套「热路径只做 append、OLAP 与事务数据分库（ClickHouse + Postgres）」的分工是 trace 平台被反复验证的形态（Langfuse v3 同构），Go 实现时值得直接采用。

### 2.2 Langfuse

**类别**：tracing平台（LLM 工程平台：观测 + Prompt 管理 + 评测一体化）

#### 定位与设计理念
Langfuse 自我定位为「开源 LLM 工程平台」，帮团队协作地开发、监控、评测、调试 AI 应用（README 原话）。它不是运行时框架，而是应用旁路的数据平台：把 tracing（调试与监控）、prompt 管理（部署与迭代）、评测（scores/datasets/experiments/LLM-as-judge/人工标注）三件事装进同一套数据模型，用「trace ↔ prompt 版本 ↔ score ↔ dataset item」的互相引用形成闭环——线上看到坏 case，一键进 playground 改 prompt、加进 dataset 回归。核心取舍：(1) 摄取路径极端强调「不拖慢宿主应用」——SDK 全异步批量、后端先落 S3 再异步入库；(2) 自托管与 Cloud 完全同一套代码和 schema，避免锁定；(3) 2025-2026 年全面转向 OTel 标准而非私有协议。一句话哲学：以开放标准（OTel、MIT、开放 API）承载 LLM 应用的全生命周期数据。

#### 数据模型
- **Trace**：代表一次请求/操作，是 observations 的容器；`user_id`、`session_id`、`tags`、`metadata`、`environment`、`release/version` 等属性会传播到 trace 内所有 observations。
- **Observation**：trace 内的单步，可任意嵌套（Trace 1—n Observation，Observation 自嵌套）。类型有 10 种：`event`（离散事件）、`span`（时长单元）、`generation`（LLM 调用，含 prompt、token 用量与成本）、`agent`、`tool`、`chain`、`retriever`、`evaluator`、`embedding`、`guardrail`。generation/embedding 上可记录 `usage_details`（任意 usage type 字符串，如 input/output/cached_tokens）与 `cost_details`（USD），未提供时按 model 名推断（内置模型价目 + tokenizer，可自定义 model 定义）。
- **Session**：可选地把多条 trace 聚成一个会话（如聊天线程），Session 1—n Trace。
- **Score**：评测结果对象。字段：`id/name/value(数值)/stringValue/dataType(NUMERIC|CATEGORICAL|BOOLEAN|TEXT)/source(API|EVAL|ANNOTATION)/comment/configId`，且**每个 Score 恰好引用 Trace、Observation、Session、DatasetRun 四者之一**。可选绑定不可变的 **ScoreConfig**（name、dataType、min/max 或类别枚举）保证团队打分 schema 一致。
- **Dataset 族**：`Dataset`(name/description/metadata/remoteExperimentUrl) 1—n `DatasetItem`(input/expectedOutput/metadata/sourceTraceId/sourceObservationId/status=ACTIVE|ARCHIVED，按 id upsert)；`DatasetRun`（=Experiment run）1—n `DatasetRunItem`(datasetItemId + traceId[+observationId 仅向后兼容])，即每个 run item 把一条 dataset item 和一条真实 trace 连起来，Score 再挂到 trace 或 run 上。
- **Prompt**：name + type(text|chat) + prompt 内容 + config；**version 不可变递增，label（production/latest/自定义）是指向版本的可移动指针**，代码引用 label 实现零代码发布/回滚。
- 时效注意：2026 年 4 月起的「Fast Preview / v4」把模型改为 **observations-first**——trace 退化为关联 ID，trace 级属性必须写到每个 observation 上以支持高效聚合。

#### 核心功能清单
- **Trace 视图**：有。嵌套树状 observation 视图、全文检索、agent graph 可视化、多模态（大文件走 S3 直传）、log levels、comments。
- **会话/线程**：有。Sessions 分组 + 会话级视图与会话级 Score。
- **Playground**：有。多 prompt 变体并排对比执行，各自独立模型参数/变量/工具定义；可从 trace 里的 generation 直接跳入，改完可存回 Prompt 管理。
- **Prompt 管理**：有。版本 + labels、变量、prompt 引用组合（composability）、message placeholders、A/B 测试、folders、webhooks/Slack、GitHub 集成、MCP server；客户端缓存 + 服务端 Redis 缓存 + fallback prompt 保证可用性。
- **数据集**：有。支持从生产 trace 一键转 dataset item（sourceTraceId 溯源）、媒体引用、UI/SDK/API 管理。
- **实验对比**：有。DatasetRun 之间的对比视图；SDK 侧定义 task/evaluator/run-evaluator 函数跑实验，支持 CI/CD；本地 dataset 跑实验目前只产生 trace、不生成 run（roadmap 中）。
- **评测**：有。四种方法：LLM-as-a-Judge（托管 judge，可跑在 observation/trace/experiment 三个层面，产出带 reasoning 的 Score）、代码 evaluator、SDK/API 打分、UI 人工打分。
- **标注/人工反馈**：有。Annotation Queues（详见 HITL 节）、user feedback 采集、Corrections（更正输出）。
- **监控告警**：部分。自定义 dashboards 与 Metrics API 开源可用；**Monitors and Alerts（阈值告警，通知到 Slack/GitHub Actions/Webhook）仅 Langfuse Cloud 提供**。另有 Cloud 的 spend alerts。
- **成本核算**：有。ingested 优先、否则按模型价目+tokenizer 推断；按 usage type 细分；Metrics API 可按 user/tag 聚合用于计费限流。

#### 接入与实现方式
- **SDK 现状（2026-07）**：Python SDK **v4** 与 JS/TS SDK **v5** 为当前版本，均为 **OTel 原生**——SDK 是官方 OpenTelemetry client 之上的薄层，把 OTel span 自动转换为 Langfuse observation 类型，并加 LLM 特有 helper（token 用量、成本、prompt 关联、打分）。Python v3（首个 OTel 化版本）与 JS v4 已是 legacy 文档。
- **埋点形态齐全**：Python `@observe()` 装饰器 / context manager / 手动 observation；JS `startActiveObservation` context manager、observe wrapper、`LangfuseSpanProcessor` 注入 NodeSDK；OpenAI SDK drop-in 替换、LangChain callback、LlamaIndex/Vercel AI SDK 等框架集成。
- **异步导出**：SDK 本地批量、后台定时/按批发送，短生命周期进程必须显式 `flush()`/`shutdown()` 否则丢数据。
- **与 OTel 的关系**：Langfuse 后端可直接充当 OTel backend，在 `/api/public/otel` 接收 **OTLP** trace——这是 Go/Java 等无官方 SDK 语言的接入路径（配合 OpenLLMetry/OpenLIT）。后端做 OTel GenAI 语义约定 → Langfuse 数据模型的属性映射（`langfuse.user.id`、`langfuse.session.id`、`langfuse.trace.tags` 等）。关键坑：trace 级属性需传播到**每个 span**（推荐 OTel Baggage + BaggageSpanProcessor，或 SDK 的 `propagate_attributes()`）；采用 v4 observations-first 语义需在 exporter 上加 `x-langfuse-ingestion-version: 4` header。默认只导出 LLM 相关 span（gen_ai.* 属性、已知 instrumentor），可自定义 filter 放开。

#### 自托管与后端架构
- **组件**（v3 架构）：两个应用容器——**Langfuse Web**（UI+API）与 **Langfuse Worker**（异步事件处理）；存储四件套——**Postgres**（OLTP 事务数据）、**ClickHouse**（OLAP，存 traces/observations/scores）、**Redis/Valkey**（缓存+队列）、**S3/Blob**（原始事件、多模态附件、大导出）；可选外部 LLM API/Gateway（playground 与 evals 用）。
- **摄取管线**：Web 批量收 trace → **立即写 S3，Redis 只存引用排队** → Worker 从 S3 取出写入 ClickHouse。好处：削峰、数据库故障可恢复（事件先持久化于 S3）。API key 与 prompt 均有 Redis read-through 缓存。所有组件要求 UTC 时区。
- **部署形态**：docker compose（低规模）、Kubernetes Helm（生产推荐）、AWS/Azure/GCP Terraform、Railway；semver tagged releases，长迁移做成后台任务减少升级停机。
- **协议**：仓库整体 **MIT**，仅 `ee/`、`web/src/ee/`、`worker/src/ee/` 目录为商业 license（需 license key 才能运行，源码随仓库分发）。官方明确：tracing、评测、prompt 管理、experiments、标注、playground 等**全部产品功能均 MIT 无限制**；EE 仅覆盖 SCIM、审计日志、数据保留策略等企业安全能力。自托管默认向 PostHog 上报匿名遥测，`TELEMETRY_ENABLED=false` 可关。

#### 人工介入与 HITL
Langfuse 是旁路观测平台，**无 agent 运行时断点/暂停恢复/状态编辑**能力（不在其定位内）。人工介入体现在数据侧：(1) **Annotation Queues**——建队列时绑定 ScoreConfig 定义打分维度，可批量把 trace/observation/session 加入队列并指派用户，专家逐条打分+评论，全键盘快捷键操作，队列可经 API 管理以便自建标注 UI；(2) **Corrections**——在 trace/observation 上录入「模型本应输出什么」，带 diff 视图，可导出做微调数据；(3) user feedback 作为 `source=API` 的 Score 摄入；(4) comments 协作。无原生审批流（prompt label 变更有 webhook 可自行串审批；「protected labels」是否存在未核实）。

#### 值得借鉴的点（面向 Go Agent 测试平台）
1. **Score 的四向单引用设计**：一个统一的 Score 实体（数值/类别/布尔/文本 + source 枚举 API/EVAL/ANNOTATION + 可选 ScoreConfig schema 约束），恰好挂在 trace/observation/session/dataset-run 之一上。人工反馈、LLM judge、代码断言全部收敛为同一张表，对比分析零成本——比给每种评测各建一套结果表干净得多。
2. **DatasetRunItem 把「测试用例」和「真实 trace」连成一条边**：实验跑的每个 case 就是一条带完整 observation 树的生产级 trace，失败用例可直接下钻调试；反向又能从生产坏 case 经 sourceTraceId 一键入库成回归用例。Go 平台可直接复刻这四张表（Dataset/Item/Run/RunItem）。
3. **OTLP 端点 + 属性映射层做兼容面**：自研 SDK 之外提供 `/api/public/otel`，把 OTel GenAI 语义约定映射进自己的数据模型。trpc-agent-go 已有 OTel 支持的话，这是接入成本最低、生态最广的方案；注意学它「trace 级属性用 Baggage 传播到所有 span」的做法，否则 OLAP 聚合查询没法只扫 span 表。
4. **「先落对象存储、队列只传引用」的摄取管线**：Web 收到批量事件立即写 S3、Redis 排队引用、Worker 异步入 ClickHouse。天然削峰 + 事件可重放 + 大 payload（多模态）不过队列。Go 生态实现同样简单（S3 + asynq/river + ClickHouse）。
5. **prompt 的 version（不可变）+ label（可移动指针）+ 客户端 stale-while-revalidate 缓存**：发布/回滚=移动 label，不改代码不加延迟；SDK 端 TTL 过期先返回旧值后台刷新，再配 fallback prompt 兜底可用性。这套语义对 agent 的 system prompt / 工具描述管理同样适用。
6. **observations-first 的教训（v4）**：Langfuse 2026 年被迫重构——trace/observation 双实体在 agent 场景下 join 太贵，改为只有 observation、trace 退化为关联 ID、trace 属性冗余到每行。新平台设计 ClickHouse 表时可一步到位：宽表、按 span 存、聚合属性列冗余。

### 2.3 Arize Phoenix + OpenInference

**类别**：tracing平台（OTel-native LLM 可观测 + 评测一体化，附带独立语义规范 OpenInference）

#### 定位与设计理念
Phoenix 是 Arize 开源的 AI 可观测与评测平台，面向 LLM 应用/Agent 的调试、评估与迭代，覆盖开发—测试—生产全流程（生产侧深度功能引流到商业产品 Arize AX）。核心取舍是「collector 即产品」：Phoenix 自身就是一个 OTLP trace collector（HTTP + gRPC）加 Web UI，应用侧只需标准 OpenTelemetry SDK + OpenInference 语义约定即可接入，不绑定任何框架或语言。一句话哲学：**用 OTel 标准管道收数，把 LLM 领域语义（OpenInference）、评测、数据集、Prompt 工程全部叠在同一个单容器产品里，自托管零门槛、无功能阉割**。Phoenix 采用 Elastic License 2.0（ELv2，非 OSI 认证但自托管完全免费、无 feature gate）；配套的 OpenInference 规范与 instrumentor 生态则是 Apache-2.0，刻意让「标准层」比「产品层」更开放。

#### 数据模型
- **Project**：trace 的容器，用于隔离环境（dev/prod）、隔离评测运行等；每个 project 自带指标 dashboard。
- **Trace / Span**：标准 OTel 模型（trace_id/span_id/parent_id/start·end_time/status/attributes/events）。OpenInference 要求每个 span 带 `openinference.span.kind`，取值：`LLM`、`EMBEDDING`、`CHAIN`、`RETRIEVER`、`RERANKER`、`TOOL`、`AGENT`、`GUARDRAIL`、`EVALUATOR`、`PROMPT` 十种。LLM span 的关键属性是扁平化索引式命名：`llm.input_messages.0.message.role`、`llm.model_name`、`llm.invocation_parameters`、`llm.token_count.prompt/completion/total`（含 `prompt_details.cache_read/cache_write/audio` 等细分）、`llm.cost.prompt/completion/total`（USD）、`llm.tools.N.tool.json_schema`、`llm.prompt_template.template/variables/version`；检索文档用 `document.id/content/score/metadata`。
- **Session**：通过 span 属性 `session.id`（另有 `user.id`）把多轮对话的多条 trace 串成一个会话线程，UI 以聊天视图展示每轮输入输出，并按会话聚合 token 与延迟。
- **Annotation**（评分/反馈的统一实体）：可附着于 span、trace、session、document（文档级用于检索评测），字段为 `label`（分类）+ `score`（数值）+ `explanation`（解释）；annotation type 分 categorical / continuous / freeform，带优化方向（maximize/minimize/none）；annotator kind 分 **Human / LLM / Code** 三类。
- **Dataset / Example / Experiment**：Example = `input` + 可选 `output`(reference) + `metadata`，数据集有版本；Experiment = 对数据集逐例跑 task（LLM/Agent/App）产出 output，再由 Evaluator 打出 Score/Annotation。span 转入数据集时其属性与 annotation 会传播到 example metadata。
- **Score**（evals SDK 的统一输出）：`name`、`kind`(llm/code/human)、`direction`、可选 `score`/`label`/`explanation`/`metadata`。

#### 核心功能清单
- **Trace 视图**：有。瀑布式 span 树、按 span kind 着色、输入输出/属性/事件详情、检索文档展开。
- **会话/线程**：有。Sessions 功能，聊天式 UI、会话内搜索、会话级 token/延迟统计。
- **Playground**：有。Prompt Playground 支持多模型对比（OPENAI/ANTHROPIC/GOOGLE/AWS 等 14 家 provider，可用 `PHOENIX_ALLOWED_PROVIDERS` 白名单限制）、调 invocation 参数、对数据集批量跑 prompt，且 playground 运行本身产生 trace/experiment。特色功能 **Span Replay**：从已收集的 LLM span 一键回放该次调用并改参重试。
- **Prompt 管理**：有。版本控制、tag、SDK 拉取（prompts in code）、prompt 模板/变量/版本可埋进 trace（`llm.prompt_template.*`）。
- **数据集**：有。版本化，可从生产 trace、标注、CSV/手工构建，支持导出用于微调。
- **实验对比**：有。experiment 绑定数据集版本，评估器自动打分，UI 中对比多次 run、下钻 good/bad 例子。
- **评测**：有，双轨。① 客户端 evals SDK（Python + TypeScript）：LLM-as-a-judge 与 code evaluator（exact match/regex/Levenshtein），预置 RAG（faithfulness/幻觉、相关性）与 tool-calling agent 等指标，判分用 **tool calling 强制结构化输出**（label+explanation），内置 executor 做限流重试与动态并发；② 服务端 evals：在 UI 里配置评估器挂到数据集，实验结果自动打分。所有评估器运行本身被 OTel 追踪，落入专门 project（评估器可解释、可再评估）。检索专项指标：NDCG、Precision@K、Hit Rate。
- **标注/人工反馈**：有。UI 标注（hotkey 快捷打分）、REST API 上报最终用户 👍/👎、annotation 配置化（类型/范围/方向）。
- **监控告警**：部分。每个 project 自带预置 metrics dashboard（trace 量、延迟分位、成本、top models、token 细分、LLM/Tool span 错误率、annotation 均分）；可选开 Prometheus 指标（9090 端口）；**内置告警：无**（文档明确把 alerting/在线评估阈值告警指向 Arize AX）。
- **成本核算**：有。基于 token 数 + 内置模型价格表自动算 USD 成本，支持 Settings > Models 自定义价格，成本上卷到 trace/project；语义约定含 `llm.cost.*` 全套（含 cache 读写、reasoning、audio 细分）。
- 另有：数据保留策略（全局/每 project）、RBAC、OAuth2/LDAP 认证、embedding 可视化、PXI（内置 AI 调试 agent）、代码执行沙箱（WASM/DENO/E2B 等）。

#### 接入与实现方式
埋点原理 = **纯 OpenTelemetry**：应用用 OTel SDK 产 span，经 OTLP（HTTP `/v1/traces` 或 gRPC 4317，Protobuf）发给 Phoenix，Phoenix 同时是 collector 和存储/UI，无需独立 otel-collector。领域语义由 **OpenInference** 承担——它自称「与 OpenTelemetry 互补的一组约定和插件」，不是替代 OTel，而是在 OTel span attributes 上定义 LLM 专属键值（区别于 OTel 官方 GenAI semconv 的 `gen_ai.*` 命名；OpenInference 用 `llm.*`/`openinference.span.kind`，且消息列表用扁平化索引属性而非 event）。SDK 形态：① **auto-instrumentor 生态**（主力）：Python 30+ 包、JS/TS 13+ 包，另有 Java（Spring AI、LangChain4j）与 **Go** 的 instrumentation，覆盖 OpenAI、Anthropic、LangChain、LlamaIndex、Claude Agent SDK、MCP 等，一行 `instrument()` 挂 monkey-patch/回调；② 手动埋点：直接按语义约定写 OTel span；③ Phoenix 的 `register()` 帮助函数简化 tracer provider 配置。因为全链路是标准 OTLP，OpenInference 数据也可以发给任何 OTel collector/后端，Phoenix 也就天然能接任何语言（包括 Go）的 OTel SDK。

#### 自托管与后端架构
- **形态**：单容器（`arizephoenix/phoenix` Docker 镜像，含 nonroot/debug 变体），一个进程同时提供 Web UI（6006）、OTLP HTTP collector（6006 `/v1/traces`）、OTLP gRPC collector（4317）、可选 Prometheus 指标（9090）。部署路径：pip/CLI 直跑、Docker/Compose、Kubernetes/Helm、AWS CloudFormation、Railway 一键部署，也可跑在 notebook 里。
- **存储**：默认 **SQLite**（不配置时落临时目录，生产建议把 `PHOENIX_WORKING_DIR` 指向持久卷）；生产切 **PostgreSQL ≥14**（`PHOENIX_SQL_DATABASE_URL` 或 PHOENIX_POSTGRES_* 环境变量，支持自定义 schema），v14.0 起支持只读副本（`PHOENIX_SQL_DATABASE_READ_REPLICA_URL`）。**无 Redis、无独立 worker/队列**——评测重活由客户端 SDK executor 或容器内进程承担。
- **协议/License**：Phoenix 主仓 ELv2（自托管免费、无功能限制、可完全离线 air-gap，遥测可关）；OpenInference 规范与 instrumentors Apache-2.0。代码主体 Python（~45%）+ TypeScript（~40%）。
- 企业能力内置：OAuth2/LDAP/本地账号、RBAC、每 project 数据保留策略（`PHOENIX_DEFAULT_RETENTION_POLICY_DAYS`）、CSRF/SSRF 防护配置、反向代理子路径支持。

#### 人工介入与 HITL
Phoenix 是「事后观测型」HITL：**无运行时断点、无 agent 状态编辑、无审批流**（它不介入应用执行路径，这点与带 interrupt/checkpoint 的编排框架不同）。人工介入手段集中在标注闭环：UI 人工标注（categorical/continuous/freeform + hotkey 高速标注，标注配置可定义标签集与分值范围）、API 收集终端用户反馈、LLM judge 自动标注三者共用同一 annotation 模型。文档未见独立的「标注队列/任务分发」实体（未核实是否有隐藏的 queue 功能）。闭环设计突出：annotation 随 span 传播进 dataset example metadata → 实验时评估器可读取历史标注对比改进/回归 → 人工标注集可用于训练/校准 LLM judge（配 DSPy/Zenbase）。

#### 值得借鉴的点
1. **「collector 即产品」的单二进制架构**：一个进程 = OTLP gRPC/HTTP 接收端 + 存储 + UI，默认 SQLite、生产换 Postgres、无队列无 Redis。Go 平台照抄成本极低（Go 单二进制天然契合），把「docker run 一条命令起平台」做成第一卖点。
2. **领域语义放在 span attributes、层级只靠 session.id/project 两个弱关联**：不发明私有 trace 协议，trpc-agent-go 已有 OTel 输出的话，只需定义/复用一套 semconv（可直接兼容 OpenInference 的 `openinference.span.kind` + `llm.*`，Phoenix 就能白嫖成你的调试 UI），平台侧按属性建索引即可。
3. **统一 Annotation 实体（label+score+explanation × human/llm/code × span/trace/session/document）**：一张表同时承载人工反馈、LLM judge、代码断言，UI 过滤、dashboard 聚合、数据集传播全部复用，避免「评测结果」「用户反馈」「单测断言」三套模型。
4. **LLM judge 用 tool calling 强制结构化输出 + 评估器自身被 trace**：判分稳定性和可审计性都解决了；Go 侧实现就是给 judge 调用挂同一套 OTel 埋点，评估 trace 落专门 project。
5. **Span Replay**：从生产 trace 的任意 LLM span 一键进 playground 改 prompt/参数重放——把「观测」直接变成「调试动作」，是 trace UI 里回报率最高的交互，实现上只需从 span attributes 反解出请求体。
6. **成本核算走 semconv（`llm.token_count.*` + `llm.cost.*`）+ 服务端价格表兜底**：SDK 只报 token 数与模型名，价格表放服务端可更新、可自定义，成本自动上卷 trace/project——比在客户端算钱的方案演进性好得多。

### 2.4 W&B Weave

**类别**：tracing平台 + 评测工具（依附 W&B 平台的 LLM/Agent 可观测与评测层）

#### 定位与设计理念
W&B Weave 是 Weights & Biases 推出的 GenAI 应用开发工具包，官方定位为"log and debug language model inputs, outputs, and traces"并"build rigorous, apples-to-apples evaluations"。它把 W&B 在 ML 实验跟踪上的核心理念平移到 LLM 时代：**一切皆版本化对象**——函数（Op）、数据集、模型、Prompt 都自动版本化并可用 ref URI 精确引用，从而让"生产 trace → 评测数据集 → 评测运行 → 对比"形成闭环。核心取舍：不做独立产品，而是深度绑定 W&B 平台（entity/project 组织结构、团队权限、W&B API key），换取与实验跟踪/模型注册的一体化。一句话哲学：**用内容寻址的版本化把"调试观测"和"科学评测"统一在同一套数据模型上**。

#### 数据模型
以 **Op / Call / Trace / Object** 四个实体为核心（来源：tracing.mdx、call-schema-reference.mdx）：
- **Op**：被 `@weave.op` 装饰的"versioned, tracked function"，代码变更时自动产生新版本（"if the code changed from the last call"）。
- **Call**：Op 的一次执行记录，官方明言"Calls are similar to spans in the OpenTelemetry data model"。字段（Call schema）：`id`、`trace_id`、`parent_id`、`op_name`（可为 ref）、`display_name`、`inputs: Dict[str,Any]`、`output`、`exception`、`attributes`（用户自定义元数据）、`started_at`/`ended_at`、`project_id`、`wb_user_id`、`wb_run_id`、`deleted_at`，以及 `summary`：其中 `summary["usage"]` 存各 LLM 的原始 token 数，保留命名空间 `summary["weave"]` 含 `status`（SUCCESS/ERROR/RUNNING/DESCENDANT_ERROR）、`latency_ms`、`costs`（按模型的成本分解）、`trace_name`。
- **Trace**："full trees of Calls that share the same execution context"，用 `trace_id` 取整棵调用树。
- **Thread/Turn**（会话）：Thread 是共享会话上下文的 Call 分组，`weave.thread()` 上下文管理器设定/复用 `thread_id`；Turn 是线程内的高层步骤（UI 中一行），嵌套子调用不计入线程级统计。服务端有 `POST /threads/query` API（支持按 turn_count、start_time、last_updated 排序）。
- **Object 与 ref**：Object 是"versioned, serializable data"，`weave.publish()` 发布后按内容寻址自动增版，ref URI 格式 `weave:///[ENTITY]/[PROJECT]/object/[NAME]:[VERSION]`，version 可为内容 digest、`v0/v1` 序号或 `latest` 别名；ref 还支持 `#` 后的 extra 路径（`atr/row/key/ndx` 等边类型）深入到表格某行某字段（dev_docs/REF_SPEC.md）。
- **Score/Feedback**：scorer 结果与人工反馈都作为挂在 Call 上的 feedback 记录存储。

#### 核心功能清单
- **Trace 视图**：有。调用树 UI，含状态、延迟、token、成本；对 LLM 调用有 Chat 渲染视图。
- **会话/线程**：有。Threads 列表 + 详情抽屉（turn 数、延迟、chat 面板），面向多轮对话和 Agent 会话。
- **Playground**：有。支持 Bedrock/Anthropic/Azure/Deepseek/Google/Groq/Mistral/OpenAI/X.AI 九类供应商及自定义 OpenAI 兼容端点；可从某个 Call 详情页"Open chat in Playground"续跑，可编辑/重试/删除消息，支持保存模型预设。
- **Prompt 管理**：有。`weave.StringPrompt` / `weave.MessagesPrompt`，`{variable}` 占位 + `.format()`，`weave.publish` 版本化并在 Prompts 页共享。
- **数据集**：有。`weave.Dataset` 版本化；`from_calls()/from_pandas()/from_hf()`；UI 中可从 Traces 勾选行"Add selected rows to a dataset"，可视化增删改行。
- **实验对比**：有。比较视图支持 traces/models/prompts/配置对象，Summary/Side-by-side/Unified/Calls 四种模式，diff-only 过滤、数值差可显示为整数或百分比、可设 baseline；UI 一次最多展示 6 个对象。
- **评测**：有。`weave.Evaluation(dataset, scorers)`（Evaluation 是"blueprint"，每次 `.evaluate()` 是一次测量），`trials` 多次采样，`preprocess_model_input` 预处理；另有命令式 `EvaluationLogger`；还有 leaderboards。
- **标注/人工反馈**：有。emoji 反应（👍👎及自定义）、≤1024 字符 note、≤1KB 自定义结构化 payload；`AnnotationSpec`（布尔/数值/分类/自由文本）+ **Annotation Queues** 标注队列（建队列→批量加 trace→审阅者按链接进入→进度状态跟踪→结果作为结构化元数据存回 trace，可导出为数据集）。TypeScript SDK 无 feedback 功能。
- **监控告警**：有。Monitors 对生产根级 trace 自动 LLM-as-a-judge 打分（同类 signal 合并为一次推理调用），结果以带置信度的标签反馈呈现，聚合进 Monitor Scores 面板；通过 automations 可在指标跌破阈值时 Slack 通知。
- **成本核算**：有。自动从 OpenAI/Anthropic/Cohere/Mistral 等响应捕获 token 并套内置价格；`add_cost(llm_id, prompt_token_cost, completion_token_cost, effective_date)` 支持自定义/追溯定价；查询时 `include_costs=True` 从 `call.summary["weave"]["costs"]` 读取。TypeScript 不支持成本跟踪。

#### 接入与实现方式
主形态是**装饰器 + 自动 patch**：Python `@weave.op()`（TS 用 `weave.op()` 包装）在 `weave.init()` 后自动记录输入/输出/异常/父子关系，并捕获函数代码用于版本化；`postprocess_inputs/postprocess_output` 可脱敏，`tracing_sample_rate` 支持按 op 采样（只对最外层 op 生效）。对主流 LLM 库有自动集成（integrations 目录），LLM 调用无需手工埋点。可靠性方面提供**客户端 write-ahead log**：`WEAVE_ENABLE_WAL=true` 后每个 API 请求先写本地 `.weave/wal/` 下的 JSONL 再发送，进程崩溃或断网后下次启动自动补发（当前 opt-in，计划默认开启）。与 OTel 的关系：Call 模型对齐 span，且服务端提供 OTLP 摄入端点 `POST /otel/v1/traces`（多租户为 `https://trace.wandb.ai/otel/v1/traces`，自托管为 `https://<subdomain>.wandb.io/traces/otel/v1/traces`），**仅接受 protobuf**（`application/x-protobuf`）；能识别 GenAI 语义约定、OpenInference、Vercel AI SDK、MLflow、Traceloop、Vertex AI 等多套属性并按优先级映射；用 `wandb.thread_id` / `wandb.is_turn` 属性接入线程模型；局限是 OTel trace 的 tool call 在 Chat 视图只能显示原始 JSON。
#### 自托管与后端架构
SDK 仓库 wandb/weave 为 **Apache-2.0**（Python≥3.10 + TypeScript SDK）。仓库内 `weave/trace_server` 含服务端参考实现：核心是 **ClickHouse** 后端（`clickhouse_trace_server_batched.py`、`clickhouse_schema.py`、迁移器，批量 INSERT 进 `calls_raw` 等表），另有 in-memory 实现、Redis 客户端与文件存储模块。但**生产/自托管形态绑定闭源的 W&B Platform**：官方 self-managed 指南要求 Kubernetes ≥1.29（建议 ≥3 节点）、已部署 W&B Platform、**"Weave-enabled license from W&B Support"**（企业授权门槛）、Altinity ClickHouse Operator + ClickHouse Keeper（替代 ZooKeeper）+ 3 副本 ClickHouse 集群 + S3 兼容对象存储；通过 W&B CR 中 `weave-trace.enabled: true`、`clickhouse.replicated: true` 启用 weave-trace 服务（`WF_CLICKHOUSE_REPLICATED_CLUSTER` 须与集群名一致）。UI 属于 W&B 应用，闭源。SaaS 端点为 trace.wandb.ai。
#### 人工介入与 HITL
有较完整的**事后标注**体系：feedback API（reaction/note/自定义 payload，`call.feedback.add("[LABEL]", obj)`）、`AnnotationSpec` 定义结构化标注字段（创建后不可改以保证一致性）、Annotation Queues 提供队列分发 + 审阅进度（Not started/In progress/Completed）+ SDK 程序化读取，结果可导出为评测/训练数据集。Guardrails 提供**运行时介入**：`result, call = op.call(x)` 后 `await call.apply_scorer(scorer)`，按分数阻断/改写输出（如 OpenAIModerationScorer、BedrockGuardrailScorer），且 guardrail 结果自动落库兼作 monitor。但**无** Agent 执行断点/单步、无状态编辑重放、无内建审批流。

#### 值得借鉴的点
1. **Call 即 span、但字段面向 LLM 一等公民**：inputs/output/exception 结构化存储 + 保留命名空间 `summary["weave"]`（status/latency_ms/costs/usage）。Go Agent 平台可直接抄这个 schema：比裸 OTel span attributes 好查询，又保留 OTel 兼容（可做双向映射）。
2. **内容寻址 ref URI（`weave:///entity/project/object/name:digest#row/...`）**：把数据集、prompt、评测配置全部版本化并可引用到"某版本数据集的第 10 行的 input 字段"。这让评测结果可精确复现、trace 与数据集行可互相链接，是"测试平台"区别于纯 APM 的关键设施。
3. **Thread/Turn 两级会话模型**：只把高层 turn 计入会话统计、嵌套调用下钻查看，且允许通过 OTel 属性（`wandb.thread_id`/`wandb.is_turn`）声明。多轮 Agent 会话的指标聚合按 turn 而非 span 才有意义，实现成本低（calls 表加两列 + 一个 context manager）。
4. **guardrail 与 monitor 共用 scorer 抽象**：同一个 Scorer 既可离线评测（Evaluation）、也可在线抽样监控（monitor）、还可同步拦截（apply_scorer），且拦截结果自动落库"免费"变成监控数据。一套打分接口三处复用，避免平台内三套评分体系。
5. **客户端 WAL（先写本地 JSONL 再上报）**：解决进程崩溃丢 trace 和断网缓冲，Go 里用 append-only 文件即可实现，对"测试平台"抓取短生命周期 Agent 进程的数据尤其重要。
6. **从生产 trace 一键建数据集 + 标注队列回流**：Traces 页勾选→入数据集→标注队列人审→导出评测集，形成 debug→dataset→eval 飞轮；平台侧只需要"call 行到 dataset 行"的引用关系即可支撑。

> **⚠️ 核验修正**（独立核查代理逐条反驳后订正）：
>
> - **订正**：原稿关键事实「Guardrails 与 Monitors 共用 Scorer：op.call() 返回 (result, call) 后 await call.apply_scorer(scorer) 同步拦截，结果自动落库兼作监控；Monitors 用 LLM-as-a-judge 只对成功的根级 trace 打分且同类 signal 批量合并推理」经核查有误。前半正确：guardrails.mdx 证实 result, call = op.call(...) → await call.apply_scorer(scorer)，且 "every scorer result from guardrails is automatically stored in Weave's database, so your guardrails also function as monitors"。但"只对成功的根级 trace 打分"不准确：monitors.mdx 原文为 "Quality signals evaluate successful root-level traces. Error signals evaluate failed traces. Weave doesn't score child spans and intermediate Calls." —— 即仅 Quality 类 signal 限于成功的根级 trace，Error 类 signal 专门给失败的 trace 分类打分。同组 signal 合并为单次 LLM 调用属实。另注意该页已标注为旧方案，新实现推荐 Weave for Agents 的 Signals。来源: https://raw.githubusercontent.com/wandb/docs/main/weave/guides/evaluation/monitors.mdx

### 2.5 Braintrust

**类别**：评测工具/eval-first AI 观测平台（评测+tracing+代理网关一体）

> 调研说明：本环境网络策略拦截了 braintrust.dev 的直接访问（HTTP 403），官方 docs/blog 内容通过搜索引擎对官方页面的摘要获取（URL 均为官方页面）；GitHub 上的 README/LICENSE/Terraform 源码（autoevals、braintrust-proxy、terraform-aws-braintrust-data-plane、helm、braintrust-sdk-go）为直接打开原文，可信度最高。无法确认处已标「未核实」。

#### 定位与设计理念
Braintrust 自我定位是「构建高质量 AI 产品的 AI 可观测平台」，但其根是 eval-first：一切围绕 `Eval(name, {data, task, scores})` 三要素展开——data 是测试用例集（input/expected），task 是被评函数（一次 LLM 调用、多步 agent、检索流水线皆可），scores 是打分器列表。实验（experiment）是「不可变、可比较的 eval 运行记录」，可从代码或 UI 发起并接入 CI/CD 拦截回归。核心取舍有三：(1) logs 与 experiments 共用同一数据结构，生产观测与离线评测是同一份数据的两个视角；(2) 为大规模 trace 检索自研存储引擎 Brainstore，而非复用通用数仓；(3) 用 hybrid 部署（数据面进客户 VPC、控制面托管）换取企业信任。一句话哲学：**评测不是测试阶段的附属品，而是数据模型本身——生产日志天然就是下一轮评测的数据集。**

#### 数据模型
- 顶层实体是 **project**，其下挂 experiments、logs、datasets、prompts/functions、playgrounds。
- 最小单元是 **span**，字段极简且全部可选：`input`、`output`、`expected`、`metadata`、`scores`、`metrics`。其中 `scores` 必须是 string→[0,1] 数字的映射；`metrics` 存 token、延迟等数值。
- `span_attributes` 目前识别两个属性：`name`（UI 显示名）和 `type`，type 枚举为 `llm / score / function / eval / task / tool / review`（决定图标）。
- 系统字段 `span_id`、`root_span_id`、`span_parents` 构成 span 树（一个 trace = 根 span + 子孙）；`project_id / experiment_id / dataset_id / log_id` 由 SDK 自动填充，决定该 span 归属于实验还是生产日志——**同一 schema，靠归属字段区分 experiment 与 log**，因此埋点代码一次编写两处生效，分数与人工反馈对两者通用，生产数据可无缝转评测集。
- **dataset** 记录为 input/expected/metadata 三元组，每次 insert/update/delete 都被版本化，评测可 pin 到特定版本，版本可像 prompt 一样在 dev/staging/prod 环境间晋级。
- 打分产物是 **Score** 对象：`{name, score∈[0,1]|null, metadata(如 rationale)}`（autoevals 公开接口）。
- 会话/线程（session/thread）级专门实体：在本次打开的来源中未见，未核实（Go SDK 支持 W3C baggage 分布式追踪跨服务串 trace）。

#### 核心功能清单
- **trace 视图**：有。Logs 页为可搜索/过滤的 trace 表（每行=一个 trace 的根 span），点开看 span 树；支持 BTQL 查询语言过滤；另有 CLI `bt view logs`。
- **会话/线程**：未核实（未见独立 thread 视图实体，见上）。
- **playground**：有。在浏览器里把 prompt 改动对真实数据集实时跑评测、并排比输出，是 PM 与工程师共用的迭代界面。
- **prompt 管理**：有。prompt 作为 function 存储并版本化，支持环境（environments）与分阶段发布，SDK 可拉取 hosted prompt。
- **数据集**：有。全量版本化 + 可 pin 版本 + 环境绑定。
- **实验对比**：有。experiments 间 diff 对比、随时间跟踪，官方 GitHub Action（eval-action）做 CI 门禁。
- **评测**：有（核心）。离线 Eval 三要素 + 在线评测（online scoring：对生产 trace 按采样率异步跑 scorer，官方建议高流量 1–10%、关键低流量 50–100%）。
- **标注/人工反馈**：有。见 HITL 节。
- **监控告警**：有。Monitor 页仪表盘聚合请求量、延迟、token、成本、分数等，视图可保存；Automations 支持用 BTQL 写告警条件（如「1 小时内 relevancy<0.5 的响应超 5% 则告警」），触发 webhook/Slack。
- **成本核算**：有。token 用量与成本是 Monitor 页一等指标，可按维度分组对比。

#### 接入与实现方式
- SDK 矩阵：TypeScript/Python 是主力（tracing+evals），2025–2026 新增 Go、Java、Ruby、.NET、Rust（beta），由 braintrust-spec 统一生成规范。
- **Go SDK（对本项目最相关）完全构建在 OpenTelemetry 之上**：不自造 tracer，用户创建标准 `sdk/trace.TracerProvider`，`braintrust.New(tp, braintrust.WithProject(...))` 只是注册一个 exporter。每个集成是独立 Go module（`trace/contrib/openai、anthropic、genai、genkit、adk、cloudwego/eino、langchaingo、sashabaranov/go-openai`），并支持用 DataDog **Orchestrion 在编译期自动注入埋点**（`orchestrion go build`，零代码改动）；也可手动加中间件。License：Apache-2.0。
- 评测入口约定：JS 写 `*.eval.ts` 文件 + `npx braintrust run`；Python 写 `eval_*.py`；Go 用 `evaluator.Run(ctx, eval.Opts{Experiment, Dataset, Task, Scorers})` 泛型 API。
- **OTel 原生接入**：任何 OTLP exporter 指向 `https://api.braintrust.dev/otel`（EU：api-eu；自托管：自己的 API URL），header 带 `Authorization: Bearer <key>` 和 `x-bt-parent=project_id:<id>`——**用 header 决定 trace 归属的 project/experiment**。
- **AI proxy/gateway**：OpenAI 兼容 API（base_url 改为 `https://api.braintrust.dev/v1/proxy`）统一访问 OpenAI/Anthropic/Llama/Mistral 等；请求带 `seed` 参数即激活结果缓存（降本、评测可复现）；可自部署到 Vercel/Cloudflare Workers/AWS Lambda/Express。autoevals 默认也走这个 gateway 跑 LLM-judge。

#### 自托管与后端架构
- **Hybrid 模型**：控制面（UI、认证、元数据同步）由 Braintrust 托管；数据面（API、Postgres、Redis、S3、Brainstore）部署在客户自己的云。浏览器经 CORS **直连客户数据面**取数，敏感数据（logs/traces/spans/experiments/datasets/completions）不经过 Braintrust 服务器，数据面只回传 metrics/状态遥测。
- **AWS 数据面组件**（直接读 terraform-aws-braintrust-data-plane/main.tf 确认）：KMS；main VPC + quarantine VPC（**用户自定义函数/UDF 打分器在隔离 VPC 的 Lambda 里跑**，安全沙箱设计）；RDS Postgres；ElastiCache Redis；S3（Brainstore 数据桶 + Lambda responses 桶）；Lambda 服务（APIHandler、AIProxy）；ECS 服务（api-ecs、gateway-ecs 即 AI gateway）；CloudFront 作 ingress；Brainstore 跑在 EC2 上且支持 **writer / fast-reader 节点分离**。GCP/Azure 走 Terraform 模块 + 官方 Helm chart（OCI: public.ecr.aws/braintrust/helm/braintrust）部署到 K8s。
- **Brainstore**：Rust 写的自研引擎，三个设计原则：全部数据放对象存储（无本地盘核心状态）、按客户分区、倒排索引（基于 Tantivy）+ 列存双结构；WAL 保证写后立即可见与强一致；官方宣称热查 <50ms、冷查 <500ms（来自官方博客，经搜索摘要，具体数字未直接核实）。**Brainstore 闭源且需 license key**（在 UI Settings > Data Plane 获取，Terraform 部署必填）。
- 开源协议：autoevals=MIT，braintrust-proxy=MIT 风格宽松许可，Go SDK=Apache-2.0，Terraform/Helm 模块开源；**平台本体（API server、Brainstore、UI）闭源商业**。

#### 人工介入与 HITL
- **人工评审（human review）**：有。项目级配置 score types（含 categorical 分类型分数，选项映射 0–100%），人工分数写回同一 scores 字段，对 logs 和 experiments 通用；可把 trace 指派给评审人并经 Slack/webhook 通知，用队列组织评审工作（官方文章描述了 triage 队列「忽略/需评审/重复」+ SME 队列补 expected 真值的模式；「kanban 队列」表述来自官方 article，细节未核实）。
- **agent 运行时断点/状态编辑/审批流**：无。Braintrust 不介入应用运行时，HITL 全部发生在数据/标注层而非执行层。

#### 值得借鉴的点
1. **六字段统一 span schema + 归属字段区分实验与日志**：`input/output/expected/scores/metrics/metadata` 全可选，`experiment_id` vs `log_id` 决定视角。对 trpc-agent-go 平台意味着一套埋点、一张表同时支撑「跑测试」和「看生产」，生产 trace 一键回流为回归数据集——这是 Braintrust 最值钱的架构决策，实现成本却很低。
2. **OTel-first、exporter-only 的 SDK 形态**：Braintrust Go SDK 不自造 tracer，只注册 exporter，并用 `x-bt-parent` header 做归属路由。trpc-agent-go 已有 OTel telemetry，平台侧只需实现一个 OTLP 接收端 + header 路由，即可零锁定接入任何 OTel 用户；Orchestrion 编译期注入也值得评估（Go 无运行时 monkey-patch，这是 Go 生态做「零代码埋点」的现实路径）。
3. **Eval 三要素 + 统一 Scorer 签名独立开源**：scorer 统一为 `(input, output, expected) → {score∈[0,1], metadata.rationale}`，归一化到 [0,1] 使任何打分方法可互换可对比。可以直接做一个 Go 版 autoevals（Levenshtein/ExactMatch/JSONDiff/LLMClassifier），既是产品护城河也是获客入口（autoevals 964 星远超其 SDK）。
4. **online scoring = 同一 scorer 定义 + 采样率异步跑在生产 trace 上**：离线在线共用打分器，只加一个 sampling rate 配置，低成本把评测延伸到生产监控。
5. **LLM 代理内建显式缓存**（`seed` 参数激活）：评测场景大量重复请求，缓存直接省钱且保证可复现；OpenAI 兼容 API 是最小接入成本方案。
6. **控制面/数据面分离 + 浏览器 CORS 直连数据面**：敏感 trace 不出客户 VPC、UDF 打分器放隔离 VPC 沙箱执行——对私有化/合规要求高的企业客户（国内场景尤甚）是决定性卖点，架构上从第一天就值得预留这条边界。

> **⚠️ 核验修正**（独立核查代理逐条反驳后订正）：
>
> - **订正**：原稿关键事实「Eval 三要素为 data/task/scores；JS 评测文件约定 *.eval.ts 且用 npx braintrust run 执行，Python 为 eval_*.py」经核查有误。部分有误。data/task/scores 三要素、*.eval.[ts|tsx|js|jsx] 命名、eval_*.py 命名均被 autoevals README 证实，且 JS SDK CLI 源码 INCLUDE_EVAL=["**/*.eval.ts",...] 佐证。但执行命令有误：README 中的 `npx braintrust run` 已过时——当前 JS SDK CLI（braintrust-sdk-javascript/js/src/cli/index.ts）只定义 eval/push/pull 三个子命令，没有 run；官方命令为 `npx braintrust eval`（及新 CLI `bt eval`）。来源: https://raw.githubusercontent.com/braintrustdata/braintrust-sdk-javascript/main/js/src/cli/index.ts 、https://www.braintrust.dev/docs/reference/cli/quickstart
> - **订正**：原稿关键事实「AI proxy 以 OpenAI 兼容 API 暴露于 https://api.braintrust.dev/v1/proxy，请求带 seed 参数才激活缓存；可自部署到 Vercel/Cloudflare/AWS Lambda/Express；MIT 风格许可」经核查有误。缓存条件表述过强，其余正确。URL、四个自部署目标（Vercel/Cloudflare/AWS Lambda/Express）、MIT 许可（LICENSE 文件为标准 MIT）均证实。但官方文档写明缓存有三种模式：默认 auto 模式下 temperature=0 **或** 设置 seed 均会缓存，并可用 x-bt-use-cache: always/never 强制开关——并非"只有带 seed 才激活"（README 的 "A seed activates the proxy's cache" 只是简化示例）。另注：官方 docs 已将该 proxy 页标注为 deprecated。来源: https://www.braintrust.dev/docs/deploy/ai-proxy

### 2.6 Opik (Comet)

**类别**：tracing平台 / LLM 可观测 + 评测一体化平台

#### 定位与设计理念
Opik 是 Comet 出品的开源 LLM 工程平台，定位是覆盖 LLM 应用「评估、测试、监控、优化」全生命周期：开发期用 tracing 调试、评测期用 datasets/experiments 回归、生产期用在线评估规则和 guardrails 兜底，最后用 agent optimizer 自动改进提示词。核心取舍是「全开源（Apache-2.0）+ 重后端」：不像很多竞品把开源版做成阉割版，Opik 自托管版包含全部功能（仅缺用户管理），代价是后端组件较重（Java 后端 + ClickHouse + MySQL + Redis + Zookeeper + MinIO）。一句话哲学：**观测数据（trace）是评测和优化的原料，三者必须在同一平台闭环**——experiment item 直接挂 trace、在线规则直接给生产 trace 打分、optimizer 直接消费已记录的 dataset 和 metric。README 宣称支持日均 4000 万+ trace 的规模。

#### 数据模型
层级为 **Project → Thread → Trace → Span（可嵌套）**：
- **Trace**：一次与 LLM/agent 的完整交互，字段含唯一 ID、input、output、起止时间、metadata（模型、temperature 等）、tags、feedback scores、聚合成本。
- **Span**：trace 内的单个操作（函数调用、API 请求、数据处理步骤），有 parent-child 层级、start/end 时间、类型（LLM 调用、tool/函数调用、数据处理、外部 API、自定义），LLM span 上带 provider/model/token usage，用于自动算成本（USD，逐 span 计价、trace 聚合、项目 dashboard 汇总）。
- **Thread**：用用户自定义的 `thread_id`（项目内唯一）把多条 trace 聚成一次多轮会话，是会话级评测（Conversational Coherence、User Frustration 等）的作用对象。
- **Feedback Score**：可打在 trace 或 thread 上，来源可以是 SDK、UI 人工标注、在线评估规则；同一 item 支持多人打分并做分歧分析。
- **Dataset / Dataset Item**：item 含 input、expected output 及元数据。**Experiment / Experiment Item**：一次评测运行；每个 experiment item 存 input、期望输出、实际输出、feedback scores，并**关联一条 trace** 以便下钻定位失分原因；实验层再算聚合指标供横向对比。
- **Prompt**：库内提示词按 commit ID 自动版本化，可打 tag（如 production），可与 experiment 关联。

#### 核心功能清单
- **Trace 视图**：有。嵌套 span 树、输入输出、多模态、agent graph 可视化（log_agent_graph）、分布式追踪。
- **会话/线程**：有。thread_id 聚合多轮对话，可整线程评测与标注。
- **Playground**：有。多提供商（OpenAI/Anthropic/Bedrock/Vertex/Azure/OpenRouter 等）、`{{variable}}` 模板对接 dataset 批量跑、支持图像多模态，运行自动记录到专用 `playground` 项目。
- **Prompt 管理**：有。中心化 prompt 库、commit 版本化、版本 tag、SDK `Prompt/ChatPrompt` 类按名取用（`client.get_prompt()`）、OQL 检索、与实验联动。
- **数据集**：有。手工/合成/生产 trace 导出三种来源，支持 CSV、API 导入。
- **实验对比**：有。多 experiment 聚合指标对比 + item 级下钻到 trace。
- **评测**：有，是强项。20+ 启发式指标（Levenshtein、ROUGE、BLEU、IsJson、RegexMatch 等）+ 近 20 个 LLM-as-a-judge 指标（Hallucination、Moderation、AnswerRelevance、ContextPrecision/Recall、**G-Eval**、LLM Juries、Trajectory Accuracy、Agent Tool Correctness 等）+ 会话级指标；judge 默认用 OpenAI GPT-5-nano，可经 LiteLLM（Python）/Vercel AI SDK（TS）换模型。另有在线评估规则：对生产 trace 按可调采样率（0-100%）自动跑 judge/Python 代码指标，thread 规则默认 15 分钟冷却等会话完结。
- **标注/人工反馈**：有。Annotation Queues——把 trace/thread 批量入队，定义评分维度，生成链接分享给 SME 在极简界面里逐条打分+评论。
- **监控告警**：监控 dashboard 有（feedback 分数、trace 量、token 用量随时间变化）；文档目录含 alerts.mdx，告警功能存在但细节未核实。
- **成本核算**：有。基于 provider+model+token usage 自动估算 USD 成本，9 个集成自动采集，价格表为仓库内 JSON，支持自定义价格。

#### 接入与实现方式
Python SDK 为主（README 显示代码占比 Python 55%、TypeScript 43%）。四种埋点方式：1) **`@track` 装饰器**——被装饰函数自动创建 span，嵌套调用自动生成父子 span，记录入参与返回值；2) **框架集成回调**：60+ 集成（OpenAI、LangChain、LangGraph、LlamaIndex、CrewAI、Google ADK、Bedrock 等），其中 9 个自动采集 token/成本；3) **低层 client** 手工建 trace/span；4) **OpenTelemetry OTLP 端点**：后端直接暴露 `/api/v1/private/otel`（自托管为 `http://localhost:5173/api/v1/private/otel`），**仅支持 HTTP 传输，gRPC exporter 明确会报错**，认证/路由靠 `OTEL_EXPORTER_OTLP_HEADERS` 里的 Authorization、projectName、Comet-Workspace 头。分布式追踪用类似 OTel 的上下文传播机制（跨服务传 headers）。SDK 侧还有 offline fallback（离线缓冲）能力。

#### 自托管与后端架构
Apache-2.0，全功能开源（自托管唯一缺失是用户管理）。docker-compose 组件清单（已核实源文件）：**opik-backend（Java 主后端，Dropwizard 系）**、**opik-python-backend**（执行用户 Python 评测代码 + optimizer studio + RQ worker）、**opik-frontend**（Nginx + Web UI，5173 端口）、**MySQL 8.4**（状态库：项目、prompt、规则等元数据）、**ClickHouse 25.8**（分析库：trace/span 大数据量存储与聚合，配 **Zookeeper 3.9** 协调）、**Redis 7.2**（缓存/队列，含 RQ）、**MinIO**（S3 对象存储，附件/大 payload），可选 **opik-guardrails-backend**（独立安全校验服务，`./opik.sh --guardrails` 启动，有 GPU 自动用 GPU，目前仅自托管可用），另带可选 Jaeger + OTel Collector 做平台自身观测。部署形态：本地 `opik.sh`/`opik.ps1` 一键 Docker（实验用），生产走 Kubernetes/Helm；文档强调 server 与 `opik`/`opik-optimizer` SDK 版本需对齐。Agent Optimizer 是独立 SDK（opik-optimizer），提供 MetaPrompt、HRPO、Few-shot Bayesian、Evolutionary（遗传算法）、GEPA（外部包封装）、Parameter（调 temperature/top_p）等优化器，消费平台内 dataset+metric，结果回平台对比。

#### 人工介入与 HITL
- **标注队列**：有（见上），是 Opik HITL 的核心形态——异步、离线、面向 SME 的批量评审，而非运行时介入。
- **UI 人工打分**：有，trace/thread 详情页可直接 annotate（annotate_traces.mdx）。
- **运行时断点/暂停 agent/状态编辑**：无。Opik 是旁路观测平台，不在 agent 执行路径上（guardrails 例外：其校验调用是阻塞式的，可拦截不合规响应，但那是自动规则，非人工审批）。
- **审批流**：无。未见 human approval / 断点续跑类机制。

#### 值得借鉴的点
1. **Experiment Item 强制挂 trace**：每条评测结果都关联一条完整 trace，失分可直接下钻到具体 span。对 Go Agent 测试平台，这意味着评测 runner 应复用同一套 tracing 管道（跑测试时也产生 trace），而不是另存一份「测试日志」——一张表加 trace_id 外键即可实现，收益是调试成本骤降。
2. **MySQL(元数据) + ClickHouse(trace 明细) 双库分离**：trace/span 是高写入、重聚合的时序型数据，与项目/prompt/规则等低频元数据分开存。Go 平台若预期规模不小，从第一天就把 span 写入走列存（ClickHouse），避免日后从 PG 迁移。
3. **OTLP HTTP ingestion 端点作为"万能接入口"**：自研 SDK 覆盖不了所有框架，直接在后端暴露 `/v1/traces` OTLP HTTP 接收器，把 OTel GenAI 语义的 span 映射进自家模型，就能白嫖整个 OTel instrumentation 生态——对 Go 生态尤其划算（Go 项目普遍已有 OTel）。注意 Opik 只做了 HTTP 未做 gRPC，Go 平台可两者都支持形成差异化。
4. **在线评估规则 = 「LLM judge + 采样率 + thread 冷却期」三件套**：对生产流量按百分比采样跑 judge，thread 级规则等 15 分钟会话静默后再评，避免评到半截对话。这套参数化设计成本低、直接可抄。
5. **Annotation Queue 的 SME 分发模式**：把「工程师视角的 trace 视图」和「非技术标注员的极简打分界面」拆成两个 UI，用分享链接+预定义评分维度降低人工评测组织成本，多人打分再做分歧分析。这是低成本高质量人工反馈的成熟范式。
6. **thread_id 由调用方自定义**：会话聚合不靠平台生成 ID，而是让业务传自己的会话 ID（项目内唯一），零迁移成本地兼容任何既有会话体系——Go SDK 设计 session 概念时照抄即可。

> **⚠️ 核验修正**（独立核查代理逐条反驳后订正）：
>
> - **未核实**：「支持 60+ 框架集成」——核查代理无法访问来源亦检索不到佐证，采信需谨慎。

### 2.7 Helicone + PromptLayer

**类别**：代理网关式观测（Helicone）+ Prompt管理/CMS与评测（PromptLayer）

#### 定位与设计理念
**Helicone**：开源（Apache-2.0，YC W23）的 "AI Gateway + LLM 可观测平台"，卖点是 "one line of code"——把客户端 baseURL 改成 `https://ai-gateway.helicone.ai` 即完成接入，日志、成本、延迟、缓存、限流全部在网关层随流量获得。核心取舍：**用网络层的一跳换取零代码侵入**——不需要 SDK、不挑语言，但可见性天然止步于"LLM 请求边界"，应用内部的非 LLM 步骤需要靠 header 约定补充。一句话哲学：观测不该是埋点工程，而是流量的副产品。

**PromptLayer**：闭源 SaaS（仅 SDK 开源），定位是 "prompt 工程平台"——Prompt Registry（prompt CMS）是第一公民，版本、release label、评测、请求日志都围绕"让 prompt 像代码一样可版本化、又让非工程师能改"展开。一句话哲学：prompt 是需要独立发布周期的生产资产，不该被硬编码在代码里。

#### 数据模型
**Helicone** 以单条 **request** 为原子实体，其上叠加：
- **Custom Property**：`Helicone-Property-[Name]` header 传入的扁平 KV（如 `Helicone-Property-Environment: production`），用于过滤、成本分段、告警；`Helicone-User-Id` 是特化 property，驱动 per-user 成本/指标。
- **Session**：三个 header 构成——`Helicone-Session-Id`（UUID，组 ID）、`Helicone-Session-Path`（斜杠路径如 `/abstract/outline/lesson-1`，**用路径字符串而非 span 父子指针表达层级**；文档强调"相同 path 代表同类工作，而非时间顺序"）、`Helicone-Session-Name`（人类可读名）。LLM 调用、向量库查询、工具执行都可挂进同一 session 树。
- **Score**：`POST /v1/request/{requestId}/score`，requestId 来自响应头 `helicone-id`；**只接受整数和布尔**（0.92 需转成 92），默认延迟 10 分钟聚合。

**PromptLayer** 的实体更丰富：
- **Request log**：provider/model/input/output/tokens/price/tags/metadata，且可关联 `prompt_name + prompt_version_number`，把线上流量回链到 prompt 版本。
- **Trace/Span**：`POST /spans-bulk` 接受的 span 结构就是 OTel 形态（`trace_id`/`span_id`/`parent_id`/`SpanKind`/`attributes`/`events`），并允许在 span 上挂 `log_request` 载荷；SDK 侧用 `@pl_client.traceable` 装饰器建父子 span。
- **Prompt Template**：带版本号、`commit_message`、`release_labels`（如 prod/staging）、metadata（可存 model provider/name/parameters，即模型配置随 prompt 版本走）。
- **Dataset**：可版本化，且能从请求历史按过滤条件生成（`create_dataset_version_from_filter_params`）。
- **Eval Report**：评测流水线产出多列打分（每列 boolean/数值），汇总 `overall_score`。
- **Score**：`POST /rest/track-score`，0–100 整数，可命名多个 score。

#### 核心功能清单
- **Trace 视图**：Helicone 有（Sessions 树视图，含 agent 全流程）；PromptLayer 有（traces + spans）。
- **会话/线程**：Helicone 有（Sessions，header 驱动）；PromptLayer 无独立会话实体，靠 trace/metadata/tags 组织。
- **Playground**：PromptLayer 有（官网宣传 prompt 编辑与测试环境，细节未逐页核实）；Helicone 有 prompt/experiments 相关 UI（docs 存在 prompts.mdx、experiments.mdx，细节未核实）。
- **Prompt 管理**：Helicone 有（prompt versioning，docs 含 prompts 与 prompts-legacy 目录）；PromptLayer 是核心功能（registry + release label + 零停机发布）。
- **数据集**：两者都有（Helicone datasets.mdx；PromptLayer 动态数据集+从日志生成）。
- **实验对比**：Helicone 有 experiments；PromptLayer 有批量评测 + A/B（release label 按流量分配，细节未核实）。
- **评测**：Helicone 偏"score 汇聚器"（接 RAGAS/自定义框架的结果）+ docs 有 evaluation 目录；PromptLayer 有内建 eval pipeline（含 LLM Assertion 等 eval 类型、CI 触发、`POST /reports/{id}/run`）。
- **标注/人工反馈**：Helicone 有 feedback（用户级好评/差评，feedback.mdx）+ scores API；PromptLayer 有人工打分（track.score），专门的标注队列未核实。
- **监控告警**：Helicone 有（alerts.mdx + webhooks.mdx，可按 property 触发）；PromptLayer 未见独立告警功能（未核实）。
- **成本核算**：Helicone 核心强项（成本按 user/property/feature 分段，"计算每用户每对话成本"）；PromptLayer 有（span/log 带 price 字段），但非主打。

#### 接入与实现方式
这是两者最值得对比的地方——**代理式 vs SDK 式**：
- **Helicone 代理模式**：改 baseURL，所有配置都通过 **HTTP header 即 API**（`Helicone-Property-*`、`Helicone-Session-*`、`Helicone-Cache-Enabled`、`Cache-Control: max-age=...`、`Helicone-Cache-Bucket-Max-Size`、`Helicone-Cache-Seed`）。代理在转发前完成日志、token/成本计算、缓存、限流、重试。
- **Helicone 异步日志模式**（`@helicone/async` 包）：不经代理、无额外延迟/单点风险，但官方明确**失去 cache、rate limits、retries 等主动能力**——这就是代理式埋点的取舍清单。
- **PromptLayer SDK 模式**：包装 provider client（`openai = pl.openai`），请求照常直连 provider，SDK 旁路上报；`return_pl_id=True` 拿回 request_id 供后续打分；另有 REST `log_request` 手动上报、`@traceable` 装饰器做函数级 trace、`pl.run(prompt_name=..., prompt_release_label="prod")` 把"取模板+执行+记日志"合一。
- **与 OTel 关系**：Helicone 不走 OTel，自有 header 协议；PromptLayer 的 `/spans-bulk` span schema 就是 OTel 数据形态（trace_id/span_id/SpanKind/attributes），事实上兼容 OTel 心智模型，但未见标准 OTLP endpoint（未核实）。

#### 自托管与后端架构
**Helicone**（Apache-2.0，monorepo）：README 列出的组件为 **Web（Next.js 前端）、Worker（Cloudflare Workers 代理）、Jawn（Express + Tsoa 的日志收集后端）、Supabase（应用库+认证）、ClickHouse（分析库）、Minio（对象存储）**。手动自托管文档要求：ClickHouse（时序/分析）、Minio（S3 兼容）、Postgres 17.4、mailhog、**Jawn（"Backend + Main Proxy"）**、Web。部署形态：docker-compose（推荐）、Helm（生产/企业）；官方博客称自托管栈从 12 个容器精简到 4 个。**Kafka：当前 README 与自托管文档均未提及，"云端曾用 Kafka" 未核实**。云端缓存存于 Cloudflare Workers KV（300+ 边缘节点）。
**PromptLayer**：平台闭源 SaaS，只有 SDK（prompt-layer-library / prompt-layer-js）开源；自托管/私有化部署选项未核实。

#### 人工介入与 HITL
两者都**没有** agent 断点续跑、状态编辑、审批流之类的运行时 HITL。人工介入形态：Helicone 是事后打分（scores API）+ 终端用户 feedback；PromptLayer 的 HITL 藏在 **prompt 发布流程**里——非工程师在 registry 改 prompt、用临时 release label 灰度、手动把 label 晋升到 prod（zero-downtime release 文档演示了 `new-var` 临时标签流程），这本质上是一个人工审批点。标注队列：两者均未核实到该功能。

#### 值得借鉴的点
1. **"header/metadata 即协议"的扁平 KV 埋点面**：Helicone 用 `Helicone-Property-*` 一招同时喂过滤、成本分段、告警、导出。Go 平台可在 context/metadata 里定义同样一套扁平 KV，proxy 与 SDK 两种接入共用同一协议，后端只需一张 properties 宽列。
2. **Session-Path 路径字符串代替 span 树**：`/task/research/web_search` 这种路径既能渲染层级，又天然支持"同 path = 同类工作"的跨 session 聚合（比 parent_span_id 更易做 GROUP BY），对 agent 工作流的步骤级成本/延迟统计非常实用，且用户手写零门槛。
3. **Score 收窄为 int/bool + 延迟批量聚合**：Helicone 拒绝浮点、默认 10 分钟延迟聚合，是对 ClickHouse 类分析库非常务实的写入设计；平台定位做"score 汇聚器"（接任意外部评测框架的结果）而非绑定自家评测。
4. **Prompt release label 解耦发布**：代码里只写 `prompt_release_label="prod"`，改 prompt 不用发版；PromptLayer 的 zero-downtime 流程（新版本挂临时 label → 代码切换 → 晋升 prod）可直接抄成 Go 平台的 prompt/配置发布机制。
5. **数据集从生产日志过滤生成**：PromptLayer 的 `create_dataset_version_from_filter_params` 把"线上 bad case 回流成回归测试集"做成了一等 API，配合 CI 触发 eval pipeline（`/reports/{id}/run`），是测试平台闭环的关键一环。
6. **双模接入并明码标价**：同时提供代理模式（换取缓存/限流/重试等主动能力）与异步 SDK 模式（零延迟风险），并像 Helicone 一样在文档里明确写出 async 模式失去哪些功能——对 Go Agent 平台，等价物是 "gRPC 拦截器/中间件模式 vs 异步上报模式" 的功能矩阵。

### 2.8 AgentOps + Laminar + Lunary

**类别**：agent 原生观测平台（tracing平台）

#### 定位与设计理念
- **AgentOps**（agentops.ai）：定位是"AI Agent 的 Observability 和 DevTool 平台"，覆盖从原型到生产。核心哲学是"两行代码接入、其余全自动"——`import agentops; agentops.init(API_KEY)` 后 SDK 自动识别已安装的 LLM provider 并埋点。取舍：牺牲后端可控性（自托管文档较薄），换取与十几个 agent 框架（OpenAI Agents SDK、CrewAI、AG2/AutoGen、LangChain、LlamaIndex、CamelAI、LiteLLM 等）的开箱集成和会话回放体验。
- **Laminar**（lmnr-ai/lmnr，YC S24）：定位是"open-source observability platform for AI agents"，哲学是 OTel 原生 + 性能优先——Rust 后端、20x trace 压缩、SQL 直查 trace。取舍：不做重量级 prompt 管理，把评测做成"unopinionated 的 SDK/CLI"，把分析交给 SQL。
- **Lunary**：定位是"面向 LLM 聊天机器人/生产应用的 developer toolkit"，主打观测 + prompt 管理 + 评测 + 防护（radar/guardrails）一体化，SOC 2/ISO 27001 认证，强调可自托管进 VPC。注意：其主仓库 lunary-ai/lunary 目前（2026-07）返回 404，官方 Python SDK 仓库已 archived，当前开源状态存疑（详见下文，部分信息来自历史 fork，标注未核实）。

#### 数据模型
- **AgentOps**：官方 core-concepts 文档给出明确层级：`SESSION`（根容器，"a single user interaction with your agent"）→ `AGENT`（自治组件）→ `WORKFLOW`（操作的逻辑分组）→ `OPERATION/TASK`（具体函数）→ `LLM` / `TOOL`（叶子 span）。整个模型是 OpenTelemetry span 的 kind 分类，LLM span 上自动挂 Model、Provider、token 数和 "Cost: The estimated cost of the interaction" 字段。
- **Laminar**：OTel 原生的 trace/span 模型；在其上叠加 Datasets（含标注 UI）、Evals（SDK/CLI 产生的评测结果）、Signals（用自然语言描述的 agent 行为监控，如 "agent is stuck in a loop"）、SQL 可查询的 traces/spans/metrics/events 四类表。
- **Lunary**：以事件流为中心：runs（llm/agent/tool/chain 类型的运行记录，含 cost/token/latency）、threads（对话线程，聊天回放的载体）、feedback（用户反馈绑定到 run）、templates（版本化 prompt）、radar 分类结果（按预定义规则给响应打标）、topics（自动话题分类）。字段级细节因主仓库不可访问，未核实。

#### 核心功能清单
- **AgentOps**：trace视图：有（"Session replays——step-by-step agent execution graphs"、瀑布/事件图 Event Graphs）；会话/线程：有（Session 即根实体，含 Chat Viewer）；playground：无（README/文档未见）；prompt管理：无；数据集：无；实验对比：无（有 benchmark 支持但非产品化实验对比）；评测：弱（未见产品化 eval）；标注/人工反馈：无；监控告警：弱（有 dashboard 分析，告警未核实）；成本核算：有且为强项（跨 provider 的 LLM Cost Management，span 级 cost 字段）。多 agent：有 "Multi-agent framework visualization"。
- **Laminar**：trace视图：有（含实时 trace 查看的自研 real-time engine、全文检索）；会话/线程：有（session 概念，未核实细节）；playground：有（README 提及，需配 LLM provider，未核实细节）；prompt管理：无（非重点）；数据集：有（Datasets + 自定义标注 UI）；实验对比：有（evals 结果对比）；评测：有（unopinionated evals SDK + CLI，可进 CI/CD）；标注/人工反馈：有（annotation UI）；监控告警：有（Signals，自然语言定义行为 + Slack 通知；SQL dashboard builder）；成本核算：有（LLM span 自动记 cost，未核实粒度）。特色：MCP/CLI 用 SQL 查 trace，供 coding agent 使用；chat-with-trace。
- **Lunary**：trace视图：有（agent tracing、logs/traces 调试）；会话/线程：有且为强项（threads/chat replay、用户级追踪）；playground：有（prompt 编辑测试，未核实）；prompt管理：有（版本化、协作、A/B 测试）；数据集：有（fine-tuning 数据集导出）；实验对比：有（dashboard 或 CI 触发评测）；评测：有（自定义 metric）；标注/人工反馈：有（feedback tracking、thumbs up/down 绑定 run）；监控告警：有（实时监控 + agent 表现异常通知）；成本核算：有（cost/token/latency 分析）。另有 radar（按预定义标准对 LLM 响应分类以便回查）、topics 自动分类、PII masking。

#### 接入与实现方式
- **AgentOps**：Python 装饰器族 `@session`/`@agent`/`@operation`/`@task`/`@workflow`，自动记录输入输出、异常，支持 async 与 generator 函数；同时 `init()` 后自动 monkey-patch 已安装的 LLM 库（"automatically identifies installed LLM providers and instruments their API calls"）。官方明确 "AgentOps is built on OpenTelemetry"。主 SDK 为 Python，仓库含 TypeScript（约 37.5%）。
- **Laminar**：OpenTelemetry-native SDK（Python `lmnr` / TS `@lmnr-ai/lmnr`），"1 line of code" 自动埋 Vercel AI SDK、Browser Use、Stagehand、LangChain、OpenAI、Anthropic、Gemini 等；trace 经 gRPC exporter 上报——这意味着任何 OTLP 客户端（含 Go）理论上可直接对接其后端。
- **Lunary**：SDK 包装器模式：JS 侧 `monitorOpenAI(new OpenAI())`、`monitorAnthropic(...)` 扩展客户端对象，支持流式；上下文通过 tags/userId/userProps 传递；有 LangChain JS/Python 回调、LiteLLM callback 集成。与 OTel 无关（自有事件协议）。Python SDK 仓库已 archived（后续接入方式未核实）。

#### 自托管与后端架构
- **AgentOps**：MIT license；仓库 `app/` 目录含开源的 Dashboard + API backend，可本机运行，但 README 未列出数据库/基础设施组件清单（存储选型未核实）。
- **Laminar**：Apache-2.0；docker-compose-full.yml 共 6 个服务：`app-server`（Rust 后端，HTTP + gRPC + 实时 API）、`frontend`（Next.js）、`postgres`（16，应用状态）、`clickhouse`（分析型 trace 存储）、`rabbitmq`（异步摄取队列）、`quickwit`（v0.8.2，全文检索/日志索引）。另提供 lightweight 单命令 `docker compose up -d` 版本。注意：线索里的 "RabbitMQ/ClickHouse" 属实，且还有 Quickwit。
- **Lunary**：历史上核心为 Apache-2.0，TypeScript monorepo（`packages/backend` + `packages/frontend`），PostgreSQL 15+ 单数据库，npm 迁移脚本，8080 端口（信息来自主仓库的 fork，现状未核实）；官方宣称支持 VPC 内 Kubernetes/Docker 部署，部分功能需 Enterprise license。主仓库当前 404，是否仍开放源码未核实。

#### 人工介入与 HITL
- **AgentOps**：无断点/状态编辑/标注队列/审批流，仅回放式事后调试。
- **Laminar**：有标注侧能力（Datasets 的 annotation UI 可视为轻量标注队列），无执行期断点/状态编辑/审批流。
- **Lunary**：有人工反馈闭环（feedback tracking + radar 筛出问题响应供人工回查），无断点/状态编辑/审批流。三者均不做执行期 HITL——这是与 LangGraph 类框架层的明确分工。

#### 值得借鉴的点
1. **抄 AgentOps 的 span kind 枚举**：SESSION/AGENT/WORKFLOW/TASK/LLM/TOOL 六级层级直接映射到 trpc-agent-go 的 Runner/Agent/Tool 概念，用 OTel span attribute（如 `agentops.span.kind`）实现，前端即可按 kind 渲染多 agent 瀑布图与回放。
2. **成本作为 span 一等字段**：AgentOps 在每个 LLM span 上物化 model/provider/tokens/cost，聚合到 session 级。Go 平台应在埋点层查价目表算 cost 而不是查询时算，否则模型价格变动会污染历史数据。
3. **Laminar 的存储三分法**：Postgres 存元数据/配置、ClickHouse 存 span 分析、Quickwit 做全文检索、RabbitMQ 解耦摄取。Go 生态可直接复刻（ClickHouse + NATS/RabbitMQ），摄取路径与查询路径分离是高吞吐 agent trace 的关键。
4. **只做 OTLP gRPC 摄取口**：Laminar 证明"OTel 原生"能让任意语言 SDK（包括 Go 的官方 otel-sdk）零成本接入，平台不必自造上报协议；agent 语义用 span attributes 约定即可。
5. **SQL-over-traces + MCP**：Laminar 把 trace 暴露为 SQL 可查表并提供 MCP server，让 coding agent 自己查线上 trace 调试——对"Agent 测试平台"这是天然的测试断言接口（断言某 trace 中 tool 调用次数/顺序）。
6. **Lunary 的 radar 模式**：用预定义规则持续对生产响应分类打标、沉淀成待人工复查队列，是低成本的"生产数据→测试用例"回流机制，比全量人工标注可行得多。

### 2.9 OpenTelemetry GenAI 语义约定 + OpenLLMetry (Traceloop)

**类别**：标准层 / OTel 自动埋点库

#### 定位与设计理念
这不是一个平台，而是「标准 + 埋点库」的组合，解决的是 LLM 可观测数据的**互操作性**问题：OTel GenAI 语义约定定义了 LLM/Agent 遥测数据「长什么样」（统一的 span/event/metric 命名与属性 schema），OpenLLMetry 则是把主流 LLM SDK 调用「自动翻译」成这种标准数据的埋点库集合。核心取舍是：**不绑定任何后端**——数据以 OTLP 协议输出，可以打到 Datadog、Grafana、Langfuse、SigNoz 等 20+ 目的地。行业收敛到 OTLP 作 ingest 标准的原因由此可见：埋点方只写一次（instrumentation 与 vendor 解耦），后端方只解析一种协议（凭 `gen_ai.*` 属性即可渲染 LLM 专用 UI），双边成本都最低。一句话哲学：「LLM 可观测只是可观测的一个子域，复用 OTel 全套管线（SDK/Collector/OTLP），只新增语义层」。

重要时效信息：**GenAI 语义约定已于近期从 OTel 主 semconv 仓库迁出**，主仓库中的 gen_ai 内容已标记 deprecated，现由独立仓库 open-telemetry/semantic-conventions-genai 维护（用 Weaver 管理对核心 semconv 的依赖），整体仍处 Development（非 Stable）状态。

#### 数据模型
语义约定层的实体全部是标准 OTel 三信号，靠属性命名承载语义：
- **Span（五大类）**：Inference / Embeddings / Retrieval / Memory / Execute Tool。span 名约定为 `{gen_ai.operation.name} {gen_ai.request.model}`（如 `chat gpt-4`）。必填属性：`gen_ai.operation.name`、`gen_ai.provider.name`（新版取代了旧的 `gen_ai.system`）；推荐属性：`gen_ai.request.model`、`gen_ai.response.model/finish_reasons`、`gen_ai.usage.input_tokens/output_tokens`、`error.type`。SpanKind 规则：跨进程调用用 CLIENT，进程内框架操作用 INTERNAL。
- **Agent Span**：操作名包括 `create_agent`、`invoke_agent`、`plan`、`invoke_workflow`、`execute_tool`；属性 `gen_ai.agent.id/name/description/version`、`gen_ai.tool.name` 等。层级关系：`invoke_agent` 为父 span，plan span 内含子 LLM 调用，tool 执行 span 与 task span 作为兄弟节点挂在 invoke_agent 下。
- **会话**：`gen_ai.conversation.id` 属性（要求业务上确实存在会话 ID 时才填，明确禁止用 UUID/trace ID 合成兜底）——即"session"不是独立实体，而是 span 上的关联键。
- **内容承载（属性 vs 事件的取舍）**：prompt/completion 以结构化 JSON schema 定义为 `gen_ai.input.messages` / `gen_ai.output.messages`，可作为 span 属性（不支持结构化时记为 JSON 字符串）或放入事件 `gen_ai.client.inference.operation.details`（取代了早期按消息拆分的 gen_ai.user.message/gen_ai.choice 事件设计）。所有内容类字段均为 **Opt-In 级别**：规范明文要求默认不采集，由 `OTEL_INSTRUMENTATION_GENAI_CAPTURE_MESSAGE_CONTENT` 类开关控制。
- **Score/评测**：定义了 `gen_ai.evaluation.result` 事件（必填 metric 名，条件必填 score 数值与 label，推荐 explanation 与 response ID）——标准层已为"评测结果回传 trace"预留了 schema。
- **Metrics**：全部为 Histogram。客户端：`gen_ai.client.token.usage`（单位 {token}）、`gen_ai.client.operation.duration`（秒）、time_to_first_chunk、time_per_output_chunk；服务端：`gen_ai.server.request.duration`、time_to_first_token、time_per_output_token；Agent 维度：`gen_ai.workflow.duration`、`gen_ai.invoke_agent.duration`、`gen_ai.execute_tool.duration`。

OpenLLMetry 的数据模型在此之上加了一层自有命名空间：`traceloop.span.kind`（枚举 workflow/task/agent/tool）、`traceloop.workflow.name`、`traceloop.entity.name/path/input/output`、`traceloop.association.properties`（关联 user_id 等业务属性）、`traceloop.prompt.key/version/template` 等。注意它的 gen_ai 属性与最新 semconv 有偏差（如用 `gen_ai.usage.prompt_tokens/completion_tokens` 而非 input/output_tokens，用扁平化 `gen_ai.prompt`/`gen_ai.completion` 承载内容），另有向量库属性 `db.vector.query.top_k/result_count` 等。

#### 核心功能清单
（注意：以下针对"标准+埋点库"本身；配套商业平台 Traceloop 的功能未打开其产品文档，标未核实）
- **trace 视图**：无（只产出数据，渲染靠下游后端）
- **会话/线程**：仅提供 `gen_ai.conversation.id` 关联键，无 UI
- **playground**：无
- **prompt 管理**：OpenLLMetry 有 `traceloop.prompt.key/version/version_hash/template/template_variables` 属性用于把 trace 关联到 Traceloop 平台的 prompt registry（平台侧功能细节未核实）；语义约定层无
- **数据集**：无
- **实验对比**：无
- **评测**：标准层有 `gen_ai.evaluation.result` 事件 schema（承载评测结果，不执行评测）；OpenLLMetry 本身不做评测
- **标注/人工反馈**：无
- **监控告警**：无（但 metrics 定义使得任何 Prometheus/Grafana 栈可直接告警）
- **成本核算**：无直接功能；`gen_ai.client.token.usage` 直方图 + request.model 维度是成本计算的原料

#### 接入与实现方式
OpenLLMetry 是**纯 OTel 自动埋点（monkey-patch）+ 装饰器**形态，Apache-2.0：
- 每个 provider/框架一个独立 pip 包（`opentelemetry-instrumentation-openai/-anthropic/-bedrock/...`），覆盖 16+ LLM 提供商（OpenAI/Azure、Anthropic、Bedrock、Vertex、Gemini、Cohere、Mistral、Ollama、Watsonx 等）、7 个向量库（Pinecone、Chroma、Qdrant、Milvus、Weaviate、LanceDB、Marqo）、11+ 框架（LangChain、LlamaIndex、CrewAI、Haystack、LiteLLM、LangGraph、OpenAI Agents、MCP 等）。
- `traceloop-sdk` 是一层胶水：`Traceloop.init()` 一行启用全部 instrumentation，并提供 `@workflow`/`@task`/`@agent`/`@tool` 装饰器手工划分业务层级（写入 traceloop.span.kind）。
- 语言支持：Python 为主，JS/TS 另有 openllmetry-js；**Go 版（traceloop/go-openllmetry）仅早期 alpha**，无自动埋点，需手动调 `LogPrompt()/LogCompletion()`，最近 release 为 2025-11 的 v0.1.3——对 Go 生态而言，自动埋点这条路目前是空白。
- **内容捕获与隐私**：默认记录 prompt/completion（`TRACELOOP_TRACE_CONTENT` 默认 "true"，设 false 关闭），且支持运行时按请求覆盖——`should_send_prompts()` 同时检查环境变量与 OTel context 中的 `override_enable_content_tracing` 键，二者任一为真即记录，实现"全局关、特定用户/请求开"的细粒度控制。这与 OTel 规范的"默认不采集、显式 opt-in"方向相反，是它常被诟病的点。另外自 0.49.2 起 SDK 不再回传任何自身使用遥测。

#### 自托管与后端架构
OpenLLMetry **没有后端**——它是纯客户端库，数据经 OTLP exporter 直发或经 OTel Collector 中转到 23 个受支持目的地（Datadog、Honeycomb、New Relic、Grafana、SigNoz、Splunk、Langfuse 等）。不存在存储选型、队列问题；"自托管"即自托管任意 OTLP 兼容后端。开源协议 Apache-2.0（Python/JS/Go 三个仓库一致）。Traceloop 商业平台的自托管/混合部署形态未核实。

#### 人工介入与 HITL
无。无断点、无状态编辑、无标注队列、无审批流——标准层最接近的只有 `gen_ai.evaluation.result` 事件可承载人工评分结果，但采集与流转需平台自建。

#### 值得借鉴的点
1. **直接把 OTel GenAI semconv 作为你平台的 ingest schema**：trpc-agent-go 平台只要 span 上带 `gen_ai.operation.name`/`gen_ai.provider.name`/`gen_ai.usage.*`，就能被 Grafana/Datadog/Langfuse 全生态消费，也能反向接收任何 OpenLLMetry 埋点应用的数据。注意用新仓库（semantic-conventions-genai）的属性名（`gen_ai.provider.name`、`input_tokens`），旧名已 deprecated。
2. **抄 agent span 层级模型**：`invoke_agent` 为根、`plan`（内含 LLM 子 span）与 `execute_tool` 为子/兄弟节点、`invoke_workflow` 编排多 agent——这套父子关系可直接映射 trpc-agent-go 的 Runner/Agent/Tool 调用栈，UI 树状渲染有现成规则可依。
3. **内容捕获做三态开关**：结合两家做法——规范的"默认关 + `OTEL_INSTRUMENTATION_GENAI_CAPTURE_MESSAGE_CONTENT` 全局开"，加上 OpenLLMetry 的 context 级 `override_enable_content_tracing` 按请求覆盖。生产环境默认脱敏、调试会话/白名单租户单独放开，这是隐私合规和可调试性的最优平衡。
4. **内容放结构化 messages 属性而非拆散的事件**：`gen_ai.input/output.messages` 的 JSON schema 让后端能还原完整多轮对话（含 tool_call），比旧的按消息发事件方案查询开销小得多；同时保留 events 通道给超大 payload（span 属性有大小限制时降级到 event/log）。
5. **traceloop.span.kind 式的业务分层装饰器**：workflow/task/agent/tool 四级枚举 + `traceloop.entity.input/output`，在 Go 里可做成 `trace.WithSpanKind()` 风格的 option 或中间件，让用户在自动埋点之上标注业务语义——trace 视图按 workflow 聚合的前提就是这个属性。
6. **Go 自动埋点是市场空白**：go-openllmetry 仍是手动 Log 的 alpha 状态。trpc-agent-go 若在框架层内建符合 semconv 的自动埋点（框架埋点不需要 monkey-patch，天然优势），即是 Go 生态的差异化能力。

### 2.10 LangGraph Studio + Google ADK Web UI

**类别**：调试UI（开发期单机调试台）

#### 定位与设计理念
两者都是"开发期调试台"，而非生产观测平台。LangGraph Studio 官方定义为 "specialized agent IDE"：可视化、交互、调试实现了 Agent Server API 协议的 agent 系统，并与 LangSmith 的 tracing/评测/prompt 工程集成。它的核心取舍是：调试对象不是"日志/trace"，而是**可执行的活体 graph**——UI 直接连到一个正在运行的 agent server，因此能改状态、重放、打断点，而不只是事后看 trace。ADK Web 的定位更明确："built-in developer UI……for easier agent development and debug"，文档用醒目警告写明 "ADK Web is not meant for use in production deployments"。一句话哲学：**生产观测平台回答"发生了什么"，开发调试台回答"如果改一下会怎样"**——前者只读，后者可写（编辑状态、fork、重放、改 eval case）。

#### 数据模型
**LangGraph Studio**（复用 LangGraph/LangSmith 模型）：`assistant`（graph + 配置的版本化实例）→ `thread`（会话，携带持久状态）→ `run`（一次执行，追加到 thread）→ `checkpoint`（每个超步后的状态快照，含 `thread_id` + `checkpoint_id`，通过 `get_state_history` 枚举，`state.next` 标记下一个待执行节点）。评测侧对接 LangSmith 的 `dataset / example / experiment / evaluator / feedback` 实体。时间旅行的全部能力都建立在 checkpoint 链上。
**ADK Web**：`session`（含 `state` 键值字典 + `events` 列表）→ `event`（执行事件，含 `state_delta`/`artifact_delta`）→ `invocation`（按用户消息分组的一轮执行）；trace 是 OpenTelemetry 风格的 span，按 user message 分组展示。评测实体为 `EvalSet`（`eval_set_id`、`eval_cases[]`）→ `EvalCase`（`eval_id`、`conversation[]`、`session_input`{app_name,user_id,state}）→ 每轮 invocation 含 `invocation_id`、`user_content`、`final_response`、`intermediate_data.tool_uses[]`（含 args/name）与 `intermediate_responses`（子 agent 中间回复），由正式 Pydantic schema（eval_set.py/eval_case.py）背书。

#### 核心功能清单
**LangGraph Studio**：trace 视图——有（可从 LangSmith trace 一键 "Run in Studio"，还能把远程 trace 的 thread 克隆到本地 agent 调试）；会话/线程——有（thread 列表、状态逐节点展开、Pretty/JSON 切换）；playground——有（View LLM Runs 打开节点内单次 LLM 调用调参）；prompt 管理——有（节点直改 prompt + 保存为新 assistant 版本）；数据集——有（Add to Dataset 把节点输入输出存成 example）；实验对比——有（Run experiment 对 LangSmith dataset 跑整个 assistant，evaluator 自动打分，结果跳 LangSmith 看）；评测——有（依托 LangSmith evaluator）；标注/人工反馈——Studio 本体无独立标注队列（LangSmith 侧有 feedback，未在 Studio 页面核实）；监控告警——无；成本核算——无。
**ADK Web**：trace 视图——有（Trace tab，行悬停联动聊天消息，点击出 Event/Request/Response/Graph 四面板，蓝色行表示产生了 event）；会话——有（session 创建/切换）；state 检查——有且**可修改**；artifacts 检查——有；playground——无（无独立单次 LLM 调参界面）；prompt 管理——无；数据集——无通用 dataset，但有 eval set 管理；实验对比——弱（eval 历史记录每次 run 的指标，无正式实验对比视图）；评测——有（Eval tab：建 eval set → "Add current session" 把当前会话存为 case → 铅笔编辑 case → 滑杆配置 tool_trajectory_avg_score / response_match_score 阈值 → Run Evaluation，失败项悬停看 Actual vs Expected 对比）；标注——无；监控告警——无；成本核算——无。另有 Visual Builder（拖拽建 agent，Python only）和 `adk conformance` 录制回放测试。

#### 接入与实现方式
**LangGraph Studio**：零埋点——不是 SDK 插桩，而是**协议接入**：`langgraph dev` 启动本地 Agent Server（默认端口 2024），Studio 前端托管在 smith.langchain.com，通过 `?baseUrl=http://127.0.0.1:2024` 连本地 server（需 LangSmith API key）。所有可观测数据来自 checkpointer 落的 checkpoint 和 server API，与 OTel 无直接关系；prompt 可编辑性靠 graph 配置 schema 里的 `json_schema_extra: {"langgraph_nodes": [...], "langgraph_type": "prompt"}` 元数据声明。`langgraph dev` 还内置 DAP（Debug Adapter Protocol）支持，可挂 IDE 断点。
**ADK Web**：框架自动埋点——ADK runtime 内置 OpenTelemetry 兼容 instrumentation，Trace tab 直接消费；Go 版可加 `-otel_to_cloud` 导出到 Cloud Trace，Python 用 `telemetry.maybe_set_otel_providers()` 注册全局 OTel provider。前端是独立 Angular 应用（adk-web 仓库），靠 `adk api_server` 暴露的 REST API 工作；Go 版则以库形式嵌入：`full.NewLauncher()` 把 web server、REST API、Web UI 打进单个二进制，`go run agent.go web api webui` 启动。

#### 自托管与后端架构
**LangGraph**：`langgraph dev`——单进程内存态、状态 pickle 到本地目录、热重载、无 Docker；`langgraph up`——Docker 三容器（API server + PostgreSQL 持久化 + Redis），默认端口 8123，模拟生产。Studio UI 本身不开源、云端托管；LangGraph 框架开源。生产部署走 LangSmith Cloud 或 Self-hosted（自托管 Studio 细节未核实）。
**ADK Web**：完全本地单机。Python `adk web` 默认 8000 端口，session 默认 in-memory，可 `--session_service_uri "sqlite:///sessions.db"`；artifact 默认落本地 `.adk/artifacts`。Go 版 trace 默认内存保留 1 万条（`-trace_capacity`，默认 10000）。前端 adk-web 为 Apache 2.0 开源。无队列、无独立数据库组件——这正是"调试台"与"平台"的架构分界。

#### 人工介入与 HITL
**LangGraph Studio**：最完整。断点——Interrupt 按钮选任意节点、选 pause before/after，thread log 里点 Continue 恢复（即 LangGraph interrupt 机制的 UI 化，断点即 HITL）；状态编辑——"Edit node state" 改任意节点输出后点 Fork，从该 checkpoint 派生新分支运行；重放——"Re-run from here" 不改状态直接从 checkpoint 重跑（可换 assistant 配置）。底层语义清晰：replay = 拿旧 checkpoint config 再 invoke（checkpoint 之前的节点不重执行，之后的节点真实重跑，LLM 调用会再次发生）；fork = `update_state` 在旧 checkpoint 上创建分支 checkpoint，**原历史不可变**。Chat mode 还支持编辑 human message 后 fork 会话。无标注队列/审批流。
**ADK Web**：可查看并修改 session state、可编辑 eval case 里的 agent 回复，但无节点级断点、无 checkpoint fork/重放（conformance 的 replay 是录制回放测试，不是交互式 time-travel）。无标注队列/审批流。

#### 值得借鉴的点
1. **Checkpoint-first 的时间旅行模型**：trpc-agent-go 若在 Runner 每个事件/步骤后落不可变 checkpoint（thread_id + checkpoint_id 链表），"暂停/编辑状态/fork 重放"就全是免费能力——fork 只是"在旧 checkpoint 上写一个分支 checkpoint 再 resume"，实现成本低而调试价值极高；关键设计是**历史不可变、fork 产生新分支**，避免状态回滚的正确性泥潭。
2. **"本地 server + 托管 UI"接入模式**：LangGraph Studio 用 `?baseUrl=localhost:2024` 让云 UI 直连本地进程，用户零部署就能用最新 UI。Go 平台可定义一个小型 Debug Server API 协议（threads/state/checkpoints/interrupt），UI 与框架解耦、可独立演进。
3. **"Add current session → eval case" 闭环**：ADK 把"聊出一个好/坏例子"一键固化为可编辑、可回归的 eval case（含 tool_uses 轨迹 groundtruth），并配 tool_trajectory_avg_score（轨迹精确匹配）+ response_match_score（ROUGE-1）两个廉价确定性指标跑 CI。这是测试平台最该抄的工作流：调试数据直接变测试资产。
4. **trace 行与聊天消息双向联动 + Event/Request/Response/Graph 四面板**：ADK Trace tab 把"发给 LLM 的原始 request"作为一等公民展示，这是 agent 调试最高频需求；成本仅为内存 ring buffer（默认 1 万条），无需数据库。
5. **prompt 可编辑性靠 schema 元数据声明**（langgraph_nodes/langgraph_type）：框架不猜哪个字段是 prompt，由开发者在配置 schema 上标注，UI 据此渲染编辑入口并版本化为新 assistant——Go 里可用 struct tag 实现同样机制。
6. **明确划清 dev/prod 边界**：ADK 在文档显著位置声明 dev-only，同时留 `-otel_to_cloud`/OTel provider 出口把生产观测交给外部平台；LangGraph 用 dev（内存）/up（PG+Redis）双命令分层。调试台不必做保留策略、告警、成本核算，做薄反而好用。

### 2.11 OpenAI Agents SDK Tracing + MLflow Tracing

**类别**：框架内建tracing / 开源观测平台（tracing平台）

#### 定位与设计理念
**OpenAI Agents SDK Tracing** 是 agent 框架"内建 tracing"的代表：SDK 自带一套非 OTel 的私有 trace/span 模型，默认开启、零配置，把 agent loop 的每个语义步骤（LLM 调用、工具、handoff、guardrail、语音）自动埋点，数据默认发到 OpenAI 托管的 Traces dashboard（platform.openai.com/traces，免费但不可自托管；ZDR 组织不可用）。核心取舍：牺牲 OTel 标准化，换取与框架语义完全对齐的类型化 span 和"装完即用"的体验；同时通过极简的 `TracingProcessor` 接口把导出目的地插件化，形成了 25+ 家厂商的外部 processor 生态。一句话哲学：框架负责产生高质量语义化 trace，去哪儿消费由 processor 插件决定。

**MLflow Tracing**（MLflow 3.x，Linux 基金会项目，Apache-2.0）是框架无关的开源自托管观测平台：完全 OTel 兼容（可 ingest 也可 export，支持 GenAI Semantic Conventions），靠 autolog 一行代码给 40+ 框架/模型库自动埋点，trace 落到 MLflow Experiment，与评测、数据集、prompt 管理组成端到端平台。一句话哲学：用 OTel 标准 + 自有存储换取无厂商锁定的完整 LLMOps 闭环。

#### 数据模型
**OpenAI Agents SDK**：Trace 表示一次 workflow 端到端运行，字段：`workflow_name`、`trace_id`（格式必须为 `trace_<32位字母数字>`）、`group_id`（可选，用于把同一会话/线程的多条 trace 关联，如 chat thread ID）、`disabled`、`metadata`。Span 有 `started_at/ended_at`、`trace_id`、`parent_id`、以及类型化的 `span_data`（`AgentSpanData`、`GenerationSpanData`、function/guardrail/handoff/transcription/speech/custom 等）。无内置 score/评测实体。

**MLflow**：Trace = `TraceInfo` + `TraceData` 两部分。`TraceInfo` 是轻量元数据行：`trace_id`、`trace_location`（目前即 Experiment）、`request_time`、`state`（OK/ERROR/IN_PROGRESS/STATE_UNSPECIFIED）、`execution_duration`、`request_preview`/`response_preview`（根 span 输入输出的截断预览）、`client_request_id`、`trace_metadata`（不可变，如 session/user）、`tags`（可变，用于过滤）。`TraceData` 是 Span 树；Span 字段：`span_id`、`trace_id`、`parent_id`、`name`、`start/end_time_ns`、`status`、`inputs/outputs`、`attributes`、`events`（异常栈），带 `SpanType`（CHAT_MODEL/LLM/CHAIN/AGENT/TOOL/RETRIEVER/ROUTER 等）。会话经 `session_id`/`user` 元数据实现（3.11.0 起有专用参数）。质量信号是一等实体：`Feedback`/`Expectation`（统称 Assessment）挂在 trace 或具体 span 上，带 `AssessmentSource`（HUMAN/LLM_JUDGE 等）与修订历史。

#### 核心功能清单
**OpenAI Agents SDK Tracing**：trace 视图——有（托管 Traces dashboard）；会话/线程——仅 `group_id` 关联，无会话 UI 实体；playground——无（SDK 层面）；prompt 管理——无；数据集——无；实验对比——无；评测——无（SDK 另有 evals 产品，tracing 本身无）；标注/人工反馈——无；监控告警——无；成本核算——无。功能面窄，定位纯埋点+导出。

**MLflow Tracing**：trace 视图——有（UI + IDE/notebook 内联展示、搜索/过滤 `search_traces`）；会话/线程——有（session/user 追踪与分组）；playground——有（LLM Playground，多轮对话、采样参数、tools、结构化输出，接 AI Gateway 与 Prompt Registry）；prompt 管理——有（Prompt Registry，版本+alias 生命周期、prompt 优化）；数据集——有（Evaluation Datasets，可从生产 trace 建集，要求 SQL 后端）；实验对比——有（Experiment 体系、回归测试）；评测——有（scorers/LLM judges、离线评测 + 生产流量自动在线评测，judge 支持采样率与过滤条件）；标注/人工反馈——有（UI/API `mlflow.log_feedback`，多标注人聚合）；监控告警——监控 dashboard 有（延迟、token 用量），原生告警未在所读文档中见到（未核实）；成本核算——有 token 用量与成本追踪（token-usage-cost 文档）。

#### 接入与实现方式
**OpenAI Agents SDK**：运行时自动埋点——`Runner.run()` 整体包在 `trace()` 中，agent 每次运行包 `agent_span()`，LLM 生成包 `generation_span()`，工具/guardrail/handoff 各有对应 `*_span()`。当前 trace/span 通过 Python `contextvar` 传播（天然并发安全）；`with trace(...)` 可把多次 run 聚成一条 trace；`custom_span()` 自定义。敏感数据可用 `RunConfig.trace_include_sensitive_data`（默认 True）和环境变量关闭。插件化架构：全局 `TraceProvider` → 默认 `BatchTraceProcessor`（后台队列，默认 max_queue_size=8192、max_batch_size=128、5 秒调度，进程退出前 flush，另提供 `flush_traces()` 供 Celery/后台任务强刷）→ `BackendSpanExporter` POST 到 `https://api.openai.com/v1/traces/ingest`。扩展点两个：`add_trace_processor()` 追加（多写）、`set_trace_processors()` 整体替换（不再发 OpenAI）。`TracingProcessor` 抽象类仅 6 个方法：`on_trace_start/on_trace_end/on_span_start/on_span_end/shutdown/force_flush`。与 OTel 无关（Langfuse 等厂商在自己的 processor 里做模型转换）。

**MLflow**：三种方式並用——(1) autolog：`mlflow.openai.autolog()` 等一行开启，覆盖 OpenAI Agents SDK、LangChain/LangGraph、LlamaIndex、DSPy、Anthropic、Bedrock、CrewAI 等大量库（含 TS 侧）；(2) 手动：`@mlflow.trace` 装饰器（支持 sync/async/generator，自动记录函数名、输入输出、异常事件，自动挂父子关系）、`mlflow.start_span` 上下文管理器、`mlflow.trace(fn)` 包装第三方函数；(3) 纯 OTel：任何语言（含 Go）用标准 OTLP 直接打到 MLflow Server 的 `/v1/traces` 端点。导出侧同样标准：设 `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` 即转发 Datadog/Grafana/Jaeger 等，支持 dual export（MLflow + OTel 后端双写）。

#### 自托管与后端架构
**OpenAI Agents SDK**：SDK 本身 MIT 开源，但默认后端（Traces dashboard）是 OpenAI 托管服务，无自托管选项；自托管需换 processor 接第三方（官方列表点名 MLflow self-hosted/OSS、Langfuse 等）。

**MLflow**：Apache-2.0，四组件——多语言 SDK（Python/TypeScript/Java/R）；Backend Store（经 SQLAlchemy 支持 postgresql/mysql/sqlite/mssql，存 experiment/trace 元数据）；Artifact Store（S3 等对象存储，存大文件与 trace attachments）；Tracking Server（FastAPI，REST API + UI，可 Docker Compose、K8s Helm、AWS/Azure/GCP 部署，支持 basic auth/SSO/RBAC/workspaces 多租户）。存储布局值得注意：`TraceInfo` 一行存 `trace_info` 表；3.3.0 起每个 span 以 JSON 单行存 `spans` 表（此前存 artifact store，改动原因是查询性能与事务一致性）；二进制附件（图/音频/PDF）仍走 artifact store，span 内只留 `mlflow-attachment://` 引用 URI。无独立队列组件，靠客户端 async logging（默认开启，10 worker 线程、1000 队列、满则丢弃、带重试退避）+ `MLFLOW_TRACE_SAMPLING_RATIO` trace 级采样；生产另有轻量 `mlflow-tracing` SDK 包（依赖极少，与完整包互斥）。

#### 人工介入与 HITL
**OpenAI Agents SDK tracing**：无任何 HITL 能力（SDK 的 HITL 属于 agent 运行层，不在 tracing 面）。**MLflow**：无执行断点/状态编辑（它是观测层不是运行时），但标注侧完整：UI/API 双通道 feedback（`mlflow.log_feedback`，带 source 类型 HUMAN/LLM_JUDGE、rationale、修订历史）；Expectation（人工标 ground truth）；3.14.0 起有实验性 **Review Queues**——把 trace 打包成队列、定义结构化问题（Pass/Fail、分类、打分、自由文本）、指派多个 reviewer、共享 pending/complete/declined 状态，答案回写到 trace 直接供评测使用。

#### 值得借鉴的点
1. **抄 TracingProcessor 的 6 方法接口**（on_trace_start/end、on_span_start/end、shutdown、force_flush）+ add/set 两个注册函数（追加 vs 替换语义）。这是被 25+ 厂商验证过的最小插件面，Go 里就是一个 interface + 注册函数，你的平台后端只需实现一个 processor 就能接入任何采用此模式的框架。
2. **类型化 span_data 而非扁平 attributes**：AgentSpanData/GenerationSpanData/HandoffSpanData 等按 agent 语义强类型建模，UI 可以针对每类 span 做专用渲染（handoff 画箭头、generation 显示消息列表）。Go 中用带 tag 的 struct 实现比 map[string]any 更利于消费端。
3. **默认 BatchTraceProcessor + 显式 flush_traces()**：后台队列（8192/128/5s）+ 退出钩子 + 供短命 worker 调用的强刷 API，这是把埋点开销降到可默认开启的关键工程细节；MLflow 侧对应的"队列满即丢弃 + 重试超时"策略也值得照搬——观测数据宁可丢不可阻塞业务。
4. **学 MLflow 的 TraceInfo/TraceData 分离存储**：元数据一行进 `trace_info` 表撑列表页与过滤，span JSON 单行进 `spans` 表，二进制附件进对象存储留引用 URI。MLflow 3.3.0 专门把 span 从对象存储迁回 DB，说明"列表查询走索引、详情走 DB、大块走对象存储"是踩过坑后的结论。
5. **OTLP ingest 端点（/v1/traces）+ GenAI SemConv 作为 Go 生态的接入正道**：MLflow 明确用它覆盖 Java/Go/Rust 等无原生 SDK 的语言。你的平台做一个 OTLP receiver + SemConv 映射，trpc-agent-go 只需内建 OTel exporter 即可对接自家平台和一切 OTel 后端。
6. **敏感数据开关做在 RunConfig/env 两级**（trace_include_sensitive_data 控制 LLM/工具输入输出是否入 span），以及 MLflow 的 trace 级采样 + 生产 judge 按采样率/过滤串自动评测——这三个"生产化旋钮"（脱敏、采样、在线评测）应在数据模型设计初期就预留。

### 2.12 promptfoo + DeepEval

**类别**：评测工具

#### 定位与设计理念
**promptfoo** 是一个 CLI + 库形态的 LLM 应用评测与红队测试工具（TypeScript/Node，MIT 协议）。README 声明其已并入 OpenAI（"Promptfoo is now part of OpenAI. Promptfoo remains open source and MIT licensed."），但仍保持开源。核心哲学：**声明式、本地优先**——用一份 YAML 描述 providers × prompts × tests 的笛卡尔积矩阵，"LLM evals run 100% locally"，prompt 不出本机。取舍：牺牲编程灵活性换取零代码上手和可 diff 的配置。

**DeepEval**（confident-ai 出品，Python，Apache 2.0）自我定位为"LLM 应用的 Pytest"：把 LLM 评测塞进开发者熟悉的单元测试工作流，LLM-as-a-judge 与 NLP 模型均可本地运行。取舍与 promptfoo 相反：**代码优先**，指标是可实例化、可继承的 Python 类，胜在可编程性；配套商业云平台 Confident AI 承担数据集管理、tracing 和生产监控。

一句话：promptfoo 是"YAML 驱动的评测矩阵引擎"，DeepEval 是"pytest 风格的指标断言库"。

#### 数据模型
两者都是测试框架而非 tracing 平台，核心实体是"用例—运行—断言结果"而非 trace/span：

- **promptfoo**：顶层 `promptfooconfig.yaml` 包含 `prompts`（支持 `file://` 引用与 Nunjucks 模板）、`providers`（如 `openai:gpt-5-mini`，可带 temperature 等参数）、`tests`（每条含 `vars` 变量和 `assert` 断言数组）、`defaultTest`（全体用例共享的 vars/assert）。一次 eval 生成 **providers × prompts × tests 的笛卡尔积**（2 prompts × 2 providers × 2 tests = 8 个结果格子）；vars 为数组时进一步展开组合。断言对象字段：`type`、`value`、`threshold`、`weight`（默认 1.0）、`metric`（命名指标标签，用于聚合报表）。用例总分 = 各断言分的加权平均，测试级 `threshold` 决定 pass/fail。结果持久化在 SQLite（`promptfoo.db`）中，按 eval run 组织。
- **DeepEval**：原子单位是 `LLMTestCase`（字段：`input`、`actual_output`、`expected_output`、`retrieval_context`），多轮对话有 ConversationalTestCase；数据集侧实体是 **Golden**（单轮 = input + 期望输出；conversational golden 定义场景与期望结局），聚合为 EvaluationDataset。每个 Metric 输出 **0–1 的 score + reason 文本**，`threshold`（默认 0.5）决定 `is_successful()`；trace/span 级实体依赖 Confident AI 平台（组件级评测通过 tracing 挂 golden + metrics）。

#### 核心功能清单
- **trace 视图**：promptfoo 无独立 trace 视图，但断言体系含 `trace-span-count`、`trajectory:tool-used` 等 tracing/轨迹断言（细节未核实）；DeepEval 开源库本身无，trace 观测在 Confident AI 云端。
- **会话/线程**：promptfoo 无；DeepEval 有多轮对话测试用例与 Conversation Simulator（由 conversational golden 生成真实多轮交互）。
- **playground**：promptfoo web viewer 提供 "Edit and re-run"（在 eval creator 中改配置重跑）；DeepEval 无。
- **prompt 管理**：promptfoo 以文件/YAML 管理 prompt（版本随 git）；DeepEval 开源库无（云端有）。
- **数据集**：promptfoo 的 tests 可从 CSV/Google Sheets/脚本加载；DeepEval 有一等公民的 EvaluationDataset + **Synthesizer 合成器**（从文档 > 已有 goldens > 从零，三种来源按可靠性排序）。
- **实验对比**：promptfoo web viewer 有 Eval selector 和 **Compare 功能对两次 eval 做 diff**（绿=新增、红=移除）；DeepEval 的跨 run 趋势对比在 Confident AI。
- **评测**：两者核心。promptfoo 约 40 种断言类型；DeepEval 有 G-Eval、DAG、RAG 四件套（Faithfulness/Answer Relevancy/Contextual Precision/Recall）、Agent 指标（Task Completion、Tool Correctness、Step Efficiency、Plan Adherence）、安全指标（Bias/Toxicity/PIILeakage）等。
- **标注/人工反馈**：两者开源部分均无标注队列；Synthesizer 文档仅建议人工"review, edit, enrich"合成数据。
- **监控告警**：promptfoo 开源版无；DeepEval 无（生产监控归 Confident AI）。
- **成本核算**：promptfoo 有 `cost` 和 `latency` 断言类型（可直接对成本/延迟设门禁）；DeepEval 开源库无成本核算。
- **红队**：promptfoo 有完整 red team 子系统（`promptfoo redteam run` 漏洞扫描）；DeepEval 的安全测试以指标形式提供（红队能力在其姊妹项目，未核实）。

#### 接入与实现方式
- **promptfoo**：不侵入被测应用——它是**外部驱动器**，按矩阵主动调用 provider（HTTP API、自定义 JS/Python provider、可执行文件均可作为 provider，因此 Go 服务可用 HTTP provider 接入）。npx/npm/brew/pip 安装，Node ^20.20.0 或 >=22.22.0。断言中 `javascript`/`python`/`webhook` 类型允许任意自定义校验逻辑。与 OTel 的关系：存在 trace 相关断言，说明可消费 tracing 数据，机制未核实。
- **DeepEval**：Python 3.9+，pip 安装。接入即写 pytest 测试文件：`assert_test(test_case, metrics)` 或 `assert_test(golden, metrics)`（后者走 tracing 路径，配合 `@observe` 类组件埋点做 component-level eval）。判官模型可换：OpenAI/Azure/Ollama/Gemini 或继承 `DeepEvalBaseLLM` 自定义。与 OTel 无直接绑定；有 LangChain/CrewAI/OpenAI 等框架集成。

#### 自托管与后端架构
- **promptfoo**：架构极简——单个 **Express server 同时提供 Web UI 和 API**，存储用 **SQLite**（默认 `/home/promptfoo/.promptfoo/promptfoo.db`）+ 文件系统存大媒体（图片等外置为引用而非 base64）。无队列、无独立数据库服务。部署：ghcr.io/promptfoo/promptfoo:latest 镜像 + volume、docker-compose、实验性 Helm chart（PVC 持久化）。CLI 通过 `promptfoo share` 把本地 eval 结果上传到自托管实例。缓存层：磁盘缓存 `~/.promptfoo/cache`（cache-manager + keyv），只缓存成功响应，默认 TTL 14 天，`--no-cache`/`PROMPTFOO_CACHE_ENABLED=false` 可关。MIT。
- **DeepEval**：开源部分是**纯 Python 库，无服务端可自托管**；结果查看、趋势、tracing 依赖 Confident AI SaaS（`CONFIDENT_API_KEY` 可选，不配则完全本地跑）。Apache 2.0。

#### 人工介入与 HITL
两者开源版均**无断点/状态编辑/审批流**。promptfoo web viewer 的 "Edit and re-run" 只是配置级人工迭代；DeepEval 的 HITL 仅体现为"合成 goldens 需人工审核后入库"的流程建议，标注队列在 Confident AI 云端。对 HITL 需求，这两家都不是参照对象。

#### 值得借鉴的点
1. **断言即数据的类型体系**：promptfoo 用统一的 `{type, value, threshold, weight, metric}` 五字段建模所有断言，确定性断言（equals/contains/regex/is-json/javascript…）与模型评分断言（llm-rubric/similar/factuality/g-eval…）共用同一 schema，且任何类型前缀 `not-` 即取反。Go 平台可直接照抄：一个 Assertion 接口 + 注册表，YAML 可声明、加权平均聚合、`assert-set` 支持分组部分通过。这让用例可序列化、可 diff、可复用（`assertionTemplates` + `$ref`）。
2. **笛卡尔积用例矩阵 + defaultTest**：providers × prompts × tests 的自动展开，加上 defaultTest 做公共断言/变量下沉，用极少配置生成大量对照组——对 Agent 平台就是 models × agent 配置 × 用例集的矩阵，天然支持模型/prompt A/B。
3. **成功响应磁盘缓存作为 CI 加速器**：以 provider+请求内容哈希为 key、只缓存成功、TTL 14 天、CI 里用 `PROMPTFOO_CACHE_PATH` 跨 run 复用。LLM 评测在 CI 的最大痛点是又贵又慢，这个设计成本极低、收益极大。
4. **双门禁模式**：promptfoo 走"退出码 + `--fail-on-error` + JSON/JUnit XML 输出供解析自定义阈值"；DeepEval 走"`deepeval test run` 包装 pytest，指标 threshold 不达即断言失败挂掉构建"（支持 `-n` 并行、`-c` 缓存、重试）。Go 平台建议两条都留：`go test` 原生集成（对应 DeepEval 模式）+ CLI 退出码/JUnit 输出（对应 promptfoo 模式）。
5. **G-Eval 的可复现性设计**：`criteria`（探索期让 LLM 自动 CoT 生成评估步骤）与 `evaluation_steps`（固化后显式指定、跳过重新生成保证跨 run 可复现）二选一，加 `rubric` 把 1–10 分限定到不重叠分数带防止"中间分聚集"，最后用 token 概率加权归一化到 0–1。做 LLM 判官指标时这三层（criteria→steps→rubric）是目前最成熟的可复现方案。
6. **回归对比放在 viewer 而非引擎**：promptfoo 引擎只管产出结构化 eval run（SQLite），基线回归靠 web viewer 的 Compare 两次 run 做 diff（绿增红减）+ `--tag key=value` 打 git SHA/CI run ID 关联流水线上下文。把"基线"实现为"任选两次 run 的 diff + 标签检索"比维护专门的 baseline 状态机简单得多，值得作为 V1 方案。

> **⚠️ 核验修正**（独立核查代理逐条反驳后订正）：
>
> - **订正**：原稿关键事实「10. G-Eval criteria 与 evaluation_steps 二选一（criteria 经 CoT 生成 steps，显式 steps 可复现）；先生成 1–10 分再用 token 概率加权归一化为 0–1；rubric 分数区间不重叠。deepeval test run 包装 pytest，-n 并行、-c 缓存，指标不达 threshold 即挂掉构建；无 CONFIDENT_API_KEY 则完全本地运行」经核查有误。两处问题。(a) 分数范围错误：所引页面明确是让 LLM "generate a score between 1–5, where 5 is better than 1"，再用输出 token 概率加权求和归一化到 0–1——是 1–5 而非 1–10（1–10 可能与 rubric 的 score_range "0 - 10, inclusive" 混淆了；rubric 区间不重叠、criteria/evaluation_steps 二选一、CoT 自动生成、显式 steps 提升可复现性均获该页支持）。来源：https://raw.githubusercontent.com/confident-ai/deepeval/main/docs/content/docs/%28custom%29/metrics-llm-evals.mdx 。(b) 来源张冠李戴：deepeval test run/-n/-c/CONFIDENT_API_KEY 等内容完全不在所引页面；这些事实本身成立，但正确来源是 docs/content/docs/command-line-interface.mdx（"run evaluation test files through pytest with the deepeval pytest plugin enabled"）、docs/content/docs/evaluation-flags-and-configs.mdx（-n 并行、-c 读本地缓存）及 README（本地运行、Confident AI 登录可选）。

### 2.13 Datadog LLM Observability + New Relic AI Monitoring

**类别**：APM（企业级应用性能监控厂商的 LLM/Agent 观测产品）

#### 定位与设计理念
两者都是「把 LLM/Agent 观测长在既有 APM 体系上」的代表。Datadog LLM Observability（文档中已逐步更名为 Agent Observability）定位为"监控、排障、评估 LLM 应用"的一站式产品：以 trace 为单位记录每次请求，覆盖单次推理、静态 workflow、动态 agent 三个复杂度层级，并叠加成本/延迟运营监控、质量评估（Patterns 主题聚类）、安全扫描（敏感数据脱敏、prompt injection 检测）、异常检测（Insights）。核心取舍：不做独立系统，复用 ddtrace APM tracer 和 Datadog 平台（告警、Dashboard、Sensitive Data Scanner），一句话哲学是"LLM 观测只是 APM 的一种新 span 语义"。New Relic AI Monitoring 更极致：它直接是"APM for AI"——不发新 SDK，而是给现有各语言 APM agent 加开关，捕获 LLM 事件并与分布式追踪关联；关闭 distributed tracing 或开启 high security mode 时 AI 数据即停止采集，说明它完全寄生于 APM 管道。

#### 数据模型
Datadog：三个核心实体——Span（一个操作单元，含 name、时间戳、duration、error、input/output、metadata、metrics、tags）、Trace（一次请求，由嵌套 span 组成，root span 界定起止）、Evaluation（质量检查结果，挂在 span/trace/session 上）。七种 span kind：`llm`（模型调用）、`workflow`（静态编排）、`agent`（LLM 动态决定执行路径）、`tool`、`task`（无外部调用的内部步骤）、`embedding`、`retrieval`（向量库检索）；层级规则明确：只有 llm/workflow/agent 可作 root，tool/task/embedding/retrieval 只能是子 span。会话用 `session_id` 参数串联（大多数装饰器都接受）。Evaluation 有 label + metric_type（categorical/score/boolean/json）+ value，可按 span_id 精确关联，也可用 `span_with_tag_value` 按 tag 关联。成本经 metrics（input_tokens/output_tokens）与 `cost_tags` 传播。New Relic：以事件（NRDB event）为模型，如 LlmEmbedding、LlmChatCompletionMessage（有 `set_llm_token_count_callback` API 为其补 token_count），Agent 监控中 agent 与 tool 被建模为实体地图（entity map）里的一等实体节点，工具调用与 agent 间移交（handoff）是有向边。

#### 核心功能清单
Datadog：trace 视图——有（端到端瀑布 + agent 执行可视化：工具选择、任务移交给哪个 agent）；会话/线程——有（session_id，反馈可挂到 session）；playground——本次打开的文档未见，未核实；prompt 管理——未见专门功能，无；数据集——有（可版本化的 Datasets）；实验对比——有（Experiments，跨 accuracy/correctness/duration/estimated cost 等约 9 个字段对比，gov 站点不可用）；评测——有（Managed 评估目前仅 Language Mismatch + 敏感数据扫描；幻觉/Failure to Answer/Sentiment/Toxicity/Prompt Injection/Topic Relevancy/Tool Selection/Tool Argument Correctness/Goal Completeness 共 9 个是 LLM-as-judge 模板，由用户接入的 OpenAI/Azure OpenAI/Anthropic/Bedrock/Vertex AI 运行；另支持外部评估经 API 提交、Export API 导出 span 离线评估）；标注/人工反馈——部分有（End-User Feedback API 收集评分/评论，无标注队列）；监控告警——有（复用 Datadog 监控体系）；成本核算——有（token 指标、cost_tags、实验的 estimated cost）。New Relic：trace 视图——有（AI Responses 页 + trace waterfall，agent/tool span 内嵌）；会话——文档未见独立会话实体，未核实；playground/prompt 管理/数据集/实验——无；评测——无内置 LLM-as-judge（有模型对比 Model Comparison）；人工反馈——有（用户正/负反馈关联 API）；监控告警——有（复用 NR 告警）；成本核算——有（token 用量到 agent 粒度、跨模型成本对比）。

#### 接入与实现方式
Datadog 三条路：1) LLMObs SDK（Python 3.7+/Node.js 16+/Java 8+），Python 侧提供 `@llm(model_name, model_provider)`、`@workflow`、`@agent`、`@tool`、`@task`、`@embedding`、`@retrieval` 装饰器（均可带 name/session_id/ml_app），`LLMObs.annotate()` 补 input/output/metadata/metrics/tags；开启方式 `ddtrace-run` + `DD_LLMOBS_ENABLED=1`/`DD_LLMOBS_ML_APP`，或 `LLMObs.enable()`；支持 agentless 模式（`DD_LLMOBS_AGENTLESS_ENABLED` + API key 直发，无需本机 Datadog Agent）。SDK 底层就是 ddtrace APM tracer，对 OpenAI/LangChain/Bedrock/Anthropic 等有零代码 auto-instrumentation。2) HTTP intake API：Spans API 与 Evaluations API 直接 POST，供无 SDK 语言（如 Go）使用，可提交完整 trace 层级。3) OTel：原生支持 GenAI 语义约定 v1.37+，OTLP 直发（header 里 `dd-api-key` + `dd-otlp-source=llmobs`），旧框架需 `OTEL_SEMCONV_STABILITY_OPT_IN=gen_ai_latest_experimental`；按 `gen_ai.operation.name` 映射 span kind（chat/completion→llm，embeddings→embedding，execute_tool→tool，invoke_agent/create_agent→agent，其余→workflow），`gen_ai.usage.input_tokens` 等自动映射。New Relic：无独立 SDK，就是各语言 APM agent 里加 `ai_monitoring` 能力：Go 3.31.0+（go-openai 1.19.4+、AWS SDK v2）、Java 8.12.0+、.NET 10.23.0+、Node 11.13.0+、Python 9.8.0+（OpenAI/Boto3/LangChain 0.1.0+/LangGraph 1.0.7+/Google GenAI/FastMCP）、Ruby 9.8.0+；agent 框架层对 LangGraph、Strands、AutoGen 自动埋点；另有 OpenLIT（OTel）接入路线。

#### 自托管与后端架构
两家均为纯 SaaS，闭源商业产品，无自托管后端。Datadog 数据路径：SDK →（本机 Datadog Agent 或 agentless 直发）→ 指定 DD_SITE；部分功能（Experiments）在政府云站点不可用；后端存储/队列选型不公开，未核实。New Relic 数据经 APM agent 上报云端（事件存储即其 NRDB，本次打开页面未直接确认，未核实）；提供 drop filters 在数据出境前丢弃敏感字段。对自托管有硬需求的团队，这两家只能作为"设计参考"而非"直接复用"。

#### 人工介入与 HITL
两家都没有执行期 HITL：无断点、无状态编辑、无审批流。Datadog 有事后人工通道：End-User Feedback（评分/评论挂 span/trace/session）+ 外部评估 API + Export API 导出人工复核；配置评估需 "Agent Observability Write" 权限（有权限分层）。New Relic 仅有用户反馈（正/负）关联 API。均无标注队列。

#### 值得借鉴的点
1. 抄 span kind 枚举与 root 约束：`llm/workflow/agent/tool/task/embedding/retrieval` 七分类 + "只有 llm/workflow/agent 可为 root"的校验规则，是目前业界最清晰的 agent trace 语义模型，Go 平台可直接在 trace schema 层实现同样的 kind 字段和层级校验，UI 按 kind 着色/过滤。
2. 对齐 OTel GenAI semconv v1.37+ 并做双向映射：Datadog 用 `gen_ai.operation.name` → span kind 的映射表证明"自有模型 + OTel 兼容层"可共存；trpc-agent-go 已有 OTel 输出，做一张同样的映射表就能同时兼容自有 UI 和第三方后端。
3. 评估结果与 span 的松耦合关联：`submit_evaluation(label, metric_type∈{categorical,score,boolean,json}, value, span 或 span_with_tag_value)`——支持"按 tag 关联"意味着离线评估作业不必在执行期持有 span_id，这对异步/批量评测管道极其实用。
4. 把"托管评估"拆成模板：Datadog 把幻觉/毒性等做成 9 个 LLM-as-judge 模板 + 用户自带 judge 模型的 provider 连接，平台方不养推理集群、不碰用户密钥边界，架构上很轻，适合小团队照抄。
5. agentless 双模上报：同一 SDK 支持"经本机 Agent"与"直发云端"两种链路（一个环境变量切换），Go 平台可对应设计"经 sidecar/collector"与"SDK 直连"双模，降低接入门槛。
6. New Relic 的启示：把 agent/tool 建成实体地图中的一等实体（节点=agent/模型/工具，边=调用与移交），并让 AI 数据严格复用分布式追踪管道（tracing 关了 AI 数据就没了）——对已有微服务追踪的企业，这种"零新增基础设施"叙事是最强卖点，也是 Go 生态平台该讲的故事。

> **⚠️ 核验修正**（独立核查代理逐条反驳后订正）：
>
> - **订正**：原稿关键事实「两家均为闭源纯 SaaS 无自托管后端；Datadog 提供 End-User Feedback 与外部评估 API 但无标注队列/断点等执行期 HITL；New Relic 提供 drop filters 在上报前丢弃敏感数据」经核查有误。『无标注队列』一说错误：Datadog LLM Observability 现已提供 Annotation Queues——'Annotation Queues provide a structured workflow for (systematic) human review of LLM traces'，支持结构化标签、自由备注、经 Automation Rules 自动路由，可用于构建基准数据集（https://docs.datadoghq.com/llm_observability/evaluations/annotation_queues/，evaluations 首页与侧边导航均列出）。正确说法应为：Datadog 有事后人工标注队列，但无执行期断点/干预类 HITL。另一处措辞不精确：New Relic drop filters 是在数据到达 New Relic ingest pipeline 后、写入 NRDB 前丢弃（'evaluate data forwarded by the agent within the data ingest pipeline'），并非在客户端上报前丢弃（https://docs.newrelic.com/docs/ai-monitoring/drop-sensitive-data/）。闭源纯 SaaS、End-User Feedback、External Evaluations API、drop filters 存在性本身均属实。

## 3. 横向能力矩阵

> ✅ 完整支持 · ◐ 部分/受限（说明见括注或工具档案）· — 不支持 · **P0/P1/P2** = 本平台路线图阶段（见 §5）。列按"全生命周期平台 → 网关 → 调试器 → 测试框架 → APM"排列；AgentOps/Laminar/Lunary、PromptLayer、New Relic 等见 §2 档案，未入矩阵。

| 能力 | LangSmith | Langfuse | Phoenix | Weave | Braintrust | Opik | Helicone | LangGraph Studio | promptfoo | Datadog LLMObs | **本平台 testplatform** |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| 开源 | —（闭源商业） | ✅ MIT 核心（部分 EE） | ✅ ELv2 | ◐ SDK Apache-2.0，服务端闭源 | ◐ autoevals/proxy MIT，平台闭源 | ✅ Apache-2.0 | ✅ | ◐ dev server 开源，UI 免费闭源 | ✅ MIT | — | ✅（本仓库内） |
| 自托管 | ◐ 企业版付费 | ✅ | ✅ 单容器起步 | ◐ 企业版 | ◐ hybrid（数据面进客户 VPC） | ✅ | ✅ | ✅ 本地 dev | ✅ CLI 本地 | — | ✅ 单二进制 |
| Trace 树/链路视图 | ✅ Run 树 | ✅ observations | ✅ OTel 原生 | ✅ calls | ✅ | ✅ | ◐ 请求级+Session 层级 | ✅ 图视角 | — | ✅ 七种 span kind | ✅ agent/model/tool |
| 会话/线程维度 | ✅ Threads | ✅ Sessions | ✅ Sessions | ◐ Threads | ◐ | ✅ Threads | ✅ Sessions | ✅ Threads | — | ✅ | ✅ |
| **上下文拼接来源归因** | ◐ 仅展示最终 prompt | ◐ | ◐ | ◐ | ◐ | ◐ | ◐ | ◐ 状态可见 | — | ◐ | ✅ **独有**（逐条消息溯源+拼接条件） |
| Playground | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ◐ | ✅（即运行图） | ◐ viewer | — | ✅ 调试台 |
| Prompt 管理/版本化 | ✅ Hub+commit | ✅ labels+缓存 | ✅ | ◐ objects 版本化 | ✅ functions | ✅ | ✅ | — | ◐ YAML 即版本 | ◐ | ◐ instruction 热更 → **P1 版本化** |
| 数据集 | ✅ | ✅ | ✅ | ✅ 版本化 | ✅ | ✅ | ◐ | — | ✅ YAML 矩阵 | ◐ | ✅ 测试用例 |
| 实验对比/回归 | ✅ experiments | ✅ | ✅ | ✅ | ✅ **核心定位** | ✅ | ◐ | — | ✅ 矩阵+CI | ◐ | ◐ 批量运行 → **P0 对比视图** |
| 代码断言/规则评测 | ✅ | ✅ | ✅ | ✅ scorers | ✅ | ✅ | ◐ | — | ✅ **断言体系最全** | ✅ | ✅ 8 种断言 |
| LLM-as-judge | ✅ 在线+离线 | ✅ | ✅ evals 库 | ✅ | ✅ autoevals | ✅ G-Eval | ◐ | — | ✅ rubric | ✅ 在线质量检查 | — → **P1** |
| 标注队列/人工评审 | ✅ 最完整（预留锁/多审/pairwise） | ✅ | ◐ | ◐ | ✅ | ✅ | — | — | — | ✅ | — → **P1 简版** |
| 终端用户反馈 API | ✅ | ✅ | ◐ | ✅ | ✅ | ✅ | ✅ scores | — | — | ✅ | — → **P1** |
| 监控告警 | ✅ 阈值+多渠道 | ◐ | — | ◐ monitors | ◐ | ◐ | ✅ | — | ◐ CI 即门禁 | ✅ **APM 级** | — → **P2** |
| 成本核算 | ✅ 自动+自定义价格表 | ✅ | ◐ | ✅ | ✅ | ✅ | ✅ **最强（网关直采）** | — | ◐ | ✅ | — → **P0** |
| OTel/OTLP 兼容 | ✅ ingest+gen_ai.* | ✅ v3 SDK 即 OTel | ✅ **原生** | ◐ | ✅ OTLP | ✅ | ◐ | — | — | ✅ | — → **P1**（框架已内建 OTel） |
| **执行期断点/介入** | —（事后标注） | — | — | ◐ guardrails 拦截 | — | ◐ guardrails | ◐ 网关限流/缓存 | ✅ interrupt+状态编辑+**time-travel fork** | — | — | ✅ **模型/工具断点+上下文编辑+响应注入** |
| 埋点形态 | 装饰器/包装器/OTLP | 装饰器/集成/OTLP | OTel instrumentor | 装饰器 | Eval 框架+proxy | 装饰器/集成 | **网关代理** | 框架内建 | 配置驱动 | SDK+auto-instr | **框架回调** |

**三个结论**：
1. 全生命周期平台（前 6 列）功能集合高度趋同——trace/playground/prompt/数据集/评测/标注六件套是"及格线"，差异在深度（LangSmith 的标注队列、Braintrust 的实验、Helicone 的成本）与商业模式（开源程度）；
2. **执行期介入是断层**：只有 LangGraph Studio 一类"开发期调试器"真正做了断点与 time-travel，观测平台全部缺席——本平台的断点介入+上下文编辑处在竞争最稀疏的格子；
3. 本平台的补强顺序（P0 成本/实验对比 → P1 LLM 评审/标注/OTLP → P2 告警）正好沿"及格线"补齐，同时守住"上下文归因 + 执行期介入"两个差异化格子。

## 4. 通用设计模式提炼

> 本节把 13 组工具中反复出现的设计收敛为可直接落地的模式。每个模式给出：它是什么、谁做得最好、实现要点、以及对本平台（testplatform）的意义。

### 模式 1：统一追踪数据模型 —— trace → span 层级 + 类型枚举

**是什么**：所有平台的地基都是同一个模型：一次端到端请求为 trace（根），内部每个操作为有类型的 span/observation，父子嵌套成树。span 类型决定 UI 渲染方式与聚合维度。

**代表**：LangSmith 的 Run tree（run_type: llm/chain/tool/retriever/prompt/parser）、Langfuse 的 trace→observations（span/generation/event 三型）、Datadog 的七种 span kind（llm/workflow/agent/tool/task/embedding/retrieval）、OTel GenAI semconv。

**实现要点**：
- generation（LLM 调用）是特化 span：必须存**全量请求消息**（而非增量）、模型名、生成参数、usage、完成原因——这是 playground 复现、成本核算、上下文审计的前提；
- 类型枚举尽量对齐 OTel GenAI / Datadog 的集合，未来互操作成本低；
- event（时间点事件，无时长）与 span（有时长）分开建模。

**对本平台**：`trace.go` 的 Span.Kind 目前只有 agent/model/tool 三型，建议扩展 retrieval/embedding/workflow（多 Agent 编排节点），并让 `Detail` 中 generation 字段结构化（现在是 map）。

### 模式 2：session / user 业务维度上卷

**是什么**：trace 之上再挂两级业务维度：session（多轮对话线程）聚合多个 trace，user 聚合多个 session。成本、token、评分、错误率都能按这三级上卷。

**代表**：Langfuse sessions/users、Helicone 的 Session 层级（通过请求 header 声明 path 式层级）、LangSmith threads。

**对本平台**：已有 session 维度（会话列表/事件视图），缺 user 维度上卷和 session 级统计卡（本会话累计 token/成本/轮数）。

### 模式 3：接入方式光谱 —— 五种埋点形态的取舍

| 形态 | 代表 | 优点 | 代价 |
| --- | --- | --- | --- |
| 装饰器/包装函数 | LangSmith `@traceable`、Weave `@weave.op`、Opik `@track` | 粒度精确、类型友好 | 侵入业务代码 |
| 框架回调/处理器 | LangChain callbacks、**trpc-agent-go callbacks（本平台）**、OpenAI Agents SDK processors | 一次接入全覆盖、业务零侵入 | 绑定框架 |
| 网关/代理 | Helicone、Braintrust proxy、Portkey | 换 baseURL 即接入、跨语言 | 只见 HTTP 层，看不到工具执行与框架内部 |
| OTel 自动埋点 | OpenLLMetry、OpenInference instrumentors | 标准化、生态复用 | 语义粒度受库支持限制 |
| SDK 内建 tracing | OpenAI Agents SDK、MLflow autolog | 零配置 | 深度绑定该 SDK |

**对本平台**：当前是"框架回调"形态（正确选择）。值得补：暴露 OTLP ingest（模式 4），让非 trpc-agent-go 的服务也能把 trace 打进来。

### 模式 4：OTLP 作为通用 ingest，向 GenAI semconv 对齐

**是什么**：行业收敛趋势——自家 SDK 底层直接用 OTel 实现（Langfuse v3 SDK、Phoenix 原生、Laminar），平台同时接受任意 OTLP 数据；语义层对齐 `gen_ai.*` 属性约定。

**好处**：白嫖整个 instrumentation 生态；用户可双写多后端；换平台不换埋点。

**对本平台**：trpc-agent-go 框架本身已内建 OpenTelemetry（`telemetry/` 包）。P1 建议：collector 同时消费框架 OTel span（而不仅是回调），并提供 `/v1/traces` OTLP HTTP 端点 + gen_ai.* 属性映射，一举两得。

### 模式 5：存储演进路径 —— 从内存到列存

**是什么**：几乎所有自托管平台走同一条路：dev 工具用嵌入式存储（Phoenix 默认 SQLite），起步用 Postgres，规模化后把 trace 明细迁到 ClickHouse（列存+高压缩，聚合查询快百倍），大 payload（截图/长上下文）进 S3/blob，摄取走 Redis/队列异步批量写。

**代表**：Langfuse v3（Postgres 元数据 + ClickHouse 明细 + Redis 队列 + S3 blob + worker 进程）、LangSmith 自托管（Postgres + ClickHouse + Redis）、Opik（MySQL + ClickHouse）、Braintrust（自研 Brainstore）。

**对本平台**：当前内存 ring（重启即失）。P0 先落 SQLite（单文件、零依赖，Go 用 modernc.org/sqlite 免 CGO）；接口抽成 `TraceStore` interface，将来平滑换 Postgres/ClickHouse。**不要直接上 ClickHouse**——那是日均百万 trace 的问题。

### 模式 6：异步摄取管道

**是什么**：SDK 端批量缓冲 + 后台线程发送（不阻塞业务请求）；服务端 API 先落队列、worker 异步入库；写入幂等（event id 去重）。

**对本平台**：单机场景可以简化，但落库改为异步批量（channel + 定时 flush）值得现在就做，避免 UI 查询与写入互相拖累。

### 模式 7：Prompt 管理闭环

**是什么**：prompt registry（版本化存储）+ 部署标签（production/staging 指针）+ playground 编辑调试 + 从线上 trace 一键"在 playground 中打开"复现。改 prompt 不用改代码、不用重新部署。

**代表**：Langfuse Prompt Management（版本 + labels + 客户端缓存）、LangSmith Prompt Hub + Playground、PromptLayer（把 prompt 当 CMS 管理，含审批流）、Braintrust prompts-as-functions。

**对本平台**：设置页的 Instruction 热更新是雏形。P1 建议：instruction/prompt 版本表（谁、何时、改了什么、diff），运行记录关联 prompt 版本——"这次跑得差是因为哪次 prompt 改动"是最高频的排查需求。

### 模式 8：数据集-实验闭环（回归测试的工业形态）

**是什么**：生产 trace 一键入数据集 → 数据集跑实验（新 prompt/新模型/新代码）→ 与 baseline 逐条 diff + 汇总指标对比 → CI 门禁（分数低于基线则 fail）。核心实体：dataset / dataset_item（input+expected）/ experiment / experiment_item（output+scores）。

**代表**：Braintrust（Eval 三要素 data/task/scores，整个产品围绕此构建）、LangSmith experiments、promptfoo（YAML 声明 providers×prompts×tests 矩阵）、Langfuse datasets。

**对本平台**：测试用例模块已是雏形（用例=dataset_item，批量运行=experiment）。缺的是**实验对比视图**（两次批量运行逐用例 diff）和**从 trace 一键生成用例**。这两个是 P0 价值最高的功能。

### 模式 9：评测三层次 —— 代码断言 / LLM 评审 / 人工标注

**是什么**：成熟平台都是三层并存，统一落到 score 实体（可挂 trace/span/session，含 name/value/source/comment）：
1. **代码断言**：contains/regex/JSON schema/工具调用检查——快、确定、免费（promptfoo 的断言类型体系最完整，值得整体参照）；
2. **LLM-as-judge**：离线批量（实验评分）+ 在线抽样（生产质量监控，如幻觉/相关性/毒性）；prompt 模板 + 结构化输出分数（Langfuse、LangSmith、Datadog quality checks）；
3. **人工**：标注队列（把 trace 分配给人打分，LangSmith annotation queues）+ 终端用户反馈 API（👍👎，Weave feedback）。

**对本平台**：第 1 层已有 8 种断言。P1 加 `llm_judge` 断言类型（用被测模型或独立评审模型 + 评分 rubric）；score 独立建模而不是只有 pass/fail。

### 模式 10：HITL —— 断点、状态编辑、时间旅行

**是什么**：开发期调试的最高形态：任意步骤挂起（interrupt/breakpoint）→ 检查并**编辑状态**→ 放行；以及 time-travel——从历史执行的任意 checkpoint fork 出新分支重放。

**代表**：LangGraph interrupt + Studio（图可视化、thread state 编辑、从任意节点 fork）、ADK web（事件/状态检查）。生产观测平台（Langfuse 等）反而普遍**没有**这能力——这是 dev-tool 与生产观测的分界线。

**对本平台**：断点介入（改上下文/改参数/注入响应）已达到 Studio 同级的"当前点介入"。差 time-travel：需要把每次模型调用前的完整上下文当 checkpoint 存（已经存了！`Detail["messages"]`），加"从此步重放"按钮——用存档上下文直接重发模型即可，这是低成本高杀伤力的 P1 功能。

### 模式 11：成本核算

**是什么**：usage（prompt/completion/cache tokens）× 价格表（model→单价，含缓存读写差价）→ 每 generation 算成本 → 按 trace/session/user/feature 上卷 → 仪表盘与告警。

**代表**：Helicone/Langfuse/LangSmith 全都内建；OpenRouter 式价格表往往直接开源可抄。

**对本平台**：usage 已采集。P0 加价格表（JSON 可配）+ run/session 级成本展示。

### 模式 12：内容捕获开关与隐私

**是什么**：prompt/completion 属于敏感数据。标准做法：内容捕获总开关（OTel GenAI 约定 content 默认**不**采集）、脱敏 hook（正则/自定义函数处理 PII）、超长 payload 截断并转存 blob。

**对本平台**：内网测试平台可默认全采，但接生产流量前需要此开关；架构上在 collector 入口留一个 `Sanitizer func(msg) msg` 钩子即可。

### 模式 13：在线监控与告警

**是什么**：错误率、p95 延迟、成本突增、评测分数下滑 → 阈值告警 → 通知渠道（邮件/webhook/Slack）。生产观测平台标配，纯 dev 工具（Phoenix 单机、Studio）可无。

**对本平台**：P2。测试平台阶段用"批量回归失败即 CI 报警"替代即可。

---

## 5. 对 testplatform 的落地路线图

> 原则：先补"数据资产化"（持久化、成本、实验对比），再补"评测深度"（LLM 评审、标注），最后补"标准对齐"（OTLP）。每项标注参考对象与落地位置。

### P0（当前迭代就值得做）

| 事项 | 参考对象 | 落地位置与要点 |
| --- | --- | --- |
| trace 持久化（SQLite） | Phoenix 的嵌入式起步路线 | `internal/platform/trace.go` 抽 `TraceStore` 接口，加 sqlite 实现（modernc.org/sqlite 免 CGO）；异步批量写 |
| 成本核算 | Langfuse/Helicone 价格表 | 价格表 JSON（model→输入/输出/缓存单价），AfterModel 里算 cost，Run/Session 上卷，UI 加成本列 |
| 实验对比视图 | Braintrust experiments、promptfoo viewer | 两次批量测试（TestRun）逐用例并排 diff：输出、断言、耗时、token；汇总胜负表 |
| 从 trace 一键生成用例 | LangSmith "add to dataset" | 链路页加按钮：run 的 input+输出断言草稿 → 预填用例编辑器 |
| session 级统计 | Langfuse sessions | 会话列表加累计 token/成本/轮数/平均延迟 |

### P1（下个阶段）

| 事项 | 参考对象 | 落地位置与要点 |
| --- | --- | --- |
| LLM-as-judge 断言 | Langfuse evals、promptfoo llm-rubric | 断言类型加 `llm_judge{rubric, judge_model}`；评审模型可独立配置；分数落 score 实体 |
| score 实体化 | Langfuse scores | 独立 score 表（name/value/source: code|llm|human, 挂 run/span），断言结果与人工标注统一进这里 |
| 单步重放（time-travel） | LangGraph Studio fork | 模型 span 已存完整上下文；加"从此步重放"：用（可编辑后的）存档上下文直接调当前模型，生成新 run 并与原 run 关联 |
| prompt 版本化 | Langfuse prompt management | instruction 每次修改存版本+diff；run 记录关联 prompt 版本 |
| OTLP ingest + gen_ai.* 对齐 | Langfuse v3、Phoenix | 消费框架内建 OTel span；`/v1/traces` HTTP 端点；span 属性映射到现有 Span 模型 |
| 标注队列（简版） | LangSmith annotation queues | 把选中 run 加入队列，标注页逐条打分/评语 → score |
| 终端用户反馈 API | Weave feedback | `POST /api/runs/{id}/feedback`（👍👎+评语），落 score |

### P2（规模化再说）

- Postgres/ClickHouse 存储后端（数据量到十万级 trace 再动手）
- 在线抽样评估 + 告警（错误率/成本/分数阈值 → webhook）
- 多项目/多环境隔离与 RBAC
- 内容脱敏钩子与采集开关（接生产流量的前提）
- trace 导出（OTLP 转发到 Langfuse/Datadog 等，做"薄前端厚生态"）

### 刻意不做（当前定位下）

- 自研 ClickHouse 级摄取管道（单团队测试平台用不上，见模式 5）
- 通用 prompt CMS/审批流（PromptLayer 的主场，超出测试平台边界）
- Gemini Live 式音视频调试（框架尚无此场景）

## 6. 参考资料

**LangSmith (LangChain)**
- <https://docs.langchain.com/langsmith/run-data-format>
- <https://docs.langchain.com/langsmith/observability-concepts>
- <https://docs.langchain.com/langsmith/annotate-code>
- <https://docs.langchain.com/langsmith/trace-with-opentelemetry>
- <https://docs.langchain.com/langsmith/self-hosted>
- <https://docs.langchain.com/langsmith/kubernetes>
- <https://docs.langchain.com/langsmith/evaluation-concepts>
- <https://docs.langchain.com/langsmith/online-evaluations-llm-as-judge>
- <https://docs.langchain.com/langsmith/annotation-queues>
- <https://docs.langchain.com/langsmith/dashboards>
- <https://docs.langchain.com/langsmith/alerts>
- <https://docs.langchain.com/langsmith/cost-tracking>
- <https://docs.langchain.com/langsmith/threads>
- <https://docs.langchain.com/langsmith/prompt-engineering-concepts>

**Langfuse**
- <https://langfuse.com/docs/observability/data-model（经官方文档源码仓库 github.com/langfuse/langfuse-docs main@631f861，2026-07-08 克隆，下同）>
- <https://langfuse.com/self-hosting>
- <https://langfuse.com/docs/evaluation/scores/data-model>
- <https://langfuse.com/docs/evaluation/experiments/data-model>
- <https://langfuse.com/docs/prompt-management/data-model>
- <https://langfuse.com/docs/prompt-management/features/caching>
- <https://langfuse.com/docs/prompt-management/features/playground>
- <https://langfuse.com/docs/evaluation/evaluation-methods/annotation-queues>
- <https://langfuse.com/docs/evaluation/evaluation-methods/llm-as-a-judge>
- <https://langfuse.com/docs/observability/sdk/overview>
- <https://langfuse.com/integrations/native/opentelemetry>
- <https://langfuse.com/docs/observability/features/token-and-cost-tracking>
- <https://langfuse.com/docs/observability/features/corrections>
- <https://langfuse.com/docs/metrics/features/monitors>

**Arize Phoenix + OpenInference**
- <https://github.com/Arize-ai/phoenix>
- <https://github.com/Arize-ai/openinference>
- <https://raw.githubusercontent.com/Arize-ai/openinference/main/spec/semantic_conventions.md>
- <https://raw.githubusercontent.com/Arize-ai/phoenix/main/docs/phoenix/self-hosting.mdx>
- <https://raw.githubusercontent.com/Arize-ai/phoenix/main/docs/phoenix/self-hosting/configuration.mdx>
- <https://raw.githubusercontent.com/Arize-ai/phoenix/main/docs/phoenix/self-hosting/license.mdx>
- <https://raw.githubusercontent.com/Arize-ai/phoenix/main/docs/phoenix/user-guide.mdx>
- <https://raw.githubusercontent.com/Arize-ai/phoenix/main/docs/phoenix/tracing/concepts-tracing/what-are-traces.mdx>
- <https://raw.githubusercontent.com/Arize-ai/phoenix/main/docs/phoenix/tracing/llm-traces/sessions.mdx>
- <https://raw.githubusercontent.com/Arize-ai/phoenix/main/docs/phoenix/tracing/concepts-tracing/annotations-concepts.mdx>
- <https://raw.githubusercontent.com/Arize-ai/phoenix/main/docs/phoenix/tracing/llm-traces/metrics.mdx>
- <https://raw.githubusercontent.com/Arize-ai/phoenix/main/docs/phoenix/tracing/how-to-tracing/cost-tracking.mdx>
- <https://raw.githubusercontent.com/Arize-ai/phoenix/main/docs/phoenix/evaluation/llm-evals.mdx>
- <https://raw.githubusercontent.com/Arize-ai/phoenix/main/docs/phoenix/evaluation/how-to-evals.mdx>

**W&B Weave**
- <https://github.com/wandb/weave>
- <https://github.com/wandb/weave/tree/master/weave/trace_server>
- <https://raw.githubusercontent.com/wandb/weave/master/dev_docs/REF_SPEC.md>
- <https://raw.githubusercontent.com/wandb/weave/master/DOCS.md>
- <https://raw.githubusercontent.com/wandb/docs/main/weave/guides/tracking/tracing.mdx>
- <https://raw.githubusercontent.com/wandb/docs/main/weave/guides/tracking/call-schema-reference.mdx>
- <https://raw.githubusercontent.com/wandb/docs/main/weave/guides/tracking/ops.mdx>
- <https://raw.githubusercontent.com/wandb/docs/main/weave/guides/tracking/objects.mdx>
- <https://raw.githubusercontent.com/wandb/docs/main/weave/guides/tracking/threads.mdx>
- <https://raw.githubusercontent.com/wandb/docs/main/weave/guides/tracking/feedback.mdx>
- <https://raw.githubusercontent.com/wandb/docs/main/weave/guides/tracking/annotation-queues.mdx>
- <https://raw.githubusercontent.com/wandb/docs/main/weave/guides/tracking/costs.mdx>
- <https://raw.githubusercontent.com/wandb/docs/main/weave/guides/tracking/otel.mdx>
- <https://raw.githubusercontent.com/wandb/docs/main/weave/guides/tracking/write-ahead-log.mdx>

**Braintrust**
- <https://raw.githubusercontent.com/braintrustdata/autoevals/main/README.md>
- <https://raw.githubusercontent.com/braintrustdata/braintrust-proxy/main/README.md>
- <https://raw.githubusercontent.com/braintrustdata/braintrust-sdk-go/main/README.md>
- <https://raw.githubusercontent.com/braintrustdata/terraform-aws-braintrust-data-plane/main/README.md>
- <https://raw.githubusercontent.com/braintrustdata/terraform-aws-braintrust-data-plane/main/main.tf>
- <https://raw.githubusercontent.com/braintrustdata/terraform-aws-braintrust-data-plane/main/examples/braintrust-data-plane/README.md>
- <https://raw.githubusercontent.com/braintrustdata/helm/main/README.md>
- <https://www.braintrust.dev/docs/instrument>
- <https://www.braintrust.dev/docs/core/logs>
- <https://www.braintrust.dev/docs/core/monitor>
- <https://www.braintrust.dev/docs/annotate/human-review>
- <https://www.braintrust.dev/docs/integrations/sdk-integrations/opentelemetry>
- <https://www.braintrust.dev/docs/reference/platform/architecture>
- <https://www.braintrust.dev/docs/platform/experiments>

**Opik (Comet)**
- <https://github.com/comet-ml/opik>
- <https://raw.githubusercontent.com/comet-ml/opik/main/deployment/docker-compose/docker-compose.yaml>
- <https://raw.githubusercontent.com/comet-ml/opik/main/apps/opik-documentation/documentation/fern/docs/tracing/concepts.mdx>
- <https://raw.githubusercontent.com/comet-ml/opik/main/apps/opik-documentation/documentation/fern/docs/tracing/opentelemetry/overview.mdx>
- <https://raw.githubusercontent.com/comet-ml/opik/main/apps/opik-documentation/documentation/fern/docs/tracing/cost_tracking.mdx>
- <https://raw.githubusercontent.com/comet-ml/opik/main/apps/opik-documentation/documentation/fern/docs/evaluation/concepts.mdx>
- <https://raw.githubusercontent.com/comet-ml/opik/main/apps/opik-documentation/documentation/fern/docs/evaluation/metrics/overview.mdx>
- <https://raw.githubusercontent.com/comet-ml/opik/main/apps/opik-documentation/documentation/fern/docs/evaluation/annotation_queues.mdx>
- <https://raw.githubusercontent.com/comet-ml/opik/main/apps/opik-documentation/documentation/fern/docs/production/guardrails.mdx>
- <https://raw.githubusercontent.com/comet-ml/opik/main/apps/opik-documentation/documentation/fern/docs/production/rules.mdx>
- <https://raw.githubusercontent.com/comet-ml/opik/main/apps/opik-documentation/documentation/fern/docs/self-host/overview.mdx>
- <https://raw.githubusercontent.com/comet-ml/opik/main/apps/opik-documentation/documentation/fern/docs/agent_optimization/overview.mdx>
- <https://raw.githubusercontent.com/comet-ml/opik/main/apps/opik-documentation/documentation/fern/docs/prompt_engineering/playground.mdx>
- <https://raw.githubusercontent.com/comet-ml/opik/main/apps/opik-documentation/documentation/fern/docs/prompt_engineering/prompt_management.mdx>

**Helicone + PromptLayer**
- <https://github.com/Helicone/helicone>
- <https://raw.githubusercontent.com/Helicone/helicone/main/docs/features/sessions.mdx>
- <https://raw.githubusercontent.com/Helicone/helicone/main/docs/features/advanced-usage/custom-properties.mdx>
- <https://raw.githubusercontent.com/Helicone/helicone/main/docs/getting-started/self-host/manual.mdx>
- <https://raw.githubusercontent.com/Helicone/helicone/main/docs/features/advanced-usage/caching.mdx>
- <https://raw.githubusercontent.com/Helicone/helicone/main/docs/features/advanced-usage/scores.mdx>
- <https://raw.githubusercontent.com/MagnivOrg/prompt-layer-library/master/README.md>
- <https://docs.promptlayer.com/features/prompt-registry/overview>
- <https://docs.promptlayer.com/running-requests/traces>
- <https://docs.promptlayer.com/features/prompt-history/scoring-requests>
- <https://docs.promptlayer.com/features/evaluations/programmatic>
- <https://www.npmjs.com/package/@helicone/async>
- <https://www.helicone.ai/blog/self-hosting-journey>

**AgentOps + Laminar + Lunary**
- <https://github.com/AgentOps-AI/agentops>
- <https://raw.githubusercontent.com/AgentOps-AI/agentops/main/README.md>
- <https://raw.githubusercontent.com/AgentOps-AI/agentops/main/docs/v2/concepts/core-concepts.mdx>
- <https://github.com/lmnr-ai/lmnr>
- <https://raw.githubusercontent.com/lmnr-ai/lmnr/main/README.md>
- <https://raw.githubusercontent.com/lmnr-ai/lmnr/main/docker-compose-full.yml>
- <https://github.com/lunary-ai>
- <https://github.com/brotheralameen1/lunary>
- <https://github.com/lunary-ai/lunary-js>
- <https://docs.lunary.ai/docs/features/observability>

**OpenTelemetry GenAI 语义约定 + OpenLLMetry (Traceloop)**
- <https://github.com/open-telemetry/semantic-conventions-genai>
- <https://github.com/open-telemetry/semantic-conventions-genai/blob/main/docs/gen-ai/gen-ai-spans.md>
- <https://github.com/open-telemetry/semantic-conventions-genai/blob/main/docs/gen-ai/gen-ai-agent-spans.md>
- <https://github.com/open-telemetry/semantic-conventions-genai/blob/main/docs/gen-ai/gen-ai-events.md>
- <https://github.com/open-telemetry/semantic-conventions-genai/blob/main/docs/gen-ai/gen-ai-metrics.md>
- <https://github.com/traceloop/openllmetry>
- <https://raw.githubusercontent.com/traceloop/openllmetry/main/packages/opentelemetry-semantic-conventions-ai/opentelemetry/semconv_ai/__init__.py>
- <https://raw.githubusercontent.com/traceloop/openllmetry/main/packages/opentelemetry-instrumentation-openai/opentelemetry/instrumentation/openai/utils.py>
- <https://github.com/traceloop/go-openllmetry>
- <https://www.traceloop.com/docs/openllmetry/privacy/traces>

**LangGraph Studio + Google ADK Web UI**
- <https://raw.githubusercontent.com/langchain-ai/docs/main/src/langsmith/studio.mdx>
- <https://raw.githubusercontent.com/langchain-ai/docs/main/src/langsmith/use-studio.mdx>
- <https://raw.githubusercontent.com/langchain-ai/docs/main/src/langsmith/observability-studio.mdx>
- <https://raw.githubusercontent.com/langchain-ai/docs/main/src/langsmith/local-dev-testing.mdx>
- <https://raw.githubusercontent.com/langchain-ai/docs/main/src/oss/langgraph/use-time-travel.mdx>
- <https://raw.githubusercontent.com/google/adk-docs/main/docs/runtime/web-interface/index.md>
- <https://raw.githubusercontent.com/google/adk-docs/main/docs/evaluate/index.md>
- <https://raw.githubusercontent.com/google/adk-docs/main/docs/integrations/cloud-trace.md>
- <https://github.com/google/adk-web>
- <https://docs.langchain.com/langsmith/studio>
- <https://google.github.io/adk-docs/runtime/web-interface/>
- <https://google.github.io/adk-docs/evaluate/>

**OpenAI Agents SDK Tracing + MLflow Tracing**
- <https://github.com/openai/openai-agents-python/blob/main/docs/tracing.md>
- <https://github.com/openai/openai-agents-python/blob/main/src/agents/tracing/processor_interface.py>
- <https://github.com/openai/openai-agents-python/blob/main/src/agents/tracing/processors.py>
- <https://github.com/openai/openai-agents-python/blob/main/LICENSE>
- <https://github.com/openai/openai-agents-python/blob/main/README.md>
- <https://github.com/mlflow/mlflow/blob/master/docs/docs/genai/tracing/index.mdx>
- <https://github.com/mlflow/mlflow/blob/master/docs/docs/genai/concepts/trace.mdx>
- <https://github.com/mlflow/mlflow/blob/master/docs/docs/genai/concepts/span.mdx>
- <https://github.com/mlflow/mlflow/blob/master/docs/docs/genai/tracing/opentelemetry/index.mdx>
- <https://github.com/mlflow/mlflow/blob/master/docs/docs/genai/tracing/opentelemetry/export.mdx>
- <https://github.com/mlflow/mlflow/blob/master/docs/docs/genai/tracing/prod-tracing.mdx>
- <https://github.com/mlflow/mlflow/blob/master/docs/docs/genai/tracing/app-instrumentation/manual-tracing.mdx>
- <https://github.com/mlflow/mlflow/blob/master/docs/docs/genai/tracing/track-users-sessions/index.mdx>
- <https://github.com/mlflow/mlflow/blob/master/docs/docs/genai/tracing/integrations/listing/openai-agent.mdx>

**promptfoo + DeepEval**
- <https://raw.githubusercontent.com/promptfoo/promptfoo/main/README.md>
- <https://raw.githubusercontent.com/promptfoo/promptfoo/main/site/docs/configuration/guide.md>
- <https://raw.githubusercontent.com/promptfoo/promptfoo/main/site/docs/configuration/expected-outputs/index.md>
- <https://raw.githubusercontent.com/promptfoo/promptfoo/main/site/docs/usage/self-hosting.md>
- <https://raw.githubusercontent.com/promptfoo/promptfoo/main/site/docs/configuration/caching.md>
- <https://raw.githubusercontent.com/promptfoo/promptfoo/main/site/docs/integrations/ci-cd.md>
- <https://raw.githubusercontent.com/promptfoo/promptfoo/main/site/docs/usage/web-ui.md>
- <https://raw.githubusercontent.com/confident-ai/deepeval/main/README.md>
- <https://raw.githubusercontent.com/confident-ai/deepeval/main/docs/content/docs/metrics-introduction.mdx>
- <https://raw.githubusercontent.com/confident-ai/deepeval/main/docs/content/docs/%28custom%29/metrics-llm-evals.mdx>
- <https://raw.githubusercontent.com/confident-ai/deepeval/main/docs/content/docs/evaluation-unit-testing-in-ci-cd.mdx>
- <https://raw.githubusercontent.com/confident-ai/deepeval/main/docs/content/docs/synthetic-data-generation-introduction.mdx>

**Datadog LLM Observability + New Relic AI Monitoring**
- <https://docs.datadoghq.com/llm_observability/>
- <https://docs.datadoghq.com/llm_observability/terms/>
- <https://docs.datadoghq.com/llm_observability/instrumentation/>
- <https://docs.datadoghq.com/llm_observability/instrumentation/sdk/?tab=python>
- <https://docs.datadoghq.com/llm_observability/instrumentation/otel_instrumentation/>
- <https://docs.datadoghq.com/llm_observability/evaluations/>
- <https://docs.datadoghq.com/llm_observability/evaluations/managed_evaluations/>
- <https://docs.datadoghq.com/llm_observability/evaluations/evaluation_compatibility/>
- <https://docs.datadoghq.com/llm_observability/experiments/>
- <https://docs.datadoghq.com/llm_observability/monitoring/agent_monitoring/>
- <https://raw.githubusercontent.com/newrelic/docs-website/develop/src/content/docs/ai-monitoring/intro-to-ai-monitoring.mdx>
- <https://raw.githubusercontent.com/newrelic/docs-website/develop/src/content/docs/ai-monitoring/compatibility-requirements-ai-monitoring.mdx>
- <https://raw.githubusercontent.com/newrelic/docs-website/develop/src/content/docs/ai-monitoring/explore-ai-data/view-ai-agents.mdx>

## 7. 核验记录（附录）

| 工具 | 核验条数 | 确认 | 反驳(已订正) | 未核实 | 核查员总评 |
| --- | --- | --- | --- | --- | --- |
| LangSmith (LangChain) | 10 | 10 | 0 | 0 | 10 条声明全部核实为 confirmed，与官方文档高度一致且多处逐字吻合。核验方式：docs.langchain.com 被网络代理拦截，改为核对其官方构建源仓库 github… |
| Langfuse | 10 | 10 | 0 | 0 | 10 条声明全部得到官方来源支持（10 confirmed，0 refuted）。langfuse.com 在本环境被网络策略拦截，故改用其官方文档仓库 langfuse/lang… |
| Arize Phoenix + OpenInference | 10 | 10 | 0 | 0 | 10 条声明全部经声称来源（raw.githubusercontent.com 原文抓取）或官方渲染文档独立验证，无一被反驳，整体可靠性很高；仅两处口径瑕疵：JS instrume… |
| W&B Weave | 10 | 9 | 1 | 0 | 整体高度可靠：10 条中 9 条经原始来源逐项证实（含精确措辞、限额、原文引用），仅第 9 条的 Monitors 描述有一处实质性错误——Error 类 signal 会对失败的… |
| Braintrust | 10 | 8 | 2 | 0 | 整体可靠性高：10 条中 8 条经官方文档、GitHub 源码或 Terraform 代码逐项证实；2 条部分有误——(1) autoevals README 中的 `npx br… |
| Opik (Comet) | 11 | 10 | 0 | 1 | 整体高度可靠：10 条声明中 9 条与所引来源逐字吻合（版本号、端点、默认模型、冷却期、优化器名单等细节均精确）；仅两处小瑕疵——"60+ 框架集成"查无官方出处（所引页面写 30… |
| Helicone + PromptLayer | 10 | 10 | 0 | 0 | 这份调研可靠性很高：10 条声明全部经原始来源（GitHub README、仓库内 raw 文档、openapi.json、npm registry、SDK 源码）核实为 conf… |
| AgentOps + Laminar + Lunary | 10 | 10 | 0 | 0 | 10 条声明经逐条访问声称来源（GitHub 仓库/raw 文件/组织页）与独立检索反驳测试后全部成立，无一被推翻；仅第 2 条的 span 层级实为树形而非严格线性链、第 10 … |
| OpenTelemetry GenAI 语义约定 + OpenLLMetry (Traceloop) | 10 | 10 | 0 | 0 | 10 条声明全部经原始来源（含仓库原始 markdown、Python 源码逐字核对、历史版本比对、Go module proxy 权威时间戳）验证为 confirmed，可靠性极… |
| LangGraph Studio + Google ADK Web UI | 10 | 10 | 0 | 0 | 10 条声明全部经原始来源（langchain-ai/docs 与 google/adk-docs 的 raw 文件及 google/adk-web 仓库）逐条核对为 confir… |
| OpenAI Agents SDK Tracing + MLflow Tracing | 10 | 10 | 0 | 0 | 10 条声明全部经原始来源（GitHub 仓库源码/文档原文）或官方站点直接验证为 confirmed，未发现实质性错误；仅两处来源标注轻微不精确（第 3 条的 TraceProv… |
| promptfoo + DeepEval | 10 | 9 | 1 | 0 | 整体可靠性高：10 条中 9 条经原始来源逐字核实成立（含 OpenAI 收购这一反直觉事实，已由 OpenAI 官方公告与 TechCrunch 独立佐证），仅第 10 条存在实… |
| Datadog LLM Observability + New Relic AI Monitoring | 10 | 9 | 1 | 0 | 整体可靠性高：10 条中 9 条经官方文档（含原始 HTML 表格核对）逐项证实，细节（版本号、env 变量、映射表、span 限制）均准确；仅第 10 条被反驳——Datadog… |

合计：核验 131 条关键事实，确认 125 条，反驳并订正 5 条（已在 §2 对应档案标注），未核实 1 条。

---

*本报告由多代理调研流水线生成（13 组深度调研 + 逐份反驳式核验 + 全景查漏，27 个代理 / 762 次检索与页面抓取），汇编与分析章节由主代理撰写。日期基准 2026-07-08。*
