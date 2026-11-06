#!/usr/bin/env bash
set -euo pipefail
inventory="$(git rev-parse --show-toplevel)/tools/ci_surface_inventory.sh"
scratch=$(mktemp -d)
trap 'rm -rf "$scratch"' EXIT
cd "$scratch"
git init -q
mkdir -p .github/workflows tools docs
printf '# fixture\n' > README.md
printf 'exit 0\n' > tools/real.sh
printf 'exit 0\n' > tools/orphan.sh
cat > .github/workflows/test.yml <<'YAML'
name: Fixture Workflow
jobs:
  test:
    name: Quality Gate
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v7
      - name: Verify Gate
        run: |
          # bash tools/comment-only.sh
          echo "tools/example-only.sh"
          cat <<EOF
          bash tools/heredoc-only.sh
          EOF
          bash tools/real.sh
YAML
git add .
printf 'CI gate runs tools/real.sh. Quality Gate and Verify Gate apply.\n' > docs/checks.md
bash "$inventory" > green.txt
grep -q 'STALE-CLAIM=0 BROKEN-WIRE=0 ORPHAN=1' green.txt
grep -q 'tools/orphan.sh.*ORPHAN' green.txt
grep -q 'actions/checkout@v7' green.txt
grep -q 'Quality Gate' green.txt
printf 'tools/fake.sh is wired into CI.\n' >> docs/checks.md
if bash "$inventory" > red.txt; then printf 'Fake claim unexpectedly passed\n' >&2; exit 1; fi
grep -q 'tools/fake.sh.*STALE-CLAIM' red.txt
printf '      - run: bash tools/missing.sh\n' >> .github/workflows/test.yml
if bash "$inventory" --json > red.json; then printf 'Broken wire unexpectedly passed\n' >&2; exit 1; fi
grep -q '"surface":"tools/missing.sh".*"verdict":"BROKEN-WIRE"' red.json
grep -q '"stale_claims":1,"broken_wires":1,"orphans":1' red.json
printf 'PASS: baseline, orphan warning, heredoc/comment exclusion, fake claim red, broken wire red, JSON verdicts\n'
grep 'tools/fake.sh.*STALE-CLAIM' red.txt
