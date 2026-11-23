//go:build windows && !server && desktoptest

package main

import (
	"os"
	"strconv"
)

// webviewTestArguments enables loopback-only browser automation in the dedicated test build.
func webviewTestArguments() []string {
	port, err := strconv.Atoi(os.Getenv("SHENGJI_TEST_CDP_PORT"))
	if err != nil || port < 1024 || port > 65535 {
		return nil
	}
	return []string{"--remote-debugging-address=127.0.0.1", "--remote-debugging-port=" + strconv.Itoa(port)}
}
