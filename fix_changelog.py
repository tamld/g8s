with open('CHANGELOG.md', 'r') as f:
    lines = f.readlines()

new_lines = []
for line in lines:
    if line.startswith('## [Unreleased]'):
        new_lines.append('## [Unreleased]\n')
        new_lines.append('\n')
        new_lines.append('## [0.10.0] - 2026-09-20\n')
        new_lines.append('### Added\n')
        new_lines.append('- **System-wide effectiveness metrics** (#292): `SystemMetricsCollector` with task throughput, latency, success rates, worker/session metrics, supervisor aggregates. HTTP endpoints: `GET /api/v1/system/metrics` (JSON), extended `/metrics` (Prometheus).\n')
        new_lines.append('- **Session ownership and isolation** (#291): Schema v10 with `session_id` columns in tasks/supervisor_tasks tables, session-scoped queries, per-session quota tracking (`session_quotas` table).\n')
        new_lines.append('- **Supervisor intervention benchmark** (#296): 6 benchmarks covering happy path, failure recovery, escalation, cycle duration, optimizer latency, aggregate metrics computation.\n')
        new_lines.append('- **Real reviewer with scope + test gates** (#302): `RealReviewer` with diffScope (orchestrator) and test gate (go test on modified packages), 30s timeout, 1MB output cap.\n')
        new_lines.append('- **Checkpoint/recovery for long-running tasks** (#290): Checkpointing infrastructure with resume capability.\n')
        new_lines.append('\n')
        new_lines.append('### Changed\n')
        new_lines.append('- **v0.3.0 → v0.10.0**: Complete P0-P2 enhancement issues resolved (#289, #291, #292, #294, #295, #296, #302).\n')
        new_lines.append('\n')
    elif line.startswith('## [0.9.2] - 2026-09-18'):
        new_lines.append(line)
    elif line.startswith('## [0.10.0] - 2026-09-20'):
        # Skip this, we added it under unreleased
        pass
    else:
        new_lines.append(line)

with open('CHANGELOG.md', 'w') as f:
    f.writelines(new_lines)
