# wukongim-message-indexer + OpenSearch 修改清单

> **⚠️ 已过时（2026-06-12）：v1.8 起以 [`v1.8-opensearch-mapping.md`](./v1.8-opensearch-mapping.md) 为准。**
> 本文档保留作 v1.7 → v1.8 演进历史；字段名 / 单位以 v1.8 mapping 为权威。
> v1.8 关键变化：image/video 删 `thumbUrl`；video 加 `second`（秒）删 `durationMs`；file 用 `size`（byte）/ `extension`（原样不转小写）替换 `sizeBytes`/`ext`。

> 目标：让 octo-server 4 端点（A 文档 v4.1）直查 OpenSearch 即可拿全字段，不再依赖 messages 表 JOIN。

## 一、改动总览

| 层 | 改动 | 工作量 |
|---|---|---|
| OpenSearch mapping | 加字段（媒体尺寸时长 / 文件元信息 / mergeForward 顶层 childCount）| 0.5d |
| indexer transform/doc.go | ToDoc 抽更多字段 + 新增 mergeForward 解析 | 2d |
| indexer 单测 + 部署 | 单测覆盖 + 镜像发布 | 0.5d |
| OS 全量 reindex | alias 双写 + 历史回填 + 切读 | 1–3d 等待 |
| **合计** | | **3d 实现 + 1–3d reindex 等待** |

## 二、OpenSearch mapping 补丁（在研发同学最新草稿基础上加这些）

研发草稿已加 `payload.mergeForward.msgs.{messageId, type, searchText}`，方向对。下面是**还要补**的字段。

### 2.1 mergeForward 顶层补 childCount

```json
"mergeForward": {
  "type": "object",
  "properties": {
    "childCount": { "type": "integer" },
    "msgs": { ...保持研发草稿不变... }
  }
}
```

**为什么**：A 文档 v4.1 §2.1 forward 命中要返 `outer_preview = { child_count }`，前端判断卡片形态 + 显示"共 N 条"。msgs 是 object（非 nested），数组长度在 OS 端无法精确取，必须 indexer 写入时算好。

**title 字段不要**：业务侧没有可靠的 title 来源（默认"群聊的聊天记录"已是前端写死文案），不在 mapping 占位。

### 2.2 引用（reply / quote）—— 不处理

**本期忽略引用消息的判别和被引内容召回**。引用消息的外层正文（`payload.text.content`）已经能被搜到，能定位到原消息即可，不区分"是否引用类型"。

后续如有需要专门搜索"被引消息原文"的需求，再单独立项。

### 2.3 image 加 thumbUrl / width / height

```json
"image": {
  "url": ...,
  "caption": ...,
  "name": ...,
  "thumbUrl": { "type": "keyword", "ignore_above": 1024 },
  "width":    { "type": "integer" },
  "height":   { "type": "integer" }
}
```

**为什么**：A 文档 v4.1 §2.2 `_search_media` 要返 `thumb_url / width / height`（瀑布布局必备）。v1.7 mapping 已显式删除 width/height，本次撤销。

### 2.4 video 加 thumbUrl / durationMs / width / height

```json
"video": {
  "url": ...,
  "cover": ...,
  "thumbUrl":   { "type": "keyword", "ignore_above": 1024 },
  "durationMs": { "type": "long" },
  "width":      { "type": "integer" },
  "height":     { "type": "integer" }
}
```

**为什么**：A 文档 §2.2 视频要返 thumb_url / duration_ms / width / height。v1.7 mapping 已显式删除 durationSec / width / height，本次撤销并改 ms 单位。

### 2.5 file 加 sizeBytes / ext

```json
"file": {
  "url": ...,
  "name": ...,
  "caption": ...,
  "sizeBytes":  { "type": "long" },
  "ext":        { "type": "keyword", "ignore_above": 32 }
}
```

**为什么**：A 文档 §2.3 `_search_files` 要返 file_size_bytes / file_ext。业务 payload 顶层已有 `size` + `extension` 字段（实例：`{"extension":"mp4","name":"videodemo.mp4","size":23014356,"type":8,"url":"..."}`），indexer 直接抽入。

**previewUrl 不加**：业务 payload 不提供预览链接。A 文档 §2.3 已明示 `preview_url string? (uri) 不支持时为 null`，octo-server 适配层直接返 null。后续需预览能力时再补（本期不在范围）。

### 2.6 不动的部分

- `messageId / messageSeq / from / to / channelId / channelType / timestamp / topic / clientMsgNo / streamNo / streamId` — 已齐
- `payloadRaw` — enabled:false 备查，保留
- `revoked / edited` 系列 — 撤回方案本期搁置（详见 §五），mapping 字段先留着
- `meta.{timePluginReceivedMs, timeIndexedMs}` — 不动

## 三、indexer transform/doc.go 改动

### 3.1 PayloadParsed 新增 MergeForward 子对象

`internal/transform/doc.go` 现有 6 个 type 子对象（Text / Image / Gif / Voice / Video / File），加 1 个：

```go
type PayloadParsed struct {
    Type         *int                  `json:"type,omitempty"`
    Text         *TextPayload          `json:"text,omitempty"`
    Image        *ImagePayload         `json:"image,omitempty"`
    Gif          *GifPayload           `json:"gif,omitempty"`
    Voice        *VoicePayload         `json:"voice,omitempty"`
    Video        *VideoPayload         `json:"video,omitempty"`
    File         *FilePayload          `json:"file,omitempty"`
    MergeForward *MergeForwardPayload  `json:"mergeForward,omitempty"`
}
```

### 3.2 各子结构补字段

```go
type ImagePayload struct {
    URL      string `json:"url,omitempty"`
    Caption  string `json:"caption,omitempty"`
    Name     string `json:"name,omitempty"`
    ThumbURL string `json:"thumbUrl,omitempty"`   // 新
    Width    int    `json:"width,omitempty"`      // 新
    Height   int    `json:"height,omitempty"`     // 新
}

type VideoPayload struct {
    URL        string `json:"url,omitempty"`
    Cover      string `json:"cover,omitempty"`
    ThumbURL   string `json:"thumbUrl,omitempty"`     // 新
    DurationMs int64  `json:"durationMs,omitempty"`   // 新（业务 payload 里若是秒，×1000）
    Width      int    `json:"width,omitempty"`        // 新
    Height     int    `json:"height,omitempty"`       // 新
}

type FilePayload struct {
    URL        string `json:"url,omitempty"`
    Name       string `json:"name,omitempty"`
    Caption    string `json:"caption,omitempty"`
    SizeBytes  int64  `json:"sizeBytes,omitempty"`   // 新（从 payload.size 抽）
    Ext        string `json:"ext,omitempty"`         // 新（从 payload.extension 抽，转小写）
    // PreviewURL 不入索引：业务 payload 不提供，octo-server 返 null（A 文档允许）
}

type MergeForwardPayload struct {
    ChildCount int                `json:"childCount,omitempty"`  // 新（msgs 数组长度）
    Msgs       []MergeForwardMsg  `json:"msgs,omitempty"`        // 已有结构
}

type MergeForwardMsg struct {
    MessageID  int64  `json:"messageId"`
    Type       int    `json:"type"`
    SearchText string `json:"searchText,omitempty"`
}
```

**不再加 ReplyPayload**：本期不抽 reply 字段，外层消息正文召回已能定位原消息（见 §2.2）。

### 3.3 ToDoc 抽取规则补充

在现有 `switch t` 上扩展。

**原则**：所有要参与返回 / 查询 / 过滤的字段、indexer 都从 payload bytes 抽到结构化子对象（`payload.image / .video / .file / .mergeForward`）。`payloadRaw` 只做备查兑底（`enabled: false`，不入倒排、结果返回不能做字段级 source-filter）——**octo-server 下游不应该从 payloadRaw 里抽字段**，拼拼全 source 拖 KB 级 JSON 只为抿两个 int 不划算。

```
1. case payloadTypeImage(2)：补抽 thumbUrl / width / height
   - 业务 payload 顶层已有 width / height（实例：`{"height":1668,"width":2880,"name":"官网.png","type":2,"url":"..."}`），indexer 拼 `payloadInt(raw, "width")` / `payloadInt(raw, "height")` 即可
   - thumbUrl 业务侧若有独立字段则抽；没有允许为空（前端退使用 url 原图 + 压缩参数）

2. case payloadTypeVideo(5)：补抽 thumbUrl / durationMs / width / height
   - duration 业务可能是秒，indexer 统一转毫秒（×1000），字段命名 durationMs 防歧义
   - thumbUrl 与 cover 关系：若 payload 里只有 cover，thumbUrl=cover；若两者都有按业务字段优先

3. case payloadTypeFile(8)：补抽 sizeBytes / ext
   - 业务 payload 顶层已有 size + extension + name + url（实例：`{"extension":"mp4","name":"videodemo.mp4","size":23014356,"type":8,"url":"..."}`）
   - `size` → `payload.file.sizeBytes`（字段名显示带单位避免歧义）
   - `extension` → `payload.file.ext`（转小写）。业务上有独立字段了，**不需要从 name 切割**；宗主 name 没 ext 也能返回正确值
   - `name / url` → `payload.file.name / .url`（mapping 已有）
   - **previewUrl 业务 payload 不提供**：indexer 不写入 mapping（也不在 mapping 添加该字段）。octo-server 适配层返 `null`（A 文档 v4.1 §2.3 `preview_url string? (uri) 不支持时为 null` 已明示允许）。后续如需预览，业务侧增 payload.file.previewUrl 字段，或 octo-server 在 BFF 现场签发（不在本期范围）

4. case payloadTypeMergeForward(11)：完整解析
   - childCount = len(msgs)
   - 每个 msg 的 searchText 由 buildSearchText(child.payload) 生成
   - 顶层无 title 抽取（业务无可靠来源）

5. payload.reply 字段：本期忽略不抽（见 §2.2）

buildSearchText(payload) 规则：
   - type=1 (text)  → payload.content
   - type=2 (image) → payload.caption || payload.name || "[图片]"
   - type=3 (gif)   → "[GIF]"
   - type=4 (voice) → "[语音]"
   - type=5 (video) → payload.caption || "[视频]"   (视频通常无 caption，多数返 "[视频]")
   - type=8 (file)  → payload.caption || payload.name
   - type=11 (mergeForward) → "[聊天记录]"   (避免无限递归)
   - 其它 → ""（或 "[未知]"，不参与召回）
```

### 3.4 单测要求

新增 / 补：
- TestToDoc_MergeForward: childCount / msgs.searchText 拼接正确
- TestToDoc_ImageMediaFields: thumbUrl/width/height 写入
- TestToDoc_VideoDurationMs: 秒→毫秒转换
- TestToDoc_FileSizeAndExt: sizeBytes 与 ext（含 tar.gz 边界）

## 四、OS 全量 reindex（SRE 配合）

mapping 加字段不能就地改 OpenSearch 现有索引（`dynamic: strict`），必须 alias 双写 + 历史回填：

```
1. PUT _index_template/wukongim-messages（写入新版 mapping）
2. 创建新引导索引 wukongim-messages-000002（按 ISM rollover 命名规则递增）
3. 切换写别名 wukongim-messages-write 指向新索引（indexer 滚动重启后写新索引）
4. _reindex API 回填 wukongim-messages-000001 → wukongim-messages-000002
   - 历史数据需要重新跑 indexer 的 ToDoc 逻辑才能补出新字段（_reindex script 拿不到 payloadRaw 里的字段也补不出 mergeForward.msgs.searchText 等聚合字段）
   - 因此实际策略：写 reindex script 调 painless 从 payloadRaw 抽 → 或起一个一次性回填任务，从 Kafka 历史 topic 重放（更稳）
5. 抽样 diff 验证（旧索引 vs 新索引同 messageId 的字段差异）
6. 切读别名 wukongim-messages-read 指向新索引
7. 观察 24h
8. 老索引下线（保留 7 天）
9. 出问题回滚：读别名切回旧索引，30s 内见效
```

工作量：SRE 1d + 等待回填 1–3d（看历史数据量级）。runbook 抄 `wukongim-kafka-plugin/docs/specs/2026-05-28-octo-search.md` §III。

## 五、本期不做的项（避免范围蔓延）

| 项 | 现状 | 风险 |
|---|---|---|
| 撤回 / 编辑识别 | kafka-plugin 不识别撤回事件，indexer 不做 partial update | 撤回有效窗口仅 ISM hot 阶段 ~30 天；超 30 天的撤回搜索仍能命中。需产品明示可接受 |
| 流式消息合并 | 每条 MessageDTO 一篇文档 | 流式消息会被拆成多条搜出来，本期接受 |
| RichText(14) 富文本 | 不解析 | 富文本正文本期无法搜索；如需要后续追加 RichTextPayload |
| Location(6) / Card(7) / VectorSticker / EmojiSticker | 不解析 | 这几类不参与文本搜索（位置/名片不应该被搜，OK） |
| OCR / 视频转录 / 图片文件名抽取 | 不做 | A 文档 v4.1 _search_media 不接受 keyword，本来就不需要 |

## 六、与 octo-server 这侧的接口约定

- octo-server 4 个 search handler 直查 OpenSearch（不调任何 octo-search HTTP）
- 字段映射：
  - `message_kind` 由 octo-server 判：`payload.mergeForward != null → "forward"`；else `"text"`（不区分 quote）
  - `outer_preview.child_count` ← `payload.mergeForward.childCount`（仅 forward 分支返）
  - `snippet` ← OS highlight 输出（跨 `payload.text.content + payload.mergeForward.msgs.searchText + payload.image.caption + payload.file.name` 几个字段，单片段 120 字）
  - `sender_name / sender_avatar_url` ← octo-server 自己 user / group_member 表 JOIN（不依赖 ES）
  - 其余字段直接从 ES `_source` 抽
- **octo-server 不读 `payloadRaw`**：`payloadRaw` 仅侜 indexer 备查 / 调试（mapping 上是 `enabled:false`，不入倒排 / 不能做字段级投影，拼过网包拖成本高）。所有要返的字段 indexer 抽到 `payload.<type>.*` 结构化子对象，下游一律读结构化字段。

## 七、待研发同学确认的一点

**`mergeForward.msgs[].searchText` 是 indexer 在 ToDoc 时拼好，还是业务在发消息时已经塞好？**
- 推荐：indexer 在 ToDoc 时拼（buildSearchText 规则 §3.3），业务侧无侵入
- 草稿里 msgs 已有 searchText 字段，建议确认 indexer 自己拼，不依赖业务输入
