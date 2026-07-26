# FlowSpace 工作规划领域

FlowSpace 将长期目标拆成可执行任务，并通过排期、执行实例和学习资源支持持续推进。本文只定义领域语言，不记录数据库、API 或界面实现。

## 项目与执行

**Project（项目）**:
一组围绕同一目标组织的任务，回答“为什么做”。学习项目是 Project 的一种。
_Avoid_: 任务组、列表

**Task（任务）**:
一个稳定、可执行的事项定义，回答“做什么”。Task 的标题、生命周期和所属项目只有一个事实来源。
_Avoid_: 思维导图节点、日程

**Task Occurrence（任务执行实例）**:
Task 在某个计划时间产生的一次实际执行。完成、阻塞或跳过发生在 Occurrence 上。
_Avoid_: 日程项、任务副本

**Task Dependency（任务依赖）**:
两个 Task 之间影响执行顺序的前置关系。
_Avoid_: 父子任务、思维导图连线

## 学习路线

**Learning Roadmap（学习路线）**:
学习项目中的宏观阶段、主题和推荐顺序。
_Avoid_: 学习任务列表、思维导图

**Roadmap Node（路线节点）**:
Learning Roadmap 中的一个阶段、主题或里程碑。它不是可执行任务，完成进度由关联 Task 的执行事实聚合得到。
_Avoid_: Task、任务节点

**Task Mind Map（任务思维导图）**:
一个 Roadmap Node 下的任务拆解视图，其所有可见节点都引用真实 Task。
_Avoid_: 子 Roadmap、执行流程图

**Task Placement（任务位置）**:
Task 在 Task Mind Map 中的父子关系、顺序、分支与视觉位置。
_Avoid_: Task 副本、Task Dependency

**Decomposition Relation（拆解关系）**:
Task Mind Map 中表达“由什么组成”的父子关系，不表示执行先后。
_Avoid_: 依赖、前置条件

**Unplaced Task（待整理任务）**:
已经属于某个 Roadmap Node，但尚未明确放入其 Task Mind Map 的 Task。
_Avoid_: 孤儿任务

## 学习资源

**Article Resource（文章资源）**:
workspace 内按规范化 URL 复用的一份文章元数据。
_Avoid_: 文章副本、Roadmap 资源

**Task Article Link（任务文章关联）**:
一个 Task 与一个 Article Resource 之间的明确关联。
_Avoid_: 节点文章、画布链接

**Unassigned Article（待分配文章）**:
从旧 Roadmap Node 资源迁移而来、尚未由用户明确关联到具体 Task 的文章。
_Avoid_: 默认根任务文章

