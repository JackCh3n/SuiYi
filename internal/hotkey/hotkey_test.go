package hotkey

import "testing"

// 离线单元测试（CI 可跑；Start 需要真实注册，只在 Windows 手动验证）
func TestParse(t *testing.T) {
	cases := []struct {
		in       string
		mods, vk uint32
		name     string
		wantErr  bool
	}{
		{"alt+f", 1, 'F', "alt+f", false},
		{"Alt+F", 1, 'F', "alt+f", false},
		{"ctrl+alt+t", 2 | 1, 'T', "ctrl+alt+t", false},
		{"shift+5", 4, '5', "shift+5", false},
		{"win+f2", 8, 0x71, "win+f2", false},
		{"ctrl + f12", 2, 0x7B, "ctrl + f12", false},
		{"f9", 0, 0, "", true},      // 无修饰键：会全局抢占单键
		{"alt", 0, 0, "", true},     // 只有修饰键
		{"alt+foo", 0, 0, "", true}, // 非法主键
		{"alt+f+j", 0, 0, "", true}, // 多个主键
		{"", 0, 0, "", true},        // 空
	}
	for _, c := range cases {
		mods, vk, name, err := Parse(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("Parse(%q) 应报错，得到 mods=%d vk=%d", c.in, mods, vk)
			}
			continue
		}
		if err != nil {
			t.Errorf("Parse(%q) 意外报错: %v", c.in, err)
			continue
		}
		if mods != c.mods || vk != c.vk || name != c.name {
			t.Errorf("Parse(%q) = (%d,%d,%q)，期望 (%d,%d,%q)", c.in, mods, vk, name, c.mods, c.vk, c.name)
		}
	}
}
