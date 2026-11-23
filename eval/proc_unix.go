//go:build !windows

package eval

// Unix 侧的进程控制：独立进程组 + 整组信号。
//
// 为什么必须整组：靶常常是「shell 脚本 → 真正服务」或者「go run → 编译产物」这种
// 两层结构。只杀直接子进程，孙进程会活下来继续占端口，下一道题起靶时就连到了
// 上一道题的靶上——分数会串台，而且极难排查。Setpgid 之后按 -pid 发信号，
// 一次把整棵树收掉。

import (
	"os/exec"
	"syscall"
)

// setProcAttr 让子进程进入独立进程组（pgid = 子进程 pid）。
func setProcAttr(c *exec.Cmd) {
	if c.SysProcAttr == nil {
		c.SysProcAttr = &syscall.SysProcAttr{}
	}
	c.SysProcAttr.Setpgid = true
}

// signalTerm 给整个进程组发 SIGTERM，给靶刷日志/正常退出的机会。
func signalTerm(c *exec.Cmd) error {
	if c.Process == nil {
		return nil
	}
	// 负 pid = 进程组；失败（进程已退或不是组长）时退回单进程信号。
	if err := syscall.Kill(-c.Process.Pid, syscall.SIGTERM); err != nil {
		return c.Process.Signal(syscall.SIGTERM)
	}
	return nil
}

// signalKill 给整个进程组发 SIGKILL，兜底不可回收的情况。
func signalKill(c *exec.Cmd) error {
	if c.Process == nil {
		return nil
	}
	if err := syscall.Kill(-c.Process.Pid, syscall.SIGKILL); err != nil {
		return c.Process.Kill()
	}
	return nil
}
