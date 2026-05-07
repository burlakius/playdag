import sys, json
ctx = json.load(sys.stdin)
print(json.dumps({"status": "PASSED", "provides": {"mock.key": "mock_value"}}))
