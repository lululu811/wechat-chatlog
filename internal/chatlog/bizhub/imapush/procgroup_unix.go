//go:build unix

package imapush

import (
	"os/exec"
	"syscall"
)

// setKillProcessGroup 让超时时连 node + imaskai 拉起的整棵进程树一起收掉。
//
// imaskai 是 Node.js 脚本，可能拉起 fetch / 解压 / 异步请求等孙进程。
// CommandContext 只 kill 直接子进程（node），这些孙进程继续持有 stdout/stderr 的写端，
// cmd.Wait() 要等管道关闭 —— 于是超时形同虚设：HTTP 请求一直挂着，
// 后台还攒下一堆做到一半的子进程。
//
// 做法：给子进程单独开一个进程组（Setpgid），取消时用负 pid 把整组 SIGKILL。
func setKillProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		// 负 pid 表示整个进程组
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
