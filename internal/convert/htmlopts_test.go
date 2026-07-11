package convert

import (
	"strings"
	"testing"
)

func TestParseHTMLOpts(t *testing.T) {
	t.Run("valid full opts", func(t *testing.T) {
		o, err := ParseHTMLOpts([]byte(`{"page_size":"a4","margin_mm":10,"landscape":true,"print_background":false}`))
		if err != nil {
			t.Fatalf("ParseHTMLOpts: unexpected error: %v", err)
		}
		want := HTMLOpts{PageSize: "a4", MarginMM: 10, Landscape: true, PrintBackground: false}
		if o != want {
			t.Errorf("ParseHTMLOpts = %+v, want %+v", o, want)
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
		want := HTMLOpts{PageSize: "letter", MarginMM: 5}
		if o != want {
			t.Errorf("HTMLOptsFromMap = %+v, want %+v", o, want)
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
		o := HTMLOpts{PageSize: "a4", MarginMM: 10}
		if err := ValidateHTMLApplicability(EngineHTML, "html", "pdf", o); err != nil {
			t.Errorf("ValidateHTMLApplicability(html,pdf) = %v, want nil", err)
		}
	})
}

func TestBuildPrintCSS(t *testing.T) {
	t.Run("only mapped constant present, no other size token", func(t *testing.T) {
		css := buildPrintCSS(HTMLOpts{PageSize: "a4", MarginMM: 10})
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
		css := buildPrintCSS(HTMLOpts{PageSize: "a4", MarginMM: 5, PrintBackground: true})
		if strings.Count(css, "!important") < 3 {
			t.Errorf("buildPrintCSS output %q does not carry !important on every property", css)
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
