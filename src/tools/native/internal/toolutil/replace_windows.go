//go:build windows

package toolutil

import "golang.org/x/sys/windows"

func replacePath(source, target string) error {
	return movePath(source, target, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}

func publishNewPath(source, target string) error {
	return movePath(source, target, windows.MOVEFILE_WRITE_THROUGH)
}

func movePath(source, target string, flags uint32) error {
	sourcePointer, err := windows.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	targetPointer, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(sourcePointer, targetPointer, flags)
}
