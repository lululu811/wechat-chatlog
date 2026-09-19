package glance

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestScanAllWritable(t *testing.T) {
	pid := uint32(4841)
	regions, err := GetVmmap(pid)
	if err != nil {
		t.Fatal(err)
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
	fmt.Printf("\nTOTAL regions with fts5: %d\n", hits)
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