package server

import (
	"testing"
	"unicode"

	"github.com/Kur1sulab/lance/config"
)

// TestTrMsgSwitchesLanguage pins the backend output-language switching: a
// user-facing Chinese message is returned verbatim under zh (and when unwired),
// and translated to a non-Chinese string under en/es/ja. This is the backend
// analogue of the web i18n catalog — it proves writeErr responses follow the
// configured language.
func TestTrMsgSwitchesLanguage(t *testing.T) {
	t.Cleanup(func() { config.CurrentLanguage = nil })

	hasHan := func(s string) bool {
		for _, r := range s {
			if unicode.Is(unicode.Han, r) {
				return true
			}
		}
		return false
	}

	// A representative catalog key (an existing server error constant value).
	const srcMsg = errAssetIDRequired

	// Unwired → source passthrough.
	config.CurrentLanguage = nil
	if got := trMsg(srcMsg); got != srcMsg {
		t.Fatalf("unwired: trMsg must pass the source message through, got %q", got)
	}

	// zh → passthrough (the key itself).
	config.CurrentLanguage = func() string { return "zh" }
	if got := trMsg(srcMsg); got != srcMsg {
		t.Fatalf("zh: trMsg must return the Chinese source, got %q", got)
	}

	// en/es/ja → a translated, non-empty string. en and es must carry no Han.
	for _, lang := range []string{"en", "es", "ja"} {
		config.CurrentLanguage = func() string { return lang }
		got := trMsg(srcMsg)
		if got == "" || got == srcMsg {
			t.Fatalf("%s: trMsg must translate away from the source, got %q", lang, got)
		}
		if (lang == "en" || lang == "es") && hasHan(got) {
			t.Fatalf("%s: translation must not contain Han characters, got %q", lang, got)
		}
	}

	// Unknown language code → source fallback (config.NormalizeLanguage rejects it).
	config.CurrentLanguage = func() string { return "fr" }
	if got := trMsg(srcMsg); got != srcMsg {
		t.Fatalf("unknown lang: trMsg must fall back to the source, got %q", got)
	}

	// A string not in the catalog (propagated lower-layer error) passes through.
	config.CurrentLanguage = func() string { return "en" }
	const notInCatalog = "some-lower-layer-error: connection refused"
	if got := trMsg(notInCatalog); got != notInCatalog {
		t.Fatalf("non-catalog string must pass through unchanged, got %q", got)
	}
}
