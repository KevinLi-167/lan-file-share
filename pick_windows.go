//go:build windows

package main

import (
	"path/filepath"
	"runtime"
	"syscall"
	"unsafe"
)

// 通过 Win32 原生「打开文件」对话框让管理端选择要共享的本机文件。
// 只登记路径，不复制文件内容。

var (
	comdlg32            = syscall.NewLazyDLL("comdlg32.dll")
	procGetOpenFileName = comdlg32.NewProc("GetOpenFileNameW")
)

const (
	ofnReadOnly      = 0x00000001
	ofnHideReadOnly  = 0x00000004
	ofnNoChangeDir   = 0x00000008
	ofnPathMustExist = 0x00000800
	ofnFileMustExist = 0x00001000
	ofnAllowMultiSel = 0x00000200
	ofnExplorer      = 0x00080000
	openFileBufChars = 32768
)

type openFileNameW struct {
	StructSize      uint32
	Owner           uintptr
	Instance        uintptr
	Filter          *uint16
	CustomFilter    *uint16
	MaxCustomFilter uint32
	FilterIndex     uint32
	File            *uint16
	MaxFile         uint32
	FileTitle       *uint16
	MaxFileTitle    uint32
	InitialDir      *uint16
	Title           *uint16
	Flags           uint32
	FileOffset      uint16
	FileExtension   uint16
	DefExt          *uint16
	CustData        uintptr
	Hook            uintptr
	TemplateName    *uint16
	Reserved        uintptr
	ReservedDword   uint32
	FlagsEx         uint32
}

func utf16Ptr(value string) *uint16 {
	ptr, err := syscall.UTF16PtrFromString(value)
	if err != nil {
		return nil
	}
	return ptr
}

// pickLocalFiles 返回用户在原生对话框中选中的本机文件绝对路径。
// 用户取消时返回空切片且 error 为 nil。
func pickLocalFiles() ([]string, error) {
	// 对话框依赖调用线程的消息队列，把当前 goroutine 钉在这个线程上。
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	// 过滤器是「显示名\x00通配符\x00...\x00\x00」的双 null 结尾格式
	filter := make([]uint16, 0, 64)
	filter = appendUTF16(filter, "所有文件")
	filter = appendUTF16(filter, "*.*")
	filter = append(filter, 0)

	buffer := make([]uint16, openFileBufChars)

	ofn := openFileNameW{
		StructSize: uint32(unsafe.Sizeof(openFileNameW{})),
		Filter:     &filter[0],
		File:       &buffer[0],
		MaxFile:    uint32(len(buffer)),
		Title:      utf16Ptr("选择要共享的本机文件"),
		Flags: ofnExplorer | ofnAllowMultiSel | ofnFileMustExist |
			ofnPathMustExist | ofnNoChangeDir | ofnHideReadOnly | ofnReadOnly,
	}

	ret, _, _ := procGetOpenFileName.Call(uintptr(unsafe.Pointer(&ofn)))
	if ret == 0 {
		// 用户取消或系统报错，都按「没有选择」处理
		return nil, nil
	}

	return parseMultiSelect(buffer), nil
}

func appendUTF16(dst []uint16, value string) []uint16 {
	for _, r := range value {
		dst = append(dst, uint16(r))
	}
	return append(dst, 0)
}

// parseMultiSelect 解析 OFN_ALLOWMULTISELECT 的返回缓冲：
// 单选取时是一整条完整路径；多选取时是「目录\x00文件1\x00文件2\x00\x00」。
func parseMultiSelect(buffer []uint16) []string {
	end := indexOfZero(buffer)
	if end < 0 {
		return nil
	}
	first := syscall.UTF16ToString(buffer[:end])

	rest := buffer[end+1:]
	if len(rest) == 0 || rest[0] == 0 {
		return []string{first}
	}

	dir := first
	var out []string
	for len(rest) > 0 && rest[0] != 0 {
		stop := indexOfZero(rest)
		if stop < 0 {
			break
		}
		name := syscall.UTF16ToString(rest[:stop])
		if name != "" {
			out = append(out, filepath.Join(dir, name))
		}
		rest = rest[stop+1:]
	}
	return out
}

func indexOfZero(buffer []uint16) int {
	for idx, ch := range buffer {
		if ch == 0 {
			return idx
		}
	}
	return -1
}
