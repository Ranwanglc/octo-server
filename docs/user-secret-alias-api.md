# 用户外部密钥别名表 — API Schema / 契约（YUJ-3538，Secrets 1/3 octo-server）

> 本文是 octo-server 产出的对外契约，供 **octo-web**（Secrets 2/3，前端 UI）与
> **openclaw-channel-octo**（Secrets 3/3，channel 插件 write-secret 工具 +
> use-time resolve）对接。

## 背景与信任边界

用户在 Octo 之外自定义管理 token 密钥：

- 用户**不在 Octo 聊天里直接输 key**；key 存在 octo-server（加密落库）。
- 用户在 Octo 里用**自然语言别名**引用某个 key（语音友好，允许中文/空格）。
- bot 经 channel 插件在 **use-time** 调 `resolve` 拿明文，写到目标位置。

**信任边界**：明文 key 不得出现在 Octo 的消息 / 会话 / knowledge graph / 经 Octo
的 LLM 请求里。因此 **任何接口永不回显明文/密文**，明文只在 `resolve` 成功响应体里
出现一次，直达调用方（channel 插件）。

## 核心概念

| 概念 | 说明 |
| --- | --- |
| `secret_id` | 稳定内部 ID（UUID）。**引用锚点**：改名（`display_name`）不断引用。 |
| `display_name` | 用户原始别名短语，语音友好，允许中文/空格/大小写，可重命名。 |
| `display_name` 唯一性 | 按 **normalize**（去空格 + 折叠大小写 + 简繁归一）在 **per-user** 维度判重；撞了报 409 提示换名。 |
| `kind` | 枚举 `llm` / `external`，**仅用于分类过滤展示**，不参与鉴权/解析。 |
| `masked` | 明文尾 4 位掩码（如 `****1234`），供 list 展示，不泄漏完整 key。 |

## 鉴权方式

| 接口组 | 路由前缀 | 鉴权 | owner 判定 |
| --- | --- | --- | --- |
| CRUD（用户态） | `/v1/manager/secrets` | 标准用户会话（`token` header，AuthMiddleware） | owner = 当前登录用户 UID |
| resolve（插件态） | `/v1/bot/secrets/resolve` | **bf_ bot token**（`Authorization: Bearer bf_...`） | owner = 该 bot 的 `creator_uid`，**只返该 owner 的 key** |

- resolve 复用 **bot 凭证体系**：`bf_` bot token 已绑定 owner（`robot.creator_uid`）。
  服务端用 token 反查活跃 User Bot（`status=1`），认定「合法的、代表本用户的
  OpenClaw channel 插件在取」，并把可解析范围**限定到该 owner 本人**。
- anti-enumeration：bot token 无效 / 非 `bf_` 前缀 / 查不到 owner，统一返 `401`，
  不区分具体原因。

---

## 主密钥落点（加密实现说明）

- 算法：**AES-256-GCM**，密文 = 版本前缀 `enc:v1:` + `nonce(12B) || ciphertext || tag(16B)`，
  落 `varbinary(8240)`。复用 octo-server 现有对称加密做法，不自造轮子。明文上限
  8192B，加密后约 8192 + 前缀 7 + nonce 12 + tag 16 = 8227B，列宽 8240 留余量，
  确保最大尺寸 key 不被严格 MySQL 拒插、也不会在非严格模式下静默截断成脏行。
- 主密钥：环境变量 **`OCTO_USER_API_KEY_SECRET`（32 字节）**，与 `modules/botfather`
  的 user-api-key 加密**同一把主密钥落点**。本模块用独立域分离串
  `octo-user-secret-alias-cipher-v1` 经 `HMAC-SHA256(masterKey, domain)` 派生子密钥，
  与 botfather / oidc 的子密钥在密码学上相互独立。
- 主密钥轮换：当前为单密钥（与 botfather user-api-key 现状一致，无内建多版本轮换）。
  轮换需运维替换 `OCTO_USER_API_KEY_SECRET` 并对存量密文重新加密；密文版本前缀
  `enc:v1:` 为未来引入多版本主密钥预留识别位。
- 主密钥缺失/长度非法：模块不阻断进程启动，但所有写接口 / resolve 返回 `500`
  `err.server.usersecret.resolve_failed`，运维补齐 env 后重启即恢复。

---

## 错误码

所有错误响应保留**真实 HTTP 状态码**（无 legacy 固定 400），body 为统一 i18n 信封：

```json
{ "status": 400, "error": { "code": "err.server.usersecret.xxx", "http_status": 400, "details": {} } }
```

| HTTP | code | 触发场景 |
| --- | --- | --- |
| 400 | `err.server.usersecret.request_invalid` | 入参缺失/格式非法（空 `display_name`/`key`、超长、body 解析失败、update 既无 key 又无 display_name） |
| 401 | `err.server.usersecret.unauthorized` | CRUD 未登录；resolve bot 凭证无效 / 认不出合法 owner |
| 404 | `err.server.usersecret.not_found` | CRUD 目标 `secret_id` 不存在（非本 owner 即视为不存在）；**resolve 解引用零命中** |
| 409 | `err.server.usersecret.duplicate_name` | create/rename 时归一化别名撞已有别名（**换名**提示） |
| 422 | `err.server.usersecret.ambiguous` | **resolve 需确认**：任何 pinyin/模糊命中（**含恰好 1 个候选**），返脱敏候选列表让上层显式确认（见下）。仅 exact 唯一命中才直接返明文 |
| 429 | （平台限流，非本模块 errcode） | CRUD 路由挂 `SharedUIDRateLimiter`（per-login-user 桶）；resolve 挂 per-IP `StrictIPRateLimitMiddleware`（tag=`usersecret_resolve`，默认 50rps/burst200）。瞬时突发被限流时返回，下游应退避重试 |
| 500 | `err.server.usersecret.resolve_failed` | 解引用失败（密文解密/认证失败等内部异常）；主密钥未就绪 |

---

## CRUD 接口（用户态，`/v1/manager/secrets`）

### 1. 新建 — `POST /v1/manager/secrets`

请求：

```json
{ "display_name": "Claude 密钥", "kind": "llm", "key": "sk-xxxxxxxx" }
```

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `display_name` | string | 是 | 别名短语，≤128 字符 |
| `kind` | string | 否 | `llm` / `external`，缺省/未知归 `external` |
| `key` | string | 是 | 明文 key，≤8192 字符；**仅本次请求体出现，落库即加密** |

响应 `201 Created`（脱敏视图，无明文/密文）：

```json
{
  "secret_id": "a1b2c3d4-...",
  "display_name": "Claude 密钥",
  "kind": "llm",
  "masked": "****xxxx",
  "created_at": "2026-06-07T17:00:00Z",
  "updated_at": "2026-06-07T17:00:00Z"
}
```

错误：`400`（入参非法）、`409`（撞名换名）。

### 2. 列表 — `GET /v1/manager/secrets[?kind=llm|external]`

响应 `200`：

```json
{ "secrets": [ { "secret_id": "...", "display_name": "...", "kind": "llm", "masked": "****xxxx", "created_at": "...", "updated_at": "...", "last_used_at": "..." } ] }
```

`kind` query 可选，按分类过滤。**永不含明文/密文。**

### 3. 更新 — `PUT /v1/manager/secrets/{secret_id}`

承载「换 key」与「重命名」两种动态修改，二选一或同时：

```json
{ "key": "sk-new-rotated" }          // 换 key：只更新密文，secret_id/display_name 不变
{ "display_name": "新名字" }          // 重命名：secret_id/密文不变，改名不断引用
{ "key": "sk-new", "display_name": "新名字" }  // 同时
```

- `key` 与 `display_name` 至少给一个，否则 `400`。
- 响应 `200`：更新后的脱敏视图（结构同 create 响应）。
- 错误：`400`、`404`（`secret_id` 不存在）、`409`（rename 撞名）。
- **幂等**：传入「与当前完全相同」的 `display_name`（含归一化等价的大小写/空格变体）
  不报错，原样返回当前视图——前端整表提交「未改名 + 新 key」时，rename 步幂等放行，
  key 轮换照常生效（不会误返 `404`）。

### 4. 删除 — `DELETE /v1/manager/secrets/{secret_id}`

响应 `200 {"status":200}`。错误：`404`（不存在）。

---

## resolve 接口（插件态，use-time）

### `POST /v1/bot/secrets/resolve`

Header：`Authorization: Bearer bf_<bot_token>`

请求：

```json
{ "query": "我的米要" }
```

- `query` 可为 `secret_id`（精确直查）或 `display_name`（精确匹配 + 拼音/模糊匹配）。
  上例「我的米要」是「我的密钥」的同写法同音变体，会走 pinyin 模糊命中（返 `422` 候选确认，
  非直接返明文，见下）。

**解析规则**：

1. `query` 命中本 owner 某 `secret_id` → **exact 唯一命中**，返明文。
2. 否则按名称匹配：
   - **exact 命中**（归一化 display_name 完全相等：去空格 + 折叠大小写 + 简繁归一）：
     唯一 → **返 200 明文**；多条 → `422` 候选确认。
   - 无 exact 命中时用**拼音/模糊命中**（语音场景：同写法/同音变体，如「我的米要」命中
     「我的密钥」）：**无论命中 1 条还是多条，一律返 `422` 候选确认，不自动返明文。**
3. 零命中 → `404`。

> **设计说明（security，P1）**：fuzzy/pinyin 命中**即使恰好只有 1 个候选**也不自动返明文。
> 模糊档用双向 pinyin 子串命中、无最小长度约束，短/部分 query（如 `pen` 命中 `openai`）会
> 「唯一命中」一把用户**并未指定**的密钥，自动解密就成了**静默错选**——channel 插件会拿错 key
> 去外部认证。故只有 **exact 唯一命中**（secret_id 直查 或 归一化 display_name 完全相等）足够
> 确定到能自动解密；任何 fuzzy 命中一律降级为 `422` 候选，由上层显式确认后用具体 `secret_id`
> 再 resolve。这样既保留语音 UX，又堵死静默错选明文。

**exact 唯一命中** 响应 `200`：

```json
{ "secret_id": "a1b2c3d4-...", "value": "sk-xxxxxxxx" }
```

> `value` 是明文，仅此一处返回，直达调用方，不进任何日志/审计。

**候选确认** 响应 `422`（候选列表走统一 i18n 错误信封的 `error.details.candidates`，**不返明文**）。
触发条件：exact 命中多条，**或任何 pinyin/模糊命中（含恰好 1 个候选）**：

```json
{
  "status": 422,
  "error": {
    "code": "err.server.usersecret.ambiguous",
    "message": "名称匹配到多个密钥，请进一步区分。",
    "http_status": 422,
    "details": {
      "candidates": [
        { "secret_id": "...", "display_name": "我的密钥", "kind": "external", "masked": "****1111", "created_at": "...", "updated_at": "..." },
        { "secret_id": "...", "display_name": "我的米要", "kind": "external", "masked": "****2222", "created_at": "...", "updated_at": "..." }
      ]
    }
  }
}
```

上层（channel 插件）据 `error.details.candidates` 让用户/agent 选定后，用具体
`secret_id` 重新 `resolve` 拿明文。**注意：只有一个候选时也必须走这一步确认**——
fuzzy 唯一命中不代表用户指定了它，须显式确认。

**未命中** 响应 `404` `err.server.usersecret.not_found`。
**解引用失败** 响应 `500` `err.server.usersecret.resolve_failed`。

> **限流**：`resolve` 端点挂 per-IP 严格限流（`StrictIPRateLimitMiddleware`，
> tag=`usersecret_resolve`，默认 50 rps / burst 200，可经 `DM_USERSECRET_RESOLVE_IP_RPS`
> / `DM_USERSECRET_RESOLVE_IP_BURST` 覆盖）。它会返明文且每次调用（含坏 token）都写
> 一条审计行，故必须挡 token 探测 + 审计写放大。触顶返回 `429`，下游应退避重试。
> Redis 故障时 fail-open（放行 + 告警）。

### resolve 审计（P0）

每次 resolve 落一条审计（`user_secret_resolve_audit`）：谁（caller_kind=`user_bot` +
`caller_id`=robot_id）、何时、解了哪个 owner 的哪个 `secret_id`、命中结果。
`result` 枚举区分如下结果，便于把「坏请求 / 真未命中 / 基础设施故障 / 鉴权失败」分开排查：

| `result` | 含义 |
|---|---|
| `ok` | exact 唯一命中、解密成功 |
| `not_found` | 真实零命中（owner 名下无此别名/secret_id） |
| `ambiguous` | exact 命中多条，**或任何 fuzzy/pinyin 命中（含恰好 1 个候选）**，返候选列表待确认 |
| `request_invalid` | 已鉴权 caller 发了坏请求（空/无法解析的 body、空 query） |
| `unauthorized` | bot 凭证缺失/非法（鉴权失败也留痕，越权探测线索） |
| `decrypt_fail` | 命中但密文解密失败 |
| `internal_error` | DB/基础设施异常（鉴权查询或名称扫描报错），区别于真实 `not_found` |

审计为 best-effort（失败仅记日志，不阻塞返回），且**不记录明文/密文**。`query` 列
按 **rune 边界**截断到 128 字符存储（`VARCHAR(128)` 按字符计长；不按字节切，避免切断
多字节 UTF-8 码点）。`caller_kind` 当前仅产生 `user_bot`（本单只认 `bf_` user bot）。

---

## 给下游两仓的对接要点

### octo-web（Secrets 2/3）

- 走 CRUD 四接口（用户会话鉴权）。
- 列表展示用 `display_name` + `kind` + `masked` + 时间元数据；**前端拿不到也不应展示明文**。
- 「换 key」与「重命名」都走 `PUT`（按需传 `key` / `display_name`）。
- 撞名 `409` → 提示用户换名。

### openclaw-channel-octo（Secrets 3/3）

- write-secret 工具：调 CRUD `POST/PUT`（以用户身份），把用户给的 key 存进来。
- use-time resolve：用 bot 自己的 `bf_` token 调 `POST /v1/bot/secrets/resolve`，
  入参用户口述的别名 `query`。
- 处理三态：
  - `200` = **exact 唯一命中**（secret_id 直查 或 归一化 display_name 完全相等）→ 拿 `value` 写目标位置。**这是唯一拿明文的路径**。
  - `422` = **候选确认**（exact 多条，**或任何 fuzzy/pinyin 命中——哪怕 `candidates` 只有 1 个**）→ 按 `error.details.candidates` 让用户/agent 显式选定，再用具体 `secret_id` 重新 resolve。**不要把 `candidates` 长度为 1 当作命中直接用，必须确认。**
  - `404` = 零命中 → 提示「没有这个 key」。
- 明文 `value` 落到指定位置后即用即弃，**不要回写进 Octo 任何消息/上下文**。

> ⚠️ 契约变更（对齐 PR#301 代码）：旧文档曾写「唯一 fuzzy/pinyin 命中返 200 明文」。
> **该承诺已作废。** 现在只有 exact 唯一命中返 200；任何 fuzzy/pinyin 命中（含唯一）一律返
> 422 候选确认。详见上文「解析规则」的 security 设计说明。

#### 已知限制：英文品牌别名的语音音译跨形态匹配

- pinyin 模糊匹配把英文留 ASCII、汉字转拼音。因此**英文别名（如存的 `claude`）与其中文音译
  说法（如「克劳德」）算出的 pinyin 键不同，无法跨形态互相命中**。
- 实践约束：**英文品牌别名需用其英文拼写、或已存别名的同写法变体来查询**（如存「Claude 密钥」，
  用「claude 密钥」「claude密钥」可命中；用纯中文音译「克劳德密钥」**不保证**命中）。
- 英文 ↔ 中文音译（`claude` ↔ 克劳德）的跨形态匹配**当前不支持，属已知限制**，留待后续迭代
  （需要单独的品牌名→音译映射，见 follow-up）。当前 resolve 行为不会因此改动。
