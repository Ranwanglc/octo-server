---
type: Journal
title: "Journal: group-default-avatar (increment 1 — create API + data model)"
description: Server-rendered default group avatar groundwork — create-group API gains custom avatar text/color params, persisted via new columns; avatarrender gains group-name helpers.
tags: ["group", "avatar", "wire-contract", "default-avatar"]
timestamp: 2026-06-25T03:00:28Z
# --- octospec extension fields ---
task: group-default-avatar
source: self
---
# Journal: group-default-avatar (increment 1 — create API + data model)

PRD「群聊与个人默认头像」Module 2 的后端实现，第一增量。把个人默认头像
(PR #346 server-rendered) 的思路延伸到群头像：默认头像将由服务端用「色块圆 +
群名前 4 字 / 群组图标」渲染，取代现有「成员头像九宫格合成」。本增量只落**创建
接口 + 数据模型 + 渲染助手**；avatarGet 渲染分支、改群 API、拆合成事件留待后续。

## What was done
- **DB 迁移** `modules/group/sql/20260625000001_group_avatar_custom.sql`（可重入，
  INFORMATION_SCHEMA 守卫 + 存储过程，含 Up/Down）：
  - `avatar_text VARCHAR(16) NOT NULL DEFAULT ''` —— 自定义头像文字，''=未自定义。
  - `avatar_color TINYINT DEFAULT NULL` —— 自定义色板下标，NULL=未自定义。
- **模型** `modules/group/db.go` `Model`：加 `AvatarText string` + `AvatarColor *int`。
  `*int` 而非 `int`：0 是合法色板下标，无法兼作哨兵；且 `util.AttrToUnderscore`+
  dbr `Record()` 会写入结构体所有字段，`*int=nil → NULL` 让既有建群路径（service.go
  另有一处 `s.db.Insert`）无需逐处显式赋值即可落到“未自定义”。与既有 `GroupMd *string`
  同一处理范式。`QueryWithGroupNo` 用 `Select("*")`，新列自动读回。
- **渲染助手** `pkg/avatarrender/`（纯增量，未改既有 IndividualText/palette 值/顺序）：
  - `text.go`：`GroupText(name)` 取可见字符**前 4** rune（区别于个人的后两字）；
    `VisibleRuneCount(s)` 供校验；抽 `visibleRunes` 复用。
  - `palette.go`：`PaletteSize()` + `ColorByIndex(i)`（自定义色 + 校验越界）。
- **创建接口** `modules/group/api.go`：`groupReq` 加 `avatar_text`/`avatar_color`
  （可选、向后兼容）；`checkAvatar()` 校验（文字 ≤4 可见 rune、颜色 nil 或 [0,palette)，
  超限不静默截断、经 `respondGroupRequestInvalid(c, field)` 返回）；`groupCreate`
  落库。`CreateGroupServiceReq` + `CreateGroup` insert 透传字段。

## Rules honored
- **error-handling (load-bearing)**：新参数校验走 `httperr.ResponseErrorL` +
  复用 catch-all `ErrGroupRequestInvalid`（`SafeDetailKeys:["field"]`），**未新增
  errcode → 零 i18n 改动**。`make i18n-lint` / `i18n-extract-check` 通过；
  `TestGroupNoLegacyResponseError` 源码守卫通过。avatarGet 仍是二进制端点，未动。
- **space-isolation (load-bearing)**：仅给 groupCreate 加可选字段，未放宽任何
  WHERE、未改 Auth/Space 边界、未改 openGroups 免鉴权 avatar 路由。
- **testing**：新单测用 `testutil.NewTestServer` + `CleanAllTables`。

## Verification
- `go build ./...`、`golangci-lint run ./pkg/avatarrender/... ./modules/group/...`
  （0 issues）、`go vet`、`gofmt` 通过。
- 纯单测通过：`avatarrender` 的 `GroupText`/`VisibleRuneCount`/`ColorByIndex`、
  group 的 `TestGroupReqCheckAvatar`（11 例）。
- **集成测试本地实跑通过**（自建 MySQL 8.0 / Redis 7 / WuKongIM v2.2.4-20260313，
  与 ci.yml 一致）：`TestGroupAvatarCustomDBRoundTrip`（nil→NULL 读回 nil、自定义
  值原样读回）、`modules/group` 全套（19s 无回归）、`avatarrender` 全套、
  `base/event` avatar 测试。
- 迁移由 `testutil.NewTestServer` 自动 apply，测试库实测列：
  `avatar_text varchar(16) NOT NULL`、`avatar_color tinyint NULL DEFAULT NULL`。

## Learnings
- dbr `InsertInto(...).Columns(util.AttrToUnderscore(m)...).Record(m)` 会写入
  结构体**全部**字段，故新增列的 Go 零值会覆盖 DB `DEFAULT`。需要“缺省=未设置”
  语义且零值本身合法（如 0 是合法下标）时，用指针字段让 nil→NULL 落到 DB 默认，
  避免逐 insert 点补值。（候选 learning，见 pending/）

## Increment 2 — avatarGet 服务端渲染分支
把 `avatarGet`（`modules/group/api.go`）从「一律重定向对象存储」改为按 `is_upload_avatar`
分流：
- `is_upload_avatar==1` → 维持历史重定向版本化对象（自定义上传不回归）。
- 否则 → `writeGroupDefaultAvatar` 服务端实时出图：文字 = `avatar_text` 优先，否则
  群名；颜色 = `avatar_color` 优先，否则 `ColorForSeed(group_no)`；`GroupText` 取前 4 字、
  `Renderable` 判定可渲染则出「色块圆 + 文字」，否则回退群组图标。带内容相关弱 ETag
  （渲染版本 + group_no + 实际色 + 文字）+ `Cache-Control: public, max-age=300,
  must-revalidate` + 304，改名/换色后短时 revalidate 到新图。

`pkg/avatarrender` 新增（均不改既有 `Render`/`IndividualText`/palette，个人头像不受影响）：
- `group.go`：`GroupAvatarLines` 排版决策 + `RenderGroup` 两行渲染。**对齐设计稿**：
  含 CJK 且 ≥3 字排 2 行（上少下多）——「架构讨论」→2×2、「三个字」→1+2「三」/「个字」；
  纯拉丁或 ≤2 字单行。两行字号/留白经样张目检对齐设计稿（`groupMaxInkWidthRatio` 等）。
- `render.go`：`RenderIcon`（色块圆 + 群组图标）；图标为**占位资产**
  `icons/group-placeholder.png`（白色双人剪影），Figma 正式 SVG 到位后替换该文件即可。
- `etag.go`：共享 `ETag`/`IfNoneMatch`（行为同 user 历史 avatarETag，提升为共享实现）。

增量 2 验证：`avatarrender` 全套（GroupAvatarLines/RenderGroup/RenderIcon/ETag）+
`modules/group` 全套（19s 无回归）+ 4 个 avatarGet 集成测试（渲染字节级匹配
RenderGroup、空名→RenderIcon、自定义覆盖、上传→302、ETag→304）+ golangci 0 issues +
i18n-lint + 源码守卫。样张目检 4 字 2×2 / 3 字 1+2 / 拉丁单行均匹配设计稿。

## Follow-ups（后续增量）
1. ~~`avatarGet` 默认/自定义服务端渲染分支 + 弱 ETag/304 + 群组图标占位~~ ✅ 增量 2。
2. 改群 API：`avatar_text`/`avatar_color` 两个 update key（`groupUpdateActionMap`）。
3. 拆除九宫格合成事件链路（`handleGroupAvatarUpdateEvent` 等）。
4. 外部素材：群组图标 SVG、自定义色板精确色值（占位待替换）。
