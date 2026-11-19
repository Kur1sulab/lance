package server

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/Kur1sulab/lance/db"
	"github.com/Kur1sulab/lance/mcphttp"
	"github.com/Kur1sulab/norma/mcp"
	actool "github.com/Kur1sulab/norma/tool"
)

// mcpClient is the shared surface of a connected MCP server (stdio, Streamable HTTP,
// or legacy SSE),
// so tools/list and cleanup are handled uniformly regardless of transport.
type mcpClient interface {
	Tools(context.Context) ([]actool.CoreTool, error)
	Close() error
}

// connectMCP 返回的四种传输配置校验错误是用户可见的 —
// pgRefreshMCP(server_mgmt.go) 会将 discoverAndCacheMCP 的失败
// 用 writeErr(502, errMgmtToolDiscoverFail+err.Error()) 原样放入响应体。
// 其他调用处（assembly.go 的智能体组装、discoverEmptyMCPsOnStartup 启动时自动发现、
// sync_scopesentry.go 同步·pgSaveMCP 新增后立刻发现)全部仅用 log.Printf 记录，或
// 被丢弃，不会作为 agent 大脑的输入。因此将其本地化。传输方式 enum
// (stdio/http/sse) 与 URL 属于 wire 标识符，保持原文，UI mcpPage(传输方式·命令·
// 与远程 URL)的写法保持一致。
const (
	errMCPStdioNoCommand      = "stdio 传输方式缺少命令"
	errMCPHTTPNoURL           = "http 传输方式缺少 URL"
	errMCPSSENoURL            = "sse 传输方式缺少 URL"
	errMCPUnknownTransportFmt = "未知的传输方式：%q"
)

// connectMCP dials one MCP server per its transport. Callers must Close the client.
func connectMCP(ctx context.Context, m *db.MCPServer) (mcpClient, error) {
	switch m.Transport {
	case "stdio":
		if m.Command == "" {
			return nil, errors.New(errMCPStdioNoCommand)
		}
		return mcp.NewStdioClient(ctx, m.Name, m.Command, jsonStrMap(m.Env), jsonStrSlice(m.Args)...)
	case "http":
		if m.URL == "" {
			return nil, errors.New(errMCPHTTPNoURL)
		}
		// env map doubles as HTTP headers (e.g. Authorization).
		return mcphttp.New(ctx, m.Name, m.URL, jsonStrMap(m.Env), m.Insecure)
	case "sse":
		if m.URL == "" {
			return nil, errors.New(errMCPSSENoURL)
		}
		return mcphttp.NewSSE(ctx, m.Name, m.URL, jsonStrMap(m.Env), m.Insecure)
	default:
		return nil, fmt.Errorf(trMsg(errMCPUnknownTransportFmt), m.Transport)
	}
}

// discoverAndCacheMCP connects to one MCP, lists its tools, and persists the tool
// names to mcp_tools_cache so the UI shows them without a live connection.
func (s *Server) discoverAndCacheMCP(ctx context.Context, m *db.MCPServer) error {
	cl, err := connectMCP(ctx, m)
	if err != nil {
		return err
	}
	defer cl.Close()
	ts, err := cl.Tools(ctx)
	if err != nil {
		return err
	}
	tools := make([]db.MCPTool, 0, len(ts))
	for _, t := range ts {
		tools = append(tools, db.MCPTool{Name: t.Name(), Description: t.Description()})
	}
	if err := s.m.pg.SaveMCPTools(m.ID, tools); err != nil {
		return err
	}
	log.Printf("[mcp] from %s, discovered and cached %d tool(s)", m.Name, len(tools))
	return nil
}

// discoverEmptyMCPsOnStartup fills the tool cache for any enabled MCP that has none
// yet (notably the seeded browser MCP on first run). Runs sequentially in one
// goroutine so we never spawn many stdio servers (npx) at once, and never blocks
// startup. Best-effort: a failure leaves the cache empty to retry next start.
func (s *Server) discoverEmptyMCPsOnStartup() {
	servers, err := s.m.pg.ListMCP()
	if err != nil {
		log.Printf("[mcp] auto-discovery at startup: failed to read the list: %v", err)
		return
	}
	for _, m := range servers {
		if !m.Enabled || len(m.Tools) > 0 {
			continue
		}
		ctx, cancel := context.WithTimeout(s.ctx, 90*time.Second)
		if err := s.discoverAndCacheMCP(ctx, m); err != nil {
			log.Printf("[mcp] auto-discovery of %s at startup failed: %v", m.Name, err)
		}
		cancel()
	}
}
