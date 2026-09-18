import json, subprocess
result = subprocess.run(['gh', 'run', 'view', '35264223827', '--json', 'jobs'], capture_output=True, text=True)
print('stdout:', result.stdout[:200])
print('stderr:', result.stderr)
data = json.loads(result.stdout)
print('Jobs:', len(data.get('jobs', [])))
for job in data.get('jobs', []):
    name = job.get('name', '').lower()
    conclusion = job.get('conclusion')
    print(f'  Job: {name}, Conclusion: {conclusion}')
    if 'windows' in name and conclusion != 'success':
        print('windows_failed')
    if 'windows' not in name and conclusion != 'success':
        print('non_windows_failed')