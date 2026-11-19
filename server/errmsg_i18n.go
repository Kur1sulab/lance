package server

import "github.com/Kur1sulab/lance/config"

// 将用户可见的后端消息（中文原文）替换为当前输出语言的目录。本仓库的
// writeErr 响应文案及部分 writeJSON 错误字段以中文常量和字面量编写。
// 这里以中文原文为键，英语、西班牙语、日语翻译为值。中文(zh)则
// 本身就是键，因此不保存；查询失败或为 zh 时原样返回原文。
//
// 键由源码中的中文字面量按字节原样提取生成。写入 writeErr 的运行时值与键
// 一一对应，不会错位而悄悄漏掉。
//
// 格式化字符串常量(%d·%s·%w 等)格式化后的结果与键不同，因此在调用处先用
// trMsg(模板)替换模板本身，再交给 fmt.Sprintf/fmt.Errorf。

// trMsg 将中文原文消息替换为当前激活的输出语言。若目录中没有或激活语言
// 为中文则原样返回。config.ActiveLanguage 从运行时设置（设置表）
// 生效，因此用户选择的语言会立即应用。
func trMsg(msg string) string {
	if m, ok := errCatalog[msg]; ok {
		if v, ok2 := m[config.ActiveLanguage()]; ok2 && v != "" {
			return v
		}
	}
	return msg
}

// loadOutputLanguage resolves the user-facing output language from the source of
// truth: the runtime setting (settings table) over config.Language() (LANCE_LANG >
// config file > zh). Call it only to (re)fill the cache; it does a DB query and a
// config file read.
func (s *Server) loadOutputLanguage() string {
	if s.m != nil && s.m.pg != nil {
		if v, ok, _ := s.m.pg.GetSetting(settingOutputLanguage); ok {
			if nv := config.NormalizeLanguage(v); nv != "" {
				return nv
			}
		}
	}
	return config.Language()
}

// outputLanguage returns the cached output language, resolving it on first use.
func (s *Server) outputLanguage() string {
	if v, ok := s.outputLang.Load().(string); ok && v != "" {
		return v
	}
	v := s.loadOutputLanguage()
	s.outputLang.Store(v)
	return v
}
