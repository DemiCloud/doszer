package prepare

import "testing"

func TestINI(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		op      func(f *iniFile) bool
		want    string
		changed bool
	}{
		{
			"replace keeps the rest",
			"; comment\r\n[Desktop]\r\nPattern=50 0 0\r\nwallpaper=leaves.bmp\r\n\r\n[colors]\r\nBackground=0 128 128\r\n",
			func(f *iniFile) bool { return f.set("desktop", "Wallpaper", "(None)") },
			"; comment\r\n[Desktop]\r\nPattern=50 0 0\r\nwallpaper=(None)\r\n\r\n[colors]\r\nBackground=0 128 128\r\n",
			true,
		},
		{
			"same value is no change",
			"[colors]\nBackground = 0 0 0\n",
			func(f *iniFile) bool { return f.set("colors", "Background", "0 0 0") },
			"[colors]\nBackground = 0 0 0\n",
			false,
		},
		{
			"added at the end of the section, before blank lines",
			"[Desktop]\r\nPattern=(None)\r\n\r\n[windows]\r\nload=\r\n",
			func(f *iniFile) bool { return f.set("Desktop", "Wallpaper", "(None)") },
			"[Desktop]\r\nPattern=(None)\r\nWallpaper=(None)\r\n\r\n[windows]\r\nload=\r\n",
			true,
		},
		{
			"first of duplicate sections",
			"[a]\nx=1\n[a]\ny=2\n",
			func(f *iniFile) bool { return f.set("a", "z", "3") },
			"[a]\nx=1\nz=3\n[a]\ny=2\n",
			true,
		},
		{
			"new section",
			"[a]\r\nx=1",
			func(f *iniFile) bool { return f.set("colors", "Background", "0 0 0") },
			"[a]\r\nx=1\r\n\r\n[colors]\r\nBackground=0 0 0\r\n",
			true,
		},
		{
			"new section before Ctrl-Z",
			"[a]\r\nx=1\r\n\x1a",
			func(f *iniFile) bool { return f.set("b", "y", "2") },
			"[a]\r\nx=1\r\n\r\n[b]\r\ny=2\r\n\x1a",
			true,
		},
		{
			"commented key untouched",
			"[386Enh]\n;PermSwapDOSDrive=C\nPermSwapDOSDrive=C\nPermSwapSizeK=4096\n",
			func(f *iniFile) bool { return f.remove("386enh", "permswapdosdrive") },
			"[386Enh]\n;PermSwapDOSDrive=C\nPermSwapSizeK=4096\n",
			true,
		},
		{
			"remove absent key",
			"[386Enh]\nPaging=Yes\n",
			func(f *iniFile) bool { return f.remove("386Enh", "PermSwapSizeK") },
			"[386Enh]\nPaging=Yes\n",
			false,
		},
		{
			"same key in another section untouched",
			"[a]\nk=1\n[b]\nk=2\n",
			func(f *iniFile) bool { return f.set("b", "k", "3") },
			"[a]\nk=1\n[b]\nk=3\n",
			true,
		},
	}
	for _, tt := range tests {
		f := parseINI([]byte(tt.in))
		if got := tt.op(f); got != tt.changed {
			t.Errorf("%s: changed = %v, want %v", tt.name, got, tt.changed)
		}
		if got := string(f.bytes()); got != tt.want {
			t.Errorf("%s:\n got %q\nwant %q", tt.name, got, tt.want)
		}
	}
}

func TestINIGet(t *testing.T) {
	f := parseINI([]byte("[a]\n;k=0\nK = 1 \n[a]\nk=2\n"))
	if v, ok := f.get("A", "k"); !ok || v != "1" {
		t.Errorf("get = %q, %v; want \"1\", true", v, ok)
	}
	if _, ok := f.get("a", "missing"); ok {
		t.Error("get found a missing key")
	}
}
