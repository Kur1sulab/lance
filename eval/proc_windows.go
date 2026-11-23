//go:build windows

package eval

// Windows 侧没有 Unix 进程组语义，只能杀直接子进程。
// 想让靶树被完整回收，靶自身应当做到「父进程退出即自杀」，或者改用容器形态。

import "os/exec"

func setProcAttr(c *exec.Cmd) {}

func signalTerm(c *exec.Cmd) error {
	if c.Process == nil {
		return nil
	}
	return c.Process.Kill()
}

func signalKill(c *exec.Cmd) error {
	if c.Process == nil {
		return nil
	}
	return c.Process.Kill()
}
