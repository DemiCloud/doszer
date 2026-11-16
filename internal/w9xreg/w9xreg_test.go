package w9xreg

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

func sz(name, s string) Value { return Value{Name: name, Type: TypeSZ, Data: []byte(s)} }

func TestParseRoundTrip(t *testing.T) {
	root := &Key{Subkeys: []*Key{
		{Name: "Software", Subkeys: []*Key{
			{Name: "Example Co", Subkeys: []*Key{
				{Name: "Game", Values: []Value{sz("Path", `D:\GAME`), {Name: "Level", Type: TypeDWORD, Data: []byte{3, 0, 0, 0}}}},
			}},
		}},
		{Name: "System", Values: []Value{sz("", "default")}},
	}}
	got, err := Parse(Encode(root))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, root) {
		t.Errorf("got %+v, want %+v", got, root)
	}
}

func TestParseRejects(t *testing.T) {
	good := Encode(&Key{Subkeys: []*Key{{Name: "A"}}})
	tests := []struct {
		name string
		data []byte
	}{
		{"not a registry", []byte(strings.Repeat("x", 0x100))},
		{"truncated", good[:0x30]},
		{"missing RGDB block", func() []byte { b := append([]byte(nil), good...); b[0x10] = 2; return b }()},
		{"key without a record", func() []byte { b := append([]byte(nil), good...); b[0x20+0x20+0x1C+0x18] = 9; return b }()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Parse(tt.data); err == nil {
				t.Error("accepted")
			}
		})
	}
}

func TestAddedAndWriteReg(t *testing.T) {
	before := &Key{Subkeys: []*Key{
		{Name: "Software", Subkeys: []*Key{
			{Name: "Old", Values: []Value{sz("Keep", "1"), sz("Change", "a")}},
		}},
	}}
	after := &Key{Subkeys: []*Key{
		{Name: "SOFTWARE", Subkeys: []*Key{
			{Name: "old", Values: []Value{sz("keep", "1"), sz("Change", "b")}},
			{Name: "New Co", Subkeys: []*Key{
				{Name: "Game", Values: []Value{
					sz("Path", `D:\GAME "1"`),
					sz("", "x"),
					{Name: "Level", Type: TypeDWORD, Data: []byte{0x10, 0, 0, 0}},
					{Name: "Bin", Type: TypeBinary, Data: bytes.Repeat([]byte{0xAB}, 30)},
					{Name: "Multi", Type: TypeMultiSZ, Data: []byte("a\x00b\x00\x00")},
				}},
			}},
		}},
	}}
	var buf bytes.Buffer
	if err := WriteReg(&buf, Group{Root: "HKEY_LOCAL_MACHINE", Entries: Added(before, after)}); err != nil {
		t.Fatal(err)
	}
	want := "REGEDIT4\r\n" +
		"\r\n[HKEY_LOCAL_MACHINE\\SOFTWARE\\New Co]\r\n" +
		"\r\n[HKEY_LOCAL_MACHINE\\SOFTWARE\\New Co\\Game]\r\n" +
		"\"Path\"=\"D:\\\\GAME \\\"1\\\"\"\r\n" +
		"@=\"x\"\r\n" +
		"\"Level\"=dword:00000010\r\n" +
		"\"Bin\"=hex:ab,ab,ab,ab,ab,ab,ab,ab,ab,ab,ab,ab,ab,ab,ab,ab,ab,ab,ab,ab,ab,ab,\\\r\n" +
		"  ab,ab,ab,ab,ab,ab,ab,ab\r\n" +
		"\"Multi\"=hex(7):61,00,62,00,00\r\n" +
		"\r\n[HKEY_LOCAL_MACHINE\\SOFTWARE\\old]\r\n" +
		"\"Change\"=\"b\"\r\n"
	if buf.String() != want {
		t.Errorf("got\n%s\nwant\n%s", buf.String(), want)
	}
}
