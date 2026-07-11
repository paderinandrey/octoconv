package convert

import (
	"fmt"
	"strings"
	"testing"
)

func TestParseHTMLOpts(t *testing.T) {
	t.Run("valid full opts", func(t *testing.T) {
		o, err := ParseHTMLOpts([]byte(`{"page_size":"a4","margin_mm":10,"landscape":true,"print_background":false}`))
		if err != nil {
			t.Fatalf("ParseHTMLOpts: unexpected error: %v", err)
		}
		if o.PageSize != "a4" || o.MarginMM == nil || *o.MarginMM != 10 || !o.Landscape || o.PrintBackground {
			t.Errorf("ParseHTMLOpts = %+v (MarginMM=%v), want a4/10/landscape/no-bg", o, ptrVal(o.MarginMM))
		}
	})

	t.Run("absent margin_mm leaves MarginMM nil (unset, not zero)", func(t *testing.T) {
		o, err := ParseHTMLOpts([]byte(`{"page_size":"a4"}`))
		if err != nil {
			t.Fatalf("ParseHTMLOpts: unexpected error: %v", err)
		}
		if o.MarginMM != nil {
			t.Errorf("ParseHTMLOpts with no margin_mm = MarginMM %v, want nil", ptrVal(o.MarginMM))
		}
	})

	t.Run("explicit margin_mm:0 is preserved as non-nil zero", func(t *testing.T) {
		o, err := ParseHTMLOpts([]byte(`{"margin_mm":0}`))
		if err != nil {
			t.Fatalf("ParseHTMLOpts: unexpected error: %v", err)
		}
		if o.MarginMM == nil || *o.MarginMM != 0 {
			t.Errorf("ParseHTMLOpts with margin_mm:0 = %v, want non-nil 0", ptrVal(o.MarginMM))
		}
	})

	t.Run("unknown field rejected", func(t *testing.T) {
		if _, err := ParseHTMLOpts([]byte(`{"page_size":"a4","zoom":2}`)); err == nil {
			t.Error("ParseHTMLOpts with unknown field 'zoom' = nil error, want error")
		}
	})

	t.Run("unsupported page_size rejected", func(t *testing.T) {
		_, err := ParseHTMLOpts([]byte(`{"page_size":"b4"}`))
		if err == nil {
			t.Fatal("ParseHTMLOpts with unsupported page_size = nil error, want error")
		}
		if !strings.Contains(err.Error(), "unsupported page_size") {
			t.Errorf("error = %q, want it to mention 'unsupported page_size'", err.Error())
		}
	})

	t.Run("margin_mm above range rejected", func(t *testing.T) {
		if _, err := ParseHTMLOpts([]byte(`{"margin_mm":99}`)); err == nil {
			t.Error("ParseHTMLOpts with margin_mm=99 = nil error, want error")
		}
	})

	t.Run("margin_mm negative rejected", func(t *testing.T) {
		if _, err := ParseHTMLOpts([]byte(`{"margin_mm":-1}`)); err == nil {
			t.Error("ParseHTMLOpts with margin_mm=-1 = nil error, want error")
		}
	})

	t.Run("duplicate top-level key rejected", func(t *testing.T) {
		if _, err := ParseHTMLOpts([]byte(`{"page_size":"a4","page_size":"letter"}`)); err == nil {
			t.Error("ParseHTMLOpts with duplicate key = nil error, want error")
		}
	})

	t.Run("trailing bytes rejected", func(t *testing.T) {
		if _, err := ParseHTMLOpts([]byte(`{"page_size":"a4"}garbage`)); err == nil {
			t.Error("ParseHTMLOpts with trailing bytes = nil error, want error")
		}
	})

	t.Run("top-level null rejected", func(t *testing.T) {
		if _, err := ParseHTMLOpts([]byte(`null`)); err == nil {
			t.Error("ParseHTMLOpts with top-level null = nil error, want error")
		}
	})

	t.Run("empty object valid, zero opts", func(t *testing.T) {
		o, err := ParseHTMLOpts([]byte(`{}`))
		if err != nil {
			t.Fatalf("ParseHTMLOpts({}) unexpected error: %v", err)
		}
		if o != (HTMLOpts{}) {
			t.Errorf("ParseHTMLOpts({}) = %+v, want zero value", o)
		}
	})
}

func TestHTMLOptsFromMap(t *testing.T) {
	t.Run("round trip valid map", func(t *testing.T) {
		m := map[string]any{"page_size": "letter", "margin_mm": float64(5)}
		o, err := HTMLOptsFromMap(m)
		if err != nil {
			t.Fatalf("HTMLOptsFromMap: unexpected error: %v", err)
		}
		if o.PageSize != "letter" || o.MarginMM == nil || *o.MarginMM != 5 {
			t.Errorf("HTMLOptsFromMap = %+v (MarginMM=%v), want letter/5", o, ptrVal(o.MarginMM))
		}
	})

	t.Run("persisted map without margin_mm re-parses to nil margin", func(t *testing.T) {
		m := map[string]any{"page_size": "a4"}
		o, err := HTMLOptsFromMap(m)
		if err != nil {
			t.Fatalf("HTMLOptsFromMap: unexpected error: %v", err)
		}
		if o.MarginMM != nil {
			t.Errorf("HTMLOptsFromMap without margin_mm = MarginMM %v, want nil", ptrVal(o.MarginMM))
		}
	})

	t.Run("persisted explicit margin_mm:0 re-parses to non-nil zero", func(t *testing.T) {
		m := map[string]any{"margin_mm": float64(0)}
		o, err := HTMLOptsFromMap(m)
		if err != nil {
			t.Fatalf("HTMLOptsFromMap: unexpected error: %v", err)
		}
		if o.MarginMM == nil || *o.MarginMM != 0 {
			t.Errorf("HTMLOptsFromMap with margin_mm:0 = %v, want non-nil 0", ptrVal(o.MarginMM))
		}
	})

	t.Run("nil map yields zero opts", func(t *testing.T) {
		o, err := HTMLOptsFromMap(nil)
		if err != nil {
			t.Fatalf("HTMLOptsFromMap(nil) unexpected error: %v", err)
		}
		if o != (HTMLOpts{}) {
			t.Errorf("HTMLOptsFromMap(nil) = %+v, want zero value", o)
		}
	})

	t.Run("corrupt persisted map applies same strictness", func(t *testing.T) {
		m := map[string]any{"page_size": "not-a-real-size"}
		if _, err := HTMLOptsFromMap(m); err == nil {
			t.Error("HTMLOptsFromMap with invalid page_size = nil error, want error")
		}
	})

	t.Run("unknown field in persisted map rejected", func(t *testing.T) {
		m := map[string]any{"page_size": "a4", "zoom": float64(2)}
		if _, err := HTMLOptsFromMap(m); err == nil {
			t.Error("HTMLOptsFromMap with unknown field = nil error, want error")
		}
	})
}

func TestValidateHTMLApplicability(t *testing.T) {
	t.Run("empty opts always apply", func(t *testing.T) {
		if err := ValidateHTMLApplicability(EngineDocument, "docx", "pdf", HTMLOpts{}); err != nil {
			t.Errorf("ValidateHTMLApplicability with zero opts = %v, want nil", err)
		}
	})

	t.Run("non-empty opts on non-html engine rejected", func(t *testing.T) {
		o := HTMLOpts{PageSize: "a4"}
		if err := ValidateHTMLApplicability(EngineDocument, "docx", "pdf", o); err == nil {
			t.Error("ValidateHTMLApplicability on document engine = nil error, want error")
		}
	})

	t.Run("non-empty opts on non-pdf target rejected", func(t *testing.T) {
		o := HTMLOpts{PageSize: "a4"}
		if err := ValidateHTMLApplicability(EngineHTML, "html", "png", o); err == nil {
			t.Error("ValidateHTMLApplicability with non-pdf target = nil error, want error")
		}
	})

	t.Run("non-empty opts on html->pdf accepted", func(t *testing.T) {
		o := HTMLOpts{PageSize: "a4", MarginMM: intPtr(10)}
		if err := ValidateHTMLApplicability(EngineHTML, "html", "pdf", o); err != nil {
			t.Errorf("ValidateHTMLApplicability(html,pdf) = %v, want nil", err)
		}
	})
}

func TestBuildPrintCSS(t *testing.T) {
	t.Run("only mapped constant present, no other size token", func(t *testing.T) {
		css := buildPrintCSS(HTMLOpts{PageSize: "a4", MarginMM: intPtr(10)})
		if !strings.Contains(css, "A4") {
			t.Errorf("buildPrintCSS output %q does not contain mapped constant A4", css)
		}
		if !strings.Contains(css, "10mm") {
			t.Errorf("buildPrintCSS output %q does not contain margin 10mm", css)
		}
		// No other page-size keyword should leak into the output.
		for size, constant := range htmlPageSizeCSS {
			if size == "a4" {
				continue
			}
			if strings.Contains(css, constant) {
				t.Errorf("buildPrintCSS output %q unexpectedly contains unrelated size constant %q", css, constant)
			}
		}
	})

	t.Run("landscape appended to size", func(t *testing.T) {
		css := buildPrintCSS(HTMLOpts{PageSize: "letter", Landscape: true})
		if !strings.Contains(css, "letter landscape") {
			t.Errorf("buildPrintCSS output %q does not contain 'letter landscape'", css)
		}
	})

	t.Run("print_background true uses exact color adjust", func(t *testing.T) {
		css := buildPrintCSS(HTMLOpts{PageSize: "a4", PrintBackground: true})
		if !strings.Contains(css, "print-color-adjust: exact") {
			t.Errorf("buildPrintCSS output %q does not request exact color adjust", css)
		}
	})

	t.Run("print_background false uses economy color adjust", func(t *testing.T) {
		css := buildPrintCSS(HTMLOpts{PageSize: "a4", PrintBackground: false})
		if !strings.Contains(css, "print-color-adjust: economy") {
			t.Errorf("buildPrintCSS output %q does not request economy color adjust", css)
		}
	})

	t.Run("every injected property carries !important", func(t *testing.T) {
		css := buildPrintCSS(HTMLOpts{PageSize: "a4", MarginMM: intPtr(5), PrintBackground: true})
		if strings.Count(css, "!important") < 3 {
			t.Errorf("buildPrintCSS output %q does not carry !important on every property", css)
		}
	})

	t.Run("no-opts job omits the margin declaration entirely", func(t *testing.T) {
		// A zero-value HTMLOpts (no client opts) must NOT force a
		// `margin: 0mm !important` default -- it should emit no margin at all
		// so chromium's default print margin / the client HTML's own @page
		// margin is respected (WR-02/CR-03).
		css := buildPrintCSS(HTMLOpts{})
		if strings.Contains(css, "margin") {
			t.Errorf("buildPrintCSS(no opts) = %q, want no margin declaration", css)
		}
	})

	t.Run("explicit margin_mm:0 emits margin: 0mm !important", func(t *testing.T) {
		// An explicit zero is a deliberate edge-to-edge request and must be
		// honored, distinct from "unset".
		css := buildPrintCSS(HTMLOpts{PageSize: "a4", MarginMM: intPtr(0)})
		if !strings.Contains(css, "margin: 0mm !important") {
			t.Errorf("buildPrintCSS(margin_mm:0) = %q, want 'margin: 0mm !important'", css)
		}
	})

	t.Run("explicit margin_mm:15 emits margin: 15mm !important", func(t *testing.T) {
		css := buildPrintCSS(HTMLOpts{PageSize: "a4", MarginMM: intPtr(15)})
		if !strings.Contains(css, "margin: 15mm !important") {
			t.Errorf("buildPrintCSS(margin_mm:15) = %q, want 'margin: 15mm !important'", css)
		}
	})

	t.Run("injection cannot reach CSS -- ParseHTMLOpts rejects attacker text first", func(t *testing.T) {
		// An attacker attempting to smuggle CSS/JS breakout text through
		// page_size is rejected at the parse boundary, long before
		// buildPrintCSS could ever see it.
		_, err := ParseHTMLOpts([]byte(`{"page_size":"a4</style><script>alert(1)</script>"}`))
		if err == nil {
			t.Fatal("ParseHTMLOpts accepted an injection payload as page_size, want rejection")
		}
		// Even if a caller somehow constructed an HTMLOpts by hand (bypassing
		// ParseHTMLOpts) with an out-of-enum PageSize, buildPrintCSS's map
		// lookup yields "" for any key outside htmlPageSizeCSS -- it can
		// never emit attacker-controlled bytes.
		css := buildPrintCSS(HTMLOpts{PageSize: "a4</style><script>alert(1)</script>"})
		if strings.Contains(css, "<script>") || strings.Contains(css, "alert(1)") {
			t.Errorf("buildPrintCSS leaked raw client bytes into CSS: %q", css)
		}
	})
}

// intPtr returns a pointer to v, for constructing HTMLOpts with an explicit
// (non-nil) MarginMM in tests.
func intPtr(v int) *int { return &v }

// ptrVal renders a *int for test diagnostics: "nil" when unset, else the value.
func ptrVal(p *int) string {
	if p == nil {
		return "nil"
	}
	return fmt.Sprintf("%d", *p)
}
