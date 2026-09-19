package bizarch

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
)

func TestClipperManifest_ContainsRequiredKeys(t *testing.T) {
	t.Parallel()

	must := []string{
		"name:",         // template name
		"noteName:",     // file name template
		"noteFolder:",   // folder template
		"triggers:",     // URL match
		"mp.weixin.qq.com", // target host
		"#js_name",     // author selector
		"#publish_time", // publish time selector
		"ghID",         // chatlog-friendly metadata
	}
	for _, k := range must {
		if !strings.Contains(ClipperManifest, k) {
			t.Errorf("ClipperManifest missing required key %q", k)
		}
	}
}

func TestPrintManifest_WritesToStdout(t *testing.T) {
	// Capture stdout.
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	PrintManifest()

	w.Close()
	os.Stdout = old
	var buf bytes.Buffer
	io.Copy(&buf, r)

	got := buf.String()
	if got != ClipperManifest {
		t.Errorf("PrintManifest output differs from ClipperManifest constant (delta: %d bytes)", len(got)-len(ClipperManifest))
	}
	if !strings.Contains(got, "triggers:") {
		t.Errorf("PrintManifest output missing triggers section")
	}
}