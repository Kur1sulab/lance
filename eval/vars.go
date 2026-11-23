package eval

// 占位符替换。任务定义里的 URL / 路径 / banner 允许写 {{var}}，由运行期注入。
//
// 为什么严格失败而不是「替换不到就留原文」：判据里一个没替换成功的 {{canary:x}}
// 会变成一个永远匹配不上的字符串，结果是 achieved=false——看起来像「打不穿」，
// 实际是任务写错了。这种假阴性会直接污染评测结论，所以这里遇到未知变量一律报错终止。

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

var placeholderRe = regexp.MustCompile(`\{\{\s*([A-Za-z0-9_:.\-]+)\s*\}\}`)

// vars 是本次运行的变量表。
type vars struct {
	m map[string]string
}

func newVars() *vars {
	return &vars{m: map[string]string{}}
}

func (v *vars) set(k, val string) {
	v.m[k] = val
}

// resolve 替换一个模板串。没有任何占位符时原样返回。
func (v *vars) resolve(tpl string) (string, error) {
	if !strings.Contains(tpl, "{{") {
		return tpl, nil
	}
	var firstErr error
	out := placeholderRe.ReplaceAllStringFunc(tpl, func(m string) string {
		key := strings.TrimSpace(placeholderRe.FindStringSubmatch(m)[1])
		val, ok := v.m[key]
		if !ok {
			if firstErr == nil {
				firstErr = fmt.Errorf("未知占位符 {{%s}}（可用：%s）", key, strings.Join(v.keys(), ", "))
			}
			return m
		}
		return val
	})
	if firstErr != nil {
		return "", firstErr
	}
	return out, nil
}

func (v *vars) resolveAll(tpls []string) ([]string, error) {
	out := make([]string, len(tpls))
	for i, t := range tpls {
		s, err := v.resolve(t)
		if err != nil {
			return nil, err
		}
		out[i] = s
	}
	return out, nil
}

func (v *vars) keys() []string {
	ks := make([]string, 0, len(v.m))
	for k := range v.m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}
