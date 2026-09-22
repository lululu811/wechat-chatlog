//go:build unix

package mdexport

import (
	"os/exec"
	"syscall"
)

// setKillProcessGroup 让超时时连脚本拉起的整棵进程树一起收掉。
//
// 导出脚本会启动外部抓取器，抓取器往往再拉起一个无头浏览器 ——
// 只 kill bash 会留下这些孙进程：它们既占着 stdout 管道让 cmd.Wait() 不返回，
// 又会在后台攒下一堆抓到一半的浏览器。
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
