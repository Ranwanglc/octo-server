# 信息约束盘点：当前实现 vs spec v4.2 前端契约

> 日期：2026-06-12
> 范围：`feat/messages-search` HEAD `703d25f` + OS mapping v1.8
> 依据：`api-spec-v2-server-to-frontend.html` v4.2 + `v1.8-opensearch-mapping.md` + `internal/transform/doc.go`（kafka-plugin payload 解析）
> 目的：盘点**当前 mapping/业务 payload 不能提供**或**只能近似提供**的展示字段，作为 PRD 与产品验收时的"已知约束清单"

---

## 一、字段级缺口（按 spec 字段名分类）

### `MediaSearchHit.thumb_url`（图片缩略图）

- **spec 要求**：缩略图 URL（视频亦给一帧封面）
- **业务 payload 实情**：`ImageContent.decodeJSON` 只解 `url/caption/name/width/height`，**业务原生 payload 没有 thumb_url 字段**
- **当前实现行为**：`MediaHit.ThumbURL` 对 image 永远空（`omitempty` 不出现在 wire），video 用 `cover` 兜底
- **影响**：图片瀑布卡只能用原图 URL（前端可加 CDN `?imageView2` 之类的尺寸参数自做缩略，但 octo 这边没统一缩略服务）
- **解锁前置条件**：① 业务消息发送链路把客户端生成的缩略图 URL 写进 payload（如 `image.thumbUrl`）；② 或基础设施提供"原图 URL → 缩略图 URL"的统一变换规则；③ indexer mapping v1.9 加 `payload.image.thumbUrl: keyword`

### `MediaSearchHit.thumb_url`（视频封面）

- **spec 要求**：视频缩略图（一帧封面）
- **业务 payload 实情**：v1.8 mapping 有 `payload.video.cover`（keyword），客户端发送时如果传了就有
- **当前实现行为**：`MediaHit.ThumbURL = video.cover`，单一来源
- **风险**：依赖客户端实际填 cover 字段。**老消息或某些客户端版本可能不传 cover**，这种情况 video 也没缩略图
- **解锁前置条件**：客户端发送规范统一要求填 cover；或服务侧加视频帧抽取生成

### `MediaSearchHit.duration_ms`（视频时长）

- **spec 要求**：毫秒数
- **业务 payload 实情**：v1.8 `payload.video.second`（**整数秒**）
- **当前实现行为**：`MediaHit.DurationMs = int64(video.second) * 1000`
- **影响**：精度丢失到秒级（18.5 秒视频显示 18000 ms 而非 18500 ms）。前端展示"00:18"或"00:19"差异，UX 上**可接受**
- **解锁前置条件**：mapping 改 `second` 为 `durationMs`（v1.7 曾这样设计）。但业务 payload 端 `VideoContent.decodeJSON` 实际是秒，indexer 不会自己造毫秒精度，所以**改字段名也无意义**，除非客户端改协议

### `MediaSearchHit.width / height`（图片/视频尺寸）

- **spec 要求**：缩略图宽 / 高（瀑布布局）
- **业务 payload 实情**：v1.8 mapping 加了 `image.width/height` + `video.width/height`
- **当前实现行为**：`MediaHit.Width / Height` 直接从 mapping 读 ✓
- **风险**：依赖客户端实际填了 width/height。**老消息（v1.8 reindex 之前的）走 reindex painless 从 `payloadRaw` 回填**，回填成功率 100% 取决于老 payload 是否有 width/height 字段。客户端历史版本是否一直填这俩字段需要 Niko 确认
- **PRD 建议**：标注"瀑布卡 width/height 从 v1.8 起可用，老消息可能为 0（前端按 1:1 兜底）"

### `MediaSearchHit.duration_ms`（图片/gif/voice）

- **spec 要求**：仅 `media_kind="video"` 才有
- **业务 payload 实情**：voice payload v1.8 mapping 只有 `url`（v1.7 曾有 durationMs 但 v1.8 删了）
- **当前实现行为**：voice 不进 `_search_media`（DSL filter `payload.type IN [2,5]` 只含 image/video，**voice=4 不召回**）
- **影响**：spec §2.2 也规定 media 只含 image/video，**与设计一致**
- **风险点**：如果 PRD 后续要加"语音消息搜索"，目前 voice 信息只有 URL，**没有时长 / 转写文本**

### `FileSearchHit.preview_url`（文件预览 URL）

- **spec 要求**：预览 URL，不支持时为 null
- **业务 payload 实情**：v1.8 `payload.file` 没有 previewUrl 字段（只有 url/name/caption/size/extension）
- **当前实现行为**：`FileHit.PreviewURL = nil`（永远 null，`search_files.go:17` 注释明确："always nil this release"）
- **影响**：前端永远走"不支持预览"分支
- **解锁前置条件**：① 业务接入文档预览服务（如 office365 / wps cloud preview）；② 客户端发送时把预览链接写进 payload；或 ③ octo-server 加一层"扩展名 → 预览 URL"的查找逻辑（依赖外部预览服务可用性）

### `FileSearchHit.file_size_bytes`（文件大小）

- **spec 要求**：字节数
- **业务 payload 实情**：v1.8 `payload.file.size`（long，byte）
- **当前实现行为**：`FileHit.FileSizeBytes = file.size` ✓
- **风险**：依赖客户端发送时填 size。**老消息（v1.7 mapping）没有 size 字段**，走 reindex painless 从 `payloadRaw` 回填；老 payload 如果本来就没传 size，则索引里 size=0
- **PRD 建议**：标注"文件大小老消息可能为 0（前端显示 '未知大小' 兜底）"

### `FileSearchHit.file_ext`（扩展名）

- **spec 要求**：扩展名小写，不含 `.`
- **业务 payload 实情**：v1.8 `payload.file.extension`（**原样大小写**）
- **当前实现行为**：`FileHit.FileExt = file.extension`（**保留原样**），如 `extension` 字段空则从 filename 切尾兜底
- **偏离**：spec 写"小写"，indexer 实际"原样"。**已知偏离**，等产品决策：
  - A. 改 spec：file_ext 不强制小写（前端做大小写归一）
  - B. octo-server BFF 层做 `strings.ToLower`（最简单，1 行改）
  - C. indexer v1.9 改成强制小写（mapping 已有 32 字 keyword，可加 normalizer）
- **PRD 必决项**

### `FileSearchHit.download_url`（下载 URL）

- **spec 要求**：未在 §2.3 表格列必填，但 example 输出有
- **业务 payload 实情**：v1.8 `payload.file.url`
- **当前实现行为**：`FileHit.DownloadURL = file.url`，`omitempty` 在 url 为空时不出现
- **风险**：依赖业务 url 字段是公开可访问的 / 鉴权后可访问。**octo-server BFF 层不重新生成签名 URL**，直接透传索引里的字段。如果文件 URL 需要鉴权，前端要自己处理 401/403

### `MessageSearchHit.snippet`（搜索高亮片段）

- **spec 要求**：服务端注入 `<mark>`，单片段最长 120 字
- **业务 payload 实情**：highlight 字段（`payload.text.content` / `payload.image.caption` / `payload.image.name` / `payload.file.caption` / `payload.file.name` / `payload.mergeForward.msgs.searchText`）通过 IK 分词
- **当前实现行为**：`buildSearchMessagesHighlight` + `pickSnippet` 优先级 text → image.caption → file.name → mergeForward.msgs.searchText
- **风险点**：mergeForward 内层命中时 snippet 来自**子消息的 searchText**，而不是被引消息的 sender / context。前端展示需注意"在合并转发卡里搜到的"
- **当前已知缺口**：**quote/reply 消息**的搜索 —— v4.2 已决策**不区分 quote**（外层正文 snippet 已能定位），但意味着：
  - 用户搜 "alice 说的 abc"，如果 abc 出现在被引文本（reply.payload）里，**搜不到**（mapping 没索引 reply）
  - 转发的转发（嵌套 mergeForward）只索引外层一层

### `MessageSearchHit.sender_avatar_url`（发送人头像）

- **spec 要求**：绝对 URL（R8 `_url` 后缀 + URI 格式）
- **业务实情**：octo-server 没有"用户头像 URL"标准接口，messages_search 模块自拼 `{base}/users/{uid}/avatar`
- **当前实现行为**：依赖 `OCTO_USER_AVATAR_BASE_URL` 环境变量；不配则相对路径 `users/{uid}/avatar`
- **解锁前置条件**：SRE 配 `OCTO_USER_AVATAR_BASE_URL`；或 octo-server 主仓库加统一头像 URL 工具函数（messages_search 主动复用，避免发明二份配置）

### `MessageSearchHit.sender_name`（发送人显示名）

- **spec 要求**：显示名
- **业务实情**：DM = `user.name`；群 = `member.remark > user.name` 优先级
- **当前实现行为**：`senderJoin` 调 `userService.GetUsers` + `groupService.GetMembers`（仅群）
- **风险**：① user.name 是 user 表的当前值，**不是消息发送时点的快照**——用户改名后历史消息会跟着变；② `member.remark` 是当前查询用户视角的 remark，跨用户不同
- **PRD 建议**：标注"sender_name 是查询时的当前显示名，非消息发送时快照"

### `OuterPreview`（合并转发卡概要）

- **spec 要求**：仅 `message_kind=forward` 返 `{child_count}`
- **业务 payload 实情**：v1.8 `payload.mergeForward.childCount`（integer）
- **当前实现行为**：`OuterPreview{ChildCount: ...}`，ChildCount<=0 时整个 OuterPreview 返 nil（防御）
- **缺口**：**没有 mergeForward 的 title** —— spec v4.0 草案曾要 title，v4.2 删除（mlclaw 决策"前端写死'群聊的聊天记录'文案"）。如果 PRD 想恢复"自定义 title"展示，需要 indexer 重新加 title 字段
- **缺口**：mergeForward 内层 sender 列表（"AAA、BBB 等 N 人"概要）未提供。需要 indexer 投影 `mergeForward.msgs[].fromUid` 或 `fromName`，目前**只有 messageId/type/searchText**

---

## 二、Reply / Quote 消息整体不索引

- **业务 payload 实情**：octo-server 业务里 `payload.reply` 是任意 type 消息可带的字段（不是独立 ContentType），结构 `{message_id, from_uid, payload}`
- **v1.7/v1.8 mapping**：**完全不索引 reply 字段**（v4.2 决策已拍板"引用消息忽略，能搜到原消息即可"）
- **当前 search 行为**：搜被引文本搜不到，搜外层文本能搜到外层
- **解锁前置条件**：v1.9 mapping 加 `payload.reply.{messageId, fromUid, fromName, searchText}`，indexer 把 reply.payload 拼搜索文本

---

## 三、撤回 / 编辑识别

- **业务 payload 实情**：撤回走 `messageExtra.ContentEdit` 覆盖 + `revoked` flag；编辑同理
- **v1.8 mapping**：有 `revoked / revokedAt / revokedBy / edited / editedAt / editVersion` 字段
- **当前 search 行为**：DSL 已 `MustNot(TermQuery("revoked", true))` 过滤掉撤回消息 ✓
- **缺口**：① **kafka-plugin 不识别撤回事件，indexer 无 partial update**（笔记 2026-06-11 已记录）。撤回消息进 OS 后 `revoked=false`，撤回事件不会触发 update。仅当**新消息进来时**整条文档才会被覆盖 → 撤回消息**实际不会被过滤**
- ② ISM warm 阶段索引置 read_only，**撤回有效窗口仅 hot 阶段 ~30 天**
- ③ 编辑同样问题：编辑后 ContentEdit 需要 partial update，目前没实现 → 搜的还是原始内容

**PRD 必标**："本期搜索结果可能命中已撤回 / 已编辑前内容（30 天内）"

---

## 四、富文本 / 卡片 / 位置 / 名片 ContentType 不索引

- **业务 payload 实情**：octo-lib `ContentType` 包含 `Location=6` / `Card=7` / `RichText=14`
- **v1.8 mapping**：**完全不索引这些类型的 payload 字段**
- **当前 search 行为**：搜不到位置 / 名片 / 富文本 / @提及人名 等
- **PRD 必标**："本期不支持位置消息、名片消息、富文本消息（包括 @ 提及）的内容搜索"
- **解锁前置条件**：v1.9 mapping 加 `payload.location.{address, name}` / `payload.card.{name, intro}` / `payload.richText.searchText` 等

---

## 五、流式消息（streaming）

- **业务 payload 实情**：mapping 有 `streamNo / streamId` 字段（v1.7 起）
- **当前 search 行为**：DSL 不区分 stream，搜结果可能命中半成品流式消息（如 AI 回复中途）
- **PRD 必标**：是否过滤 streaming 中的消息（`MustNot(ExistsQuery("streamNo"))` 或 stream 完成后 indexer 才落 doc）

---

## 六、跨群 / 全局搜索

- **当前实现**：所有 4 个 endpoint 都强制 `channel_type + channel_id` 必填，**单频道搜索**
- **PRD 缺口**：spec v4.2 没要求"跨群"，但产品后续可能问"我的所有群里搜 X"。当前 `MustNot revoked + Filter channelId` DSL 不支持。要加"跨我所在所有群"，需要：
  - ① octo-server 先查 `groupService.GetMembersWithUIDAndGroupIds(loginUID, ...)` 拿到群列表
  - ② DSL 改 `Filter(TermsQuery("channelId", [...]))` 多频道
  - ③ 性能：群多了（>1000）会让 OS query terms 列表过长，需要按 batch 拆分
- 不在本期范围

---

## 七、性能侧约束（已知）

- **OS 集群规模**：v1.8 template `number_of_shards: 1 / replicas: 2 / total_shards_per_node: 2`，单 shard，写入吞吐受限于单节点。当前测试集群 3 节点（10.10.148.6/15/12）性能上限尚未 benchmark
- **冷热分层**：ISM warm 后索引 read_only，搜索仍可读但写入（撤回 update）失败 ↑ 见三
- **track_total_hits=false** 已配（前面回答过），>10000 命中只回 `>= 10000`
- **deep paging** 走 search_after，无 from/offset 限制
- **缺口**：**没有跨索引联合搜索的优化**（如 `wukongim-messages-2026-*` rollover 后多索引）。ISM 一旦切到月级 rollover，alias 自动 rollup 但搜索可能横扫多个月份的 index → 跨 30 天搜需要 benchmark

---

## 八、PRD 优先级建议

按"对前端 UX 影响 × 解锁难度"排序：

| # | 缺口 | UX 影响 | 解锁难度 | 是否阻塞本期上线 |
|---|---|---|---|---|
| 1 | **撤回消息漏过滤** | 高（搜出已撤回内容） | 中（kafka-plugin + indexer 加 partial update） | ⚠️ **建议本期上线前评估** |
| 2 | 图片 thumb_url 业务无 | 中 | 高（基础设施依赖） | 否（前端用原图 URL 凑合） |
| 3 | 视频 duration_ms 精度只到秒 | 低 | 高（要改协议） | 否 |
| 4 | 文件 preview_url 永远 null | 中 | 高（接预览服务） | 否（前端走"不支持预览"分支） |
| 5 | file_ext 大小写不一致（spec vs 实现） | 低 | 低（BFF 1 行 ToLower 或前端归一） | 否，**PRD 拍板即可** |
| 6 | sender_name 不是发送时点快照 | 低 | 高（要存历史 user 表） | 否（标注约定） |
| 7 | 老消息 width/height/size 可能为 0 | 中 | 看 reindex 回填率（painless 从 payloadRaw 回填） | 否（前端兜底） |
| 8 | reply/quote 文本不索引 | 中 | 中（mapping v1.9 + indexer 改 buildSearchText） | 否 v4.2 已决策不做 |
| 9 | 富文本 / @ 提及 / 位置 / 名片不索引 | 中 | 中（同上） | 否（标注本期不支持） |
| 10 | streaming 半成品消息可能命中 | 低 | 低（DSL `MustNot(Exists("streamNo"))`） | 否（看产品决策） |
| 11 | 跨群 / 全局搜索 | 中 | 中 | 否（不在本期 spec） |

---

## 九、PRD 拍板项（请产品 / 业务方决策）

1. **撤回消息漏过滤是否本期阻塞**？（强烈建议至少标注"30 天前撤回的消息可能仍出现在搜索结果中"）
2. **file_ext 大小写**：BFF ToLower vs 前端归一 vs 改 indexer
3. **streaming 消息是否过滤**：`MustNot(Exists("streamNo"))` 一行加上还是不加
4. **mergeForward 子消息预览**：仅 `child_count` 还是要加"前 N 个发送人列表"
5. **sender_name 快照语义**：是否在文档里明确写"查询时点快照，非发送时快照"
6. **未来扩展**：reply 索引 / 富文本索引 / 跨群搜索的优先级排期

---

**作者**：solo-builder
**生成时间**：2026-06-12
**HEAD**：`703d25f` (feat/messages-search 已 push)

---

## 决议 D24：visibles 白名单 post-filter（2026-06-15 追加；2026-06-15 修订）

> 触发：PR #361 review (yujiawei 11:24 / lml2468 11:25)。权威读路径
> `modules/message/api.go::MsgSyncResp.from` 隐藏消息靠 6 个信号；
> 此前 `filterVisible` 只覆盖前 4 个（revoke/全局删/用户删/channel offset），
> 漏 visibles 白名单（read-path :2956）→ 群成员能搜出不在 visibles
> 白名单的定向消息内容。
>
> **修订说明（2026-06-15）**：本节最初同时提了 visibles 与 expire 双 gate，
> 在后续调查（见 D25）中确认 expire 字段在 octo-server 生产是死字段，
> 已撤掉 expire gate；本节仅保留 visibles。

### 已落地（本 PR）

1. **OS doc schema**：`Doc` 加 `visibles []string` 字段
   （`modules/messages_search/source.go`）。
2. **post-filter gate**（`modules/messages_search/visibility.go::filterVisible`）：
   - **visibles 白名单**：`len(Visibles)>0` 且 `loginUID ∉ Visibles` → 丢弃
3. `projectDocRef` 从 typed `_source` 读取 `visibles` 塞进 `msgRef`，过滤循环就地判定，
   不走 DB（visibles 是消息本身字段，无需额外 MySQL roundtrip）。

### 暂态妥协：indexer 字段未补齐期间的 fail-open

当前 `wukongim-message-indexer` **尚未把 visibles 写入 OS `_source`**。
本 PR 提前在 search 端落 schema + gate，**字段就绪即生效**（lml2468 原话）。
indexer 补字段前的过渡期：

- `visibles` 字段缺失 → JSON 反序列化为 `nil`，`len(r.Visibles)==0` → 跳过白名单 gate

**这与权威读路径的语义一致**：read path 同样在 `visibles` 为非空数组才查白名单。
区别是 read path 直接从 MySQL `message.payload` 读这个字段，**永远准确**；
search 这边依赖 indexer 异步写入 OS doc，**未写入则等同于"消息没设 visibles"**。

### 为什么仍要 gate（而非"反正没人写，跳过即可"）

生产里 visibles **不是空字段**：服务端生成的群退群事件（`modules/group/api.go:2995`）、
入群申请（`modules/group/invite.go`）等系统消息真在用 `payload.visibles` 限定可见范围
（典型如"只让群管理员看到"）。读路径的 4 条权威可见性入口里 3 条都过滤它
（`MsgSyncResp.from` / `respondSingleMessage` / 旧 `modules/search/api.go:402`）。
搜索若不 gate，群普通成员可以搜出群管才能看到的事件内容。

用户消息（私聊/群聊）链路目前不写 visibles —— octo-server `Message.sendMessage`
全链路无 `payload["visibles"] = ...` 写入，客户端理论上可塞但目前没产品功能挂在
visibles 上。但只要 indexer 把服务端系统消息也索引（v1.9 必然），visibles gate
就成为正确性必须项。

### Fail-open 的安全 vs 可见性 trade-off

明确选 fail-open 而非 fail-closed 的理由：

- **fail-closed（缺失字段一律丢弃）**会让现有所有未补字段的历史 doc 全部从
  搜索结果中消失 — 99.9% 的消息根本没设 visibles，正常用户搜历史会得到
  空白页面，UX 灾难
- **fail-open**只在两类场景泄露：
  1. 历史 doc 上有 visibles 字段但 indexer 没写出来 — 实际不存在，
     因为 indexer 之前根本不解析 visibles
  2. indexer 上线 v1.9（写入 visibles）之前的窗口期，新消息也走老 indexer
     不写 visibles — 这个窗口期是有限的，且需要 indexer 升级
- 本期 search 上线时 indexer 一并升级到写入 visibles 是收尾动作

### 跟进项（non-blocking）

| # | 跟进 | 责任 |
|---|---|---|
| 1 | indexer 在 v1.9 加 `visibles: keyword[]` 写入逻辑 | indexer 团队 |
| 2 | 上线后 sample 一批 OS doc 验证字段非空率，群系统消息 visibles 召回率 ≥ 99% | search owner |
| 3 | 若发现字段写入率低于阈值，评估改为 fail-closed（接受 UX 妥协） | search owner |

### 不做（明确 out-of-scope）

- 不在本 PR 改 indexer（那是另一个 PR / 另一个仓库）
- 不为 visibles 加 OS-side `terms` 过滤（pre-filter）—— 仅 post-filter 是足够的
  正确性保证，pre-filter 是性能优化，等 OS 字段填充率稳定后再评估
- 不查 MySQL `message.payload` 实时拉 visibles —— 等于把搜索退化成
  一次 IN 查全表 payload，规模上不可行。post-filter 的语义已经正确

---

## 决议 D25：expire 字段在 octo-server 生产是死字段，搜索不加 gate（2026-06-15）

> 触发：D24 初稿同时为 visibles + expire 各加一条 post-filter gate。
> 之后逐链路追溯 expire 字段的写入路径，确认它在 octo-server → wukongim
> 这条链路上**没有 per-message 写入入口**，给搜索加 expire gate 是防御
> 一个不存在的风险，制造假安全感而无实际收益。本节记录调查证据，并明确
> 撤回 D24 中的 expire gate（visibles gate 保留）。

### 调查链路

1. **octo-server → wukongim 的 `MsgSendReq` 没有 Expire 字段**
   - octo-lib `config/msg.go` 的
     `type MsgSendReq struct { Header / Setting / FromUID / ChannelID / ChannelType / StreamNo / Subscribers / Payload }`
     仅 8 字段，**无 Expire**
   - 所有 octo-server 调 `ctx.SendMessage(MsgSendReq)` 的入口
     （用户消息 `Message.sendMessage` / Bot API / OBO fanout / Manager / incomingwebhook）
     都无法把 per-message Expire 传给 wukongim

2. **`channel_setting.msg_auto_delete` 不入消息存储链路**
   - `CreateOrUpdateMsgAutoDelete` 仅 `UPDATE channel_setting SET msg_auto_delete=?`
   - 下游消费方只有 `channelResp.Extra["msg_auto_delete"]` 给客户端读 +
     `/channels/.../message/autodelete` 接口设置时发条 Tip 系统消息
   - **不告诉 wukongim、不写 message 表 expire 列、不参与任何消息发送**
   - 实际产品作用：客户端侧 self-destruct UI 提示

3. **wukongim 回调 webhook 里的 `MessageResp.Expire`**（octo-lib `config/msg.go:953`）
   - 来源是 wukongim 全局配置 `MessageExpire: time.Hour * 24 * 7`
   - **per-message 语义不存在**

4. **后果**：octo-server 的 message 表 expire 列对所有消息要么全 0、要么全局统一值；
   权威读路径 `MsgSyncResp.from` (`modules/message/api.go:2919`) 那段过期判断在
   生产里几乎永远进不到 `IsDeleted=1` 那条分支。

### 决议

搜索路径**不**为 Expire 加 gate。理由：

- 没有真实风险，加 gate 只是制造假安全感
- 每条无意义的 gate 都增加未来维护负担（解读 / 边界 / 测试），带来负熵
- visibles 那条 gate 是真在防御真实泄漏路径（D24 已论证），与 expire 不可类比

### 未来如何重新启用

如果未来 octo-lib 扩 `MsgSendReq` schema 加 Expire 字段、wukongim 实现 per-message
TTL、indexer 把 expire 写入 OS doc，则按 D24 visibles gate 的实现模式在
`filterVisible` 里加回 expire 分支：

```go
if r.Expire > 0 && r.Timestamp > 0 && nowUnix-int64(r.Expire) >= r.Timestamp {
    continue
}
```

同时需要在 `Doc` 加回 `Expire uint32` / 在 `msgRef` 加 `Expire/Timestamp` /
在 `projectDocRef` 投影这两字段，以及对应单元测试。
