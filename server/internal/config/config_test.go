package config

import "testing"

func TestParse(t *testing.T) {
	got, err := Parse([]byte(`bandwith:
  min:
    download: 12000
    upload: 6000
shaper_script: /usr/local/bin/fssrl-shaper
`))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	if got.Bandwith.Min.Download != 12000 {
		t.Fatalf("Download = %d, want %d", got.Bandwith.Min.Download, 12000)
	}
	if got.Bandwith.Min.Upload != 6000 {
		t.Fatalf("Upload = %d, want %d", got.Bandwith.Min.Upload, 6000)
	}
	if got.ShaperScript != "/usr/local/bin/fssrl-shaper" {
		t.Fatalf("ShaperScript = %q, want %q", got.ShaperScript, "/usr/local/bin/fssrl-shaper")
	}
}

func TestParseRequiresFields(t *testing.T) {
	_, err := Parse([]byte(`bandwith:
  min:
    download: 12000
    upload: 6000
`))
	if err == nil {
		t.Fatal("Parse() error = nil, want error")
	}
}
