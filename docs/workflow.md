# DMWork V1 协作流程规范

> 适用于 `dmwork-org` 组织下所有仓库：`dmworkim`、`dmwork-web`、`dmwork-android`

---

## 1. 组织与权限

| 团队 | 权限 | 职责 |
|------|------|------|
| `product` | Triage | 提 Issue、评论、打标签、管理里程碑 |
| `dev` | Push | 写代码、提 PR、Code Review、合并 |

- 所有成员通过 GitHub Team 管理，不单独授权仓库
- 管理员：`yujiawei`

---

## 2. Issue 管理

### 2.1 创建 Issue

- 使用仓库提供的 **Issue 模板**（Feature Request 等）
- 标题格式：简明描述，中英文均可
- 必须添加至少一个 **分类标签** 和一个 **优先级标签**

### 2.2 标签体系

**分类标签：**

| 标签 | 用途 |
|------|------|
| `feature` | 新功能需求 |
| `bug` | 缺陷修复 |
| `product` | 产品层面讨论或需求 |
| `android` | Android 端相关 |
| `design` | UI/UX 设计 |
| `discussion` | 讨论、方案探索 |

**优先级标签：**

| 标签 | 含义 | 响应要求 |
|------|------|----------|
| `P0-critical` | 线上故障、安全漏洞、核心功能不可用 | 立即处理 |
| `P1-important` | 重要功能缺失、影响体验 | 当前迭代内完成 |
| `P2-minor` | 优化改进、非紧急需求 | 排入后续迭代 |

### 2.3 里程碑

- 每个迭代周期创建一个 Milestone（如 `v1.1`、`v1.2`）
- Issue 创建后应关联到对应 Milestone
- Milestone 设定截止日期，到期前完成所有关联 Issue

---

## 3. 分支与开发流程

### 3.1 分支命名

| 类型 | 格式 | 示例 |
|------|------|------|
| 功能 | `feat/简要描述` | `feat/email-search` |
| 修复 | `fix/简要描述` | `fix/forget-pwd-code-type` |
| 重构 | `refactor/简要描述` | `refactor/upload-module` |
| 文档 | `docs/简要描述` | `docs/api-readme` |

### 3.2 默认分支

| 仓库 | 默认分支 |
|------|----------|
| `dmworkim` | `main` |
| `dmwork-web` | `main` |
| `dmwork-android` | `master` |

### 3.3 开发流程

```
1. 从默认分支创建功能分支
2. 本地开发、测试
3. 提交 PR（使用 PR 模板）
4. Code Review 通过后合并（Squash Merge 优先）
5. 删除功能分支
```

**紧急修复（P0）：**
- 可直接在功能分支修复后 fast-track 合并
- 修复后立即部署验证
- 事后补充 changelog

---

## 4. PR 规范

### 4.1 PR 模板

提交 PR 时按仓库提供的模板填写，至少包含：
- **改动说明**：做了什么、为什么
- **测试情况**：如何验证
- **关联 Issue**：`Closes #xx` 或 `Fixes #xx`

### 4.2 Commit Message 格式

```
<type>: <简要描述>

<可选的详细说明>
```

**type 类型：**
- `feat` — 新功能
- `fix` — 修复
- `refactor` — 重构
- `docs` — 文档
- `chore` — 构建/工具/依赖
- `style` — 代码风格（不影响逻辑）

**示例：**
```
fix: 忘记密码验证码无效 — code_type 未传

emailSendCode 没有传 code_type 参数，默认为 0（注册），
但忘记密码验证时后端用 code_type=2，Redis key 不匹配。
```

### 4.3 Review 要求

- `dev` 团队成员互相 Review
- P0 修复可自行合并，事后知会团队
- 合并方式：**Squash Merge**（保持主分支整洁）

---

## 5. 发布与部署

### 5.1 后端（dmworkim）

```bash
# 本地编译（服务器上执行）
cd /home/yu/dmworkim
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o TangSengDaoDaoServer ./main.go

# 部署到 Docker
docker cp TangSengDaoDaoServer tsdd-octo-server-1:/home/
docker restart tsdd-octo-server-1

# 验证
docker logs tsdd-octo-server-1 --tail 5
```

> ⚠️ 因 `go.mod` 的 `replace` 指令指向本地路径，必须在服务器上本地编译，不能用 Docker build。

### 5.2 Web 端（dmwork-web）

```bash
cd /home/yu/dmwork-web
yarn build
# Web 通过 Docker 容器（端口 82）自动更新
```

### 5.3 Android（dmwork-android）

```bash
cd /home/yu/dmwork-android
export ANDROID_HOME=/home/yu/android-sdk
./gradlew assembleDebug

# 更新下载地址
sudo cp app/build/outputs/apk/debug/app-debug.apk /var/www/html/dmwork.apk

# 同步到 GitHub Release
gh release upload <tag> app/build/outputs/apk/debug/app-debug.apk --clobber
```

**下载地址：** `https://api-test.example.com/download/dmwork.apk`

### 5.4 版本号规则

- 格式：`vX.Y.Z`（如 `v1.1.0`）
- X — 大版本，Y — 迭代版本，Z — 补丁版本
- 每个 Milestone 对应一个 Y 版本
- 紧急修复递增 Z

---

## 6. 变更记录

- 所有功能和修复需记录到 `docs/changelog.md`
- 格式参考：

```markdown
## v1.1.0 (2026-03-04)

### 新功能
- 搜索用户支持邮箱查找

### 修复
- Android 忘记密码验证码无效
- Android 文件消息显示"未知消息"

### 改进
- Android 文本文件内置预览
```

---

## 7. 自动化监控

| 监控项 | 频率 | 通知频道 |
|--------|------|----------|
| CI/PR 监控（dmworkim） | 30min | #通知 |
| CI/PR 监控（dmwork-web） | 30min | #通知 |
| PR Review（dmworkim） | 1h | #通知 |
| PR Review（dmwork-web） | 1h | #通知 |
| 服务器巡检 | 30min | #巡检 |

---

## 8. 安全优先级

```
安全 > 稳定 > 体验 > 新功能
```

- P0 安全问题立即修复，不等 Review
- Token、密钥等敏感信息禁止提交到代码仓库
- 定期安全审计

---

## 9. 沟通渠道

| 频道 | 用途 |
|------|------|
| `#dmwork-v1` | V1 需求、开发、测试讨论 |
| `#deepim-v2` | V2 (DeepIM) 相关讨论 |
| `#通知` | CI/PR 自动通知 |
| `#巡检` | 服务器/系统巡检报告 |

**原则：V1 内容仅在 #dmwork-v1，V2 内容仅在 #deepim-v2，不混。**
