import json
with open('/tmp/issues.json') as f:
    data = json.load(f)
for issue in data:
    labels = [l['name'] for l in issue['labels']]
    print(f'#{issue["number"]}: {issue["title"][:60]} | Labels: {labels}')