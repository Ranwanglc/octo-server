# 各类搜索 · OS 查询与响应字段拼接（临时速查）

> 目的：让你一眼看清每个端点怎么打 DSL、命中后从哪些字段拼回响应。基于 A 文档 v4.2 + 当前 indexer mapping。
>
> 准确度截止：`feat/messages-search` 0d5912f（2026-06-12）。再次大改 sort/cursor/sender_join 时同步本文。

字段命名约定：
- ES `_source` 字段用 v1.8 mapping 真实名（驼峰：`messageSeq / payload.video.cover / payload.file.size / payload.file.extension`）
- 响应给前端用 A 文档命名（snake_case：`message_seq / thumb_url / file_size_bytes`）
- octo-server 适配层做转换（含 `second*1000 → duration_ms` 单位换算）

公共：所有 4 个端点都挂 `Routing: channelId`（OS 索引 `_routing.required: true`）+ `MustNot revoked=true` + `MustNot payload.type=99`（cmd）。

---

## 1. POST /v1/messages/_search（文本 + 转发卡）

### OS 查询 DSL（伪代码）

```json
{
  "query": {
    "bool": {
      "must": [
        { "multi_match": {
            "query": "<keyword>",
            "fields": [
              "payload.text.content^3",
              "payload.image.caption", "payload.image.name",
              "payload.file.caption",  "payload.file.name",
              "payload.mergeForward.msgs.searchText"
            ]
        }}
      ],
      "filter": [
        { "term":  { "channelId": "<routed>" } },
        { "terms": { "from": ["<sender>", ...] } },        // 可选
        { "range": { "timestamp": { "gte":..., "lte":... } } } // 可选
      ],
      "must_not": [
        { "term": { "revoked": true } },
        { "term": { "payload.type": 99 } }
      ]
    }
  },
  "sort": [
    { "timestamp": "desc" },
    { "messageId": "desc" }
  ],
  "highlight": {
    "pre_tags": ["<mark>"], "post_tags": ["</mark>"],
    "fragment_size": 120, "number_of_fragments": 1,
    "fields": {
      "payload.text.content": {},
      "payload.mergeForward.msgs.searchText": {},
      "payload.image.caption": {},
      "payload.file.name": {}
    }
  },
  "search_after": [<ts>, <msgID>],   // 解 cursor
  "size": 20
}
```

`sort` 三种 case：
- `time_desc`（默认）→ `[timestamp desc, messageId desc]`
- `time_asc` → `[timestamp asc, messageId asc]`
- `relevance` → `[timestamp desc, _score desc, messageId desc]`

### 响应字段拼接（每条 hit）

| A 文档字段 | 来源 | 处理 |
|---|---|---|
| `message_id` | `_source.messageId` | int64 → string |
| `message_seq` | `_source.messageSeq` | uint64 → int64 |
| `message_kind` | `_source.payload.mergeForward != null ? "forward" : "text"` | 判别 |
| `snippet` | hit.highlight 任一字段命中片段 | 优先级：`text.content > mergeForward.msgs.searchText > image.caption > file.name` |
| `sender_id` | `_source.from` | 直接 |
| `sender_name` | octo-server JOIN `user/group_member` | LRU cache，按 channel_type 分群/DM |
| `sender_avatar_url` | 同上 | URL 模板拼接 |
| `sent_at` | `_source.timestamp` (epoch_seconds) | → RFC3339 |
| `outer_preview` | 仅 forward 分支：`{ child_count: _source.payload.mergeForward.childCount }`；text 分支返 null | 不返 title（业务无来源） |
| `channel_id` | 请求回显 | thread 拼 `{group_no}____{thread_short_id}` |

`payloadRaw` 不读。

---

## 2. POST /v1/messages/_search_media（图片+视频，无 keyword）

### OS 查询 DSL

```json
{
  "query": {
    "bool": {
      "filter": [
        { "term":  { "channelId": "<routed>" } },
        { "terms": { "payload.type": [2, 5] } },           // image=2, video=5
        { "terms": { "from": ["<sender>", ...] } },        // 可选
        { "range": { "timestamp": { "gte":..., "lte":... } } } // 可选
      ],
      "must_not": [
        { "term": { "revoked": true } }
      ]
    }
  },
  "sort": [{ "timestamp": "desc" }, { "messageId": "desc" }],
  "search_after": [<ts>, <msgID>],
  "size": 20
}
```

`keyword` 必空（端点级校验，传非空报 400）。

### 响应字段拼接

| A 文档字段 | 来源 | 处理 |
|---|---|---|
| `message_id` | `_source.messageId` | string |
| `message_seq` | `_source.messageSeq` | int64 |
| `media_kind` | `_source.payload.type` | 2→"image"，5→"video" |
| `thumb_url` | image 分支：永远空（v1.8 删除了 image.thumbUrl）；video 分支：`_source.payload.video.cover`（v1.8 删除了 video.thumbUrl） | image 不返 thumb；video 取 cover |
| `width` | `_source.payload.<image|video>.width` | int |
| `height` | 同上 | int |
| `duration_ms` | `_source.payload.video.second * 1000`（v1.8 indexer 写秒，BFF 换算 ms） | 仅 video 返；image 不返 |
| `sender_id` | `_source.from` | |
| `sender_name` | JOIN | |
| `sent_at` | `_source.timestamp` | → RFC3339 |
| `month_bucket` | 适配层从 `sent_at` 现场算 | 按用户时区算 `"YYYY-MM"` |

不返 snippet。

---

## 3. POST /v1/messages/_search_files（按文件名，keyword 可选）

### OS 查询 DSL

```json
{
  "query": {
    "bool": {
      "must": [
        // 仅 keyword 非空时挂 multi_match
        { "multi_match": {
            "query": "<keyword>",
            "fields": ["payload.file.name^2", "payload.file.caption"]
        }}
      ],
      "filter": [
        { "term":  { "channelId": "<routed>" } },
        { "term":  { "payload.type": 8 } },                // file
        { "terms": { "from": ["<sender>", ...] } },        // 可选
        { "range": { "timestamp": { "gte":..., "lte":... } } } // 可选
      ],
      "must_not": [
        { "term": { "revoked": true } }
      ]
    }
  },
  "sort": [{ "timestamp": "desc" }, { "messageId": "desc" }],
  "highlight": {                                          // 仅 keyword 非空时挂
    "pre_tags": ["<mark>"], "post_tags": ["</mark>"],
    "fragment_size": 120, "number_of_fragments": 1,
    "fields": { "payload.file.name": {} }
  },
  "search_after": [<ts>, <msgID>],
  "size": 20
}
```

keyword 空时跳过 `multi_match` + `highlight`，纯过滤查询；文件名前端自己处理高亮。

### 响应字段拼接

| A 文档字段 | 来源 | 处理 |
|---|---|---|
| `message_id` | `_source.messageId` | |
| `message_seq` | `_source.messageSeq` | |
| `file_name` | `_source.payload.file.name` | 不带 mark |
| `file_size_bytes` | `_source.payload.file.size`（v1.8：byte，long） | indexer 直存 payload.size |
| `file_ext` | `_source.payload.file.extension`（v1.8：原样不转小写，keyword） | 为空时 octo-server 现场切 file.name 兜底（也保留原样） |
| `download_url` | `_source.payload.file.url` | |
| `preview_url` | **本期返 null** | indexer 不写、A 文档允许 null |
| `sender_id / sender_name / sender_avatar_url` | JOIN | |
| `sent_at` | `_source.timestamp` | RFC3339 |

---

## 4. POST /v1/messages/_search_all（聚合 message + file 时间倒序）

### OS 查询 DSL

```json
{
  "query": {
    "bool": {
      "filter": [
        { "term":  { "channelId": "<routed>" } },
        { "terms": { "payload.type": [1, 8, 11] } }    // text(1) / file(8) / mergeForward(11)
      ],
      "must_not": [
        { "term": { "revoked": true } }
      ],
      "should": [
        { "multi_match": { "query": "<keyword>", "fields": [
            "payload.text.content^3",
            "payload.mergeForward.msgs.searchText"
        ]}},
        { "multi_match": { "query": "<keyword>", "fields": [
            "payload.file.name^2",
            "payload.file.caption"
        ]}}
      ],
      "minimum_should_match": 1
    }
  },
  "sort": [{ "timestamp": "desc" }, { "messageId": "desc" }],
  "highlight": {
    "pre_tags": ["<mark>"], "post_tags": ["</mark>"],
    "fragment_size": 120, "number_of_fragments": 1,
    "fields": {
      "payload.text.content": {},
      "payload.mergeForward.msgs.searchText": {},
      "payload.file.name": {}
    }
  },
  "search_after": [<ts>, <msgID>],
  "size": 20
}
```

注：图片 / 视频 / 语音 / GIF（type=2/3/4/5）不混入此端点（A 文档 §2.4：media 不接受 keyword，单独走 _search_media）。

### 响应字段拼接

判别式 + 嵌套两子对象：

```
result_type = (_source.payload.type == 8) ? "file" : "message"
sorted_at   = msToRFC3339(_source.timestamp)
```

- `result_type == "message"` → 内层 `message: {...}` 字段集 = §1 全字段
- `result_type == "file"` → 内层 `file: {...}` 字段集 = §3 全字段

实现上：octo-server 拿到混合 hit 列表后，按 `payload.type` 分别走 `buildMessageHit` / `buildFileHit` helper（这两个 helper 在 §1 和 §3 已经写过，复用），最后按 sorted_at 时间序统一返。

---

## 5. 跨端点共用：sender JOIN

### 输入
- 一页搜索结果（≤100 条）的 `sender_id` 集合
- channel 上下文（决定查群成员还是用户表）

### 流程
1. 去重 sender_ids（一页通常 N ≪ 100 个不同人）
2. 群消息（channelType=2/5）：调 `groupService.GetMembers(groupNo)` → 拿成员显示名 + avatar
3. DM（channelType=1）：调 `userService.GetUsers(senderIDs)` → 拿用户显示名；avatar 按 `users/{uid}/avatar` 模板拼
4. LRU cache（10K 容量 / 5 分钟 TTL）按 uid 缓存，命中率监控

### 输出
- `name_map[uid] → string`
- `avatar_map[uid] → url`

每条 hit 填 `sender_name = name_map[hit.sender_id]`、`sender_avatar_url = avatar_map[hit.sender_id]`。

---

## 6. 跨端点共用：cursor 编解码

cursor 是不透明 base64，三元组的形状随 sort 模式：
- `time_desc` / `time_asc` → `{ts, msg_id}`（int64 / int64）
- `relevance` → `{ts, score, msg_id}`（int64 / float64 / int64，score 是 OS `_score` 浮点数）

```
encode: base64.RawURLEncoding(json.Marshal(payload)) ⊕ HMAC-SHA256 签名
decode: 反向；签名 / json / 形状错误 → 400 VALIDATION_ERROR field=cursor
```

OS 查询用 `search_after`：
- time_*：`[ts, msg_id]`（与 sort 二元组一致）
- relevance：`[ts, score, msg_id]`（与 sort 三元组一致）

跨 sort 模式翻页（如 cursor 是 time_desc 取的，下页换 relevance）会触发 `stale_cursor_format`，前端要重新拉首页。

---

## 7. 跨端点共用：channel 反向映射

请求体 `channel_type / channel_id` → OS `channelId` 字段值：

| 类型 | 输入 | 转换 |
|---|---|---|
| 1 (p2p) | `channel_id` 是对端 uid | `channelId = common.GetFakeChannelIDWith(loginUID, peerUid)` |
| 2 (group) | `channel_id` 是 `group_no` | 直接用 |
| 5 (thread) | `channel_id` 已是 `{group_no}____{thread_short_id}` 拼接串 | 直接用 |

OS 查询带 `Routing(channelId)` 命中单 shard，性能最优。

---

## 8. 错误码映射

| 触发 | HTTP | R2 enum |
|---|---|---|
| OS 网络/超时/5xx | 503 | UPSTREAM_UNAVAILABLE |
| OS 4xx (DSL 错) | 500 | INTERNAL_ERROR（不暴露） |
| 入参校验（keyword 长度/必填、cursor 解码、时间格式、sender_ids 超 50、_search_media 传 keyword、page_size 超界、channel_type/id 形态不匹配） | 400 | VALIDATION_ERROR + details.{field, reason, max_length?} |
| 限流 5 QPS / 20 桶 | 429 | RATE_LIMITED |
| 中间件鉴权失败 | 401 / 403 | AUTH_REQUIRED / FORBIDDEN |
| 频道不存在 / Space 不可见 | 404 | NOT_FOUND |
| 兜底 | 500 | INTERNAL_ERROR |

成功包络：`{ "data": [...], "pagination": { "has_more", "next_cursor" } }`

失败包络：`{ "error": { "code", "message", "details", "hint" } }`

`X-Server-Time-Ms` 响应头：服务端处理耗时（不进 body）。

---

## 9. 一句话总结每端点

- **_search**：keyword 跨 5 个文本字段召回 → 命中 → JOIN sender → text/forward 分流 → 返 outer 概要
- **_search_media**：filter type IN [image,video] → 时间倒序 → 取媒体尺寸/缩略图字段 → 不带 snippet
- **_search_files**：filter type=file → keyword 可选 multi_match name+caption → 取文件元信息 → preview_url 永远 null
- **_search_all**：should[文本字段, 文件名] + filter type IN [text,forward,file] → 单游标时间倒序 → result_type 判别式分流
