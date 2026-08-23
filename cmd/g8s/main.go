package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/tamld/g8s/internal/harness"
)

const (
	Version = "0.1.0-alpha"
	AppName = "g8s"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	command := os.Args[1]
	switch command {
	case "version":
		fmt.Printf("%s v%s (The Gatekeepers — Zero-CGO, Pure Go)\n", AppName, Version)
	case "roles":
		fmt.Println("g8s Worker Roles:")
		for _, name := range harness.RoleNames() {
			r, _ := harness.GetRole(name)
			fmt.Printf("  • %-12s : %s\n", r.Name, r.Purpose)
		}
	case "permissions":
		fmt.Println("g8s Permission Profiles:")
		for _, name := range harness.PermissionNames() {
			p, _ := harness.GetPermission(name)
			fmt.Printf("  • %-16s : %s (Mutation: %t)\n", p.Name, p.Description, p.MutationAllowed)
		}
	case "help", "-h", "--help":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown command '%s'\n\n", command)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Printf("%s (The Gatekeepers) — Zero-Trust Process & Capability Harness for AI Agents\n\n", AppName)
	fmt.Println("Usage:")
	fmt.Printf("  %s <command> [arguments]\n\n", AppName)
	fmt.Println("Commands:")
	fmt.Println("  run          Dispatch a bounded task to an AI worker (agy, claude, gemini, ollama)")
	fmt.Println("  submit       Queue an asynchronous durable task (SQLite WAL Control Plane)")
	fmt.Println("  receipt      Issue, validate, or revoke write delegation receipts")
	fmt.Println("  roles        List registered worker roles and forbidden rules")
	fmt.Println("  permissions  List registered permission profiles")
	fmt.Println("  mcp          Start Stdio JSON-RPC MCP Server")
	fmt.Println("  service      Manage background daemon (install/start/status)")
	fmt.Println("  version      Show application version")
	flag.PrintDefaults()
}
