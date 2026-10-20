package main

import (
	"fmt"
	"os"
)

func anchorCommand(args []string) int {
	repoDir := "."
	for i := 0; i < len(args); i++ {
		if args[i] == "--repo" && i+1 < len(args) {
			repoDir = args[i+1]
			i++
		}
	}

	line, err := AnchorLine(repoDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "f0 anchor: %v\n", err)
		return 1
	}

	fmt.Println(line)
	return 0
}
