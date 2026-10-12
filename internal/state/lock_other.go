//go:build !unix && !windows

package state

import "os"

// tryLock does nothing where pfwd has no file locks; the Store's mutex
// still orders writers in one process.
func tryLock(*os.File) (bool, error) { return true, nil }

func unlock(*os.File) error { return nil }
