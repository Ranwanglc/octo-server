# DMWork 团队空间 产品设计文档

> **DMWork — AI Agent 时代的 IM**
>
> 版本: v0.1 Draft | 日期: 2026-03-03 | 作者: Coda

---

## 1. 背景与动机

### 现状问题

DMWork 当前是一个 **扁平的 IM 系统**：

- 所有用户在同一个池子里，没有组织边界
- 群组之间无层级关系，无法按团队归类
- Bot 是全局的，任何人都能看到所有 Bot
- 没有权限隔离，无法区分"公司内部群"和"外部协作群"

这在早期验证阶段可行，但 **无法支撑 "AI Agent 时代的 IM" 这一定位**：

- 企业需要在自己的空间内部署私有 AI Agent
- 不同团队的 Agent 不应互相可见
- Agent 的对话数据需要空间级隔离

### 目标

引入 **团队空间（Team Workspace）** 概念，让 DMWork 从"聊天工具"进化为 **"AI 协作平台"**。

---

## 2. 产品定位对比

| 维度 | Slack | 飞书 | Discord | **DMWork (目标)** |
|------|-------|------|---------|------------------|
| 核心单元 | Workspace | 组织/租户 | Server | **团队空间** |
| 频道层级 | 平铺 | 群组分组 | Category + Channel | **分区 + 频道** |
| Bot 归属 | 按 Workspace | 按组织 | 按 Server | **按团队空间** |
| AI 集成 | Slack AI (有限) | 飞书智能伙伴 | 无原生 | **原生 Agent 架构** |
| 开放性 | API 丰富 | API 丰富 | Bot API | **OpenClaw 插件** |

**DMWork 差异化**：不是在 IM 上"加 AI"，而是 **以 AI Agent 为一等公民的协作空间**。

---

## 3. 核心概念模型

```
用户 (User)
 ├── 可加入多个团队空间
 └── 在每个空间有不同的角色

团队空间 (Team)
 ├── 成员 (TeamMember)
 │    ├── Owner (创建者，最高权限)
 │    ├── Admin (管理员)
 │    ├── Member (普通成员)
 │    └── Guest (访客，受限权限)
 │
 ├── 分区 (Section)
 │    ├── 频道 (Channel/群组)
 │    │    ├── 公开频道 (所有成员可见)
 │    │    ├── 私有频道 (受邀成员可见)
 │    │    └── 公告频道 (只读，管理员发布)
 │    └── ...
 │
 ├── AI Agent (Bot)
 │    ├── 空间级 Agent (该空间专属)
 │    ├── Agent 权限 (可访问哪些频道)
 │    └── Agent 配置 (Prompt/模型/技能)
 │
 └── 设置
      ├── 空间名称/图标/描述
      ├── 邀请策略 (公开/链接邀请/仅管理员)
      └── 数据留存策略
```

---

## 4. 用户旅程

### 4.1 创建团队空间

```
用户打开 DMWork → 侧边栏点击 "+" → "创建团队空间"
→ 输入空间名称、图标、描述
→ 选择邀请方式: 邀请链接 / 邮箱邀请
→ 自动创建 #通用 频道
→ 引导创建第一个 AI Agent 或跳过
```

### 4.2 加入团队空间

```
方式 A: 邀请链接 → 打开链接 → 预览空间信息 → "加入"
方式 B: 管理员邀请 → 收到邀请通知 → "接受"
方式 C: 邮箱邀请 → 收到邮件 → 点击注册/登录 → 自动加入
```

### 4.3 空间内日常使用

```
侧边栏
├── 私聊 (跨空间)
├── 团队空间 A
│    ├── #通用
│    ├── #产品讨论
│    ├── 🤖 AI 助手区
│    │    ├── @代码助手 (私聊)
│    │    └── @文档助手 (私聊)
│    └── + 创建频道
├── 团队空间 B
│    └── ...
└── + 创建/加入空间
```

### 4.4 创建 AI Agent (空间内)

```
空间设置 → "AI Agent 管理" → "创建 Agent"
→ 设置名称/头像/描述
→ 配置 Prompt / 模型 / 技能
→ 选择可访问的频道
→ Agent 出现在空间频道列表
→ 成员可以在频道中 @Agent 或私聊 Agent
```

---

## 5. 数据模型设计

### 5.1 新增表

#### `team` - 团队空间

| 字段 | 类型 | 说明 |
|------|------|------|
| id | bigint | 主键 |
| team_no | varchar(40) | 空间唯一编号 |
| name | varchar(100) | 空间名称 |
| avatar | varchar(255) | 空间图标 |
| description | varchar(500) | 空间描述 |
| creator_uid | varchar(40) | 创建者 |
| status | smallint | 状态 (1=正常, 0=解散) |
| invite_mode | smallint | 邀请模式 (0=仅管理员, 1=链接邀请, 2=公开) |
| invite_code | varchar(40) | 邀请码 |
| max_members | int | 最大成员数 |
| version | bigint | 数据版本号 |
| created_at | timestamp | 创建时间 |
| updated_at | timestamp | 更新时间 |

#### `team_member` - 空间成员

| 字段 | 类型 | 说明 |
|------|------|------|
| id | bigint | 主键 |
| team_no | varchar(40) | 空间编号 |
| uid | varchar(40) | 用户 UID |
| role | smallint | 角色 (0=owner, 1=admin, 2=member, 3=guest) |
| nickname | varchar(40) | 空间内昵称 |
| status | smallint | 状态 |
| invite_uid | varchar(40) | 邀请人 |
| version | bigint | 版本号 |
| created_at | timestamp | 加入时间 |

#### `team_section` - 分区

| 字段 | 类型 | 说明 |
|------|------|------|
| id | bigint | 主键 |
| team_no | varchar(40) | 所属空间 |
| section_no | varchar(40) | 分区编号 |
| name | varchar(40) | 分区名称 |
| sort_num | int | 排序 |
| collapsed | smallint | 默认折叠 |
| version | bigint | 版本号 |

#### `team_channel` - 频道与空间的关联

| 字段 | 类型 | 说明 |
|------|------|------|
| id | bigint | 主键 |
| team_no | varchar(40) | 所属空间 |
| section_no | varchar(40) | 所属分区 |
| group_no | varchar(40) | 对应群组编号 |
| channel_type | smallint | 类型 (0=公开, 1=私有, 2=公告) |
| sort_num | int | 排序 |
| version | bigint | 版本号 |

### 5.2 修改现有表

#### `robot` 新增字段

| 字段 | 类型 | 说明 |
|------|------|------|
| team_no | varchar(40) | 所属团队空间 (空=全局 Bot) |

#### `group` 新增字段

| 字段 | 类型 | 说明 |
|------|------|------|
| team_no | varchar(40) | 所属团队空间 (空=独立群) |

---

## 6. API 设计

### 6.1 团队空间管理

```
POST   /v1/team                        # 创建团队空间
GET    /v1/teams                        # 获取我的空间列表
GET    /v1/team/:team_no                # 获取空间详情
PUT    /v1/team/:team_no                # 更新空间信息
DELETE /v1/team/:team_no                # 解散空间
```

### 6.2 成员管理

```
POST   /v1/team/:team_no/members        # 邀请成员
GET    /v1/team/:team_no/members        # 获取成员列表
PUT    /v1/team/:team_no/members/:uid   # 修改成员角色
DELETE /v1/team/:team_no/members/:uid   # 移除成员
POST   /v1/team/:team_no/join           # 通过邀请码加入
POST   /v1/team/:team_no/leave          # 退出空间
```

### 6.3 分区与频道

```
POST   /v1/team/:team_no/sections            # 创建分区
PUT    /v1/team/:team_no/sections/:section_no # 修改分区
DELETE /v1/team/:team_no/sections/:section_no # 删除分区

POST   /v1/team/:team_no/channels            # 在空间内创建频道
PUT    /v1/team/:team_no/channels/:group_no   # 修改频道设置
DELETE /v1/team/:team_no/channels/:group_no   # 删除频道
GET    /v1/team/:team_no/channels             # 获取空间频道列表
```

### 6.4 空间 Agent 管理

```
POST   /v1/team/:team_no/agents              # 在空间内创建 Agent
GET    /v1/team/:team_no/agents              # 获取空间 Agent 列表
PUT    /v1/team/:team_no/agents/:bot_id      # 配置 Agent
DELETE /v1/team/:team_no/agents/:bot_id      # 删除 Agent
POST   /v1/team/:team_no/agents/:bot_id/channels  # 设置 Agent 可访问的频道
```

### 6.5 邀请

```
POST   /v1/team/:team_no/invite/code         # 生成邀请码/链接
POST   /v1/team/:team_no/invite/email        # 邮箱邀请
GET    /v1/team/invite/:invite_code          # 查看邀请信息（无需登录）
POST   /v1/team/invite/:invite_code/accept   # 接受邀请
```

---

## 7. UI 设计要点

### 7.1 侧边栏改造

**现状：** 平铺的会话列表
**目标：** 分层结构

```
┌──────────────────────────┐
│  🔍 搜索                  │
├──────────────────────────┤
│  💬 私聊                  │
│    ├── 张三               │
│    └── 李四               │
├──────────────────────────┤
│  🏢 Demo Space              │  ← 团队空间 (可折叠)
│    ├── 📢 公告            │
│    ├── 💬 通用            │
│    ├── 📁 产品部          │  ← 分区
│    │   ├── #需求讨论      │
│    │   └── #设计评审      │
│    ├── 🤖 AI Agents      │  ← Agent 分区
│    │   ├── 代码助手       │
│    │   └── 文档助手       │
│    └── ⚙️ 设置           │
├──────────────────────────┤
│  🏢 另一个空间            │
├──────────────────────────┤
│  ➕ 创建/加入空间         │
└──────────────────────────┘
```

### 7.2 空间首页

进入空间后的首屏：
- 空间名称 + 图标
- 成员头像列表
- 频道列表（按分区分组）
- AI Agent 卡片区
- 快捷操作（邀请成员、创建频道、创建 Agent）

### 7.3 Agent 管理界面

```
┌─────────────────────────────────┐
│  🤖 AI Agent 管理                │
├─────────────────────────────────┤
│  ┌─────────┐  ┌─────────┐      │
│  │ 代码助手 │  │ 文档助手 │  ➕  │
│  │ 在线 ●   │  │ 在线 ●   │      │
│  └─────────┘  └─────────┘      │
├─────────────────────────────────┤
│  代码助手 详情                    │
│  ─────────                      │
│  Prompt: 你是一个编程助手...      │
│  模型: Claude Opus               │
│  技能: 代码生成、代码审查         │
│  可访问频道: #通用, #技术讨论     │
│                                  │
│  [编辑]  [频道权限]  [删除]       │
└─────────────────────────────────┘
```

---

## 8. 实现路径（分期）

### Phase 1: 最小可用 — 团队空间骨架 (2-3 周)

**后端：**
- [ ] `team` / `team_member` 表 + CRUD API
- [ ] 创建空间时自动创建 #通用 频道
- [ ] 空间成员与频道成员同步
- [ ] 邀请码加入
- [ ] `group` 表增加 `team_no` 字段

**Web 前端：**
- [ ] 侧边栏分层显示（私聊 / 团队空间 / 独立群）
- [ ] 创建空间弹窗
- [ ] 空间内频道列表
- [ ] 邀请链接页面

**Android：**
- [ ] 侧边栏适配空间结构
- [ ] 加入空间流程

### Phase 2: Agent 绑定空间 (1-2 周)

- [ ] `robot` 表增加 `team_no` 字段
- [ ] BotFather `/newbot` 支持指定空间
- [ ] 空间内 Agent 管理界面
- [ ] Agent 仅在所属空间内可见
- [ ] OpenClaw connect prompt 传递 `team_no`

### Phase 3: 分区与频道管理 (1-2 周)

- [ ] `team_section` 表 + API
- [ ] 拖拽排序频道/分区
- [ ] 公开/私有/公告频道类型
- [ ] 频道级权限控制

### Phase 4: 体验优化 (1-2 周)

- [ ] 空间切换动画
- [ ] 未读计数按空间聚合
- [ ] 空间级搜索
- [ ] 邮箱批量邀请
- [ ] 空间数据导出

---

## 9. 技术考量

### 9.1 与 WuKongIM 的关系

WuKongIM 的频道概念不变，`team` 是业务层的组织概念：
- 群组（频道）仍由 WuKongIM 管理消息投递
- `team` 层做成员同步、权限校验、UI 组织
- 创建空间频道 = 创建群组 + 关联到 `team_channel`

### 9.2 数据隔离

- 空间内的群组消息仍存在全局 `message` 表
- 空间级隔离是 **逻辑隔离**（通过 `team_no` 过滤），不是物理隔离
- 后续可考虑企业版物理隔离

### 9.3 兼容性

- 现有独立群组（`team_no` 为空）继续正常使用
- 现有 Bot（`team_no` 为空）作为全局 Bot
- 用户端可选择"不使用空间"模式，保持原有体验
- API 向后兼容，所有新增字段可选

### 9.4 性能

- 空间列表缓存在客户端，增量同步（version 机制）
- 空间成员同步复用 WuKongIM 的 channel_member 机制
- 预计单空间 < 1000 人，无需分片

---

## 10. 竞品差异化思考

### DMWork 独特优势

**1. Agent 是一等公民，不是附属品**
- Slack/飞书的 Bot 是"插件"，DMWork 的 Agent 是"团队成员"
- Agent 有自己的频道权限、知识库、技能配置
- Agent 之间可以协作（多 Agent 群聊已验证）

**2. 开放的 Agent 架构**
- 基于 OpenClaw 的插件体系，Agent 不被平台锁定
- 用户可以自带模型、自定义技能
- Agent 可以连接外部工具（GitHub、Jira、数据库...）

**3. 轻量级部署**
- 不需要企业级 SaaS，自部署即可
- 小团队 3-10 人就能用
- 从"私人 AI 助手"自然过渡到"团队 AI 协作"

### 目标用户画像

1. **AI 原生团队** (5-30人): 需要在日常协作中深度使用 AI Agent
2. **技术团队**: 需要私有部署、自定义 Agent、代码级控制
3. **创业公司**: 不想用飞书/钉钉的重型方案，想要轻量灵活的协作工具

---

## 11. 开放问题

1. **空间是否收费？** — 免费版限制 N 个 Agent / N 个成员？
2. **跨空间沟通** — 两个空间的成员如何协作？通过私聊？
3. **Agent 市场** — 是否有空间级的 Agent 模板市场？
4. **数据归属** — 空间解散后数据怎么处理？
5. **WuKongIM channel_type** — 是否需要新增一种 channel_type 来区分空间频道和普通群？

---

## 附录: 名词对照

| DMWork | Slack | 飞书 | Discord | 技术实体 |
|--------|-------|------|---------|---------|
| 团队空间 | Workspace | 组织 | Server | `team` |
| 分区 | — | 群组分组 | Category | `team_section` |
| 频道 | Channel | 群聊 | Channel | `group` + `team_channel` |
| AI Agent | Bot/App | 机器人 | Bot | `robot` + OpenClaw |
| 成员角色 | Member/Admin/Owner | 成员/管理员 | Role | `team_member.role` |
