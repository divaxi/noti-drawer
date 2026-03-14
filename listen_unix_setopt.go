//go:build unix && !freebsd && !solaris

package notidrawer

import "golang.org/x/sys/unix"

const unixSOREUSEPORT = unix.SO_REUSEPORT
