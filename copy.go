// +build !linux

package main

import (
	"io"
	"os"
)

func copyFile(src, dst *os.File) error {
	_, err := io.Copy(dst, src)
	return err
}
