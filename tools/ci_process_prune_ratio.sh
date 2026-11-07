#!/usr/bin/env bash
# D-09 prune-law gate. Receipts are individual JSON files, newest by cycle.timestamp.
# Usage: ci_process_prune_ratio.sh <receipts-dir>
# Optional G8S_REFLECTION_TOMBSTONE names the persistent marker to write. Once
# tombstoned, re-creation requires an explicit operator act: remove the marker
# after reviewing the reflection history. This script never removes it.
# Quarter accounting uses UTC calendar quarters; streak uses all supplied history.
set -euo pipefail

receipts_dir=${1:?usage: ci_process_prune_ratio.sh <receipts-dir>}
if [[ ! -d "$receipts_dir" ]]; then
  echo "E_REFLEX_RECEIPTS_DIR_MISSING: $receipts_dir" >&2
  exit 1
fi
shopt -s nullglob
receipts=("$receipts_dir"/*.json)
if (( ${#receipts[@]} == 0 )); then
  echo "no reflection receipts in state; prune-law gate skipped"
  exit 0
fi

# Parse and validate every input before emitting any decision. jq errors propagate.
for receipt in "${receipts[@]}"; do
  jq -e -s 'if length == 1 then true else error("expected one receipt per file") end' "$receipt" >/dev/null
done
summary=$(jq -ser '
  def valid:
    type == "object" and
    (.cycle.timestamp | type == "string") and
    (.cycle.timestamp | test("^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(\\.[0-9]+)?Z$")) and
    (.add_list | type == "array" and all(.[]; type == "string")) and
    (.prune_list | type == "array" and all(.[]; type == "string"));
  def timestamp: .cycle.timestamp as $at
    | ($at | sub("\\.[0-9]+Z$"; "Z") | fromdateiso8601)
      + (if $at | contains(".") then ($at | capture("(?<fraction>\\.[0-9]+)Z$").fraction | tonumber) else 0 end);
  if all(.[]; valid) then . else error("invalid reflection receipt") end
  | map(. + {epoch: timestamp})
  | (now | gmtime | .[1] = ((.[1] / 3 | floor) * 3) | .[2] = 1
      | .[3] = 0 | .[4] = 0 | .[5] = 0 | mktime) as $quarter
  | (map(select(.epoch >= $quarter))) as $current
  | ($current | map(.add_list | length) | add // 0) as $adds
  | ($current | map(.prune_list | length) | add // 0) as $prunes
  | (sort_by(.epoch) | reverse | .[:3]
      | map(select(.prune_list | length == 0)) | length) as $empty
  | [$adds, $prunes, (if length >= 3 and $empty == 3 then 3 else 0 end)]
  | @tsv
' "${receipts[@]}")
IFS=$'\t' read -r adds prunes streak <<< "$summary"
if (( streak >= 3 )); then
  echo "TOMBSTONE: $streak consecutive empty-prune cycles"
  if [[ -n "${G8S_REFLECTION_TOMBSTONE:-}" ]]; then
    touch -- "$G8S_REFLECTION_TOMBSTONE"
  fi
fi
if (( adds > 0 && prunes < adds )); then
  echo "E_REFLEX_PRUNE_RATIO_BELOW_ONE: quarter adds=$adds prunes=$prunes" >&2
  exit 1
fi
echo "prune-law gate: quarter adds=$adds prunes=$prunes"
