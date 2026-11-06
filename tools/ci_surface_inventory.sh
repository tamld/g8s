#!/usr/bin/env bash
# Static CI surface inventory; no YAML parser or network access required.
# Run from any directory inside the checkout. See --help for claim semantics.
# Regex backticks match literal Markdown delimiters.
# shellcheck disable=SC2016
set -euo pipefail
export LC_ALL=C

mode=table
case "${1:-}" in
    '') ;;
    --json) mode=json ;;
    --help)
        printf '%s\n' 'Usage: bash tools/ci_surface_inventory.sh [--json]' \
            'Inventories workflow/job/step names, run file references and uses actions.' \
            'Doc paths are references unless their line asserts CI/workflow execution.' \
            'Quality Gate, Verify Gate, quoted named gates/stages and workflow paths are checked.' \
            'Static wiring is not proof of execution or live branch protection settings.' \
            'Exit 1: STALE-CLAIM or BROKEN-WIRE; ORPHAN is advisory. Exit 2: usage/input error.'
        exit 0 ;;
    *) printf 'Unknown argument: %s\n' "$1" >&2; exit 2 ;;
esac
if (( $# > 1 )); then exit 2; fi
cd "$(git rev-parse --show-toplevel)"

# Parallel indexed arrays keep this usable with macOS Bash 3 as well as Ubuntu.
kinds=() surfaces=() sources=() verdicts=() evidence=()
wired_paths=() wired_sources=() wired_names=() wired_name_sources=()
stale=0 broken=0 orphan=0
add() {
    kinds+=("$1"); surfaces+=("$2"); sources+=("$3")
    verdicts+=("$4"); evidence+=("$5")
    case "$4" in
        STALE-CLAIM) stale=$((stale + 1)) ;;
        BROKEN-WIRE) broken=$((broken + 1)) ;;
        ORPHAN) orphan=$((orphan + 1)) ;;
    esac
}
trim() { REPLY=$(printf '%s' "$1" | sed -E "s/^[[:space:]\"']+//; s/[[:space:]\"']+$//"); }
# rg is optional, with a POSIX grep fallback on minimal CI images.
matches() {
    if command -v rg >/dev/null 2>&1; then
        printf '%s\n' "$2" | rg -o -- "$1" || :
    else
        printf '%s\n' "$2" | grep -Eo -- "$1" || :
    fi
}
lookup_path() {
    local i
    REPLY=
    for ((i=0; i<${#wired_paths[@]}; i++)); do
        if [[ ${wired_paths[i]} == "$1" ]]; then REPLY=${wired_sources[i]}; return; fi
    done
}
lookup_name() {
    local i
    REPLY=
    for ((i=0; i<${#wired_names[@]}; i++)); do
        if [[ ${wired_names[i]} == "$1" ]]; then REPLY=${wired_name_sources[i]}; return; fi
    done
}
file_pattern='[[:alnum:]_.-]+(/[[:alnum:]_.-]+)+\.(sh|ps1)|\.[[:alnum:]_-]+\.(toml|ya?ml|json)'
shopt -s nullglob
workflows=(.github/workflows/*.yml .github/workflows/*.yaml)
if (( ${#workflows[@]} == 0 )); then printf 'No workflows found\n' >&2; exit 2; fi
for file in "${workflows[@]}"; do
    add wired-workflow "$file" "$file" WIRED 'workflow file'
    wired_paths+=("$file"); wired_sources+=("$file")
    number=0 run_indent=-1 jobs=0 heredoc=
    while IFS= read -r line || [[ -n $line ]]; do
        number=$((number + 1)); source="$file:$number"
        if [[ -n $heredoc ]]; then
            trim "$line"
            [[ $REPLY == "$heredoc" ]] && heredoc=
            continue
        fi
        [[ $line =~ ^[[:space:]]*# || -z ${line//[[:space:]]/} ]] && continue
        prefix=${line%%[![:space:]]*}; indent=${#prefix}
        if (( run_indent >= 0 && indent <= run_indent )); then run_indent=-1; fi
        if [[ $line == 'jobs:' ]]; then jobs=1; continue; fi
        if (( jobs == 1 && indent == 2 )) && [[ $line =~ ^[[:space:]]+([[:alnum:]_-]+): ]]; then
            name=${BASH_REMATCH[1]}
            add wired-job "$name" "$source" WIRED 'job id'
            wired_names+=("$name"); wired_name_sources+=("$source")
        fi
        if [[ $line =~ ^[[:space:]]*(-[[:space:]]+)?name:[[:space:]]*(.*) ]] && (( indent == 0 || indent == 4 || indent == 6 || indent == 8 )); then
            trim "${BASH_REMATCH[2]}"; name=$REPLY
            add wired-name "$name" "$source" WIRED 'workflow/job/step display name'
            wired_names+=("$name"); wired_name_sources+=("$source")
        fi
        if [[ $line =~ ^[[:space:]]*(-[[:space:]]+)?uses:[[:space:]]*(.*) ]]; then
            trim "${BASH_REMATCH[2]%% #*}"
            add wired-action "$REPLY" "$source" WIRED 'uses reference'
            # golangci-lint loads its conventional config implicitly.
            if [[ $REPLY == golangci/golangci-lint-action@* && -f .golangci.yml ]]; then
                wired_paths+=(.golangci.yml); wired_sources+=("$source (action default)")
                add wired-file .golangci.yml "$source" WIRED 'golangci-lint action default config'
            fi
        fi
        code=
        if [[ $line =~ ^[[:space:]]*(-[[:space:]]+)?run:[[:space:]]*(.*) ]]; then
            code=${BASH_REMATCH[2]}; run_indent=$indent
            # A sequence marker belongs to the step, not the scalar indent.
            [[ $line =~ ^[[:space:]]*-[[:space:]] ]] && run_indent=$((indent + 2))
        elif (( run_indent >= 0 )); then code=$line
        fi
        [[ -z $code || $code =~ ^[[:space:]]*(#|echo[[:space:]]|printf[[:space:]]) ]] && continue
        heredoc=$(printf '%s' "$code" | sed -nE "s/.*<<-?[[:space:]]*['\"]?([[:alnum:]_]+)['\"]?.*/\1/p")
        while IFS= read -r path; do
            [[ -z $path ]] && continue
            if [[ $code == *"://$path"* ]]; then
                add wired-remote "$path" "$source" WIRED 'remote URL; not a checkout file'
                continue
            fi
            wired_paths+=("$path"); wired_sources+=("$source")
            verdict=WIRED; [[ -f $path ]] || verdict=BROKEN-WIRE
            add wired-file "$path" "$source" "$verdict" 'run reference'
        done < <(matches "$file_pattern" "$code")
    done < "$file"
done

docs=(README.md CHANGELOG.md docs/*.md)
for file in "${docs[@]}"; do
    [[ -f $file ]] || continue
    while IFS=: read -r number line; do
        source="$file:$number"
        ci=false
        if [[ $line =~ CI[[:space:]-]+gate|[Ww]ired.*CI|CI.*([Rr]uns|[Ee]xecutes)|([Rr]uns|[Ee]xecuted)[[:space:]]+in[[:space:]]+CI|[Ww]orkflow|Every[[:space:]]code[[:space:]]change|must[[:space:]]pass ]]; then ci=true; fi
        if [[ $line =~ not[[:space:]]+(invoked|wired|run) ]]; then ci=false; fi
        while IFS= read -r path; do
            [[ -z $path ]] && continue
            lookup_path "$path"; wire=$REPLY
            verdict=REFERENCE
            if { $ci || [[ $path == .github/workflows/* ]]; } && [[ ! $line =~ \*\*Target|local[[:space:]]and[[:space:]]CI ]]; then
                verdict=MATCH; [[ -n $wire ]] || verdict=STALE-CLAIM
            fi
            add claimed-file "$path" "$source" "$verdict" "${wire:-no workflow reference}"
        done < <(matches "$file_pattern|\.github/workflows/[[:alnum:]_.-]+\.ya?ml" "$line")
        # Backticks below are literal Markdown delimiters.
        # shellcheck disable=SC2016
        while IFS= read -r name; do
            [[ -z $name ]] && continue
            name=${name//\`/}
            lookup_name "$name"; wire=$REPLY
            verdict=MATCH; [[ -n $wire ]] || verdict=STALE-CLAIM
            add claimed-name "$name" "$source" "$verdict" "${wire:-no workflow/job/step name}"
        done < <(matches 'Quality Gate|Verify Gate|`[[:alnum:] _-]+ (Gate|Stage|Workflow)`' "$line")
        if [[ $line =~ [Ww]ired.*CI ]]; then
            add claimed-prose "$line" "$source" REVIEW 'prose retained; file/name rows carry mechanical verdicts'
        fi
    done < <(grep -nE '(tools|scripts)/|\.github/workflows/|Quality Gate|Verify Gate|Gate`|Stage`|[Ww]ired.*CI' "$file" || :)
done

# Only shell surfaces are orphan candidates. Any tracked non-self reference
# counts, including self-test/Makefile/local gate callers; comments may count.
while IFS= read -r path; do
    [[ -f $path && $path == *.sh ]] || continue
    lookup_path "$path"
    [[ -n $REPLY ]] && continue
    if git grep -q -F -e "$path" -- . ":(exclude)$path"; then continue; fi
    sibling=${path%.sh}_test.sh
    [[ $path == *_test.sh && -f ${path%_test.sh}.sh ]] && continue
    [[ -f $sibling ]] && continue
    add disk-script "$path" "$path" ORPHAN 'no tracked path reference or sibling self-test'
done < <(git ls-files --cached --others --exclude-standard -- 'tools/*.sh' 'scripts/*.sh')

json_string() {
    local value=$1
    value=${value//\\/\\\\}; value=${value//\"/\\\"}
    value=${value//$'\n'/\\n}; value=${value//$'\r'/\\r}; value=${value//$'\t'/\\t}
    printf '"%s"' "$value"
}
if [[ $mode == json ]]; then
    printf '{"schema_version":1,"branch_protection":"not queried (offline inventory)","inventory":['
    for ((i=0; i<${#kinds[@]}; i++)); do
        (( i == 0 )) || printf ','
        printf '{"kind":'; json_string "${kinds[i]}"
        printf ',"surface":'; json_string "${surfaces[i]}"
        printf ',"source":'; json_string "${sources[i]}"
        printf ',"verdict":'; json_string "${verdicts[i]}"
        printf ',"evidence":'; json_string "${evidence[i]}"; printf '}'
    done
    printf '],"summary":{"stale_claims":%d,"broken_wires":%d,"orphans":%d}}\n' "$stale" "$broken" "$orphan"
else
    printf '| Kind | Surface | Source | Verdict | Wiring/evidence |\n| --- | --- | --- | --- | --- |\n'
    for ((i=0; i<${#kinds[@]}; i++)); do
        printf '| %s | %s | %s | %s | %s |\n' "${kinds[i]}" "${surfaces[i]//|/\\|}" "${sources[i]}" "${verdicts[i]}" "${evidence[i]//|/\\|}"
    done
    printf '\nSTALE-CLAIM=%d BROKEN-WIRE=%d ORPHAN=%d\n' "$stale" "$broken" "$orphan"
    printf 'Branch protection: not queried (offline inventory); names above are static wiring only.\n'
fi
(( stale == 0 && broken == 0 ))
