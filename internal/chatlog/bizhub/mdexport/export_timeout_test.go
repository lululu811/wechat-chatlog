package mdexport

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestExportTimeoutActuallyReturns 超时必须真的能结束调用，而不是卡死在 cmd.Wait()。
//
// 背景：脚本会拉起外部抓取器，抓取器再拉起无头浏览器。CommandContext 只 kill
// 直接子进程（bash），这些孙进程继续持有 stdout/stderr 的写端，
// cmd.Wait() 要等管道关闭 —— 于是超时形同虚设：HTTP 请求一直挂着，
// 后台还攒下一堆抓到一半的浏览器进程。
//
// 这个测试用一个「后台 sleep 继承 stdout 后不退出」的替身脚本复现该场景。
func TestExportTimeoutActuallyReturns(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("依赖 bash 与 unix 进程组，Windows 上跳过")
	}

	dir := t.TempDir()
	script := filepath.Join(dir, "hang.sh")
	pidFile := filepath.Join(dir, "child.pid")

	// 后台进程故意不重定向 stdout：这样它会一直持有管道，
	// 即使父 bash 被杀，cmd.Wait() 也等不到管道关闭。
	src := fmt.Sprintf(`#!/usr/bin/env bash
sleep 60 &
echo $! > %q
sleep 60
`, pidFile)
	if err := os.WriteFile(script, []byte(src), 0o755); err != nil {
		t.Fatalf("写入替身脚本: %v", err)
	}

	exp, err := New(Config{
		Script:    script,
		OutputDir: filepath.Join(dir, "out"),
		Timeout:   time.Second,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		_, e := exp.Export(context.Background(), "https://mp.weixin.qq.com/s/test")
		done <- e
	}()

	select {
	case e := <-done:
		if e == nil {
			t.Fatal("期望超时错误，实际返回 nil")
		}
		var ee *ExportError
		if !errors.As(e, &ee) {
			t.Fatalf("期望 *ExportError，实际 %T: %v", e, e)
		}
		if ee.Kind != KindTimeout {
			t.Errorf("失败分类 = %s，期望 %s", ee.Kind, KindTimeout)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("Export 超时后仍未返回 —— 孙进程持有 stdout 管道，cmd.Wait() 被卡死")
	}

	// 整棵树都该被收掉：只杀 bash 会留下这个 sleep，
	// 每一次卡住的导出都会在后台多留一个这样的孤儿。
	raw, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("读取子进程 pid 文件: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatalf("解析 pid %q: %v", raw, err)
	}
	if alive(pid) {
		t.Errorf("后台孙进程 %d 仍然存活 —— 超时只杀了 bash，进程树没被清理", pid)
	}
}

// alive 用 kill -0 探测进程是否还在（比直接 import syscall 更跨平台）。
func alive(pid int) bool {
	cmd := exec.Command("kill", "-0", strconv.Itoa(pid))
	return cmd.Run() == nil
}
