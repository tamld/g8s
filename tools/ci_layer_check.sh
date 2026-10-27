#!/usr/bin/env bash
# Classify each changed file in this PR into one of 4 layers:
# - internal/orchestrator/ -> supervisor layer (S)
# - internal/worker/       -> worker layer (W)
# - internal/controlplane/ -> shared DB layer (D) [allowed with S or W]
# - cmd/g8s/               -> CLI layer (C)
#
# Rules:
# - False positive allowed: S+D, W+D (controlplane is shared)
# - True positive: S+W (disjoint layers touching each other)
# - True positive: W+C (worker must not own CLI)
# - True positive: S+C alone is allowed (orchestrator can edit CLI)
# - True positive: standalone single-layer PR is allowed

set -euo pipefail
cd "$(git rev-parse --show-toplevel)"

# Get the diff between HEAD and merge-base with main
BASE_BRANCH="${1:-main}"
BASE_SHA=$(git merge-base HEAD "origin/$BASE_BRANCH" 2>/dev/null || git merge-base HEAD "$BASE_BRANCH" 2>/dev/null || echo "$BASE_BRANCH")

check_file_list() {
  local files="$1"
  local s=0 w=0 c=0
  for f in $files; do
    case "$f" in
      internal/orchestrator/*) s=1 ;;
      internal/worker/*)       w=1 ;;
      cmd/g8s/*)               c=1 ;;
    esac
  done

  # Disjoint: W + S
  if [ "$w" = "1" ] && [ "$s" = "1" ]; then
    echo "::error::PR touches both internal/orchestrator/ and internal/worker/ — disjoint layer ownership violation (DEBT-34)"
    exit 1
  fi

  # Disjoint: W + C (worker must not edit CLI)
  if [ "$w" = "1" ] && [ "$c" = "1" ]; then
    echo "::error::PR touches both internal/worker/ and cmd/g8s/ — disjoint layer ownership violation (DEBT-34)"
    exit 1
  fi
  S=$s; W=$w; C=$c
}

S=0; W=0; C=0
if [ -n "${CHANGED_FILES+x}" ]; then
  check_file_list "$CHANGED_FILES"
else
  COMMITS=$(git rev-list "$BASE_SHA"..HEAD 2>/dev/null || echo "")
  COMMIT_COUNT=$(printf "%s\n" "$COMMITS" | sed '/^$/d' | wc -l | tr -d ' ')
  if [ "$COMMIT_COUNT" -gt 1 ]; then
    for sha in $COMMITS; do
      COMMIT_FILES=$(git diff-tree --no-commit-id --name-only -r "$sha" 2>/dev/null || echo "")
      check_file_list "$COMMIT_FILES"
    done
  else
    CHANGED=$(git diff --name-only "$BASE_SHA"...HEAD 2>/dev/null || git diff --name-only "$BASE_SHA" 2>/dev/null || echo "")
    check_file_list "$CHANGED"
  fi
fi

echo "Layer check: OK (S=$S W=$W C=$C)"
