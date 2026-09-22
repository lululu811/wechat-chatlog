//go:build !unix

package mdexport

import "os/exec"

// setKillProcessGroup 非 unix 平台没有进程组可用的 kill 方式，保持默认行为。
//
// 兜底仍然存在：cmd.WaitDelay 会在超时后强制关闭 I/O 管道，
// 保证 cmd.Wait() 一定会返回 —— 只是可能留下孤儿进程，需要调用方自行清理。
func setKillProcessGroup(cmd *exec.Cmd) {}
