package convert

import (
	"strings"
	"testing"
)

func TestParseAVOpts(t *testing.T) {
	t.Run("empty object valid, zero opts", func(t *testing.T) {
		o, err := ParseAVOpts([]byte(`{}`))
		if err != nil {
			t.Fatalf("ParseAVOpts({}) unexpected error: %v", err)
		}
		if o != (AVOpts{}) {
			t.Errorf("ParseAVOpts({}) = %+v, want zero value", o)
		}
	})

	t.Run("valid timecode accepted", func(t *testing.T) {
		o, err := ParseAVOpts([]byte(`{"timecode":2.5}`))
		if err != nil {
			t.Fatalf("ParseAVOpts: unexpected error: %v", err)
		}
		if o.Timecode != 2.5 {
			t.Errorf("ParseAVOpts = %+v, want timecode=2.5", o)
		}
	})

	t.Run("negative timecode rejected", func(t *testing.T) {
		_, err := ParseAVOpts([]byte(`{"timecode":-1}`))
		if err == nil {
			t.Fatal("ParseAVOpts with negative timecode = nil error, want error")
		}
		if !strings.Contains(err.Error(), "timecode") {
			t.Errorf("error = %q, want it to mention 'timecode'", err.Error())
		}
	})

	t.Run("valid resolution_height 480 accepted", func(t *testing.T) {
		o, err := ParseAVOpts([]byte(`{"resolution_height":480}`))
		if err != nil {
			t.Fatalf("ParseAVOpts: unexpected error: %v", err)
		}
		if o.ResolutionHeight != 480 {
			t.Errorf("ParseAVOpts = %+v, want resolution_height=480", o)
		}
	})

	t.Run("valid resolution_height 720 accepted", func(t *testing.T) {
		o, err := ParseAVOpts([]byte(`{"resolution_height":720}`))
		if err != nil {
			t.Fatalf("ParseAVOpts: unexpected error: %v", err)
		}
		if o.ResolutionHeight != 720 {
			t.Errorf("ParseAVOpts = %+v, want resolution_height=720", o)
		}
	})

	t.Run("valid resolution_height 1080 accepted", func(t *testing.T) {
		o, err := ParseAVOpts([]byte(`{"resolution_height":1080}`))
		if err != nil {
			t.Fatalf("ParseAVOpts: unexpected error: %v", err)
		}
		if o.ResolutionHeight != 1080 {
			t.Errorf("ParseAVOpts = %+v, want resolution_height=1080", o)
		}
	})

	t.Run("out-of-enum resolution_height rejected", func(t *testing.T) {
		_, err := ParseAVOpts([]byte(`{"resolution_height":360}`))
		if err == nil {
			t.Fatal("ParseAVOpts with resolution_height=360 = nil error, want error")
		}
		if !strings.Contains(err.Error(), "unsupported resolution_height") {
			t.Errorf("error = %q, want it to mention 'unsupported resolution_height'", err.Error())
		}
	})

	t.Run("valid codec h264 accepted", func(t *testing.T) {
		o, err := ParseAVOpts([]byte(`{"codec":"h264"}`))
		if err != nil {
			t.Fatalf("ParseAVOpts: unexpected error: %v", err)
		}
		if o.Codec != "h264" {
			t.Errorf("ParseAVOpts = %+v, want codec=h264", o)
		}
	})

	t.Run("valid codec hevc accepted", func(t *testing.T) {
		o, err := ParseAVOpts([]byte(`{"codec":"hevc"}`))
		if err != nil {
			t.Fatalf("ParseAVOpts: unexpected error: %v", err)
		}
		if o.Codec != "hevc" {
			t.Errorf("ParseAVOpts = %+v, want codec=hevc", o)
		}
	})

	t.Run("unknown codec rejected", func(t *testing.T) {
		_, err := ParseAVOpts([]byte(`{"codec":"vp9"}`))
		if err == nil {
			t.Fatal("ParseAVOpts with codec=vp9 = nil error, want error")
		}
		if !strings.Contains(err.Error(), "unsupported codec") {
			t.Errorf("error = %q, want it to mention 'unsupported codec'", err.Error())
		}
	})

	t.Run("unknown field rejected", func(t *testing.T) {
		if _, err := ParseAVOpts([]byte(`{"codec":"h264","bitrate":"9000k"}`)); err == nil {
			t.Error("ParseAVOpts with unknown field 'bitrate' = nil error, want error")
		}
	})

	t.Run("duplicate top-level key rejected", func(t *testing.T) {
		if _, err := ParseAVOpts([]byte(`{"codec":"h264","codec":"hevc"}`)); err == nil {
			t.Error("ParseAVOpts with duplicate key = nil error, want error")
		}
	})

	t.Run("trailing bytes rejected", func(t *testing.T) {
		if _, err := ParseAVOpts([]byte(`{"codec":"h264"}garbage`)); err == nil {
			t.Error("ParseAVOpts with trailing bytes = nil error, want error")
		}
	})

	t.Run("top-level null rejected", func(t *testing.T) {
		if _, err := ParseAVOpts([]byte(`null`)); err == nil {
			t.Error("ParseAVOpts with top-level null = nil error, want error")
		}
	})
}

func TestAVOptsFromMap(t *testing.T) {
	t.Run("nil map yields zero opts", func(t *testing.T) {
		o, err := AVOptsFromMap(nil)
		if err != nil {
			t.Fatalf("AVOptsFromMap(nil) unexpected error: %v", err)
		}
		if o != (AVOpts{}) {
			t.Errorf("AVOptsFromMap(nil) = %+v, want zero value", o)
		}
	})

	t.Run("empty map yields zero opts", func(t *testing.T) {
		o, err := AVOptsFromMap(map[string]any{})
		if err != nil {
			t.Fatalf("AVOptsFromMap({}) unexpected error: %v", err)
		}
		if o != (AVOpts{}) {
			t.Errorf("AVOptsFromMap({}) = %+v, want zero value", o)
		}
	})

	t.Run("round trip valid map", func(t *testing.T) {
		m := map[string]any{"resolution_height": float64(720), "codec": "hevc"}
		o, err := AVOptsFromMap(m)
		if err != nil {
			t.Fatalf("AVOptsFromMap: unexpected error: %v", err)
		}
		if o.ResolutionHeight != 720 || o.Codec != "hevc" {
			t.Errorf("AVOptsFromMap = %+v, want resolution_height=720/codec=hevc", o)
		}
	})

	t.Run("corrupt persisted map applies same strictness (invalid resolution)", func(t *testing.T) {
		m := map[string]any{"resolution_height": float64(4320)}
		if _, err := AVOptsFromMap(m); err == nil {
			t.Error("AVOptsFromMap with invalid resolution_height = nil error, want error")
		}
	})

	t.Run("unknown field in persisted map rejected", func(t *testing.T) {
		m := map[string]any{"codec": "h264", "bitrate": "9000k"}
		if _, err := AVOptsFromMap(m); err == nil {
			t.Error("AVOptsFromMap with unknown field = nil error, want error")
		}
	})
}

func TestValidateAVApplicability(t *testing.T) {
	t.Run("empty opts always apply", func(t *testing.T) {
		if err := ValidateAVApplicability(EngineDocument, "docx", "pdf", AVOpts{}); err != nil {
			t.Errorf("ValidateAVApplicability with zero opts = %v, want nil", err)
		}
	})

	t.Run("non-empty opts on non-av engine rejected", func(t *testing.T) {
		o := AVOpts{Codec: "h264"}
		if err := ValidateAVApplicability(EngineDocument, "docx", "pdf", o); err == nil {
			t.Error("ValidateAVApplicability on document engine = nil error, want error")
		}
	})

	t.Run("timecode on transcode target rejected", func(t *testing.T) {
		o := AVOpts{Timecode: 3}
		if err := ValidateAVApplicability(EngineAV, "mov", "mp4", o); err == nil {
			t.Error("ValidateAVApplicability(timecode on mp4 transcode target) = nil error, want error")
		}
	})

	t.Run("timecode on thumbnail target accepted", func(t *testing.T) {
		o := AVOpts{Timecode: 3}
		if err := ValidateAVApplicability(EngineAV, "mov", "jpg", o); err != nil {
			t.Errorf("ValidateAVApplicability(timecode on jpg thumbnail target) = %v, want nil", err)
		}
	})

	t.Run("resolution_height on thumbnail target rejected", func(t *testing.T) {
		o := AVOpts{ResolutionHeight: 720}
		if err := ValidateAVApplicability(EngineAV, "mov", "png", o); err == nil {
			t.Error("ValidateAVApplicability(resolution_height on png thumbnail target) = nil error, want error")
		}
	})

	t.Run("resolution_height on transcode target accepted", func(t *testing.T) {
		o := AVOpts{ResolutionHeight: 720}
		if err := ValidateAVApplicability(EngineAV, "mov", "webm", o); err != nil {
			t.Errorf("ValidateAVApplicability(resolution_height on webm transcode target) = %v, want nil", err)
		}
	})

	t.Run("hevc codec on webm rejected", func(t *testing.T) {
		o := AVOpts{Codec: "hevc"}
		if err := ValidateAVApplicability(EngineAV, "mov", "webm", o); err == nil {
			t.Error("ValidateAVApplicability(codec=hevc on webm target) = nil error, want error")
		}
	})

	t.Run("hevc codec on mp4 accepted", func(t *testing.T) {
		o := AVOpts{Codec: "hevc"}
		if err := ValidateAVApplicability(EngineAV, "mov", "mp4", o); err != nil {
			t.Errorf("ValidateAVApplicability(codec=hevc on mp4 target) = %v, want nil", err)
		}
	})

	t.Run("h264 codec on mp4 accepted", func(t *testing.T) {
		o := AVOpts{Codec: "h264"}
		if err := ValidateAVApplicability(EngineAV, "mov", "mp4", o); err != nil {
			t.Errorf("ValidateAVApplicability(codec=h264 on mp4 target) = %v, want nil", err)
		}
	})
}

// TestCRFConstantsDistinct is the AVO-03 required proof: the x265/HEVC path
// has its own CRF default constant, never shared with x264's (Pitfall 4).
func TestCRFConstantsDistinct(t *testing.T) {
	if x264DefaultCRF == x265DefaultCRF {
		t.Fatalf("x264DefaultCRF (%d) == x265DefaultCRF (%d), want distinct constants", x264DefaultCRF, x265DefaultCRF)
	}
}
