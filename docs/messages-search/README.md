# 消息搜索功能开发文档

本目录是 octo-server 4 个会话内搜索端点（`/v1/messages/_search` / `_search_media` / `_search_files` / `_search_all`）的设计与实施文档集合。

## 文档索引

### 对外 API 契约

- **[api-spec-v2-server-to-frontend.html](./api-spec-v2-server-to-frontend.html)** — 对外 API 契约 v4.2
  - 4 端点请求/响应结构、字段语义、错误码、cursor 分页、点击定位（复用 `/v1/message/channel/sync`）
  - 遵循 octo-openapi-dev-skill R1/R2/R3/R5/R6/R8/R10/R13
  - **本目录其他文档以此为对外契约真值**

### OpenSearch mapping（权威）

- **[v1.8-opensearch-mapping.md](./v1.8-opensearch-mapping.md)** — v1.8 mapping（2026-06-12，**当前生效**）
  - 完整 index template、字段速查、reindex painless
  - 由 indexer 仓库 `internal/indexer/mapping.go::IndexTemplateJSON` 直出
  - octo-server `modules/messages_search/source.go` 字段对齐此文档

### 实施细节

- **[octo-server-search-dev.md](./octo-server-search-dev.md)** — octo-server 开发文档（实施手册）
  - 项目骨架（`modules/messages_search/` 13 文件）+ ES client + 4 handler 完整代码骨架（olivere/elastic v6 DSL 构造）
  - 共享层（envelope / sender JOIN / cursor / channel 反向映射 / 时间转换 / 限流 / 审计 / swag）
  - 错误码映射（OS → R2 12 项 enum）
  - 实施排期 ~11 人日 ≈ 2.2 人周

- **[indexer-os-changes.md](./indexer-os-changes.md)** — wukongim-message-indexer + OpenSearch v1.7 改动清单（**已被 v1.8 取代**）
  - 保留作 v1.7 → v1.8 演进历史
  - mapping / ToDoc / reindex 操作流程以 [v1.8-opensearch-mapping.md](./v1.8-opensearch-mapping.md) 为准
  - v1.8 关键变更：image/video 删 thumbUrl；video 加 second（秒）替代 durationMs；file 用 size/extension 替代 sizeBytes/ext

- **[search-flow-cheatsheet.md](./search-flow-cheatsheet.md)** — 4 端点 OS 查询/字段拼接速查
  - 每个端点的 DSL JSON 完整示例 + 响应字段拼接表（`_source` 路径 / JOIN / 适配层算）
  - sender JOIN / cursor 编解码 / channel 反向映射的共用流程

### 参考资料

- **[OCTO-SEARCH-DEV-NOTES.md](./OCTO-SEARCH-DEV-NOTES.md)** — octo-search 开发约定 / 限制 / 经验（来自 octo-search 仓库；**非本模块**）
  - 项目定位（octo-search 与 octo-server `modules/search` 是两条并存搜索路径）
  - K8s 部署 / CI/CD / 集群访问与调试
  - 直连 cls-oqbohqyh OpenSearch 集群方法（octo-server 阶段 0 联调可参考）

## 历史文档（已废弃）

历史方案讨论 / v1-v3 迭代计划 / 已废弃的 BFF 调 HTTP 层方案归档在
`~/Projects/solo-builder-scratch/octo-server-search-compare/_archived/`，
不 commit 进 octo-server 仓库；保留供回溯。

## 开发分支

当前开发分支：`feat/messages-search`（从 `origin/develop` checkout）

## 待研发同学拍板的关键点

详见各文档对应章节：
- `indexer-os-changes.md` §七 — `mergeForward.msgs[].searchText` 是 indexer 拼还是业务塞
- `octo-server-search-dev.md` §11 — 路由组归属 / sender JOIN 第一参语义 / LRU TTL 实现方案
