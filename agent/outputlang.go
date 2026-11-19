package agent

// 用户可见输出语言（展示语言）的选择。Lance 支持英·中·西·日四种语言，
// 强制智能体用其中之一来书写面向用户的自然语言。智能体的
// "大脑"(经基准测试的中文推理正文 段[A])与语言无关，原样保留。这里确定的
// 只是面向用户可读界面的语言。

// OutputLanguage 由服务器从配置(config 中的 LANCE_LANG、运行时 settings KV)中选择
// 是服务器接线以返回输出语言代码（"en"/"zh"/"es"/"ja"）的 hook。若为 nil 或
// 为空则回退到下面的默认值。与 PromptOverride 相同方式由服务端注入。
var OutputLanguage func() string

// 服务器未接线 hook 时（例如：单独测试 agent 包）的默认输出语言。
// 生产环境中由服务器按设置层的语言来接线 OutputLanguage 钩子。
const defaultOutputLang = "zh"

// SupportedOutputLangs 是可切换的输出语言代码列表。
var SupportedOutputLangs = []string{"en", "zh", "es", "ja"}

// normalizeLang 会把支持列表之外的代码收窄为默认值。
func normalizeLang(code string) string {
	for _, c := range SupportedOutputLangs {
		if c == code {
			return code
		}
	}
	return defaultOutputLang
}

// resolveOutputLang 返回当前要应用的输出语言代码。先由服务器接线的钩子
// 并查看，没有则使用默认值。
func resolveOutputLang() string {
	if OutputLanguage != nil {
		if code := OutputLanguage(); code != "" {
			return normalizeLang(code)
		}
	}
	return defaultOutputLang
}

// langPhrase 是在（用中文写成的）指令中指出“用这种语言写”时使用的写法。
// 在括号内同时给出母语写法，避免模型混淆是哪种语言。
var langPhrase = map[string]string{
	"en": "英语（English）",
	"zh": "中文（简体中文）",
	"es": "西班牙语（Español）",
	"ja": "日语（日本語）",
}

// langBare 是在指令中反复指代时使用的简短语言名。
var langBare = map[string]string{
	"en": "英语",
	"zh": "中文",
	"es": "西班牙语",
	"ja": "日语",
}
