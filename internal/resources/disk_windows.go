
 //go:build windows

 package resources

 import (
	 "syscall"
	 "unsafe"
 )
 
 var (
	 kernel32DLL         = syscall.NewLazyDLL("kernel32.dll")
	 getDiskFreeSpaceExW = kernel32DLL.NewProc("GetDiskFreeSpaceExW")
 )
 
 // diskFreeMB returns the available disk space in MiB.
 func diskFreeMB(path string) (int64, bool) {
	 pathPtr, err := syscall.UTF16PtrFromString(path)
	 if err != nil {
		 return -1, false
	 }
 
	 var availableBytes uint64
	 var totalBytes uint64
	 var totalFreeBytes uint64
 
	 result, _, _ := getDiskFreeSpaceExW.Call(
		 uintptr(unsafe.Pointer(pathPtr)),
		 uintptr(unsafe.Pointer(&availableBytes)),
		 uintptr(unsafe.Pointer(&totalBytes)),
		 uintptr(unsafe.Pointer(&totalFreeBytes)),
	 )
 
	 if result == 0 {
		 return -1, false
	 }
 
	 return int64(availableBytes / (1024 * 1024)), true
 }
 