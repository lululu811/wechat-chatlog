package bizhub

import "errors"

var (
	ErrWorkDirEmpty = errors.New("bizhub: workDir is empty")
	ErrNoWechatDB   = errors.New("bizhub: wechatdb is nil")
	ErrStoreClosed  = errors.New("bizhub: store is closed")
	ErrFetchFailed  = errors.New("bizhub: fetch failed")
	ErrNotJSON      = errors.New("bizhub: llm output is not valid json")
)
