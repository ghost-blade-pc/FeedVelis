"""严格统计 Go JSON 测试结果，任何 skip 或失败均不算验收通过。"""
import json
import sys

counts = {"pass": 0, "fail": 0, "skip": 0}
top = 0
for line in open(sys.argv[1], encoding="utf-8"):
    event = json.loads(line)
    action = event.get("Action")
    if "Test" in event and action in counts:
        counts[action] += 1
        if action == "pass" and "/" not in event["Test"]:
            top += 1
    if action == "output" and any(x in event.get("Output", "") for x in ("FAIL", "SKIP", "DATA RACE", "SQLSTATE", "实际 nginx", "语义支持", "慢 TCP", "心跳间隔")):
        print(event["Output"], end="")
    if action == "fail" and "Test" not in event:
        counts["fail"] += 1
print(f"{sys.argv[1]}: 顶层通过={top}，含子用例={counts}")
if counts["skip"] or counts["fail"] or not counts["pass"]:
    sys.exit(1)
