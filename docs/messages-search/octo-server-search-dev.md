# octo-server 搜索功能开发文档

> 实现 A 文档 v4.1 的 4 个搜索端点。架构：octo-server 直查 OpenSearch（wukongim-message-indexer 写入），用自有 user / group_member 表 JOIN sender 信息。本文档只覆盖 octo-server 仓库内的开发工作，配套 indexer-os-changes.md 的 mapping 升级并行推进。

---

## 0. 范围与依赖

### 范围内
- 4 个新端点 handler：
  - `POST /v1/messages/_search`
  - `POST /v1/messages/_search_media`
  - `POST /v1/messages/_search_files`
  - `POST /v1/messages/_search_all`
- OpenSearch client 单例 + 配置
- 共享层：envelope / 错误码映射 / cursor 编解码 / sender JOIN（含 LRU）/ channel_id 拼接 / time 转换 / 校验 / 限流 / 审计日志 / swag

### 不在范围内
- indexer / OS mapping 改动（见 `indexer-os-changes.md`）
- octo-search 仓库（HTTP 层退役，本期不动）
- 现有 `/v1/search/global`（保留并存，老 client 不变；定义在 `modules/search/api.go:42-47`）
- 现有 `/v1/groups/:no/messages/:id` 单条获取迁 OS（属于"逐步放弃 messages 表"下一期）
- 撤回 / 编辑 partial update（本期接受撤回窗口仅 ISM hot ~30 天）

### 强依赖（前置条件）
- indexer mapping v2 已上线（见 `indexer-os-changes.md` §二/§三/§四）；本期实现可与 mapping 改造**并行**，先用 fixture 跑端到端
- SRE 给 octo-server 服务一个 OpenSearch 读账号 + 网络白名单（连 `wukongim-messages-read` 别名）

---

## 1. 现有代码资产清点（octo-server test 分支）

| 文件 / 包 | 用途 | 复用方式 |
|---|---|---|
| `modules/search/api.go` (544 行) | `/v1/search/global` 透传 wukongim usersearch | **拆**：`buildMessageSearchQuery` (`api.go:54-62`) 替换成自拼 OS DSL；`respondSearchRequestInvalid` (`api_i18n.go:10`) 复用 R2 错误码渲染；handler 框架抄成 4 新 handler |
| `modules/message/api.go::Route` (`api.go:281-318`) | 路由组挂载范本；现已有 `/v1/message` (line 289) 与 `/v1/messages` (line 314) 两个 group | 直接挂到现有 `/v1/messages` group（line 314-318），共享 `AuthMiddleware + uidLimit`；本期再加 `spacepkg.SpaceMiddleware` |
| `modules/message/api_channel_files.go` (504 行) | 按会话列文件 + payload 解析 helper（`payloadInt` / `payloadInt64` / `categoryFromFilename`） | `_search_files` 思路参考；payload 解析 helper 可借（但本期不解 payload bytes，从 ES `_source` 抽） |
| `modules/group/service.go::GetMembers` | 按 groupNo 查全部群成员（含 remark） | sender_name JOIN（群 / Thread 分支） |
| `modules/user/service.go::GetUsers` (`service.go:1016-1031`) | 按 uids 批量查 user | DM sender JOIN |
| `pkg/httperr/respond.go::ResponseErrorL` (`respond.go:22-`) | R2 错误码渲染（i18n 文案） | 复用，加 R2 12 项 enum 到 `errcode` 包 |
| `pkg/space/middleware.go::SpaceMiddleware` (`middleware.go:99`) | 鉴权后 Space 过滤（写入 validated `spaceID` 到 gin ctx） | 复用 |
| `modules/search/api.go::collectChannelIDs` (`api.go:519-544`) + `common.GetFakeChannelIDWith` | p2p / group / thread channel id 算法 | 复用；4 新 handler 反向用：业务 channel_id → OS `channelId` 字段 |
| `go.mod` (`go.mod:34`) `github.com/olivere/elastic v6.2.37+incompatible` | OS client 库 | 复用（与 OpenSearch 1.x / ES 6.x 兼容） |
| `modules/base/elastic/{db.go,service.go}` (35 + 61 = 96 行半成品骨架，`IndexerErrorModel` + `PushMessageElasticIndexTask`) | 历史遗留（octo-server 写索引方向已废） | **本期删除**（octo-server 不写索引） |

---

## 2. 项目骨架

放 `modules/messages_search/`（与现有 `modules/search/` 区分；新模块名避免与已有 `/v1/search/global` 路由组冲突）：

```
modules/messages_search/
├── 1module.go              # 模块注册（按 octo-server 多模块 1module 约定）
├── api.go                  # 4 handler 入口；Route 挂载 / DI 装配
├── search_messages.go      # POST /v1/messages/_search
├── search_media.go         # POST /v1/messages/_search_media
├── search_files.go         # POST /v1/messages/_search_files
├── search_all.go           # POST /v1/messages/_search_all
├── es_client.go            # OpenSearch client 单例 + ping
├── dsl.go                  # DSL helper（multi_match / filter / highlight / sort）
├── source.go               # _source 反序列化（Doc / Payload 等结构）
├── cursor.go               # base64 cursor + HMAC
├── envelope.go             # R1 包络（或复用 octo-lib pkg/envelope）
├── errcode.go              # OS 错误 → R2 12 项 enum 映射
├── sender_join.go          # sender_name / avatar 批量 JOIN（含 LRU）
├── channel.go              # channel_type/id 反向映射
├── validate.go             # 入参校验
├── ratelimit.go            # 5 QPS / 20 桶 per loginUID
├── audit.go                # PRM-02 审计日志
├── i18n.go                 # 错误消息中英文（仿 modules/search/api_i18n.go）
├── util.go                 # 时间格式 / hash 等
└── swagger/                # swag 注释 + types
```

模块在 `cmd/server/main.go`（或现有模块注册入口）按 octo-server 既有方式注册。`api.go::Route` 把 4 handler 挂到 `/v1/messages` group：实操见 §5.1.1。

---

## 3. 配置 schema

octo-lib `config.Config` 加 `SearchConfig`（在 octo-lib 仓库，不在 octo-server 仓库）：

```go
type SearchConfig struct {
    OSAddrs     []string      `yaml:"os_addrs"`      // 多节点（同 wukongim-indexer 的 OS_ADDRS）
    OSUsername  string        `yaml:"os_username"`
    OSPassword  string        `yaml:"os_password"`
    OSReadAlias string        `yaml:"os_read_alias"` // 默认 "wukongim-messages-read"
    Timeout     time.Duration `yaml:"timeout"`       // 默认 5s
    RateLimit   RateLimitCfg  `yaml:"rate_limit"`    // 默认 5 QPS / 20 桶
    CursorHMAC  string        `yaml:"cursor_hmac"`   // base64，cursor 防篡改密钥
}

type RateLimitCfg struct {
    QPS   float64 `yaml:"qps"`   // 默认 5
    Burst int     `yaml:"burst"` // 默认 20
}
```

`octo.yaml` 示例：

```yaml
search:
  os_addrs: ["http://os1:9200", "http://os2:9200"]
  os_username: octo_search_ro
  os_password: ${OS_PASSWORD}
  os_read_alias: wukongim-messages-read
  timeout: 5s
  rate_limit:
    qps: 5
    burst: 20
  cursor_hmac: ${SEARCH_CURSOR_HMAC}
```

---

## 4. ES client 单例（es_client.go）

```go
package messages_search

import (
    "context"
    "sync"
    "time"

    "github.com/olivere/elastic"
    "github.com/Mininglamp-OSS/octo-lib/config"
)

var (
    osOnce   sync.Once
    osClient *elastic.Client
    osErr    error
)

func ESClient(cfg config.SearchConfig) (*elastic.Client, error) {
    osOnce.Do(func() {
        opts := []elastic.ClientOptionFunc{
            elastic.SetURL(cfg.OSAddrs...),
            elastic.SetSniff(false), // 容器网络下关闭 sniff
            elastic.SetHealthcheck(true),
            elastic.SetHealthcheckTimeout(3 * time.Second),
        }
        if cfg.OSUsername != "" {
            opts = append(opts, elastic.SetBasicAuth(cfg.OSUsername, cfg.OSPassword))
        }
        c, err := elastic.NewClient(opts...)
        if err != nil {
            osErr = err
            return
        }
        // 启动期 ping，检验配置 / 网络
        ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
        defer cancel()
        if _, _, err := c.Ping(cfg.OSAddrs[0]).Do(ctx); err != nil {
            osErr = err
            return
        }
        osClient = c
    })
    return osClient, osErr
}
```

注意：olivere/elastic v6 的 client 内部已是并发安全（带连接池），单例即可。

---

## 5. 4 端点 handler 实现细节

### 5.1 POST /v1/messages/_search（search_messages.go）

#### 5.1.1 路由

挂到 `modules/message/api.go::Route` 现有 `/v1/messages` group（line 314-318），追加 `SpaceMiddleware`。或在新模块 `modules/messages_search` 自起 group：

```go
// modules/messages_search/api.go
func (h *Handler) Route(r *wkhttp.WKHttp) {
    g := r.Group("/v1/messages",
        h.ctx.AuthMiddleware(r),
        appwkhttp.SharedUIDRateLimiter(r, h.ctx),
        spacepkg.SpaceMiddleware(h.ctx),
        h.searchRateLimiter(), // 5 QPS / 20 桶 per loginUID
        h.auditMiddleware(),
    )
    g.POST("/_search",        h.searchMessages)
    g.POST("/_search_media",  h.searchMedia)
    g.POST("/_search_files",  h.searchFiles)
    g.POST("/_search_all",    h.searchAll)
}
```

#### 5.1.2 请求体 → DSL

请求体（与 A 文档 §1.8 + §2.1 对齐）：

```go
type SearchMessagesReq struct {
    ChannelType uint8             `json:"channel_type"` // 1 / 2 / 5
    ChannelID   string            `json:"channel_id"`
    Keyword     string            `json:"keyword"`      // 必填非空
    Filters     SearchFilters     `json:"filters,omitempty"`
    Sort        string            `json:"sort,omitempty"` // time_desc / time_asc / relevance
    PageSize    int               `json:"page_size,omitempty"`
    Cursor      string            `json:"cursor,omitempty"`
}

type SearchFilters struct {
    SenderIDs  []string `json:"sender_ids,omitempty"`
    SentAtFrom string   `json:"sent_at_from,omitempty"`
    SentAtTo   string   `json:"sent_at_to,omitempty"`
}
```

DSL 构造（olivere/elastic v6）：

```go
func buildSearchDSL(req SearchMessagesReq, loginUID string) elastic.Query {
    b := elastic.NewBoolQuery()

    // multi_match 跨字段召回（前提：indexer mapping v2 已上 mergeForward 字段）
    b.Must(elastic.NewMultiMatchQuery(req.Keyword,
        "payload.text.content^3",
        "payload.image.caption", "payload.image.name",
        "payload.file.caption",  "payload.file.name",
        "payload.mergeForward.msgs.searchText",
    ))

    // 频道精确过滤（routing 同字段）
    b.Filter(elastic.NewTermQuery("channelId", normalizedChannelID(req, loginUID)))

    // P0 修复（2026-06-13，feat/messages-search PR #361）：p2p 跨 Space 隔离
    // group(2)/thread(5) 的 channel_id 已隐含 Space，不再叠加 spaceId filter；
    // 仅 channel_type=1 (p2p) 走 spaceId term filter。spaceID 来自 SpaceMiddleware
    // 设入 ctx 的 `space_id`（X-Space-ID / ?space_id=），handler 在 checkChannelAccess
    // 之后调 resolveP2PSpaceScope —— 没有 spaceID 时若 RequireSpaceID=true（默认）
    // 直接 NOT_FOUND（fail-closed），关闭则跳过 filter 并 WARN 一条日志（仅 indexer
    // rollout 过渡期使用）。
    if req.ChannelType == 1 && spaceID != "" {
        b.Filter(elastic.NewTermQuery("spaceId", spaceID))
    }

    // sender 过滤
    if len(req.Filters.SenderIDs) > 0 {
        terms := make([]interface{}, len(req.Filters.SenderIDs))
        for i, s := range req.Filters.SenderIDs {
            terms[i] = s
        }
        b.Filter(elastic.NewTermsQuery("from", terms...))
    }

    // 时间窗（含起含止；按用户时区扩展）
    if req.Filters.SentAtFrom != "" || req.Filters.SentAtTo != "" {
        rng := elastic.NewRangeQuery("timestamp")
        if from := parseSentAt(req.Filters.SentAtFrom, true); from > 0 {
            rng = rng.Gte(from)
        }
        if to := parseSentAt(req.Filters.SentAtTo, false); to > 0 {
            rng = rng.Lte(to)
        }
        b.Filter(rng)
    }

    // 硬过滤：撤回 + system / cmd 类
    b.MustNot(elastic.NewTermQuery("revoked", true))
    b.MustNot(elastic.NewTermQuery("payload.type", 99))

    return b
}

func buildSearch(client *elastic.Client, req SearchMessagesReq, idx, loginUID string) *elastic.SearchService {
    s := client.Search().
        Index(idx).
        Routing(normalizedChannelID(req, loginUID)).
        Query(buildSearchDSL(req, loginUID))

    // sort
    switch req.Sort {
    case "time_asc":
        s = s.SortBy(elastic.NewFieldSort("timestamp").Asc(),
            elastic.NewFieldSort("messageId").Asc())
    case "relevance":
        s = s.SortBy(elastic.NewScoreSort(),
            elastic.NewFieldSort("timestamp").Desc())
    default: // time_desc
        s = s.SortBy(elastic.NewFieldSort("timestamp").Desc(),
            elastic.NewFieldSort("messageId").Desc())
    }

    // highlight
    hl := elastic.NewHighlight().
        PreTags("<mark>").PostTags("</mark>").
        FragmentSize(120).NumOfFragments(1).
        Field("payload.text.content").
        Field("payload.mergeForward.msgs.searchText").
        Field("payload.image.caption").
        Field("payload.file.name")
    s = s.Highlight(hl)

    // search_after cursor
    if req.Cursor != "" {
        ts, msgID, _ := decodeCursor(req.Cursor)
        s = s.SearchAfter(ts, msgID)
    }

    s = s.Size(clamp(req.PageSize, 20, 100)).TrackTotalHits(false)
    return s
}
```

#### 5.1.3 响应映射（_source → A 文档字段）

```go
items := make([]MessageHit, 0, len(result.Hits.Hits))
for _, hit := range result.Hits.Hits {
    var doc Doc
    if err := json.Unmarshal(hit.Source, &doc); err != nil {
        continue // 单条 _source 异常不致整页 5xx；记日志
    }
    items = append(items, MessageHit{
        MessageID:    strconv.FormatInt(doc.MessageID, 10),
        MessageSeq:   int64(doc.MessageSeq),
        MessageKind:  classifyKind(doc.Payload),
        Snippet:      pickSnippet(hit.Highlight),
        SenderID:     doc.From,
        SentAt:       msToRFC3339(doc.Timestamp),
        OuterPreview: buildOuterPreview(doc.Payload),
        ChannelID:    encodeChannelID(req),
    })
}

// 批量 sender JOIN
senderIDs := uniqSenders(items)
nameMap, avatarMap := senderJoin(ctx, senderIDs, req.ChannelID, req.ChannelType)
for i := range items {
    items[i].SenderName      = nameMap[items[i].SenderID]
    items[i].SenderAvatarURL = avatarMap[items[i].SenderID]
}

// 分页
hasMore := len(items) == clamp(req.PageSize, 20, 100)
var nextCursor string
if hasMore {
    last := result.Hits.Hits[len(result.Hits.Hits)-1]
    nextCursor = encodeCursor(extractSortKey(last))
}
c.Response(envelope.CursorList[MessageHit]{
    Data: items,
    Pagination: envelope.Pagination{HasMore: hasMore, NextCursor: nextCursor},
})
```

#### 5.1.4 message_kind 判别

```go
func classifyKind(p *Payload) string {
    switch {
    case p == nil:
        return "text"
    case p.MergeForward != nil:
        return "forward"
    default:
        return "text"
    }
}
```

不区分 quote（reply）类型。本期外层正文召回已能定位原消息，不需要为引用消息额外处理。

#### 5.1.5 outer_preview 构造

```go
func buildOuterPreview(p *Payload) *OuterPreview {
    if p == nil {
        return nil
    }
    if p.MergeForward != nil {
        return &OuterPreview{
            ChildCount: p.MergeForward.ChildCount,
        }
    }
    return nil // text 分支不返；quote 本期不处理
}
```

forward 分支仅返 `child_count`，不返 `title`（业务无可靠 title 来源，文案由前端写死）。

#### 5.1.6 错误码映射

OS 错误 / 业务条件 → R2 12 项 enum，详见 §8。

#### 5.1.7 可见性 post-filter（P0 修复 / 2026-06-13）

handler 在 `buildXxxHits` 之前插入 `paginateWithFilter` 闭包：每轮 OS 拉 `pageSize × 3` 命中，按 4 维信号（revoke / global delete / user delete / channel offset）过滤，循环最多 3 轮，直到收齐 `pageSize` 或 OS 没有更多结果或预算用尽。详见 `visibility.go::filterVisible` 实现注释。

```
┌──────────────┐
│ /_search_xxx │
└──────┬───────┘
       ▼
┌──────────────┐  ┌────────────────┐  ┌────────────────┐
│ paginate     │─▶│ OS 一轮拉取    │─▶│ filterVisible  │
│ WithFilter   │  │ size = page×3  │  │ 4 维 MySQL 过滤 │
└──────┬───────┘  └────────────────┘  └────────┬───────┘
       │                                       ▼
       │   是   ┌─────────────────┐     ┌──────────────┐
       ├──────│ 收齐 pageSize？ │◀────│ 拼到 collected│
       │       └────────┬────────┘     └──────────────┘
       │                │ 否
       │                ▼
       │       ┌─────────────────┐
       │       │ OS 还有 / 预算  │── 否 ─┐
       │       │ 还剩？          │       │
       │       └────────┬────────┘       │
       │                │ 是             │
       │                └────回循环      │
       │                                 │
       ▼                                 ▼
   buildHits + senderJoin    has_more / cursor 编码
```

**过取补偿语义（cursor 不变）**：cursor schema 仍是 OS 真实 last hit 的 (ts, msgID, score?)，前端无感。`has_more` 在以下两种情况为 true：
1. **截断**：filter 后的 collected > pageSize，cursor 锚在 `collected[pageSize-1]`，本页之后还有同轮残余
2. **预算耗尽**：3 轮过后仍未填满，cursor 锚在最后一轮 OS last hit

cursor 取的是 OS hit 的 sort tuple（不是 collected[末尾] 的 sort），保证下一页从 OS 真实位置继续，不会跳过被 filter 掉的命中范围。

**fail-closed**：`filterVisible` 任意 IService 调用出错 → 直接返 INTERNAL_ERROR，不会"放过去"。这是搜索路径与读路径的对等保证。

#### 5.1.8 工作量
**1.5d**（DSL + 响应映射 + 单测）

---

### 5.2 POST /v1/messages/_search_media（search_media.go）

差异（vs `_search`）：

- DSL：`bool.filter(payload.type IN [2, 5])`（image=2, video=5；按 octo-lib `common.ContentType`）
- 不挂 multi_match：`keyword` 必空，否则 400 `VALIDATION_ERROR`
- 响应字段：`thumb_url / width / height / duration_ms / media_kind / month_bucket`（适配层算 `YYYY-MM`）
- `payload.type` → `media_kind` 映射：2→"image"，5→"video"
- `image.thumb_url` 一律空（v1.8 mapping 删除了 image.thumbUrl）
- `video.thumb_url` 直取 `video.cover`（v1.8 mapping 删除了 video.thumbUrl）
- `duration_ms` 由 BFF 算：`int64(video.second) * 1000`（v1.8 indexer 用 `second`/秒，不再写 `durationMs`）
- sort 拒 `relevance`（无 keyword 无评分）

DSL 关键片段：

```go
b := elastic.NewBoolQuery()
b.Filter(elastic.NewTermQuery("channelId", normalizedChannelID(req, loginUID)))
b.Filter(elastic.NewTermsQuery("payload.type", 2, 5))
b.MustNot(elastic.NewTermQuery("revoked", true))
// sender / time 过滤同 §5.1.2
```

响应映射：

```go
func buildMediaHit(doc Doc) MediaHit {
    h := MediaHit{
        MessageID:  strconv.FormatInt(doc.MessageID, 10),
        MessageSeq: int64(doc.MessageSeq),
        SenderID:   doc.From,
        SentAt:     msToRFC3339(doc.Timestamp),
        MonthBucket: time.Unix(int64(doc.Timestamp), 0).UTC().Format("2006-01"),
    }
    if doc.Payload != nil && doc.Payload.Type != nil {
        switch *doc.Payload.Type {
        case 2:
            if doc.Payload.Image != nil {
                h.MediaKind = "image"
                // v1.8: image has no thumb concept; thumb_url stays empty.
                h.Width  = doc.Payload.Image.Width
                h.Height = doc.Payload.Image.Height
            }
        case 5:
            if doc.Payload.Video != nil {
                h.MediaKind  = "video"
                h.ThumbURL   = doc.Payload.Video.Cover
                h.Width      = doc.Payload.Video.Width
                h.Height     = doc.Payload.Video.Height
                h.DurationMs = int64(doc.Payload.Video.Second) * 1000
            }
        }
    }
    return h
}
```

**工作量 0.75d**

---

### 5.3 POST /v1/messages/_search_files（search_files.go）

差异（vs `_search`）：

- DSL：filter `payload.type=8`（file）
- `keyword` 可选：传则 `multi_match` on `payload.file.name + payload.file.caption`，不传跳过 multi_match（变纯过滤查询）
- 响应字段：`file_name / file_size_bytes / file_ext / download_url / preview_url`（A 文档 §2.3 无 `file_name_marked`）
- **`file_ext` 业务 payload 顶层直接有 `extension` 字段**（实例：`{"extension":"mp4","name":"videodemo.mp4","size":23014356,"type":8}`），indexer 抽入 `payload.file.extension`（v1.8 字段名，原样不转小写），octo-server 直读 `FilePayload.Ext`。不需要从 file.name 切割。边界兜底：如 `Ext` 为空字符串（老数据 / 业务未填）才从 name 切割
- **`file_size_bytes` 由 `payload.file.size`（v1.8 字段名，byte）映射**，octo-server 仍用 `FilePayload.SizeBytes` 取值（json tag = `size`），wire 名 `file_size_bytes` 不变
- **`preview_url` 本期返 null**：业务 payload 不提供预览链接，indexer 不写入索引。A 文档 §2.3 已明示 `preview_url string? (uri) 不支持时为 null`，适配层直接返 null

DSL 关键片段：

```go
b := elastic.NewBoolQuery()
b.Filter(elastic.NewTermQuery("channelId", normalizedChannelID(req, loginUID)))
b.Filter(elastic.NewTermQuery("payload.type", 8))
b.MustNot(elastic.NewTermQuery("revoked", true))
if req.Keyword != "" {
    b.Must(elastic.NewMultiMatchQuery(req.Keyword,
        "payload.file.name^2",
        "payload.file.caption",
    ))
}
```

`file_ext` 兜底：

```go
func resolveFileExt(f *FilePayload) string {
    if f.Ext != "" {
        return f.Ext // v1.8: indexer 已是原样，无需 ToLower
    }
    if f.Name == "" {
        return ""
    }
    return strings.TrimPrefix(filepath.Ext(f.Name), ".")
}
```

**工作量 0.75d**

---

### 5.4 POST /v1/messages/_search_all（search_all.go）

差异（vs `_search`）：

- DSL：`should[multi_match(text+plain+forward), multi_match(file.name+caption)] + filter[payload.type IN {1, 8, 11}]`（text / file / mergeForward）
- 响应字段：`result_type` 判别式（`payload.type` → "message" / "file"）+ 嵌套 `message` / `file` 子对象 + `sorted_at`（=内层 `sent_at`）

DSL 关键片段：

```go
b := elastic.NewBoolQuery()
b.Filter(elastic.NewTermQuery("channelId", normalizedChannelID(req, loginUID)))
b.Filter(elastic.NewTermsQuery("payload.type", 1, 8, 11))
b.MustNot(elastic.NewTermQuery("revoked", true))

// should 必须至少命中一个
should := []elastic.Query{
    elastic.NewMultiMatchQuery(req.Keyword,
        "payload.text.content^3",
        "payload.mergeForward.msgs.searchText",
    ),
    elastic.NewMultiMatchQuery(req.Keyword,
        "payload.file.name^2",
        "payload.file.caption",
    ),
}
b.Should(should...).MinimumShouldMatch("1")
```

响应映射：

```go
func buildSearchAllHit(doc Doc, hl elastic.SearchHitHighlight) SearchAllHit {
    h := SearchAllHit{SortedAt: msToRFC3339(doc.Timestamp)}
    if doc.Payload != nil && doc.Payload.Type != nil && *doc.Payload.Type == 8 {
        h.ResultType = "file"
        h.File = buildFileHit(doc, hl)
        h.SortedAt = h.File.SentAt
    } else {
        h.ResultType = "message"
        h.Message = buildMessageHit(doc, hl)
        h.SortedAt = h.Message.SentAt
    }
    return h
}
```

**工作量 0.5d**

---

## 6. 共享层

### 6.1 envelope（envelope.go）

R1 包络：

```go
type CursorList[T any] struct {
    Data       []T        `json:"data"`
    Pagination Pagination `json:"pagination"`
}
type Pagination struct {
    HasMore    bool   `json:"has_more"`
    NextCursor string `json:"next_cursor,omitempty"`
}
type Error struct {
    Error ErrorBody `json:"error"`
}
type ErrorBody struct {
    Code    string         `json:"code"`     // R2 12 项 enum
    Message string         `json:"message"`
    Details map[string]any `json:"details,omitempty"`
    Hint    string         `json:"hint,omitempty"`
}
```

octo-lib 已有 `pkg/envelope` 包（`octo-search-integration` §B 提到），优先复用：`envelope.Data[T] / envelope.CursorList[T] / envelope.Error / envelope.EmptyResp`。若 octo-lib 该包字段与 R1 不一致，先在 octo-lib 提 PR 对齐再用。

工作量：**0.5d**（含错误码映射 helper）

### 6.2 sender JOIN + LRU cache（sender_join.go）

```go
import lru "github.com/hashicorp/golang-lru/v2"

type senderInfo struct {
    Name   string
    Avatar string
}

var senderCache, _ = lru.New[string, senderInfo](10000)
const senderCacheTTL = 5 * time.Minute

func (h *Handler) senderJoin(
    ctx context.Context, uids []string,
    channelID string, channelType uint8,
) (nameMap, avatarMap map[string]string) {
    nameMap   = make(map[string]string, len(uids))
    avatarMap = make(map[string]string, len(uids))

    var miss []string
    for _, uid := range uids {
        if v, ok := senderCache.Get(uid); ok {
            nameMap[uid]   = v.Name
            avatarMap[uid] = v.Avatar
            continue
        }
        miss = append(miss, uid)
    }
    if len(miss) == 0 {
        return
    }

    switch channelType {
    case 2, 5: // group / thread → 用 group_member.remark 优先，回退 user.name
        groupNo := groupNoFromChannel(channelID, channelType)
        members, _ := h.groupService.GetMembers(groupNo)
        // members 是 *MemberResp，含 UID / Remark；name 缺则再 GetUsers 兜底
        gotByUID := make(map[string]*group.MemberResp, len(members))
        for _, m := range members {
            gotByUID[m.UID] = m
        }
        // 回填 user.name + avatar 给所有 miss
        users, _ := h.userService.GetUsers(miss)
        for _, u := range users {
            name := u.Name
            if m, ok := gotByUID[u.UID]; ok && m.Remark != "" {
                name = m.Remark
            }
            avatar := buildUserAvatarURL(u.UID) // 按 modules/user/1module.go:191 模板拼
            senderCache.Add(u.UID, senderInfo{Name: name, Avatar: avatar})
            nameMap[u.UID]   = name
            avatarMap[u.UID] = avatar
        }
    case 1: // p2p：直接 GetUsers
        users, _ := h.userService.GetUsers(miss)
        for _, u := range users {
            avatar := buildUserAvatarURL(u.UID)
            senderCache.Add(u.UID, senderInfo{Name: u.Name, Avatar: avatar})
            nameMap[u.UID]   = u.Name
            avatarMap[u.UID] = avatar
        }
    }
    return
}
```

注意：
- LRU 用 `golang-lru/v2`（无 TTL，需在 entry 加时间戳并惰性失效；或换 `ristretto`）。简化：5 分钟 TTL 通过 `time.AfterFunc` 异步剔除即可
- 命中率统计：用 `prometheus.Counter` 暴露 `octo_search_sender_cache_{hits,miss}_total`
- 群消息 sender JOIN 已切到 `groupService.GetMembers(groupNo)`：单群单页 ≤100 个 senders，可见性已由 Space middleware 拒掉非成员，不需要再走 (loginUID, groupNos) 过滤路径。

工作量：**1d**（含 cache + 单测）

### 6.3 cursor 编解码（cursor.go）

`search_after` 用 `[timestamp_seconds, messageId]` 做不透明 cursor，HMAC-SHA256 防篡改：

```go
type cursorPayload struct {
    TS    int64 `json:"ts"`
    MsgID int64 `json:"id"`
}

func encodeCursor(ts, msgID int64) string {
    p := cursorPayload{TS: ts, MsgID: msgID}
    body, _ := json.Marshal(p)
    mac := hmac.New(sha256.New, hmacKey())
    mac.Write(body)
    sig := mac.Sum(nil)[:8] // 截 8 字节足够
    return base64.RawURLEncoding.EncodeToString(append(body, sig...))
}

func decodeCursor(s string) (ts, msgID int64, err error) {
    raw, err := base64.RawURLEncoding.DecodeString(s)
    if err != nil || len(raw) < 9 {
        return 0, 0, errors.New("cursor: malformed")
    }
    body, sig := raw[:len(raw)-8], raw[len(raw)-8:]
    mac := hmac.New(sha256.New, hmacKey())
    mac.Write(body)
    if !hmac.Equal(mac.Sum(nil)[:8], sig) {
        return 0, 0, errors.New("cursor: bad signature")
    }
    var p cursorPayload
    if err := json.Unmarshal(body, &p); err != nil {
        return 0, 0, errors.New("cursor: unmarshal")
    }
    return p.TS, p.MsgID, nil
}
```

cursor 失效 → 400 `VALIDATION_ERROR`，`details.field="cursor"`。

工作量：**0.5d**

### 6.4 channel 反向映射（channel.go）

```go
// 输入业务 channel_type (1/2/5) + channel_id
// 输出 OS 文档里 channelId 字段值

func normalizedChannelID(req SearchMessagesReq, loginUID string) string {
    switch req.ChannelType {
    case 1: // p2p：channel_id 是对端 uid，反向算 fakeChannelID
        return common.GetFakeChannelIDWith(loginUID, req.ChannelID)
    case 2: // group：channel_id 就是 group_no
        return req.ChannelID
    case 5: // thread：channel_id 已经是 "{groupNo}____{threadShortID}" 拼接形态
        return req.ChannelID
    default:
        return req.ChannelID
    }
}

// 反向：从 thread channel_id 抠出 groupNo（sender JOIN 用）
func groupNoFromChannel(channelID string, channelType uint8) string {
    if channelType == 5 {
        // 复用 modules/thread.ParseChannelID（见 modules/search/api.go:531）
        groupNo, _, err := thread.ParseChannelID(channelID)
        if err == nil {
            return groupNo
        }
    }
    return channelID
}

// 响应里回写给前端的 channel_id（与请求体保持一致，不暴露 fakeChannelID）
func encodeChannelID(req SearchMessagesReq) string { return req.ChannelID }
```

工作量：**0.5d**（含单测，覆盖 p2p / group / thread / 异常 channelID）

### 6.5 时间字段转换（util.go）

```go
// 入：filters.sent_at_from / .sent_at_to (RFC3339 或 YYYY-MM-DD)
// 出：epoch_second (int64)；解析失败返 0（调用方据此跳过该端）
func parseSentAt(s string, startOfDay bool) int64 {
    if s == "" {
        return 0
    }
    if t, err := time.Parse(time.RFC3339, s); err == nil {
        return t.Unix()
    }
    // 退到 YYYY-MM-DD（按用户时区，端取 00:00:00 / 23:59:59）
    loc := userLocation() // 默认 Asia/Shanghai；或从 ctx 取
    if t, err := time.ParseInLocation("2006-01-02", s, loc); err == nil {
        if startOfDay {
            return t.Unix()
        }
        return t.Add(24*time.Hour - time.Second).Unix()
    }
    return 0
}

func msToRFC3339(ts uint32) string {
    return time.Unix(int64(ts), 0).UTC().Format(time.RFC3339)
}
```

工作量：**0.25d**

### 6.6 入参校验（validate.go）

清单：

- `channel_type` ∈ {1, 2, 5}；`channel_id` 形态匹配（thread 必须含 `____`）
- `keyword`：长度 ≤ 64；端点级允许性（`_search` / `_search_all` 必填非空，`_search_media` 必空，`_search_files` 可选）
- `filters.sender_ids`：长度 ≤ 50
- `sent_at_from` / `sent_at_to`：时间格式合法 + 起 ≤ 止
- `sort`：枚举 {time_desc, time_asc, relevance}；`_search_media` 拒 `relevance`
- `page_size`：1–100
- `cursor`：解码 + 验签

错误返回 R2 `VALIDATION_ERROR` + `details.{field, reason, max_length?}`。

工作量：**0.5d**

### 6.7 限流 + 审计（ratelimit.go + audit.go）

- 限流：5 QPS / 20 桶（per loginUID），用 `golang.org/x/time/rate.Limiter`，`map[uid]*rate.Limiter` + sync.Mutex 或参考 `appwkhttp.SharedUIDRateLimiter`（`modules/message/api.go:283`）
- 审计：每次调用打 log line（kind / channel_count=1 / `keyword_hash=SHA256(keyword)[:16]` / took_ms / hits_count / current_user_uid）；用 `pkg/log` 已有 `zap`

```go
log.Info("messages_search.audit",
    zap.String("kind",          "search_messages"),
    zap.String("login_uid",     loginUID),
    zap.String("channel_id",    req.ChannelID),
    zap.Uint8("channel_type",   req.ChannelType),
    zap.String("keyword_hash",  sha256Hex(req.Keyword)[:16]),
    zap.Int("hits_count",       len(items)),
    zap.Int64("took_ms",        tookMs),
)
```

工作量：**0.5d**

### 6.8 swag 注释 + make openapi-check（swagger/）

每个 handler 9 个必带标签（`@Summary` ≤ 80 / `@Description` / `@Tags` / `@Accept` / `@Produce` / `@Param` / `@Success` / `@Failure` / `@Router`）；`@Router` 相对路径不带 `/v1`。

`@Success` 引用 `envelope.CursorList[MessageHit]` 等；`@Failure` 引用 `envelope.Error`。

完成后跑 `make openapi-check` 验文档；改 endpoint 时跑 `make openapi-diff` 看 breaking。

工作量：**0.5d**

---

## 7. _source 反序列化结构（source.go）

按 indexer mapping v2 的 Doc 结构（见 `indexer-os-changes.md` §三）写：

**约束**：只读 `payload.*` 结构化子对象，不读 `payloadRaw`。`payloadRaw` 在 mapping 上 `enabled:false`，不入倒排 / 不能做字段级投影，读它要拖整块 KB 级 JSON 过网包成本高。width / height / cover / size / extension 等字段一律从 `Payload.Image / Video / File` 结构化字段取（由 indexer 在 ToDoc 时从 payload bytes 抽入，见 v1.8 mapping 文档）。

```go
type Doc struct {
    MessageID   int64    `json:"messageId"`
    MessageSeq  uint64   `json:"messageSeq"`
    From        string   `json:"from,omitempty"`
    To          string   `json:"to,omitempty"`
    ChannelID   string   `json:"channelId"`
    ChannelType uint32   `json:"channelType"`
    Timestamp   uint32   `json:"timestamp"` // epoch seconds
    Payload     *Payload `json:"payload,omitempty"`
    Revoked     bool     `json:"revoked,omitempty"`
}

type Payload struct {
    Type         *int                 `json:"type,omitempty"`
    Text         *TextPayload         `json:"text,omitempty"`
    Image        *ImagePayload        `json:"image,omitempty"`
    Gif          *GifPayload          `json:"gif,omitempty"`
    Voice        *VoicePayload        `json:"voice,omitempty"`
    Video        *VideoPayload        `json:"video,omitempty"`
    File         *FilePayload         `json:"file,omitempty"`
    MergeForward *MergeForwardPayload `json:"mergeForward,omitempty"`
}

type TextPayload struct {
    Content string `json:"content,omitempty"`
}

type ImagePayload struct {
    URL     string `json:"url,omitempty"`
    Caption string `json:"caption,omitempty"`
    Name    string `json:"name,omitempty"`
    Width   int    `json:"width,omitempty"`
    Height  int    `json:"height,omitempty"`
}

type VideoPayload struct {
    URL    string `json:"url,omitempty"`
    Cover  string `json:"cover,omitempty"`
    Width  int    `json:"width,omitempty"`
    Height int    `json:"height,omitempty"`
    Second int    `json:"second,omitempty"` // v1.8: 秒；BFF 转 duration_ms = second*1000
}

type FilePayload struct {
    URL       string `json:"url,omitempty"`
    Name      string `json:"name,omitempty"`
    Caption   string `json:"caption,omitempty"`
    SizeBytes int64  `json:"size,omitempty"`      // v1.8 OS 字段名 size；Go 字段名沿用 SizeBytes
    Ext       string `json:"extension,omitempty"` // v1.8 OS 字段名 extension；原样不转小写
    // PreviewURL 不入索引：业务 payload 不提供，适配层直接返 null
}

type GifPayload   struct{ URL string }
type VoicePayload struct{ URL string }

type MergeForwardPayload struct {
    Title      string             `json:"title,omitempty"`
    ChildCount int                `json:"childCount,omitempty"`
    Msgs       []MergeForwardMsg  `json:"msgs,omitempty"`
}

type MergeForwardMsg struct {
    MessageID  int64  `json:"messageId"`
    Type       int    `json:"type"`
    SearchText string `json:"searchText,omitempty"`
}
```

字段命名严格对齐 v1.8 OS mapping（见 [`v1.8-opensearch-mapping.md`](./v1.8-opensearch-mapping.md)）。`payload.image/video/file` 子对象字段名贴业务 payload 原名原单位（second 秒 / size byte / extension 原样）；BFF 在 wire 层（`MediaHit.duration_ms` / `FileHit.file_size_bytes` / `FileHit.file_ext`）做单位换算与重命名，wire 名仍按 A 文档 v4.2。

---

## 8. 错误码映射表（errcode.go）

| OS 错误 / 业务条件 | HTTP | R2 enum |
|---|---|---|
| OS 网络 / 超时 / 5xx | 503 | `UPSTREAM_UNAVAILABLE` |
| OS 4xx (bad request) | 500 | `INTERNAL_ERROR`（不暴露给前端） |
| 入参校验 | 400 | `VALIDATION_ERROR` |
| 限流 | 429 | `RATE_LIMITED` |
| 鉴权（中间件层） | 401 / 403 | `AUTH_REQUIRED` / `FORBIDDEN` |
| 频道不存在 / Space 不可见 | 404 | `NOT_FOUND` |
| 默认兜底 | 500 | `INTERNAL_ERROR` |

实施：在 `pkg/errcode` 新增 12 项 R2 enum；handler 端通过 `httperr.ResponseErrorL(c, errcode.ErrSearchValidationFailed, params, details)` 渲染（`pkg/httperr/respond.go:22`）。

OS 错误识别：

```go
func mapOSError(err error) (codes.Code, int) {
    if err == nil { return codes.Code{}, 0 }
    if e, ok := err.(*elastic.Error); ok {
        switch {
        case e.Status >= 500:
            return errcode.ErrSearchUpstreamUnavailable, 503
        case e.Status == 429:
            return errcode.ErrSearchRateLimited, 429
        default:
            return errcode.ErrSearchInternal, 500 // 4xx 不外露
        }
    }
    if errors.Is(err, context.DeadlineExceeded) {
        return errcode.ErrSearchUpstreamUnavailable, 503
    }
    return errcode.ErrSearchInternal, 500
}
```

---

## 9. 测试要求

### 单测覆盖

- DSL 构造：每个 handler 一份 fixture 验 DSL JSON（`elastic.SearchService.Source()` 序列化对比 testdata 黄金文件，4 个）
- `_source` 反序列化：mergeForward / 各 type 媒体 / 文件
- `classifyKind` / `buildOuterPreview` 全分支
- cursor 编解码 + 篡改用例（HMAC 验签失败）
- channel 反向映射（p2p fakeChannelID / group / thread / 异常 channelID）
- 时间格式转换（RFC3339 / `YYYY-MM-DD` / 时区）
- 入参校验各分支（每 endpoint 单独）
- sender JOIN + cache hit / miss / 群 vs DM 分支

参考既有单测：`modules/search/{api_test.go, api_i18n_test.go, richtext_search_test.go}`。

### E2E 测试

- 起本地 OpenSearch（docker-compose）+ 灌 fixture（10 条混合：text / forward / image / file）
- 4 端点逐个跑，验响应字段 100% 匹配 A 文档 v4.1
- 灰度前必须跑通

---

## 10. 实施排期

### 阶段 0：底盘（前置）

| 项 | 工作量 |
|---|---|
| SRE 配 OS 读账号 + 网络白名单（跨团队等） | 0.5d |
| octo-lib `SearchConfig` schema | 0.25d |
| `es_client.go` 单例 + ping | 0.25d |
| 索引名 / mapping 现状对齐（验 `wukongim-messages-read` 别名） | 0.25d |
| **小计** | **0.75d 实现 + 0.5d 跨团队等** |

### 阶段 1：4 handler + 共享层

| 项 | 工作量 |
|---|---|
| `_search` handler | 1.5d |
| `_search_media` handler | 0.75d |
| `_search_files` handler | 0.75d |
| `_search_all` handler | 0.5d |
| envelope（含错误码映射 helper） | 0.5d |
| sender JOIN + LRU | 1d |
| cursor 编解码 + HMAC | 0.5d |
| channel 反向映射 | 0.5d |
| 时间格式转换 | 0.25d |
| 入参校验 | 0.5d |
| 限流 + 审计 | 0.5d |
| swag 注释 + `make openapi-check` | 0.5d |
| `_source` 结构 + 错误码映射 | 0.5d |
| **小计** | **8.25d** |

### 阶段 2：联调灰度上线

| 项 | 工作量 |
|---|---|
| 本地 OS fixture E2E | 0.5d |
| dev 环境联调 + 监控 | 1d |
| 灰度 1% / 10% / 100%（净写代码 0.5d，外加 24h × 3 灰度观察窗口） | 0.5d |
| **小计** | **2d** |

### 总计

- octo-server 仓库内：**~11 人日 ≈ 2.2 人周**
- 含跨团队等待 0.5d + 灰度观察 72h
- 与 indexer mapping 改造可并行（先 fixture 跑端到端）

---

## 11. 风险与待办

### R1：mapping `mergeForward.msgs` 字段实际命名

- 需对齐 indexer 实施后的最终字段名（如 `msgs.searchText` 是否就是 text 还是 keyword 等）
- octo-server 这侧 `_source` 反序列化要按最终 mapping 调；DSL 里高亮字段路径同步调
- 推进方式：indexer 灰度时拿 1 条样本 doc 跑 `_source` Unmarshal 单测，过则锁字段

### R2：撤回过滤与 ISM warm read_only

- mapping 有 `revoked` 字段，indexer 当前不写
- 本期 octo-server DSL 仍写 `bool.must_not(revoked=true)`，indexer 后续填上数据后自动生效
- 老索引 ISM warm 后 read_only，撤回事件无法 partial update；接受撤回有效窗口仅 hot ~30 天

### R3：`/v1/search/global` 老接口存废

- 本期保留并存（`modules/search/api.go:42-47` 不动；老 client 未迁不影响）
- 长期建议下线（不在本期范围）；下线触发条件：octo-web / octo-mobile 全量切到 `/v1/messages/_search*`

---

## 12. 与既往文档关系

- **A 文档 v4.1**（`api-spec-v2-server-to-frontend.html`）：对外契约，不变
- **`indexer-os-changes.md`**：indexer + OS 改动，配套本文档
- **`octo-search-integration.md`**：废弃（octo-search HTTP 层不再调用）
- **`octo-server-impl-split.md`**：废弃（之前是 BFF 调 HTTP，现已直查 OS）
- **`octo-server-internal-impl.md`**：废弃（ACP 多次重写未跟上方向，本文取代）
