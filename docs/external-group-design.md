# OCTO 外部群技术设计文档

> **版本**: v1.0  
> **日期**: 2026-04-24  
> **作者**: Coda (AI) + 余嘉伟  
> **状态**: 待实施  
> **代码基线**: dmworkim `origin/develop` @ `5efdfa2`

---

## 1. 概述

### 1.1 背景

OCTO 当前群与 Space 是 1:1 强绑定（`group.space_id`），建群和加人时强制校验 Space 成员资格。为支持跨组织协作场景，需要在不破坏 Space 隔离体系的前提下，允许外部 Space 用户加入群聊。

### 1.2 目标

- 外部 Space 用户可通过扫码或被邀请加入群
- 群自动识别为「外部群」（企微模式，无手动开关）
- 外部群在用户来源 Space 的会话列表中正确展示
- 外部成员可邀请自己 Space 的联系人和 Bot
- 零 WuKongIM 改动，Android/iOS 仅 UI 标识（无逻辑改动）

### 1.3 核心设计决策

| 决策项 | 结论 |
|--------|------|
| 外部群识别方式 | 自动分类（企微模式），有外部成员即为外部群 |
| 手动开关 | 不需要，已有 `invite=1` 作为安全阀 |
| source_space_id 来源 | `GetUserDefaultSpaceID(user)`，不需要客户端传参 |
| 外部 Bot | 允许，检查 Bot 在邀请人的 Space 内即可 |
| 跨第三方 Space 邀请 | 天然不可能（通讯录隔离） |
| 关闭外部群 | 所有外部成员退出后自动恢复为普通群 |

---

## 2. 数据库设计

### 2.1 Migration

```sql
-- +migrate Up

-- 群表：外部群自动标记（冗余字段，由后端维护）
ALTER TABLE `group` ADD COLUMN `is_external_group` 
  SMALLINT NOT NULL DEFAULT 0 COMMENT '外部群 0.否 1.是（自动维护）';

-- 成员表：外部成员标记
ALTER TABLE `group_member` ADD COLUMN `is_external` 
  SMALLINT NOT NULL DEFAULT 0 COMMENT '外部成员 0.否 1.是';

-- 成员表：来源 Space（外部群在此 Space 会话列表中显示）
ALTER TABLE `group_member` ADD COLUMN `source_space_id` 
  VARCHAR(40) NOT NULL DEFAULT '' COMMENT '来源Space ID';

-- +migrate Down
ALTER TABLE `group` DROP COLUMN `is_external_group`;
ALTER TABLE `group_member` DROP COLUMN `is_external`;
ALTER TABLE `group_member` DROP COLUMN `source_space_id`;
```

### 2.2 字段说明

| 表 | 字段 | 类型 | 说明 |
|----|------|------|------|
| `group` | `is_external_group` | SMALLINT | 冗余字段。首个外部成员加入时设 1，最后一个退出时设 0 |
| `group_member` | `is_external` | SMALLINT | 此成员是否为外部成员（不在群的 space_id 对应 Space 中） |
| `group_member` | `source_space_id` | VARCHAR(40) | 外部成员的来源 Space。外部群在此 Space 的会话列表中显示 |

### 2.3 索引（可选）

```sql
-- 加速 space_filter 查询外部成员的群列表
CREATE INDEX idx_gm_external ON `group_member` (uid, is_external, is_deleted);
```

---

## 3. 后端改动

### 3.1 modules/group/db.go — Model 扩展

```go
// Model 新增字段
type Model struct {
    // ... 现有字段 ...
    IsExternalGroup int // 外部群（自动维护）
}

// MemberModel 新增字段
type MemberModel struct {
    // ... 现有字段 ...
    IsExternal    int    // 外部成员
    SourceSpaceID string // 来源 Space
}

// MemberDetailModel 新增字段
type MemberDetailModel struct {
    // ... 现有字段 ...
    IsExternal    int    // 外部成员
    SourceSpaceID string // 来源 Space
}
```

新增查询方法：

```go
// QueryExternalMemberCount 查询群内外部成员数量（事务内使用，带行锁）
func (d *DB) QueryExternalMemberCountTx(groupNo string, tx *dbr.Tx) (int, error) {
    var count int
    _, err := tx.SelectBySql(
        "SELECT COUNT(*) FROM group_member WHERE group_no=? AND is_external=1 AND is_deleted=0 FOR UPDATE",
        groupNo,
    ).Load(&count)
    return count, err
}

// QueryExternalGroupNosForUser 查询用户作为外部成员加入的群列表
func (d *DB) QueryExternalGroupNosForUser(uid string) (map[string]string, error) {
    var results []struct {
        GroupNo       string `db:"group_no"`
        SourceSpaceID string `db:"source_space_id"`
    }
    _, err := d.session.SelectBySql(
        "SELECT group_no, source_space_id FROM group_member WHERE uid=? AND is_external=1 AND is_deleted=0",
        uid,
    ).Load(&results)
    if err != nil {
        return nil, err
    }
    m := make(map[string]string, len(results))
    for _, r := range results {
        m[r.GroupNo] = r.SourceSpaceID
    }
    return m, nil
}
```

```go
// UpdateIsExternalGroup 更新群的外部群标记
func (d *DB) UpdateIsExternalGroup(groupNo string, value int) error {
    _, err := d.session.Update("group").
        Set("is_external_group", value).
        Where("group_no=?", groupNo).Exec()
    return err
}

// QueryMemberByGroupNoAndUID 查询单个群成员
func (d *DB) QueryMemberByGroupNoAndUID(groupNo, uid string) (*MemberModel, error) {
    var m *MemberModel
    _, err := d.session.Select("*").From("group_member").
        Where("group_no=? AND uid=? AND is_deleted=0", groupNo, uid).Load(&m)
    return m, err
}
```

### 3.2 modules/group/api.go — 核心逻辑

#### 3.2.1 groupScanJoin（扫码入群）

在 `existMember` 检查之后、创建 `memberModel` 之前插入：

```go
// === 外部成员检测 ===
isExternal := 0
sourceSpaceID := ""
if group.SpaceID != "" {
    inSpace, err := spacepkg.CheckMembership(g.ctx.DB(), group.SpaceID, scaner)
    if err != nil {
        g.Error("检查 Space 成员失败", zap.Error(err))
        c.ResponseError(errors.New("检查成员关系失败"))
        return
    }
    if !inSpace {
        // 不在群的 Space → 外部成员
        isExternal = 1
        sourceSpaceID = space.GetUserDefaultSpaceID(g.ctx, scaner)
    }
}

memberModel := &MemberModel{
    GroupNo:       groupNo,
    UID:           scaner,
    Role:          MemberRoleCommon,
    Version:       version,
    Status:        int(common.GroupMemberStatusNormal),
    InviteUID:     generator,
    Vercode:       fmt.Sprintf("%s@%d", util.GenerUUID(), common.GroupMember),
    IsExternal:    isExternal,
    SourceSpaceID: sourceSpaceID,
}
```

在事务提交后维护 `is_external_group`：

```go
// === 维护 is_external_group ===
if isExternal == 1 && group.IsExternalGroup == 0 {
    err = g.db.UpdateIsExternalGroup(groupNo, 1)
    if err != nil {
        g.Warn("更新 is_external_group 失败", zap.Error(err))
    }
}
```

#### 3.2.2 addMembers（邀请入群）

在成员列表遍历中，对每个新成员做外部检测：

```go
// 在 addMembersTx 中，构建 memberModel 前
isExternal := 0
sourceSpaceID := ""
if group.SpaceID != "" {
    inSpace, _ := spacepkg.CheckMembership(g.ctx.DB(), group.SpaceID, memberUID)
    if !inSpace {
        isExternal = 1
        // 确定 source_space_id
        operatorMember, _ := g.db.QueryMemberByGroupNoAndUID(groupNo, operator)
        if operatorMember != nil && operatorMember.IsExternal == 1 {
            // 外部成员邀请 → 同源 Space
            sourceSpaceID = operatorMember.SourceSpaceID
        } else {
            // 内部成员邀请 → 被邀请人的默认 Space
            sourceSpaceID = space.GetUserDefaultSpaceID(g.ctx, memberUID)
        }
    }
}
```

Bot 校验逻辑调整：

```go
// Bot Space 隔离检查（改动）
if group.SpaceID != "" {
    for _, memberUID := range req.Members {
        var isBot int
        err = g.ctx.DB().SelectBySql(
            "SELECT COALESCE((SELECT robot FROM `user` WHERE uid=? LIMIT 1), 0)",
            memberUID,
        ).LoadOne(&isBot)
        if err != nil {
            c.ResponseError(errors.New("查询用户信息失败"))
            return
        }
        if isBot == 1 {
            inGroupSpace, _ := spacepkg.CheckMembership(g.ctx.DB(), group.SpaceID, memberUID)
            if !inGroupSpace {
                // Bot 不在群的 Space → 检查是否在邀请人的 Space
                inviterSpace := group.SpaceID
                operatorMember, _ := g.db.QueryMemberByGroupNoAndUID(groupNo, operator)
                if operatorMember != nil && operatorMember.IsExternal == 1 {
                    inviterSpace = operatorMember.SourceSpaceID
                }
                inInviterSpace, _ := spacepkg.CheckMembership(g.ctx.DB(), inviterSpace, memberUID)
                if !inInviterSpace {
                    c.ResponseError(errors.New("该 Bot 不属于你的 Space"))
                    return
                }
                // Bot 来自邀请人的 Space → 允许，标记为外部
            }
        }
    }
}
```

#### 3.2.3 退群/踢人时维护 is_external_group

在 `groupExit`（主动退群）和 `groupMemberRemove`（踢人）的事务中：

```go
// 被移除/退出的成员是外部成员时，检查是否需要恢复群类型
if removedMember.IsExternal == 1 {
    externalCount, err := g.db.QueryExternalMemberCountTx(groupNo, tx)
    if err != nil {
        g.Warn("查询外部成员数量失败", zap.Error(err))
    } else if externalCount == 0 {
        // 最后一个外部成员退出，恢复为普通群
        _, _ = tx.Update("group").
            Set("is_external_group", 0).
            Where("group_no=?", groupNo).Exec()
    }
}
```

### 3.3 modules/group/service.go — API 响应

#### GroupResp 新增字段

```go
type GroupResp struct {
    // ... 现有字段 ...
    IsExternalGroup int `json:"is_external_group"` // 是否外部群
}
```

#### from() 方法：对外部成员替换 SpaceID

```go
func (g *GroupResp) from(model *DetailModel) *GroupResp {
    resp := &GroupResp{
        // ... 现有赋值 ...
        IsExternalGroup: model.IsExternalGroup,
    }
    return resp
}

// SetEffectiveSpaceID 对外部成员替换 SpaceID，解决 Web 端二次过滤问题
func (g *GroupResp) SetEffectiveSpaceID(loginUID string, db *dbr.Session) {
    if g.IsExternalGroup == 0 {
        return
    }
    var sourceSpaceID string
    db.SelectBySql(
        "SELECT source_space_id FROM group_member WHERE group_no=? AND uid=? AND is_external=1 AND is_deleted=0",
        g.GroupNo, loginUID,
    ).LoadOne(&sourceSpaceID)
    if sourceSpaceID != "" {
        g.SpaceID = sourceSpaceID
    }
}
```

在群详情、群信息等 API 返回前调用 `SetEffectiveSpaceID`。

#### memberDetailResp 新增字段

```go
type memberDetailResp struct {
    // ... 现有字段 ...
    IsExternal      int    `json:"is_external"`       // 外部成员
    SourceSpaceName string `json:"source_space_name"` // 来源 Space 名称（外部成员时填充）
}
```

#### 填充 source_space_name

在 `memberDetailResp` 的序列化方法中，对外部成员查询 Space 名称：

```go
// 在构建 memberDetailResp 列表时，批量查询 Space 名称避免 N+1
func (g *Group) fillSourceSpaceNames(members []*memberDetailResp) {
    // 1. 收集所有不重复的 source_space_id
    spaceIDs := make(map[string]struct{})
    for _, m := range members {
        if m.IsExternal == 1 && m.SourceSpaceID != "" {
            spaceIDs[m.SourceSpaceID] = struct{}{}
        }
    }
    if len(spaceIDs) == 0 {
        return
    }

    // 2. 批量查询 Space 名称
    ids := make([]string, 0, len(spaceIDs))
    for id := range spaceIDs {
        ids = append(ids, id)
    }
    var spaces []struct {
        SpaceID string `db:"space_id"`
        Name    string `db:"name"`
    }
    g.ctx.DB().SelectBySql(
        "SELECT space_id, name FROM space WHERE space_id IN ?", ids,
    ).Load(&spaces)
    nameMap := make(map[string]string, len(spaces))
    for _, s := range spaces {
        nameMap[s.SpaceID] = s.Name
    }

    // 3. 填充
    for _, m := range members {
        if m.IsExternal == 1 {
            m.SourceSpaceName = nameMap[m.SourceSpaceID]
        }
    }
}
```

### 3.4 modules/message/space_filter.go — 会话过滤

在 `FilterConversationsBySpace` 中，增加外部成员群的查询：

```go
func FilterConversationsBySpace(
    conversations []*SyncUserConversationResp,
    filterSpaceID string,
    loginUID string,
    ctx *config.Context,
    groupService group.IService,
) []*SyncUserConversationResp {
    // ... 现有逻辑 ...

    // 查询用户作为外部成员加入的群 → { groupNo: sourceSpaceID }
    externalGroupMap, err := group.NewDB(ctx).QueryExternalGroupNosForUser(loginUID)
    if err != nil {
        log.Warn("查询外部群失败，跳过外部群过滤", zap.Error(err))
        externalGroupMap = make(map[string]string)
    }

    return filterConversationsCore(
        conversations, filterSpaceID, defaultSpaceID,
        groupSpaceMap, botSet, botInSpace,
        skipGroupFilter, skipBotFilter,
        externalGroupMap, // 新增参数
    )
}
```

在 `filterConversationsCore` 中，群聊过滤分支加入外部群放行：

```go
// 在 spaceID != filterSpaceID 的群聊分支中，加入外部群判断
if spaceID != filterSpaceID && conv.ChannelType == common.ChannelTypeGroup.Uint8() {
    // 检查是否为用户的外部群，且 source_space_id 匹配当前 Space
    if sourceSpace, ok := externalGroupMap[conv.ChannelID]; ok {
        effectiveSource := sourceSpace
        // fallback：如果 source Space 用户已不在，使用默认 Space
        if effectiveSource == "" {
            effectiveSource = defaultSpaceID
        }
        if effectiveSource == filterSpaceID {
            filtered = append(filtered, conv)
            continue
        }
    }
    // 原有过滤逻辑...
}
```

### 3.5 modules/search/api.go — 搜索过滤

修改 `shouldIncludeGroupForSpace`，增加外部成员判断：

```go
// shouldIncludeGroupForSpace 改为方法，接收外部群映射
func shouldIncludeGroupForSpace(groupSpaceID, searchSpaceID string, 
    groupNo string, externalGroupMap map[string]string) bool {
    if searchSpaceID == "" {
        return false
    }
    if groupSpaceID == searchSpaceID {
        return true
    }
    // 外部群：source_space_id 匹配当前搜索 Space
    if sourceSpace, ok := externalGroupMap[groupNo]; ok {
        return sourceSpace == searchSpaceID
    }
    return false
}
```

搜索 API 调用前，查询用户的外部群映射：

```go
externalGroupMap, _ := group.NewDB(s.ctx).QueryExternalGroupNosForUser(loginUID)
```

---

## 4. 前端改动

### 4.1 Web — 群信息「外部群」标签

群设置页头部，根据 `channelInfo.orgData.is_external_group === 1` 显示标签：

```tsx
{channelInfo?.orgData?.is_external_group === 1 && (
    <Tag color="orange" size="small">外部群</Tag>
)}
```

### 4.2 Web — 成员列表「外部」角标 + 来源 Space

成员列表中，显示外部角标和来源 Space 名称：

```tsx
{subscriber.orgData?.is_external === 1 && (
    <span>
        <Tag color="purple" size="small">外部</Tag>
        {subscriber.orgData?.source_space_name && (
            <span className="text-gray-400 text-xs ml-1">
                来自 {subscriber.orgData.source_space_name}
            </span>
        )}
    </span>
)}
```

### 4.3 Web — 会话过滤兼容

**无需改动**。后端 `SetEffectiveSpaceID` 对外部成员替换了 `space_id` 返回值，Web 的 `shouldSkipChannelForSpace` + `channelSpaceMap` 自然匹配。

### 4.4 Android / iOS — UI 标识

会话列表**功能层面无需改动**（sync API 白名单机制自动放行），但需补充 UI 标识：

#### Android

1. **群设置页** — 读取 `channelInfo` 中 `is_external_group` 字段，为 `1` 时在群名旁显示「外部群」标签
2. **成员列表** — 读取成员 `is_external` 字段，为 `1` 时显示「外部」角标 + `source_space_name`（如「来自 XX」）
3. **系统消息** — 外部成员加入时展示「以外部成员身份加入群聊」

#### iOS

同 Android，读取相同 API 字段展示：
1. 群设置页「外部群」标签
2. 成员列表「外部」角标 + 来源 Space 名称
3. 系统消息差异化文案

#### 改动量评估

纯 UI 展示，无逻辑改动。数据来自已有 API 响应字段（`is_external_group`、`is_external`、`source_space_name`），无需新增接口。预计 Android/iOS 各 1-2 天。

---

## 5. 完整场景清单

### 5.1 加入群

| 场景 | 行为 |
|------|------|
| Space A 用户扫码进 Space A 群 | 内部成员，`is_external=0` |
| Space B 用户扫码进 Space A 群 | `is_external=1, source_space_id=B`，群 `is_external_group→1` |
| 扫码但群开了 `invite=1` | 拒绝（已有逻辑） |
| 同时在 A+B 的用户扫码进 A 群 | `CheckMembership(A)=true` → 内部成员 |
| 内部成员邀请 Space B 用户 | `is_external=1, source_space_id=B(默认Space)` |
| 外部成员邀请 Space B 同事 | `is_external=1, source_space_id=B(同源)` |
| 外部成员邀请 Space B 的 Bot | Bot 在邀请人 Space → 允许 |
| 外部成员邀请 Space C 用户 | 无法操作（通讯录隔离） |
| `invite=1` 管理员审批外部用户 | 审批通过后标记 `is_external=1` |

### 5.2 会话可见性

| 场景 | 行为 |
|------|------|
| 外部成员在 source Space 查看 | sync 放行 → 正常显示 |
| 外部成员切到其他 Space | 不显示 |
| 推送通知 | WuKongIM 直推，不经 Space 过滤 |
| Android/iOS 会话列表 | 白名单机制，信任 sync 结果 |
| Web `shouldSkipChannelForSpace` | 后端替换 space_id → 自然匹配 |
| 搜索群名 | `shouldIncludeGroupForSpace` 放行 |
| 外部成员离开 source Space | fallback 到默认 Space |

### 5.3 群内交互

| 场景 | 行为 |
|------|------|
| 发消息 / @全员 | 正常（WuKongIM 透明） |
| 查看成员列表 | 外部成员显示「外部」角标 + 来源 Space 名称 |
| 加好友 | 受 `forbidden_add_friend` 控制 |
| 修改群设置 | 非管理员/群主被拦截 |
| Thread / GROUP.md | 正常工作 |

### 5.4 退出与恢复

| 场景 | 行为 |
|------|------|
| 外部成员退群 | 事务内检查外部成员数，为 0 则恢复 `is_external_group=0` |
| 管理员踢外部成员 | 同上 |
| 群解散 | group_member 全清理，无需额外处理 |

---

## 6. 安全分析

### 6.1 不涉及的层

| 层 | 影响 |
|----|------|
| WuKongIM | 零改动。只管 channel 投递，不关心 Space |
| space_member 表 | 零改动。不写入 guest 记录，不污染 Space 成员体系 |
| Space 通讯录 | 零改动。外部成员不出现在 Space 成员列表或搜索中 |

### 6.2 信息暴露评估

| 外部成员可获取的信息 | 风险 |
|---------------------|------|
| 群内成员的 name / avatar / role | ✅ 可接受（加群即可见） |
| 群内成员的来源 Space 名称 | ⚠️ 仅在成员列表中显示（外部成员可见彼此来源） |
| Space 组织结构 / 通讯录 | ❌ 不暴露（外部成员不在 Space 内） |
| 群的 GROUP.md 内容 | ⚠️ 可见（需管理员控制 GROUP.md 内容） |

### 6.3 已有安全阀

| 控制 | 作用 |
|------|------|
| `invite=1` | 群开启邀请确认 → 管理员审批外部人 |
| `forbidden_add_friend=1` | 禁止群内互加好友 |
| 通讯录隔离 | 外部成员只能邀请自己 Space 的人 |

---

## 7. 改动文件清单

### 后端（5 个文件 + 1 个 migration）

| 文件 | 改动内容 |
|------|---------|
| `modules/group/sql/group_YYYYMMDD-01.sql` | 新增 3 个字段的 migration |
| `modules/group/db.go` | Model/MemberModel/MemberDetailModel 加字段 + 新增查询方法 |
| `modules/group/api.go` | `groupScanJoin` 标记 + `addMembers` 标记/Bot 校验 + 退群维护 |
| `modules/group/service.go` | GroupResp/memberDetailResp 加字段 + `SetEffectiveSpaceID` |
| `modules/message/space_filter.go` | 外部群会话放行 |
| `modules/search/api.go` | `shouldIncludeGroupForSpace` 外部群放行 |

### 前端（Web 2 处 UI）

| 位置 | 改动内容 |
|------|---------|
| 群设置页 | 显示「外部群」标签 |
| 成员列表 | 外部成员显示「外部」角标 + 来源 Space 名称 |

### 客户端（Android / iOS 各 2 处 UI + 1 处文案）

| 位置 | 改动内容 |
|------|---------|
| 群设置页 | 显示「外部群」标签 |
| 成员列表 | 外部成员显示「外部」角标 + 来源 Space 名称 |
| 系统消息 | 外部成员加入时差异化文案 |

---

## 8. 补充说明

### 8.1 旧群兼容

`group.space_id` 为空的旧群（Space 功能上线前创建），所有外部检测逻辑被 `if group.SpaceID != ""` 跳过。旧群中所有成员均为内部成员，不触发外部群逻辑。

### 8.2 群系统消息

外部成员加入时，系统消息展示差异化文案：
- 内部成员加入：「XX 通过扫描二维码加入群聊」（现有）
- 外部成员加入：「XX 以外部成员身份加入群聊」（新增）

实现：`MsgGroupMemberScanJoin` 结构体新增 `IsExternal int` 字段，前端根据该字段展示不同文案。

### 8.3 Channel Update CMD

`is_external_group` 变更（0→1 或 1→0）后，调用 `SendChannelUpdateToGroup(groupNo)` 通知所有群成员刷新 channelInfo 缓存。

### 8.4 service.go 双序列化方法

`from(DetailModel)` 和 `fromModel(Model)` 均需补充 `IsExternalGroup` 字段赋值。

---

## 9. 测试要点

| # | 测试用例 | 预期结果 |
|---|---------|---------|
| T1 | Space B 用户扫码进 Space A 群（invite=0） | 成功，标记 is_external=1，群变外部群 |
| T2 | Space B 用户扫码进 Space A 群（invite=1） | 拒绝 |
| T3 | 外部成员在 source Space 会话列表 | 可见 |
| T4 | 外部成员切到其他 Space | 不可见 |
| T5 | 外部成员邀请自己 Space 同事 | 成功，is_external=1 |
| T6 | 外部成员邀请自己 Space Bot | 成功 |
| T7 | 外部成员邀请第三方 Space 用户 | 联系人列表不显示（天然隔离） |
| T8 | 所有外部成员退出 | is_external_group 恢复 0 |
| T9 | Web 端群详情 space_id | 外部成员看到 source_space_id |
| T17 | 成员列表显示来源 Space 名称 | 外部成员显示「外部 · 来自 XX Space」 |
| T18 | Android 群设置页外部群标签 | 显示「外部群」标签 |
| T19 | iOS 成员列表外部角标 | 显示「外部」+ 来源 Space |
| T10 | 搜索群名 | 外部群在 source Space 可搜到 |
| T11 | 推送通知 | 外部成员正常收到 |
| T12 | 并发退群 | is_external_group 一致（事务+行锁） |
| T13 | 旧群（无 space_id）扫码 | 正常加入，不标记 is_external |
| T14 | 外部成员加入系统消息 | 展示「以外部成员身份加入」 |
| T15 | is_external_group 变更后 | 所有成员收到 channelInfo 更新 |
| T16 | 外部成员 pin 外部群 | pin 存在 source Space 下，切 Space 不可见 |
