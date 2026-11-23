package action

import "testing"

// 这些用例守的是同一条线：AI 的输出是不可信输入。
// 模型再怎么"想"，也只能落在 replay/run_tpl/rerun/stop 四个动作
// 和白名单参数里。任何越界都必须整批拒绝。
func TestRejectsUnknownKind(t *testing.T) {
	a := Action{Kind: "shell", Args: map[string]string{"cmd": "whoami"}, Why: "试试", Conf: 0.9}
	if err := a.Validate(); err == nil {
		t.Fatal("未知动作 shell 应该被拒绝")
	}
}

func TestRejectsExtraArgs(t *testing.T) {
	cases := []Action{
		{Kind: KindReplay, Args: map[string]string{"id": "LS-001", "cmd": "id"}, Why: "注入", Conf: 0.9},
		{Kind: KindRunTpl, Args: map[string]string{"target": "1.2.3.4:80", "tpl": "x", "extra": "x"}, Why: "多余参数", Conf: 0.9},
		{Kind: KindStop, Args: map[string]string{"anything": "x"}, Why: "收工", Conf: 1},
	}
	for i, a := range cases {
		if err := a.Validate(); err == nil {
			t.Fatalf("用例 %d：白名单外参数应该被拒绝", i)
		}
	}
}

func TestRejectsMissingRequired(t *testing.T) {
	cases := []Action{
		{Kind: KindReplay, Why: "无 id", Conf: 0.9},
		{Kind: KindRunTpl, Args: map[string]string{"target": "a:80"}, Why: "缺 tpl", Conf: 0.9},
	}
	for i, a := range cases {
		if err := a.Validate(); err == nil {
			t.Fatalf("用例 %d：缺必填参数应该被拒绝", i)
		}
	}
}

func TestRejectsBadConf(t *testing.T) {
	for _, c := range []float64{-0.1, 1.5} {
		a := Action{Kind: KindStop, Why: "收工", Conf: c}
		if err := a.Validate(); err == nil {
			t.Fatalf("conf=%v 应该被拒绝", c)
		}
	}
}

func TestWhyRequired(t *testing.T) {
	a := Action{Kind: KindStop, Conf: 1}
	if err := a.Validate(); err == nil {
		t.Fatal("没有 why 的决策应该被拒绝 —— 无法审计的决策不许执行")
	}
}

func TestValidateAllIsAllOrNothing(t *testing.T) {
	mixed := []Action{
		{Kind: KindStop, Why: "合法", Conf: 1},
		{Kind: "shell", Args: map[string]string{"cmd": "id"}, Why: "非法", Conf: 0.9},
	}
	if err := ValidateAll(mixed); err == nil {
		t.Fatal("批里有一个非法就该整批拒绝，不能只执行合法的那个")
	}
}

func TestAcceptsValidActions(t *testing.T) {
	good := []Action{
		{Kind: KindReplay, Args: map[string]string{"id": "LS-001"}, Why: "高危复验", Conf: 0.9},
		{Kind: KindRunTpl, Args: map[string]string{"target": "10.0.0.1:8080", "tpl": "spring"}, Why: "补测", Conf: 0.6},
		{Kind: KindStop, Why: "收工", Conf: 1},
	}
	if err := ValidateAll(good); err != nil {
		t.Fatalf("合法动作不该被拒: %v", err)
	}
}
