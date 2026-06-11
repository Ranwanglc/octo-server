# 子区订阅泄漏存量对账运维手册

一次性运维工具：清理历史上残留在 WuKongIM 里的越权子区订阅。

## 背景

被踢/退群（`group_member.is_deleted=1`）或被拉黑（`status=blacklist, is_deleted=0`）的成员，
在入群时按“父群成员”批量挂上的子区频道订阅，可能从未被对称摘除：

- 已被踢/退群的人：当年挂进 WuKongIM 的子区订阅没被摘，可能要等 channel 缓存失效才自愈，不保证。
- 被拉黑的人：成员数据源不过滤 `status`，即使 WuKongIM 重载也可能永不自愈。

事件驱动的修复只覆盖“未来发生的移除”，不处理 bug 期间已经泄漏的存量。本工具做一次性对账：
扫所有群里这两类成员，把他们在该群**所有非 deleted 子区**频道上的 IM 订阅摘掉。

## 工具行为

- **只摘订阅，不删数据**：仅调用 WuKongIM 的 `IMRemoveSubscriber`，不删 `thread_member` /
  `thread_setting` 行，不动置顶 / 会话扩展。订阅泄漏是“多挂了订阅”，对账只需摘掉这份越权订阅。
- **幂等**：`IMRemoveSubscriber` 对不存在的订阅是 no-op，重复执行安全。
- **dry-run 默认**：不带 `--apply` 时只统计“将摘除多少 (uid, 子区) 订阅对”，绝不实际调用。
- **失败不中断**：单个子区/批次调用失败只记录进报告，继续处理其余，最后以非零退出码收尾。

## 用法

工具是主二进制的一个子命令（与 `api` 同级），复用同一份 `-config`：

```bash
# 1) dry-run（默认）：只统计将摘除多少订阅对，不写
./app -config configs/tsdd.yaml reconcile-thread-subs

# 2) 真正执行：加 --apply
./app -config configs/tsdd.yaml reconcile-thread-subs --apply

# 3) 大群限速执行：每次 IM 调用间隔 200ms，单次最多带 50 个 uid
./app -config configs/tsdd.yaml reconcile-thread-subs --apply --interval 200ms --batch-size 50
```

容器内（镜像 ENTRYPOINT 为 `/home/app`，工作目录 `/home`）：

```bash
docker exec -it <octo-server-container> /home/app reconcile-thread-subs
docker exec -it <octo-server-container> /home/app reconcile-thread-subs --apply --interval 200ms
```

### 参数

| 参数 | 默认 | 说明 |
|------|------|------|
| `--apply` | `false` | 不带则 dry-run（只统计）；带上才真正摘订阅 |
| `--batch-size` | `100` | 单次 `IMRemoveSubscriber` 调用最多携带的 uid 数（同一子区频道下分批） |
| `--interval` | `0` | 每次 IM 调用之间的休眠（限速），如 `200ms`、`1s` |

### 退出码

- `0`：成功，无失败项。
- `1`：扫描阶段出错（如 DB 查询失败），未进入摘除。
- `2`：摘除阶段有失败项（已全部跑完，失败项见报告）。

## 推荐执行顺序

1. **先在 dry-run 模式跑一遍**，确认受影响群数 / 泄漏成员数 / 计划摘除对数在预期范围。
2. 建议在“数据源排除 blacklist + 拉黑路径补 helper”修复合入后再 `--apply`，
   否则跑完拉黑成员可能被其他路径重新挂回。**注意：对账幂等，重跑无害**，即便提前跑也安全。
3. 生产 `--apply` 建议带 `--interval` 限速，避免打爆 WuKongIM IM 接口。

## 报告示例

```
子区订阅泄漏对账报告 [DRY-RUN (未实际摘除任何订阅；加 --apply 才执行)]
  受影响群数:        12
  泄漏成员数(去重):  47
  扫描子区数:        38
  计划摘除订阅对:    156
  失败项:            0
```

## 回滚与影响面

- **无需回滚**：本工具只摘订阅、不删任何 DB 数据。
- **影响面**：被摘订阅的成员是已被踢/退群/拉黑的人，他们本就不该再收该群子区消息；
  摘订阅后他们停止从 WuKongIM 收到对应子区频道的消息，符合预期。
- 万一误摘了正常成员的订阅（理论上不会，判定条件是 `is_deleted=1 OR status=blacklist`），
  该成员下次正常进入子区时会按既有入群/激活路径重新挂回订阅，不造成永久损坏。
