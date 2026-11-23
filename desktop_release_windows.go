//go:build windows && !server && !desktoptest

package main

// webviewTestArguments leaves remote debugging disabled in distributed executables.
func webviewTestArguments() []string { return nil }
