// +build linux

package main

/*
#include <linux/fs.h>

#ifndef FICLONE
# define FICLONE _IOW(0x94, 9, int)
#endif
*/
import "C"
import (
	"io"
	"os"
	"syscall"
)

var do_clone = true

func copyFile(src, dst *os.File) error {
	clone_failed := false
	if do_clone {
		src_fd := src.Fd()
		dst_fd := dst.Fd()

		err, _, _ := syscall.Syscall(syscall.SYS_IOCTL, dst_fd, C.FICLONE, src_fd)
		if err != 0 {
			prnt(2, "CoW clone failed, falling back to copy")
			clone_failed = true
		} else {
			return nil
		}
	}

	// Fallback copy
	_, err := io.Copy(dst, src)

	// Don't retry CoW if the simple copy was successful
	if clone_failed && err == nil {
		do_clone = false
	}

	return err
}
