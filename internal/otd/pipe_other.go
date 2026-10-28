//go:build !windows

package otd

import (
	"io"
	"net"
	"os"
	"path/filepath"
)

// .NET named pipes are Unix sockets in the temp folder on Linux and macOS.
func dialPipe(name string) (io.ReadWriteCloser, error) {
	dir := os.Getenv("TMPDIR")
	if dir == "" {
		dir = "/tmp"
	}
	return net.Dial("unix", filepath.Join(dir, "CoreFxPipe_"+name))
}
