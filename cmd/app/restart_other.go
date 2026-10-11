//go:build windows

package main

// restartInPlace isn't available here: the process exits and whatever
// started it starts it again.
func restartInPlace(string) {}
