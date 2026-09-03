// Package completion generates shell auto-completion scripts for bash, zsh, and fish.
package completion

import (
	"fmt"
	"strings"
)

// SupportedShells lists shells for which completion scripts can be generated.
var SupportedShells = []string{"bash", "zsh", "fish"}

// Generate produces a shell autocompletion script for the specified shell.
func Generate(shell string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(shell)) {
	case "zsh":
		return generateZsh(), nil
	case "bash":
		return generateBash(), nil
	case "fish":
		return generateFish(), nil
	default:
		return "", fmt.Errorf("unsupported shell %q (supported: bash, zsh, fish)", shell)
	}
}

func generateZsh() string {
	return `#compdef g8s

_g8s() {
    local -a commands
    commands=(
        'submit:Queue an asynchronous durable task with harness safety checks'
        'get:Show the durable state of one queued task'
        'resume:Resume a NEEDS_INFO/BLOCKED task'
        'tasks:List durable tasks optionally filtered by state'
        'lineage:Show ancestry tree for a task up to root'
        'children:List direct child subtasks for a task'
        'receipt:Issue write delegation receipts'
        'doctor:Run diagnostic health and environment sanity checks'
        'init:Initialize g8s and configure IDE MCP integration'
        'config:Manage persistent configuration key-values'
        'completion:Generate shell autocompletion scripts'
        'service:Manage background daemon lifecycle'
        'mcp:Serve the Stdio JSON-RPC MCP surface on stdin/stdout'
        'roles:List registered worker roles'
        'permissions:List registered permission profiles'
        'version:Show application version'
        'help:Show help message'
    )

    if (( CURRENT == 2 )); then
        _describe -t commands 'g8s command' commands
        return
    fi

    case "$words[2]" in
        submit)
            _arguments \
                '--prompt=[Prompt text for worker]:prompt:' \
                '--role=[Worker role profile]:role:(collector scout mcp-mapper summarizer verifier test-runner)' \
                '--permission=[Permission profile]:permission:(read_only automation_read workspace_write)' \
                '--model=[Target model name]:model:(gemini-3.8-flash-high claude-3-7-sonnet-latest gpt-4o)' \
                '--timeout=[Execution timeout duration]:timeout:' \
                '--idempotency-key=[Unique submission key]:key:' \
                '--add-dir=[Allowed directory path]:dir:_files -/' \
                '--receipt-id=[Write receipt ID for workspace_write]:receipt:' \
                '--no-sandbox[Disable OS process sandbox]'
            ;;
        get|lineage|children|resume)
            _arguments '1:task ID:'
            ;;
        doctor)
            _arguments \
                '--json[Emit JSON formatted report]' \
                '--fix[Apply automatic self-healing remediations]'
            ;;
        init)
            _arguments \
                '--agent[Non-interactive headless agent mode]' \
                '--ide=[Target IDE to configure]:ide:(cursor claude windsurf antigravity all)' \
                '--json[Emit JSON output]'
            ;;
        config)
            _arguments \
                '1:subcommand:(get set list unset)' \
                '2:key:(evidence_dir default_timeout default_model default_role log_level)'
            ;;
        completion)
            _arguments '1:shell:(bash zsh fish)'
            ;;
        service)
            _arguments '1:subcommand:(install start stop restart status uninstall)'
            ;;
    esac
}

_g8s "$@"
`
}

func generateBash() string {
	return `#!/usr/bin/env bash

_g8s_completions() {
    local cur prev opts
    COMPREPLY=()
    cur="${COMP_WORDS[COMP_CWORD]}"
    prev="${COMP_WORDS[COMP_CWORD-1]}"
    opts="submit get resume tasks lineage children receipt doctor init config completion service mcp roles permissions version help"

    if [[ ${COMP_CWORD} -eq 1 ]]; then
        COMPREPLY=( $(compgen -W "${opts}" -- "${cur}") )
        return 0
    fi

    case "${COMP_WORDS[1]}" in
        submit)
            local submit_opts="--prompt --role --permission --model --timeout --idempotency-key --add-dir --receipt-id --no-sandbox"
            case "${prev}" in
                --role)
                    COMPREPLY=( $(compgen -W "collector scout mcp-mapper summarizer verifier test-runner" -- "${cur}") )
                    return 0
                    ;;
                --permission)
                    COMPREPLY=( $(compgen -W "read_only automation_read workspace_write" -- "${cur}") )
                    return 0
                    ;;
                *)
                    COMPREPLY=( $(compgen -W "${submit_opts}" -- "${cur}") )
                    return 0
                    ;;
            esac
            ;;
        doctor)
            COMPREPLY=( $(compgen -W "--json --fix" -- "${cur}") )
            return 0
            ;;
        init)
            COMPREPLY=( $(compgen -W "--agent --ide --json" -- "${cur}") )
            return 0
            ;;
        config)
            if [[ ${COMP_CWORD} -eq 2 ]]; then
                COMPREPLY=( $(compgen -W "get set list unset" -- "${cur}") )
            elif [[ ${COMP_CWORD} -eq 3 ]]; then
                COMPREPLY=( $(compgen -W "evidence_dir default_timeout default_model default_role log_level" -- "${cur}") )
            fi
            return 0
            ;;
        completion)
            COMPREPLY=( $(compgen -W "bash zsh fish" -- "${cur}") )
            return 0
            ;;
        service)
            COMPREPLY=( $(compgen -W "install start stop restart status uninstall" -- "${cur}") )
            return 0
            ;;
    esac
}

complete -F _g8s_completions g8s
`
}

func generateFish() string {
	return `# fish completion for g8s

set -l commands submit get resume tasks lineage children receipt doctor init config completion service mcp roles permissions version help

complete -c g8s -f
complete -c g8s -n "not __fish_seen_subcommand_from $commands" -a "$commands"

complete -c g8s -n "__fish_seen_subcommand_from submit" -l role -a "collector scout mcp-mapper summarizer verifier test-runner"
complete -c g8s -n "__fish_seen_subcommand_from submit" -l permission -a "read_only automation_read workspace_write"
complete -c g8s -n "__fish_seen_subcommand_from submit" -l no-sandbox

complete -c g8s -n "__fish_seen_subcommand_from doctor" -l json -d "Emit JSON report"
complete -c g8s -n "__fish_seen_subcommand_from doctor" -l fix -d "Auto-repair permissions and state directories"

complete -c g8s -n "__fish_seen_subcommand_from init" -l agent -d "Headless mode"
complete -c g8s -n "__fish_seen_subcommand_from init" -l ide -a "cursor claude windsurf antigravity all"
complete -c g8s -n "__fish_seen_subcommand_from init" -l json -d "JSON output"

complete -c g8s -n "__fish_seen_subcommand_from config" -a "get set list unset"
complete -c g8s -n "__fish_seen_subcommand_from completion" -a "bash zsh fish"
complete -c g8s -n "__fish_seen_subcommand_from service" -a "install start stop restart status uninstall"
`
}
