//go:build !linux

package main

import "os/exec"

func configureWrapExecChild(_ *exec.Cmd) func() {
	return func() {}
}
