//go:build !unix

package psc

import "os"

func ownedBy(os.FileInfo, int) bool { return true }
