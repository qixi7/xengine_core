package xutil

import (
	"os"
	"strconv"
)

// 可以考虑对pid文件加个文件锁
type FLock struct {
	f *os.File
}

func NewFlock(fileName string) (*FLock, error) {
	f, err := os.OpenFile(fileName, os.O_CREATE|os.O_TRUNC|os.O_RDWR, os.FileMode(0600))
	if err != nil {
		return nil, err
	}
	f.WriteString(strconv.Itoa(os.Getpid()))
	f.WriteString("\n")
	f.Close()
	return &FLock{f: f}, nil
}

func (f *FLock) Close() {
	f.f.Close()
	f.f = nil
}
