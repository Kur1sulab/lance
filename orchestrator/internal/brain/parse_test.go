package brain

import "testing"

func TestParseActionsStripsFences(t *testing.T) {
	raw := "```json\n{\"actions\":[{\"kind\":\"stop\",\"why\":\"收工\",\"conf\":1}]}\n```"
	as, err := parseActions(raw)
	if err != nil {
		t.Fatalf("剥掉 markdown 围栏后应该能解析: %v", err)
	}
	if len(as) != 1 || as[0].Kind != "stop" {
		t.Fatalf("解析结果不对: %+v", as)
	}
}

func TestParseActionsRejectsShellKind(t *testing.T) {
	raw := `{"actions":[{"kind":"shell","args":{"cmd":"rm -rf /"},"why":"x","conf":0.9}]}`
	if _, err := parseActions(raw); err == nil {
		t.Fatal("模型给出 shell 动作必须在解析阶段就被拒")
	}
}

func TestParseActionsRejectsProse(t *testing.T) {
	if _, err := parseActions("我觉得应该先打 LS-001，因为它等级高。"); err == nil {
		t.Fatal("纯自然语言不是合法动作，应该拒绝")
	}
}

func TestParseActionsRejectsEmpty(t *testing.T) {
	if _, err := parseActions(`{"actions":[]}`); err == nil {
		t.Fatal("空动作列表应该拒绝")
	}
}

func TestParseActionsExtractsFromProse(t *testing.T) {
	raw := `好的，我的决策是：{"actions":[{"kind":"stop","why":"没有待办","conf":1}]} 以上。`
	as, err := parseActions(raw)
	if err != nil {
		t.Fatalf("能从散文里抠出 JSON 就该接受: %v", err)
	}
	if len(as) != 1 {
		t.Fatalf("应该解析出 1 个动作，实际 %d", len(as))
	}
}
