//go:build !windows

package main

import "fmt"

func pickLocalFiles() ([]string, error) {
	return nil, fmt.Errorf("原生文件选择对话框仅支持 Windows")
}
