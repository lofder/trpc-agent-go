#!/usr/bin/env bash
# 在 CI 里对测试平台跑一轮用例回归：
#   ./ci/run_cases.sh [BASE_URL] [case_id ...]
# 不带 case_id 时运行全部用例；有失败/错误时退出码为 1。
# 典型 CI 用法：后台启动平台 → 等待就绪 → 执行本脚本。
set -euo pipefail

BASE_URL="${1:-http://localhost:8080}"
shift || true

# 等待平台就绪（最多 30s）。
for i in $(seq 1 30); do
  if curl -sf "$BASE_URL/api/status" > /dev/null 2>&1; then break; fi
  [ "$i" = 30 ] && { echo "平台未就绪: $BASE_URL"; exit 2; }
  sleep 1
done

IDS_JSON=$(python3 - "$@" <<'EOF'
import json, sys
print(json.dumps({"case_ids": sys.argv[1:]}))
EOF
)

TR_ID=$(curl -sf -X POST "$BASE_URL/api/testruns" -d "$IDS_JSON" | python3 -c "import json,sys; print(json.load(sys.stdin)['id'])")
echo "▶ 批量测试已启动: $TR_ID"

while true; do
  BODY=$(curl -sf "$BASE_URL/api/testruns/$TR_ID")
  STATUS=$(echo "$BODY" | python3 -c "import json,sys; print(json.load(sys.stdin)['status'])")
  [ "$STATUS" != "running" ] && break
  sleep 2
done

TR_BODY="$BODY" python3 <<'PYEOF'
import json, os, sys
tr = json.loads(os.environ["TR_BODY"])
print(f"状态: {tr['status']} | 通过 {tr['passed']} / 失败 {tr['failed']} / 错误 {tr['errored']} (共 {tr['total']}) 用时 {tr['duration_ms']}ms")
for cr in tr["results"]:
    mark = {"passed": "PASS", "failed": "FAIL"}.get(cr["status"], "ERROR")
    print(f"  [{mark}] {cr['case_name']} ({cr['duration_ms']}ms)")
    if cr["status"] != "passed":
        if cr.get("error"):
            print(f"        error: {cr['error']}")
        for a in cr["assertions"]:
            if not a["passed"]:
                print(f"        assert {a['type']}: {a['message']} (实际: {a.get('actual', '')})")
bad = tr["failed"] + tr["errored"]
sys.exit(1 if bad else 0)
PYEOF
