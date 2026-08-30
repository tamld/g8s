# Session Hygiene

At the end of every g8s session, run:

```bash
bash tools/cleanup_all.sh
```

This reclaims ~15 GB across:
- Go build cache (11 GB → 2 MB)
- Go mod cache (3.2 GB → 172 KB)
- Package manager caches (~3 GB)
- /tmp scratch dirs (~90 MB)
- Merged branches (saves dozens of refs)

For dry-run preview: `bash tools/cleanup_all.sh --dry-run`
