package glance

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// TestScanAllWritable 用 lldb 读目标进程内存，扫描 SQLite fts5 的特征串，
// 用来定位微信进程里哪块可写内存放着能用的库。
//
// 这是一个**排障工具**，不是常规单元测试：它依赖本机确实跑着一个可被调试的
// 微信进程，以及 lldb 的调试权限（首次使用需要授权开发者工具 / 关闭 SIP 相关限制）。
// 因此默认跳过，真需要用时显式打开：
//
//	GLANCE_TEST_PID=<pid> go test ./internal/wechat/key/darwin/glance -run TestScanAllWritable -v
//
// 之前这里把 PID 硬编码成 4841 并直接 t.Fatal，导致 `make test` 在这台机器上
// 长期是红的。红灯挂久了等于没有测试 —— 真正的回归失败会被淹没在同一片红色里。
// 所以改成「不能做就明确跳过」，而不是「做不了就失败」。
func TestScanAllWritable(t *testing.T) {
	pidStr := os.Getenv("GLANCE_TEST_PID")
	if pidStr == "" {
		t.Skip("需要显式指定目标进程：GLANCE_TEST_PID=<pid>（依赖 lldb 与调试权限）")
	}

	pid64, err := strconv.ParseUint(pidStr, 10, 32)
	if err != nil {
		t.Fatalf("GLANCE_TEST_PID 不是合法 PID: %q", pidStr)
	}
	pid := uint32(pid64)

	if _, err := exec.LookPath("lldb"); err != nil {
		t.Skip("未安装 lldb，跳过")
	}
	if err := exec.Command("ps", "-p", pidStr).Run(); err != nil {
		t.Skipf("目标进程 %d 不存在或无权访问，跳过", pid)
	}

	regions, err := GetVmmap(pid)
	if err != nil {
		t.Skipf("读取进程内存映射失败（多半是缺少调试权限），跳过：%v", err)
	}

	pattern1 := []byte{0x20, 0x66, 0x74, 0x73, 0x35, 0x28, 0x25, 0x00}
	fmt.Printf("scanning %d regions for fts5 pattern...\n", len(regions))

	hits := 0
	for _, region := range regions {
		if region.Empty {
			continue
		}
		size := region.End - region.Start
		if size > 64*1024*1024 {
			continue // skip huge regions to save time
		}
		pipePath := filepath.Join(os.TempDir(), fmt.Sprintf("cp_%d_%x", time.Now().UnixNano(), region.Start))
		if err := exec.Command("mkfifo", pipePath).Run(); err != nil {
			continue
		}

		dataCh := make(chan []byte, 1)
		errCh := make(chan error, 1)
		go func() {
			defer os.Remove(pipePath)
			f, _ := os.OpenFile(pipePath, os.O_RDONLY, 0600)
			if f == nil {
				return
			}
			defer f.Close()
			data, _ := io_ReadAll(f)
			dataCh <- data
		}()

		lldbCmd := fmt.Sprintf(`lldb -p %d -o "memory read --binary --force --outfile %s --count %d 0x%x" -o "quit"`,
			pid, pipePath, size, region.Start)
		cmd := exec.Command("bash", "-c", lldbCmd)
		cmd.Start()

		select {
		case buf := <-dataCh:
			cmd.Wait()
			n := bytes.Count(buf, pattern1)
			if n > 0 {
				hits++
				fmt.Printf("HIT region type=%-30s size=%dK hits=%d detail=%s\n",
					region.RegionType, size/1024, n, region.RegionDetail)
			}
		case err := <-errCh:
			fmt.Printf("err region %s: %v\n", region.RegionType, err)
		case <-time.After(15 * time.Second):
			cmd.Process.Kill()
		}
	}

	// 不做断言：命中数是给人看的排障信息，不是「通过与失败」的判据。
	// 如果这里断言 hits > 0，微信换个版本就会红，又回到「红灯挂久了没人看」的老路。
	t.Logf("扫描 %d 个区域，命中 fts5 特征串的区域数：%d", len(regions), hits)
}

func io_ReadAll(r *os.File) ([]byte, error) {
	var out []byte
	buf := make([]byte, 1024*1024)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			out = append(out, buf[:n]...)
		}
		if err != nil {
			return out, nil
		}
	}
}
