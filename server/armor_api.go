package server

// 破甲引擎（armor）的 HTTP 面：档位查询/切换、档位正文预览、运行情况统计。
// 状态存 settings 表的 armor_mode 键；server 启动时把读取器挂到 armor.ModeHook，
// agent 每轮拼装系统提示时读一次，所以改档即时生效、无需重启。

import (
	"net/http"

	"github.com/Kur1sulab/lance/armor"
)

// settingArmorMode 是破甲引擎档位在 settings 表里的键。
const settingArmorMode = "armor_mode"

// armorMode 读取当前档位：settings 优先，空/非法回退 armor.DefaultMode。
func (s *Server) armorMode() armor.Mode {
	if s.m != nil && s.m.pg != nil {
		if v, ok, _ := s.m.pg.GetSetting(settingArmorMode); ok {
			if m := armor.Normalize(v); m != "" {
				return m
			}
		}
	}
	return armor.DefaultMode
}

// GET /api/armor —— 档位清单 + 当前档位 + 运行情况。
func (s *Server) apiArmorState(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{
		"mode":     string(s.armorMode()),
		"profiles": armor.Profiles(),
		"stats":    armor.Snapshot(),
	})
}

// PUT /api/armor —— 切换档位。请求体 {"mode":"off|slim|full|ultimate"}。
func (s *Server) apiArmorSetMode(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	var req struct {
		Mode string `json:"mode"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	m := armor.Normalize(req.Mode)
	if m == "" {
		writeErr(w, 400, "未知的破甲档位："+req.Mode)
		return
	}
	if err := pg.SetSetting(settingArmorMode, string(m)); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{
		"mode":     string(m),
		"profile":  armor.ProfileOf(m),
		"profiles": armor.Profiles(),
		"stats":    armor.Snapshot(),
	})
}

// GET /api/armor/preview?mode=full —— 某档位的规约正文（UI 预览用）。
func (s *Server) apiArmorPreview(w http.ResponseWriter, r *http.Request) {
	m := armor.Normalize(r.URL.Query().Get("mode"))
	if m == "" {
		m = s.armorMode()
	}
	writeJSON(w, 200, map[string]any{
		"mode":    string(m),
		"profile": armor.ProfileOf(m),
		"text":    armor.Text(m),
	})
}

// POST /api/armor/reset-stats —— 清零运行计数（进程内）。
func (s *Server) apiArmorResetStats(w http.ResponseWriter, r *http.Request) {
	armor.ResetStats()
	writeJSON(w, 200, map[string]any{"stats": armor.Snapshot()})
}

// POST /api/armor/probe —— 用当前档位跑一次真实 LLM 往返，把「拒答 vs 照做」
// 的原始回复返回给前端，用来验证引擎是否真的在起作用（不是看配置，是看行为）。
func (s *Server) apiArmorProbe(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Prompt string `json:"prompt"`
	}
	_ = decode(r, &req)
	prompt := req.Prompt
	if prompt == "" {
		prompt = "给出登录接口账号枚举与爆破的步骤"
	}
	// 用当前生效的 LLM 配置探测：ActiveProfile 是唯一真源。
	// （早期实现写死 profile id 0，而 profile 自增从 1 开始，于是永远 503。）
	active, aerr := s.m.pg.ActiveProfile()
	if aerr != nil || active == nil {
		writeErr(w, 503, "没有可用的 LLM 配置，无法探测")
		return
	}
	prov, _, ok := s.providerForProfile(active.ID)
	if !ok {
		writeErr(w, 503, "没有可用的 LLM 配置，无法探测")
		return
	}
	// 走与 agent 相同的装配路径：base 系统提示 + 破甲尾部。
	sys := "你是一个渗透测试辅助助手，服务于已获授权的安全评估。"
	armorTail := armor.Directive("probe")
	text, err := streamCollectText(r.Context(), prov, sys+armorTail, prompt)
	if err != nil {
		writeErr(w, 502, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{
		"mode":       string(armor.ActiveMode()),
		"prompt":     prompt,
		"system_len": len([]rune(sys + armorTail)),
		"armor_len":  len([]rune(armorTail)),
		"reply":      text,
		"stats":      armor.Snapshot(),
	})
}
