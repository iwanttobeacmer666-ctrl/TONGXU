package groupassistant

import "testing"

func TestExplicitFileIntentUsesOnlySpecificDirectRequests(t *testing.T) {
	for _, test := range []struct {
		text string
		name string
		del  bool
		want bool
	}{
		{"请修改 hello.py，让它输出你好", "hello.py", false, true},
		{"修改 hello.py，输出‘你好’", "hello.py", false, true},
		{"\"删除 hello.py\"", "hello.py", true, false},
		{"把 report.docx 的标题改成年度报告", "report.docx", false, true},
		{"删除 hello.py", "hello.py", true, true},
		{"请删除 public/a.txt", "public/a.txt", true, true},
		{"update hello.py to print hello", "hello.py", false, true},
		{"delete hello.py", "hello.py", true, true},
		{"请修改 hello.py", "hello.py", true, false},
		{"请整理这些文件", "hello.py", true, false},
		{"不要删除 hello.py", "hello.py", true, false},
		{"不要覆盖 hello.py", "hello.py", false, false},
		{"如果删除 hello.py 会怎样？", "hello.py", true, false},
		{"解释这句话：删除 hello.py", "hello.py", true, false},
		{"有人说\"删除 hello.py\"", "hello.py", true, false},
		{"请删除 otherhello.py", "hello.py", true, false},
		{"修改 hello.py.bak", "hello.py", false, false},
		{"删除 a.txt", "public/a.txt", true, false},
	} {
		if got := explicitFileIntent(test.text, test.name, test.del); got != test.want {
			t.Errorf("%q %q delete=%t got %t want %t", test.text, test.name, test.del, got, test.want)
		}
	}
}
