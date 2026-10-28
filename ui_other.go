//go:build !windows

package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"areacoach/internal/platform"
)

func acquireSingleInstance() bool { return true }

// showUI opens the interface in the default browser and runs until the
// program is stopped (Ctrl+C or a termination signal).
func showUI(url, dataDir, theme string) {
	fmt.Println("Area Coach is running. Interface:", url)
	fmt.Println("Press Ctrl+C to quit.")
	_ = platform.Open(url)
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	<-c
}
