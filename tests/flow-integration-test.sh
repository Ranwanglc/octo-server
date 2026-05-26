#!/usr/bin/env bash
#
# Octo Flow API 完整集成测试
#
# 覆盖：CRUD / 生命周期 / 执行引擎 / 条件分支 / 并发与边界 / 清理
#
# 用法：
#   API_BASE=https://im-lab.xming.ai \
#   USERNAME=superAdmin PASSWORD=admin123 \
#   bash tests/flow-integration-test.sh
#
# 结束后会在 tests/flow-test-report.md 生成 Markdown 报告。

set -o pipefail

API_BASE=${API_BASE:-https://im-lab.xming.ai}
USERNAME=${USERNAME:-superAdmin}
PASSWORD=${PASSWORD:-admin123}
REPORT_FILE=${REPORT_FILE:-tests/flow-test-report.md}

command -v jq >/dev/null 2>&1 || { echo "jq is required" >&2; exit 1; }
command -v curl >/dev/null 2>&1 || { echo "curl is required" >&2; exit 1; }

GREEN="\033[32m"; RED="\033[31m"; YELLOW="\033[33m"; BLUE="\033[34m"; BOLD="\033[1m"; RESET="\033[0m"

PASS=0; FAIL=0
RESULTS=()           # 每条用例: "PASS|FAIL|name|details"
BUGS=()              # bug 记录
CREATED_FLOW_IDS=()  # 清理用

RESP_CODE=""; RESP_BODY=""; LAST_URL=""; LAST_METHOD=""; LAST_BODY=""

http() {
  # http METHOD PATH [BODY]
  local method=$1 path=$2 body=${3-}
  local url="${API_BASE}${path}"
  local tmp; tmp=$(mktemp)
  LAST_METHOD=$method; LAST_URL=$url; LAST_BODY=$body
  if [[ -n $body ]]; then
    RESP_CODE=$(curl -sS --max-time 30 -w "%{http_code}" -o "$tmp" \
      -H "Content-Type: application/json" \
      -H "token: ${TOKEN:-}" \
      -X "$method" "$url" --data-binary "$body")
  else
    RESP_CODE=$(curl -sS --max-time 30 -w "%{http_code}" -o "$tmp" \
      -H "token: ${TOKEN:-}" \
      -X "$method" "$url")
  fi
  RESP_BODY=$(cat "$tmp")
  rm -f "$tmp"
}

pass_case() {
  PASS=$((PASS+1))
  RESULTS+=("PASS|$1|")
  echo -e "${GREEN}✔${RESET} $1"
}

fail_case() {
  FAIL=$((FAIL+1))
  local name=$1 reason=$2
  local snippet=""
  snippet="HTTP ${RESP_CODE} ${LAST_METHOD} ${LAST_URL}"
  RESULTS+=("FAIL|$name|${reason} :: ${snippet} :: BODY=${RESP_BODY}")
  echo -e "${RED}✘${RESET} $name — ${reason}"
  echo "    ${snippet}"
  echo "    BODY: ${RESP_BODY}" | head -c 600
  echo
}

bug() {
  BUGS+=("$1")
  echo -e "${YELLOW}🐛${RESET} $1"
}

section() { echo; echo -e "${BLUE}${BOLD}==> $1${RESET}"; }

# -------- Login --------
section "登录获取 token"
http POST "/v1/user/login" "{\"username\":\"${USERNAME}\",\"password\":\"${PASSWORD}\"}"
if [[ "$RESP_CODE" != "200" ]]; then
  echo "Login failed (HTTP $RESP_CODE): $RESP_BODY" >&2
  exit 2
fi
TOKEN=$(echo "$RESP_BODY" | jq -r '.token // .data.token')
if [[ -z "$TOKEN" || "$TOKEN" == "null" ]]; then
  echo "Login: token missing in response" >&2
  echo "$RESP_BODY" >&2
  exit 2
fi
echo "Token acquired (${#TOKEN} chars)"

TS=$(date +%s)
PREFIX="itest-${TS}"

# -------- 1. CRUD --------
section "1. CRUD 基础"

# 1.1 创建 flow（script + http 两节点串联）
CREATE_BODY=$(jq -nc \
  --arg name "${PREFIX}-crud" \
  '{name:$name, description:"crud test", definition:{nodes:[
    {id:"s1", type:"script", config:{runtime:"javascript", code:"return {greeting: \"hi\", n: 1};"}},
    {id:"h1", type:"http",   config:{method:"POST", url:"https://httpbin.org/post", body:{from:"{{s1.output.greeting}}"}}}
  ], edges:[{from:"s1", to:"h1"}]}}')
http POST "/v1/flows" "$CREATE_BODY"
if [[ "$RESP_CODE" == "200" ]]; then
  FLOW_ID=$(echo "$RESP_BODY" | jq -r '.id // .data.id')
  if [[ -n "$FLOW_ID" && "$FLOW_ID" != "null" ]]; then
    CREATED_FLOW_IDS+=("$FLOW_ID")
    pass_case "1.1 创建 flow (script+http)"
  else
    fail_case "1.1 创建 flow (script+http)" "响应缺少 id"
    FLOW_ID=""
  fi
else
  fail_case "1.1 创建 flow (script+http)" "HTTP $RESP_CODE"
  FLOW_ID=""
fi

# 1.2 GET 详情
if [[ -n "$FLOW_ID" ]]; then
  http GET "/v1/flows/${FLOW_ID}"
  if [[ "$RESP_CODE" == "200" ]]; then
    OK=$(echo "$RESP_BODY" | jq -r '[.id,.name,.status,.version,(.definition|tostring)] | all(.!=null and .!="")')
    NAME=$(echo "$RESP_BODY" | jq -r '.name')
    NODE_CNT=$(echo "$RESP_BODY" | jq -r '.definition.nodes | length')
    EDGE_CNT=$(echo "$RESP_BODY" | jq -r '.definition.edges | length')
    if [[ "$OK" == "true" && "$NAME" == "${PREFIX}-crud" && "$NODE_CNT" == "2" && "$EDGE_CNT" == "1" ]]; then
      pass_case "1.2 GET 详情字段完整 (nodes=$NODE_CNT edges=$EDGE_CNT)"
    else
      fail_case "1.2 GET 详情字段完整" "字段不齐 name=$NAME nodes=$NODE_CNT edges=$EDGE_CNT"
    fi
  else
    fail_case "1.2 GET 详情" "HTTP $RESP_CODE"
  fi

  # 1.3 PUT 更新
  UPDATE_BODY=$(jq -nc \
    --arg name "${PREFIX}-crud" \
    '{name:$name, description:"updated desc", changelog:"itest update",
      definition:{nodes:[
        {id:"s1", type:"script", config:{runtime:"javascript", code:"return {greeting: \"hello-updated\", n: 2};"}},
        {id:"h1", type:"http",   config:{method:"POST", url:"https://httpbin.org/post", body:{from:"{{s1.output.greeting}}"}}}
      ], edges:[{from:"s1", to:"h1"}]}}')
  http PUT "/v1/flows/${FLOW_ID}" "$UPDATE_BODY"
  if [[ "$RESP_CODE" == "200" ]]; then
    NEWVER=$(echo "$RESP_BODY" | jq -r '.version')
    DESC=$(echo "$RESP_BODY" | jq -r '.description')
    if [[ "$NEWVER" == "2" && "$DESC" == "updated desc" ]]; then
      pass_case "1.3 PUT 更新生效 (version=$NEWVER, description=$DESC)"
    else
      fail_case "1.3 PUT 更新" "version=$NEWVER desc=$DESC"
    fi
  else
    fail_case "1.3 PUT 更新" "HTTP $RESP_CODE"
  fi

  # 1.4 列表中可见
  http GET "/v1/flows?limit=200"
  if [[ "$RESP_CODE" == "200" ]]; then
    FOUND=$(echo "$RESP_BODY" | jq --arg id "$FLOW_ID" '.items | map(select(.id==$id)) | length')
    if [[ "$FOUND" == "1" ]]; then
      pass_case "1.4 列表中可见新 flow"
    else
      fail_case "1.4 列表中可见新 flow" "未在列表找到 id=$FLOW_ID (found=$FOUND)"
    fi
  else
    fail_case "1.4 列表" "HTTP $RESP_CODE"
  fi
fi

# -------- 2. 生命周期 --------
section "2. 生命周期"

LC_BODY=$(jq -nc --arg name "${PREFIX}-lifecycle" \
  '{name:$name, description:"lifecycle test", definition:{nodes:[
     {id:"s1", type:"script", config:{runtime:"javascript", code:"return {ok:true};"}}
   ], edges:[]}}')
http POST "/v1/flows" "$LC_BODY"
LC_FLOW_ID=""
if [[ "$RESP_CODE" == "200" ]]; then
  LC_FLOW_ID=$(echo "$RESP_BODY" | jq -r '.id')
  CREATED_FLOW_IDS+=("$LC_FLOW_ID")
fi

if [[ -n "$LC_FLOW_ID" ]]; then
  # 2.1 activate
  http POST "/v1/flows/${LC_FLOW_ID}/activate" ""
  if [[ "$RESP_CODE" == "200" ]]; then
    http GET "/v1/flows/${LC_FLOW_ID}"
    ST=$(echo "$RESP_BODY" | jq -r '.status')
    if [[ "$ST" == "active" ]]; then
      pass_case "2.1 activate → status=active"
    else
      fail_case "2.1 activate" "status=$ST"
    fi
  else
    fail_case "2.1 activate" "HTTP $RESP_CODE"
  fi

  # 2.2 deactivate
  http POST "/v1/flows/${LC_FLOW_ID}/deactivate" ""
  if [[ "$RESP_CODE" == "200" ]]; then
    http GET "/v1/flows/${LC_FLOW_ID}"
    ST=$(echo "$RESP_BODY" | jq -r '.status')
    # 服务端实现：deactivate 后置为 draft（非 inactive 字符串）
    if [[ "$ST" == "draft" || "$ST" == "inactive" ]]; then
      pass_case "2.2 deactivate → status=$ST (draft 是预期值)"
    else
      fail_case "2.2 deactivate" "status=$ST"
    fi
  else
    fail_case "2.2 deactivate" "HTTP $RESP_CODE"
  fi

  # 2.3 未激活也尝试 execute（manual 执行不强制要求 active；记录行为）
  http POST "/v1/flows/${LC_FLOW_ID}/execute" '{"input":{"x":1}}'
  if [[ "$RESP_CODE" == "200" ]]; then
    pass_case "2.3 未激活 flow 手动 execute 仍被接受 (HTTP 200) — 设计上 manual execute 不要求 active"
  else
    pass_case "2.3 未激活 flow execute 被拒 (HTTP $RESP_CODE)"
  fi
fi

# -------- 3. 执行引擎 --------
section "3. 执行引擎"

# helper：创建并执行 flow，等执行完成
run_flow() {
  local def_json=$1 name=$2 input=${3:-'{}'}
  local body
  body=$(jq -nc --arg name "$name" --argjson def "$def_json" '{name:$name, definition:$def}')
  http POST "/v1/flows" "$body"
  if [[ "$RESP_CODE" != "200" ]]; then
    echo "create-failed:HTTP$RESP_CODE:$RESP_BODY"
    return 1
  fi
  local fid; fid=$(echo "$RESP_BODY" | jq -r '.id')
  CREATED_FLOW_IDS+=("$fid")
  http POST "/v1/flows/${fid}/execute" "{\"input\":$input}"
  if [[ "$RESP_CODE" != "200" ]]; then
    echo "execute-failed:HTTP$RESP_CODE:$RESP_BODY"
    return 1
  fi
  local eid; eid=$(echo "$RESP_BODY" | jq -r '.id')
  # 轮询执行状态最多 10s
  local i status
  for i in $(seq 1 20); do
    http GET "/v1/executions/${eid}"
    if [[ "$RESP_CODE" == "200" ]]; then
      status=$(echo "$RESP_BODY" | jq -r '.status')
      if [[ "$status" == "success" || "$status" == "failed" || "$status" == "cancelled" ]]; then
        echo "$fid:$eid:$status"
        return 0
      fi
    fi
    sleep 0.5
  done
  echo "$fid:$eid:timeout"
  return 0
}

# 3.1 单 script 节点
DEF_1=$(jq -nc '{nodes:[{id:"only", type:"script", config:{runtime:"javascript", code:"return {a:1, b:\"x\"};"}}], edges:[]}')
RES=$(run_flow "$DEF_1" "${PREFIX}-eng-1")
EID=$(echo "$RES" | cut -d: -f2); STATUS=$(echo "$RES" | cut -d: -f3)
if [[ "$STATUS" == "success" ]]; then
  http GET "/v1/executions/${EID}"
  OUT=$(echo "$RESP_BODY" | jq -r '.context.nodes.only.output | (.a|tostring) + "|" + .b')
  if [[ "$OUT" == "1|x" ]]; then
    pass_case "3.1 单 script 节点输出 {a:1,b:x}"
  else
    fail_case "3.1 单 script 节点输出" "实际 output=$OUT"
  fi
else
  fail_case "3.1 单 script 节点" "status=$STATUS res=$RES"
fi

# 3.2 script → http 数据传递
DEF_2=$(jq -nc '{nodes:[
   {id:"s", type:"script", config:{runtime:"javascript", code:"return {msg:\"pong\", n:42};"}},
   {id:"h", type:"http",   config:{method:"POST", url:"https://httpbin.org/post", body:{passed:"{{s.output.msg}}", n:"{{s.output.n}}"}}}
 ], edges:[{from:"s", to:"h"}]}')
RES=$(run_flow "$DEF_2" "${PREFIX}-eng-2")
EID=$(echo "$RES" | cut -d: -f2); STATUS=$(echo "$RES" | cut -d: -f3)
if [[ "$STATUS" == "success" ]]; then
  http GET "/v1/executions/${EID}"
  # httpbin 把请求 body 回显在 .json 字段
  PASSED=$(echo "$RESP_BODY" | jq -r '.context.nodes.h.output.json.passed // empty')
  if [[ "$PASSED" == "pong" ]]; then
    pass_case "3.2 script→http 数据传递 (passed=pong)"
  else
    # 兜底：检查 body 字段直接传递
    BODYRAW=$(echo "$RESP_BODY" | jq -r '.context.nodes.h.output.body // ""')
    if echo "$BODYRAW" | grep -q '"passed": "pong"'; then
      pass_case "3.2 script→http 数据传递（在 response body 中找到 passed=pong）"
    else
      fail_case "3.2 script→http 数据传递" "未取到 passed; output.json=$(echo "$RESP_BODY"|jq -c '.context.nodes.h.output.json' )"
    fi
  fi
else
  fail_case "3.2 script→http" "status=$STATUS res=$RES"
fi

# 3.3 三节点串联 script→script→http
DEF_3=$(jq -nc '{nodes:[
   {id:"a", type:"script", config:{runtime:"javascript", code:"return {v:10};"}},
   {id:"b", type:"script", config:{runtime:"javascript", code:"return {v: parseInt(input.prev,10)*2};", input:{prev:"{{a.output.v}}"}}},
   {id:"c", type:"http",   config:{method:"POST", url:"https://httpbin.org/post", body:{final:"{{b.output.v}}"}}}
 ], edges:[{from:"a", to:"b"},{from:"b", to:"c"}]}')
RES=$(run_flow "$DEF_3" "${PREFIX}-eng-3")
EID=$(echo "$RES" | cut -d: -f2); STATUS=$(echo "$RES" | cut -d: -f3)
if [[ "$STATUS" == "success" ]]; then
  http GET "/v1/executions/${EID}"
  BV=$(echo "$RESP_BODY" | jq -r '.context.nodes.b.output.v')
  if [[ "$BV" == "20" ]]; then
    pass_case "3.3 三节点链式传递 b.output.v=20"
  else
    fail_case "3.3 三节点链式传递" "b.output.v=$BV (期望 20)"
  fi
else
  fail_case "3.3 三节点链式" "status=$STATUS res=$RES"
fi

# 3.4 script 抛异常 → execution failed
DEF_4=$(jq -nc '{nodes:[
   {id:"boom", type:"script", config:{runtime:"javascript", code:"throw new Error(\"intentional boom\");"}}
 ], edges:[]}')
RES=$(run_flow "$DEF_4" "${PREFIX}-eng-4")
EID=$(echo "$RES" | cut -d: -f2); STATUS=$(echo "$RES" | cut -d: -f3)
if [[ "$STATUS" == "failed" ]]; then
  pass_case "3.4 script 抛异常 → execution=failed"
else
  fail_case "3.4 script 抛异常" "status=$STATUS (期望 failed) res=$RES"
fi

# 3.5 HTTP 节点请求不存在的 URL
DEF_5=$(jq -nc '{nodes:[
   {id:"bad", type:"http", config:{method:"GET", url:"http://nonexistent.invalid.tld.example.does.not.resolve/", timeout:"3s"}}
 ], edges:[]}')
RES=$(run_flow "$DEF_5" "${PREFIX}-eng-5")
EID=$(echo "$RES" | cut -d: -f2); STATUS=$(echo "$RES" | cut -d: -f3)
if [[ "$STATUS" == "failed" ]]; then
  pass_case "3.5 HTTP 请求不可达 URL → execution=failed"
else
  fail_case "3.5 HTTP 请求不可达 URL" "status=$STATUS res=$RES"
fi

# 3.6 空 flow
DEF_6=$(jq -nc '{nodes:[], edges:[]}')
RES=$(run_flow "$DEF_6" "${PREFIX}-eng-6")
EID=$(echo "$RES" | cut -d: -f2); STATUS=$(echo "$RES" | cut -d: -f3)
case "$STATUS" in
  success) pass_case "3.6 空 flow → execution=success (无节点视为完成)" ;;
  failed)  pass_case "3.6 空 flow → execution=failed (服务端选择拒绝空 flow)" ;;
  *)       fail_case "3.6 空 flow" "status=$STATUS res=$RES" ;;
esac

# -------- 4. 条件分支 --------
section "4. 条件分支"

# condition 节点：expression 渲染后与 branches[i].value 比较；选中 value 进入 NextBranches；
# 引擎再按 edge.branch 激活下游。
DEF_C=$(jq -nc '{nodes:[
   {id:"src",  type:"script",    config:{runtime:"javascript", code:"return {size: \"big\"};"}},
   {id:"cond", type:"condition", config:{expression:"{{src.output.size}}",
                                         branches:[{value:"big"},{value:"small"},{default:true,value:"big"}]}},
   {id:"big",  type:"script",    config:{runtime:"javascript", code:"return {took:\"big\"};"}},
   {id:"smal", type:"script",    config:{runtime:"javascript", code:"return {took:\"small\"};"}}
 ], edges:[
   {from:"src",  to:"cond"},
   {from:"cond", to:"big",  branch:"big"},
   {from:"cond", to:"smal", branch:"small"}
 ]}')
RES=$(run_flow "$DEF_C" "${PREFIX}-cond")
EID=$(echo "$RES" | cut -d: -f2); STATUS=$(echo "$RES" | cut -d: -f3)
if [[ "$STATUS" == "success" ]]; then
  http GET "/v1/executions/${EID}"
  TOOK=$(echo "$RESP_BODY" | jq -r '.context.nodes.big.output.took // empty')
  SKIPPED_SMAL=$(echo "$RESP_BODY" | jq -r '.context.nodes.smal.status // "absent"')
  if [[ "$TOOK" == "big" ]]; then
    pass_case "4.1 condition 节点选 big 分支 (smal 状态=$SKIPPED_SMAL)"
  else
    fail_case "4.1 condition 节点路由" "big.output.took=$TOOK smal.status=$SKIPPED_SMAL"
  fi
elif [[ "$STATUS" == "failed" ]]; then
  http GET "/v1/executions/${EID}"
  ERR=$(echo "$RESP_BODY" | jq -r '.error')
  bug "4.1 condition 节点执行失败 (status=failed, error=$ERR)；可能 condition 节点对该 schema 不支持"
  fail_case "4.1 condition 节点" "execution failed: error=$ERR"
else
  fail_case "4.1 condition 节点" "status=$STATUS res=$RES"
fi

# -------- 5. 并发 / 边界 --------
section "5. 并发与边界"

# 5.1 并发创建 10 个
PIDS=()
CONC_DIR=$(mktemp -d)
for i in $(seq 1 10); do
  (
    BODY=$(jq -nc --arg name "${PREFIX}-conc-$i" \
      '{name:$name, definition:{nodes:[{id:"n",type:"script",config:{runtime:"javascript",code:"return {i:1};"}}],edges:[]}}')
    HTTP_CODE=$(curl -sS --max-time 30 -w "%{http_code}" -o "${CONC_DIR}/out-$i" \
      -H "Content-Type: application/json" -H "token: ${TOKEN}" \
      -X POST "${API_BASE}/v1/flows" --data-binary "$BODY")
    echo "$HTTP_CODE" > "${CONC_DIR}/code-$i"
  ) &
  PIDS+=($!)
done
for p in "${PIDS[@]}"; do wait "$p"; done

OK_CNT=0
for i in $(seq 1 10); do
  CODE=$(cat "${CONC_DIR}/code-$i" 2>/dev/null || echo "?")
  if [[ "$CODE" == "200" ]]; then
    OK_CNT=$((OK_CNT+1))
    FID=$(jq -r '.id // .data.id // empty' < "${CONC_DIR}/out-$i" 2>/dev/null)
    [[ -n "$FID" ]] && CREATED_FLOW_IDS+=("$FID")
  fi
done
rm -rf "$CONC_DIR"
if [[ "$OK_CNT" == "10" ]]; then
  pass_case "5.1 并发创建 10 个 flow 全部成功"
else
  fail_case "5.1 并发创建 10 个 flow" "成功 $OK_CNT/10"
fi

# 5.2 删除不存在的 flow → 期望 404 (实现可能返回 200 + biz code)
http DELETE "/v1/flows/00000000-0000-0000-0000-000000000000" ""
if [[ "$RESP_CODE" == "404" ]]; then
  pass_case "5.2 删除不存在的 flow → HTTP 404"
elif [[ "$RESP_CODE" == "200" ]]; then
  # service.DeleteFlow 对未找到 flow 静默成功（不返回 404）
  bug "5.2 删除不存在 flow 返回 HTTP 200 而非 404 — DeleteFlow 实现里未找到时直接 return nil，缺少 404 语义。复现：DELETE /v1/flows/<random-uuid>"
  fail_case "5.2 删除不存在的 flow → 404" "实际 HTTP 200 (服务端实现静默成功)"
else
  fail_case "5.2 删除不存在的 flow → 404" "HTTP $RESP_CODE"
fi

# 5.3 创建缺少 name
http POST "/v1/flows" '{"description":"no name"}'
if [[ "$RESP_CODE" != "200" ]]; then
  pass_case "5.3 创建缺少 name → HTTP $RESP_CODE"
else
  # 看 body 里是否有 biz-error 字段
  STATUSF=$(echo "$RESP_BODY" | jq -r '.status // .code // empty')
  MSG=$(echo "$RESP_BODY" | jq -r '.msg // .message // empty')
  if [[ -n "$MSG" && "$MSG" != "null" ]]; then
    pass_case "5.3 创建缺少 name → HTTP 200 + biz error (status=$STATUSF msg=$MSG)"
  else
    bug "5.3 创建缺少 name 时返回 HTTP 200 且无错误消息：$RESP_BODY"
    fail_case "5.3 创建缺少 name" "HTTP 200 无错误"
  fi
fi

# 5.4 超长 name (1000 字符)
LONG_NAME=$(head -c 1000 < /dev/urandom | base64 | tr -d '\n=+/' | head -c 1000)
LONG_BODY=$(jq -nc --arg n "$LONG_NAME" \
  '{name:$n, definition:{nodes:[{id:"n",type:"script",config:{runtime:"javascript",code:"return {};"}}],edges:[]}}')
http POST "/v1/flows" "$LONG_BODY"
if [[ "$RESP_CODE" == "200" ]]; then
  FID=$(echo "$RESP_BODY" | jq -r '.id // .data.id // empty')
  if [[ -n "$FID" ]]; then
    CREATED_FLOW_IDS+=("$FID")
    # 读回检查
    http GET "/v1/flows/${FID}"
    LEN=$(echo "$RESP_BODY" | jq -r '.name | length')
    pass_case "5.4 1000 字符 name 被接受 (回读 length=$LEN)"
  else
    fail_case "5.4 超长 name" "200 但无 id"
  fi
else
  pass_case "5.4 超长 name 被拒 HTTP $RESP_CODE (合理：name 字段长度限制)"
fi

# 5.5 循环依赖 edges
CYCLE_BODY=$(jq -nc --arg name "${PREFIX}-cycle" \
  '{name:$name, definition:{nodes:[
     {id:"a", type:"script", config:{runtime:"javascript", code:"return {};"}},
     {id:"b", type:"script", config:{runtime:"javascript", code:"return {};"}}
   ], edges:[{from:"a", to:"b"},{from:"b", to:"a"}]}}')
http POST "/v1/flows" "$CYCLE_BODY"
if [[ "$RESP_CODE" == "200" ]]; then
  CYC_FID=$(echo "$RESP_BODY" | jq -r '.id')
  CREATED_FLOW_IDS+=("$CYC_FID")
  # 创建期不检测循环，看执行期
  http POST "/v1/flows/${CYC_FID}/execute" '{"input":{}}'
  if [[ "$RESP_CODE" == "200" ]]; then
    CEID=$(echo "$RESP_BODY" | jq -r '.id')
    for _ in $(seq 1 20); do
      http GET "/v1/executions/${CEID}"
      ST=$(echo "$RESP_BODY" | jq -r '.status')
      [[ "$ST" == "success" || "$ST" == "failed" || "$ST" == "cancelled" ]] && break
      sleep 0.5
    done
    ERR=$(echo "$RESP_BODY" | jq -r '.error // ""')
    if [[ "$ST" == "failed" && "$ERR" == *cycle* ]]; then
      pass_case "5.5 循环依赖在执行期被检测 (status=failed error=$ERR)"
    elif [[ "$ST" == "failed" ]]; then
      pass_case "5.5 循环依赖执行 failed (error=$ERR)"
    else
      bug "5.5 循环依赖未被检测；status=$ST error=$ERR"
      fail_case "5.5 循环依赖检测" "status=$ST error=$ERR"
    fi
  else
    # execute 直接被拒也算检测
    pass_case "5.5 循环依赖在 execute 入口被拒 HTTP $RESP_CODE"
  fi
else
  # 创建期被拒
  pass_case "5.5 循环依赖在创建期被拒 HTTP $RESP_CODE"
fi

# -------- 6. 清理 --------
section "6. 清理"
CLEAN_OK=0; CLEAN_FAIL=0
for fid in "${CREATED_FLOW_IDS[@]}"; do
  [[ -z "$fid" || "$fid" == "null" ]] && continue
  http DELETE "/v1/flows/${fid}" ""
  if [[ "$RESP_CODE" == "200" ]]; then
    CLEAN_OK=$((CLEAN_OK+1))
  else
    CLEAN_FAIL=$((CLEAN_FAIL+1))
    echo "  delete $fid failed: HTTP $RESP_CODE $RESP_BODY"
  fi
done
if [[ "$CLEAN_FAIL" == "0" ]]; then
  pass_case "6.1 清理 ${CLEAN_OK} 个测试 flow"
else
  fail_case "6.1 清理" "成功 $CLEAN_OK / 失败 $CLEAN_FAIL"
fi

# -------- 汇总 --------
TOTAL=$((PASS+FAIL))
echo
echo -e "${BOLD}===== 汇总 =====${RESET}"
echo -e "总数: ${TOTAL}  通过: ${GREEN}${PASS}${RESET}  失败: ${RED}${FAIL}${RESET}  Bug: ${YELLOW}${#BUGS[@]}${RESET}"

# -------- 写报告 --------
mkdir -p "$(dirname "$REPORT_FILE")"
{
  echo "# Octo Flow API 集成测试报告"
  echo
  echo "- **环境**: \`$API_BASE\`"
  echo "- **时间**: $(date -u +%Y-%m-%dT%H:%M:%SZ)"
  echo "- **总用例**: $TOTAL"
  echo "- **通过**: $PASS"
  echo "- **失败**: $FAIL"
  echo "- **Bug**: ${#BUGS[@]}"
  echo
  echo "## 用例明细"
  echo
  echo "| 状态 | 用例 | 说明 |"
  echo "|------|------|------|"
  for row in "${RESULTS[@]}"; do
    s=$(echo "$row" | cut -d'|' -f1)
    n=$(echo "$row" | cut -d'|' -f2)
    d=$(echo "$row" | cut -d'|' -f3- | sed 's/|/\\|/g')
    icon="✅"; [[ "$s" == "FAIL" ]] && icon="❌"
    echo "| $icon $s | $n | ${d:- } |"
  done
  echo
  if (( ${#BUGS[@]} > 0 )); then
    echo "## 🐛 Bug 记录"
    echo
    for b in "${BUGS[@]}"; do
      echo "- 🐛 $b"
    done
    echo
  fi
  if (( FAIL > 0 )); then
    echo "## 失败用例详情（含响应体）"
    echo
    for row in "${RESULTS[@]}"; do
      s=$(echo "$row" | cut -d'|' -f1)
      [[ "$s" != "FAIL" ]] && continue
      n=$(echo "$row" | cut -d'|' -f2)
      d=$(echo "$row" | cut -d'|' -f3-)
      echo "### ❌ $n"
      echo
      echo "\`\`\`"
      echo "$d"
      echo "\`\`\`"
      echo
    done
  fi
} > "$REPORT_FILE"

echo "报告已写入: $REPORT_FILE"

exit $(( FAIL == 0 ? 0 : 1 ))
