//go:build !windows

package windows

import (
	"github.com/lululu811/wechat-chatlog/internal/wechat/model"
	"github.com/shirou/gopsutil/v4/process"
)

func initializeProcessInfo(p *process.Process, info *model.Process) error {
	return nil
}
