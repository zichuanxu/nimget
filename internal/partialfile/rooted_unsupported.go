//go:build !linux && !darwin && !windows

package partialfile

import (
	"errors"
	"os"
)

var errRootedUnsupported = errors.New("safe magnet staging is unavailable on this platform")

func handleIdentity(*os.File) (Object, error)                       { return Object{}, errRootedUnsupported }
func inspectRooted(*os.File, string) (Object, error)                { return Object{}, errRootedUnsupported }
func openRooted(*os.File, string, bool, bool) (*os.File, error)     { return nil, errRootedUnsupported }
func moveRooted(*os.File, string, string, Object) error             { return errRootedUnsupported }
func renameRooted(*os.File, string, *os.File, string, Object) error { return errRootedUnsupported }
func mkdirPrivate(*os.Root, string) error                           { return errRootedUnsupported }
func removePrivateDirectory(*os.File, string, Object) error         { return errRootedUnsupported }
func syncRoot(*os.File) error                                       { return errRootedUnsupported }
