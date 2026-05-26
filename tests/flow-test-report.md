# Octo Flow API 集成测试报告

- **环境**: `https://im-lab.xming.ai`
- **时间**: 2026-05-26T04:25:04Z
- **总用例**: 20
- **通过**: 19
- **失败**: 1
- **Bug**: 1

## 用例明细

| 状态 | 用例 | 说明 |
|------|------|------|
| ✅ PASS | 1.1 创建 flow (script+http) |   |
| ✅ PASS | 1.2 GET 详情字段完整 (nodes=2 edges=1) |   |
| ✅ PASS | 1.3 PUT 更新生效 (version=2, description=updated desc) |   |
| ✅ PASS | 1.4 列表中可见新 flow |   |
| ✅ PASS | 2.1 activate → status=active |   |
| ✅ PASS | 2.2 deactivate → status=draft (draft 是预期值) |   |
| ✅ PASS | 2.3 未激活 flow 手动 execute 仍被接受 (HTTP 200) — 设计上 manual execute 不要求 active |   |
| ✅ PASS | 3.1 单 script 节点输出 {a:1,b:x} |   |
| ✅ PASS | 3.2 script→http 数据传递（在 response body 中找到 passed=pong） |   |
| ✅ PASS | 3.3 三节点链式传递 b.output.v=20 |   |
| ✅ PASS | 3.4 script 抛异常 → execution=failed |   |
| ✅ PASS | 3.5 HTTP 请求不可达 URL → execution=failed |   |
| ✅ PASS | 3.6 空 flow → execution=success (无节点视为完成) |   |
| ✅ PASS | 4.1 condition 节点选 big 分支 (smal 状态=absent) |   |
| ✅ PASS | 5.1 并发创建 10 个 flow 全部成功 |   |
| ❌ FAIL | 5.2 删除不存在的 flow → 404 | 实际 HTTP 200 (服务端实现静默成功) :: HTTP 200 DELETE https://im-lab.xming.ai/v1/flows/00000000-0000-0000-0000-000000000000 :: BODY={"status":200} |
| ✅ PASS | 5.3 创建缺少 name → HTTP 400 |   |
| ✅ PASS | 5.4 超长 name 被拒 HTTP 400 (合理：name 字段长度限制) |   |
| ✅ PASS | 5.5 循环依赖在 execute 入口被拒 HTTP 400 |   |
| ✅ PASS | 6.1 清理 13 个测试 flow |   |

## 🐛 Bug 记录

- 🐛 5.2 删除不存在 flow 返回 HTTP 200 而非 404 — DeleteFlow 实现里未找到时直接 return nil，缺少 404 语义。复现：DELETE /v1/flows/<random-uuid>

## 失败用例详情（含响应体）

### ❌ 5.2 删除不存在的 flow → 404

```
实际 HTTP 200 (服务端实现静默成功) :: HTTP 200 DELETE https://im-lab.xming.ai/v1/flows/00000000-0000-0000-0000-000000000000 :: BODY={"status":200}
```

