//go:build windows

package otd

import (
	"io"
	"os"
)

func dialPipe(name string) (io.ReadWriteCloser, error) {
	return os.OpenFile(`\\.\pipe\`+name, os.O_RDWR, 0)
}
