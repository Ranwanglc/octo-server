# Aegis OIDC 切换方案 · v3 · 完全直切

**状态**：v3 · 2026-05-10 Yu 决策定稿（v1 双写 → v2 翻译层 → v3 直切）
**Owner**：Yu  
**作者**：Coda  

## TL;DR

**verify-service 是临时方案，直接干掉**。Aegis OIDC callback 成为 user_verification 表的唯一权威写入方。

- **无双写观察期**（v1 舍弃）——verify-service 同步停
- **无翻译层**（v2 舍弃）——`/internal/verify-token` 直接删
- **老 App 代价**：未升级 App 点"去认证" → 接口 404 → UI 引导升级

**工期**：~3 人日，一次性闭合。

---

## 1. 现状（方案 J v3）速览

### 1.1 数据链路（即将被替换）

```
[App/Web 去认证]
   ↓ POST /v1/internal/verify-token (签 5min JWT)
[dmworkim] 返回 verify_url
   ↓
[App 打开 Safari/CustomTabs]
   ↓
[dmwork-verify-service] ─SAML─▶ [CAS]
   ↓ HMAC POST /internal/verification/complete
[dmworkim] upsert user_verification
   ↓ 302 octo://verified / ?verified=1
[App/Web 拉 /v1/user/current]
```

### 1.2 三端已落地能力

| 层 | 文件 | 能力 |
|---|---|---|
| 后端 dmworkim | `modules/user/api_verification.go` `modules/user/db_verification.go` `modules/user/sql/user-20260505-01.sql` `modules/oidc/api.go` `modules/oidc/oidc_client.go` | 表 + 2 接口 + OIDC 模块（已对接 Aegis） |
| dmwork-web | `packages/dmworkbase/src/Utils/displayName.ts` `Components/RealnameVerifiedBadge/` `Components/MeInfo/vm.tsx` `Service/Convert.ts` | Badge + displayName 优先级 |
| dmwork-android | `app/src/main/java/com/dmwork/im/VerifyLandingActivity.kt` `wkuikit/…/MyInfoActivity.java` `SettingActivity.java` `UserModel.java` `VerifyTokenResponse.java` | Custom Tabs + `octo://verified` |
| dmwork-ios | `Modules/WuKongBase/…/WKRealnameVerifyManager.h/m` | SFSafariViewController + `octo://verified` |
| 辅助（**即将删除**） | `dmwork-verify-service` (Rust) | CAS SAML + HMAC 回调 |

### 1.3 `user_verification` 表 schema（保留不动）

| 列 | 类型 | 说明 |
|---|---|---|
| user_id | VARCHAR(40) PK | |
| real_name | VARCHAR(128) | |
| source | VARCHAR(32) | `cas` / `wecom` / `feishu` |
| source_sub | VARCHAR(128) | |
| emp_id / dept / email / mobile | VARCHAR | |
| verified_at | DATETIME | |
| updated_at | DATETIME | |

---

## 2. 目标架构（直切版）

### 2.1 唯一数据链路

```
[App/Web 去认证]
   ↓ 直接 window.open/Safari(Aegis)
[https://accounts.example.com/profile/info?anchor=verification]
   ↓ 用户完成 CAS 实名
   ↓ Aegis 内部写入账户 is_verified=true
   ↓ 302 return_to → octo://verified / ?verified=1
[App/Web]
   ↓ 重新触发 OIDC 登录刷新（或 silent refresh /userinfo）
[dmworkim OIDC callback]
   ↓ 读 ID Token claims {is_verified, verified_at, verified_provider, legal_name, legal_email}
   ↓ 若 is_verified=true → upsert user_verification + update users.realname_verified
[App/Web 拉 /v1/user/current 看到新状态]
```

### 2.2 与 v2 的差异

| 方面 | v2（翻译层方案） | **v3（直切）** |
|---|---|---|
| `/internal/verify-token` | 保留为翻译层，返回 Aegis URL | **直接删除** |
| `/internal/verification/complete` | Phase 3 删 | **Phase 1 立即删** |
| dmwork-verify-service | Phase 3 停 | **Phase 2 立即停** |
| 未升级老 App | 零感（走翻译层） | 点按钮 → 404 → 升级引导 |
| 工期 | ~4d | **~3d** |
| 清理时间 | 6 个月后 | **即时闭合** |

---

## 3. 改动清单（v3 直切）

### 3.1 删（9 项，一次性干掉）

| # | 文件 / 组件 | 说明 |
|---|---|---|
| D1 | `dmworkim/modules/user/api_verification.go` | 整个文件 ~420 行 |
| D2 | `dmworkim/modules/user/api.go` 中 `/internal/verify-token` 和 `/internal/verification/complete` 路由注册 | 路由清理 |
| D3 | `dmworkim/modules/user/db_verification.go` 注释里 "verify-service 是唯一写入方" 相关描述更新 | 改成 "OIDC callback 是唯一写入方" |
| D4 | 环境变量清理：`OCTO_JWT_SECRET` `OCTO_INTERNAL_HMAC_SECRET` `OCTO_VERIFY_URL_BASE` | k8s / deploy |
| D5 | `dmwork-verify-service` 整个部署停服 + 代码仓库 archive | k8s + GitHub |
| D6 | dmwork-web: `MeInfo/vm.tsx:onClickVerify` 中对 `/internal/verify-token` 的调用代码 + `verifyPending` 相关状态机 | 改成直跳 Aegis |
| D7 | dmwork-android: `UserService.getVerifyToken()` + `VerifyTokenResponse.java` + `MyInfoActivity/SettingActivity/MyFragment` 里的 verify-token 调用 | 改成直跳 Aegis |
| D8 | dmwork-ios: `WKRealnameVerifyManager.startVerificationFromVC:` 中 "先拉 verify_url" 逻辑 | 改成直接 `SFSafariViewController` 打开 Aegis URL |
| D9 | 单测清理：`api_verification_test.go` 整个删、三端 verify-token 相关单测删 | 测试同步 |

### 3.2 改（3 项）

| # | 位置 | 改动 |
|---|---|---|
| M1 | `dmworkim/modules/oidc/oidc_client.go` `IDTokenClaims` struct | 增加 5 字段：`IsVerified bool` / `VerifiedAt int64` / `VerifiedProvider string` / `LegalName string` / `LegalEmail string`（tag 按 POC 实测类型） |
| M2 | `dmworkim/modules/oidc/api.go` callback handler | ID Token 验签后，若 `IsVerified==true` 调用 `user.UpsertVerificationFromOIDC` 写 `user_verification` + `users.realname_verified`；`IsVerified==false` 保持不变（不回写避免误降级） |
| M3 | `dmworkim/modules/oidc/config.go` | `Scopes` 默认值加 `identity_verification`（也允许 env 覆盖） |

### 3.3 新（1 项）

| # | 位置 | 新增 |
|---|---|---|
| N1 | `dmworkim/modules/user/service.go` | 暴露 `UpsertVerificationFromOIDC(ctx, uid, claims) error` 方法；底层复用 `db_verification.go` upsert；做 `verified_provider` strip domain (`cas.example.com` → `cas`) |

---

## 4. Aegis claims → DB 字段映射（POC 实测）

| Aegis claim | 类型 | → DB 字段 | 转换 |
|---|---|---|---|
| `is_verified` | **boolean**（文档误写 string） | `users.realname_verified` int | bool→0/1 |
| `verified_at` | **number**（Unix 秒，文档误写 string） | `user_verification.verified_at` DATETIME UTC | `time.Unix(n, 0).UTC()` |
| `verified_provider` | string (`cas.example.com`) | `user_verification.source` | **strip domain** → `cas` |
| `legal_name` | string | `user_verification.real_name` + `users.real_name` | 直填 |
| `legal_email` | string | `user_verification.email` | 直填 |
| `sub` (ID Token) | string | `user_verification.source_sub` | 沿用 OIDC sub |

**实现要点**：`IDTokenClaims` struct 按实测类型定义，不按 Aegis 文档 string 类型。

---

## 5. Phase（3 步，一次性闭合）

### Phase 0 · Aegis scope 白名单 ✅

- POC 阶段已完成。测试环境 client_id 已授权 `identity_verification` scope。
- 生产 client：需要时通知 Aegis 管理员二次授权（当前工期不阻塞）。

### Phase 1 · dmworkim 直切（1.5d）

**PR 范围**：dmworkim，一个 PR 搞定

1. `IDTokenClaims` 扩展 5 字段（M1）
2. OIDC callback 写 `user_verification`（M2）
3. `Scopes` 默认值加 `identity_verification`（M3）
4. `UpsertVerificationFromOIDC` 新 service 方法（N1）
5. **立即删** `api_verification.go` 整个文件（D1）
6. **立即删** 两个 `/internal/verify*` 路由注册（D2）
7. 更新 `db_verification.go` 权威源注释（D3）
8. 环境变量文档清理（D4）
9. 单测清理 `api_verification_test.go`（D9 后端部分）

**回滚**：feature flag `DM_OIDC_WRITE_VERIFICATION=false` 关闭 OIDC 写库（紧急保底）。revert PR 覆盖代码删除。

### Phase 2 · 三端按钮切换 + verify-service 下线（1.0d）

**PR 范围**：dmwork-web / dmwork-android / dmwork-ios 并行 3 PR + 运维动作

三端并行：
- Web: `MeInfo/vm.tsx:onClickVerify` → `window.open('https://accounts.example.com/profile/info?anchor=verification&return_to=https://octo.xxx/?verified=1', '_blank', 'noopener,noreferrer')`
- Android/iOS: 同理，`startVerification` 直接打开 Aegis URL（Safari/Custom Tabs）
- 删 verify-token 调用代码 + `VerifyTokenResponse` 相关数据结构
- **保留** `octo://verified` handler 和 `?verified=1` refresh 逻辑（Aegis 的 return_to 要用）

运维：
- k8s 停 `dmwork-verify-service` replica（当天）
- 删除 `OCTO_*` 环境变量配置
- `dmwork-verify-service` GitHub 仓库：加 README 警示 + archive

### Phase 3 · 监控 + 清理尾巴（0.5d，Phase 2 上线后 1 周做）

- 监控 `/internal/verify-token` 返回 404 的日志（应为 0 或很少）
- 监控 `user_verification` 表新增记录的 source 分布（应全部来自 OIDC）
- `dmwork-verify-service` 数据库遗留数据：verification 表数据已 upsert 到 dmworkim 侧，无需迁移
- 如果 Phase 2 上线后 `/internal/verify-token` 仍有大量 404，说明老 App 比例高——**仅此时**考虑补做翻译层（但此方案默认不做）

---

## 6. 老 App 处理策略

| App 版本 | 行为 |
|---|---|
| 新版（Phase 2 上线后发的） | 直接跳 Aegis，正常 |
| 旧版（Phase 2 之前的） | 点"去认证" → 调 `/internal/verify-token` → **404** |

旧版点击 404 时的 UX：
- Android/iOS：Retrofit/NSURLSession 捕获 HTTP 404 → Toast "请更新到最新版使用实名功能"
- Web：fetch 失败 → alert/toast 同样文案

**具体改造**：三端在 Phase 2 同时加 404 handler（+0.2d/端）——但因为老版本客户端是**已经发过的**，这个 handler 对他们无效（他们的代码是死的）。只能推送 App 升级引导。

**如果老 App 升级率低怎么办**：
- 降级 Phase 2 → 退回 v2 翻译层
- 或者：Phase 2 的 dmworkim 同时做 graceful degradation（`/internal/verify-token` 返回 410 Gone + 友好 message，让老 App 如果有容错能显示）

---

## 7. 风险与兜底

| 风险 | 概率 | 影响 | 兜底 |
|---|---|---|---|
| Aegis 宕机 | 低 | 用户无法新增实名（已实名无影响） | Aegis 状态页 + 客户端友好文案 |
| CAS email 字段覆盖率低 | 中 | 部分员工 `legal_email` 为空 | 允许 `email` 为空写入 |
| OIDC callback 写库失败 | 低 | 用户实名状态不刷新 | 日志告警 + feature flag 紧急回滚 |
| 老 App 升级率低 | 中 | 一部分用户"去认证"按钮失效 | Phase 3 观察，必要时补做翻译层 |
| 生产 client 未加 scope 白名单 | 低 | Phase 1 生产灰度阻塞（测试环境已过） | 测试环境完成后再申请生产白名单，不阻塞本 Epic |

---

## 8. 工期与派单

### 8.1 工期

| Phase | 改动 | 工期 |
|---|---|---|
| 0 | Aegis scope 白名单 | ✅ 已完成 |
| 1 | dmworkim 直切（一 PR） | 1.5 d |
| 2 | 三端按钮切换 + verify-service 下线（3 PR + 运维） | 1.0 d（3 端并行 0.3 d/端 + 运维 0.1 d） |
| 3 | 监控 + 清理尾巴 | 0.5 d |
| **合计** | | **~3 人日** |

### 8.2 派单方案

**4 个 Multica 子任务给 Titan**，Phase 1 → Phase 2 顺序执行（Phase 2 并行）：

1. `[Phase1]` dmworkim: OIDC identity_verification claim + 删 api_verification + 单测清理
2. `[Phase2a]` dmwork-web: MeInfo 去认证按钮直跳 Aegis + 删 verify-token 调用
3. `[Phase2b]` dmwork-android: 三处按钮 + UserService.getVerifyToken 删除
4. `[Phase2c]` dmwork-ios: WKRealnameVerifyManager 改造直跳
5. `[Phase3-ops]` 运维：verify-service 停服 + env 清理 + 仓库 archive（人工动作，不派 Titan）

每任务独立 PR，独立 TestBot /codex 审查，独立 merge。Phase 2 三 PR 并行，完成后统一做运维动作。
